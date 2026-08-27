#!/usr/bin/env python3
"""Find degenerate repetition in aider and Strument chat logs.

Reads whatever you point it at and reports assistant output that repeats itself:
the "the the the" and "I'll fix that. I'll fix that. I'll fix that." failure that
does not end on its own. Tool-call loops are not what this looks for — those
tend to break out by themselves, and the model's own text is where the unbroken
ones live.

Formats, detected per file rather than declared:

  aider     .aider.chat.history.md — sessions open with "# aider chat started
            at", user turns are "#### " lines, aider's own console echo is "> ",
            everything else is the model.
  strument  a Markdown transcript — turns are "## <time> — <model>", and the
            model's prose is the "### Response" section.
  jsonl     a Strument --jsonl log — one JSON object per line; assistant
            messages and reasoning arrive as separate records, which matters,
            because a model that loops while thinking and then answers cleanly
            is a different animal from one that loops in the answer.

Three detectors, because they fail differently:

  chunk     Gemini CLI's shape: a fixed-width window that recurs many times at
            short average spacing. Catches loops whose period is not a whole
            number of sentences.
  period    The tail of the output is exactly some string repeated N times,
            found with the KMP failure function (minimal period = n - border).
            This is the one that matches "it never stopped" — a loop that ran to
            the end of the response leaves the tail perfectly periodic, and no
            threshold about *how long* the repeating unit is has to be guessed.
  run       The dumbest one: the longest run of consecutive identical
            sentences. Cheap, and often the whole story.

Usage:

  find-loops.py PATH [PATH ...]        scan files and directories
  find-loops.py --self-test            check the detectors discriminate
  find-loops.py PATH --json            machine-readable findings
  find-loops.py PATH --show            print the repeating text

A zero is only meaningful next to the amount of text that was searched, so every
run reports blocks and characters scanned. "0 findings in 0 blocks" means the
parser missed; "0 findings in 900 blocks" means the logs are clean.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from collections import defaultdict
from dataclasses import dataclass, field, asdict

# ---------------------------------------------------------------- extraction


@dataclass
class Block:
    """One span of model-authored text, with enough provenance to go look."""

    path: str
    session: str  # session header or turn timestamp, for finding it again
    index: int  # nth block in the file
    kind: str  # "answer" or "reasoning"
    text: str
    line: int = 1  # 1-based line in the file where the block starts


CODE_FENCE = re.compile(r"^\s*(```|~~~)")


def strip_code_blocks(text: str) -> str:
    """Drop fenced code and table rows.

    Not squeamishness: a table, a long list, and generated code legitimately
    repeat, and Gemini CLI resets its detector on exactly these for the same
    reason. Leaving them in makes the detector loudest on the output most likely
    to be correct.

    Dropped lines are blanked rather than removed. Line numbers are the whole
    point of the report — the corpus that shaped this tool had to be assembled by
    hand because nothing said where to look — and deleting a line would shift
    every offset after it out of correspondence with the file.
    """
    out, in_fence = [], False
    for line in text.split("\n"):
        if CODE_FENCE.match(line):
            in_fence = not in_fence
            out.append("")
            continue
        if in_fence:
            out.append("")
            continue
        if line.lstrip().startswith("|"):
            out.append("")
            continue
        # Scaffolding around a fence, not prose: aider heads every
        # SEARCH/REPLACE block with the bare filename, so an answer containing
        # forty edits repeats "main.go" forty times once the fences are gone.
        #
        # It has to look like a *path*, not merely be short and spaceless. The
        # first version dropped every short line without a space and thereby
        # deleted a real finding — a token-level stutter renders one word per
        # line, and "Dynamical" repeated 84 times vanished. Caught by re-running
        # the corpus after fixing the false positive, not by the self-test.
        stripped = line.strip()
        if stripped and " " not in stripped and len(stripped) < 40 and re.search(r"[./\\]", stripped):
            out.append("")
            continue
        out.append(line)
    return "\n".join(out)


AIDER_THINK_OPEN = re.compile(r"^<thinking-content-[0-9a-f]+>$")
AIDER_THINK_CLOSE = re.compile(r"^</thinking-content-[0-9a-f]+>$")


def read_aider(path: str) -> list[Block]:
    """aider's chat history: '#### ' is the user, '> ' is aider, rest is model.

    Reasoning is kept apart, which turned out to be the whole story. aider wraps
    it in <thinking-content-HASH>, and in a corpus of ten real loops every single
    one was inside such a block and none was in an answer. Reporting them all as
    "answer" would have hidden the finding that decides where a detector belongs.

    The closing tag is usually absent — nine of those ten had none, because the
    user pressed Ctrl-C while the model was still going, which is itself the
    evidence. So a user turn or a new session ends the block too.
    """
    blocks: list[Block] = []
    session = "(before any session header)"
    current: list[str] = []
    kind = "answer"
    start = 1
    lineno = 0

    def flush() -> None:
        nonlocal kind
        if current:
            body = "\n".join(current)
            lead = len(body) - len(body.lstrip("\n"))
            if body.strip():
                blocks.append(Block(path, session, len(blocks), kind, body.strip(), start + lead))
            current.clear()
        kind = "answer"

    with open(path, encoding="utf-8", errors="replace") as f:
        for lineno, line in enumerate(f, 1):
            line = line.rstrip("\n")
            if not current:
                start = lineno
            if line.startswith("# aider chat started at"):
                flush()
                session = line[2:].strip()
                continue
            if AIDER_THINK_OPEN.match(line.strip()):
                flush()
                kind = "reasoning"
                continue
            if AIDER_THINK_CLOSE.match(line.strip()):
                flush()
                continue
            # A user turn or aider's own console echo ends the model's block.
            if line.startswith("#### ") or line == "####" or line.startswith("> "):
                flush()
                continue
            current.append(line)
    flush()
    return blocks


STRUMENT_TURN = re.compile(r"^## (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) — (.*)$")


def read_strument_md(path: str) -> list[Block]:
    """Strument's transcript: the '### Response' section of each '## ts — model'."""
    blocks: list[Block] = []
    session = "(no turn header)"
    in_response = False
    current: list[str] = []
    start = 1

    def flush() -> None:
        nonlocal in_response
        if current:
            body = "\n".join(current)
            lead = len(body) - len(body.lstrip("\n"))
            if body.strip():
                blocks.append(Block(path, session, len(blocks), "answer", body.strip(), start + lead))
            current.clear()
        in_response = False

    with open(path, encoding="utf-8", errors="replace") as f:
        for lineno, line in enumerate(f, 1):
            line = line.rstrip("\n")
            if not current:
                start = lineno
            m = STRUMENT_TURN.match(line)
            if m:
                flush()
                session = f"{m.group(1)} {m.group(2)}"
                continue
            if line.startswith("### Response"):
                flush()
                in_response = True
                continue
            # Any other section header, or the turn separator, ends the answer.
            if line.startswith("### ") or line == "---":
                flush()
                continue
            if in_response:
                current.append(line)
    flush()
    return blocks


def read_jsonl(path: str) -> list[Block]:
    """A Strument --jsonl log. Reasoning is kept apart from the answer."""
    blocks: list[Block] = []
    session = os.path.basename(path)
    with open(path, encoding="utf-8", errors="replace") as f:
        for lineno, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                print(f"{path}:{lineno}: not JSON, skipped", file=sys.stderr)
                continue
            kind = None
            if rec.get("type") == "message" and rec.get("role") == "assistant":
                kind = "answer"
            elif rec.get("type") == "reasoning":
                kind = "reasoning"
            elif rec.get("type") == "session":
                session = f"{os.path.basename(path)} {rec.get('model', '')}".strip()
            if not kind:
                continue
            text = (rec.get("text") or "").strip()
            if text:
                blocks.append(Block(path, session, len(blocks), kind, text, lineno))
    return blocks


def looks_like_session_log(path: str, probe_lines: int = 20) -> bool:
    """Does this JSONL carry Strument session records, or is it something else?"""
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            for _, line in zip(range(probe_lines), f):
                line = line.strip()
                if not line:
                    continue
                try:
                    rec = json.loads(line)
                except json.JSONDecodeError:
                    return False
                if isinstance(rec, dict) and rec.get("type") in {"session", "message", "reasoning", "turn"}:
                    return True
    except OSError:
        return False
    return False


def sniff(path: str) -> str | None:
    """Pick a reader from the name, falling back to the first lines of content."""
    name = os.path.basename(path)
    if name.endswith(".jsonl"):
        # The extension is not enough. Pointed at this repository, it matched
        # doc/experiments/*/results.jsonl — experiment output with an unrelated
        # schema — and the reader then found nothing, which reports as a clean
        # scan. A Strument session log says what it is in its records.
        return "jsonl" if looks_like_session_log(path) else None
    if "aider.chat.history" in name:
        return "aider"
    if not name.endswith(".md"):
        return None
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            head = f.read(4096)
    except OSError:
        return None
    if "# aider chat started at" in head:
        return "aider"
    if head.startswith("# Strument chat history") or STRUMENT_TURN.search(head):
        return "strument"
    return None


READERS = {"aider": read_aider, "strument": read_strument_md, "jsonl": read_jsonl}


def walk(paths: list[str]) -> list[tuple[str, str]]:
    """Every readable log under the given files and directories, with its format."""
    found: list[tuple[str, str]] = []
    for p in paths:
        if os.path.isfile(p):
            fmt = sniff(p)
            if fmt:
                found.append((p, fmt))
            else:
                print(f"{p}: unrecognized format, skipped", file=sys.stderr)
            continue
        for root, dirs, files in os.walk(p):
            dirs[:] = [d for d in dirs if d not in {".git", "node_modules"}]
            for name in sorted(files):
                full = os.path.join(root, name)
                fmt = sniff(full)
                if fmt:
                    found.append((full, fmt))
    return found


# ----------------------------------------------------------------- detectors

SENTENCE_SPLIT = re.compile(r"(?<=[.!?])\s+|\n+")


def sentences(text: str) -> list[tuple[int, str]]:
    """Split into (offset, normalized text) pairs.

    The offset rides along so a finding can name the line it starts on. re.split
    discards the separators, so positions are walked with finditer instead.
    """
    out: list[tuple[int, str]] = []
    pos = 0
    for m in SENTENCE_SPLIT.finditer(text):
        piece = text[pos : m.start()]
        norm = " ".join(piece.split())
        if norm:
            out.append((pos + (len(piece) - len(piece.lstrip())), norm))
        pos = m.end()
    tail = text[pos:]
    norm = " ".join(tail.split())
    if norm:
        out.append((pos + (len(tail) - len(tail.lstrip())), norm))
    return out


def minimal_period(seq: list[str]) -> int:
    """The shortest p with seq == prefix(p) repeated, via KMP's failure function.

    The classic fact: for a string of length n whose longest proper border is
    b, the minimal period is n - b, and the string is an exact repetition
    exactly when that period divides n. This is why the detector needs no guess
    about how long the repeating unit might be — Gemini CLI scans cycle lengths
    1 through 5 and a period-7 loop walks straight past it.
    """
    n = len(seq)
    if n == 0:
        return 0
    fail = [0] * n
    k = 0
    for i in range(1, n):
        while k and seq[i] != seq[k]:
            k = fail[k - 1]
        if seq[i] == seq[k]:
            k += 1
        fail[i] = k
    return n - fail[n - 1]


@dataclass
class Finding:
    detector: str
    severity: float  # repeats, or occurrence count; comparable within a detector
    detail: str
    sample: str = ""
    offset: int = 0  # character offset into the stripped block, for the line


def detect_period(units: list[str], min_repeats: int, max_tail: int, min_unit: int) -> Finding | None:
    """The longest suffix that is one unit sequence repeated min_repeats+ times.

    Suffixes, not the whole block, because the failure mode is a response that
    starts fine and never stops. Requiring the *whole* block to be periodic
    would find almost nothing.
    """
    best: Finding | None = None
    # Discard up to two trailing units before testing. A loop in the wild ends in
    # a fragment — the stream was cut by Ctrl-C or by the context filling — and
    # that fragment appears nowhere else in the tail, so it drives the longest
    # border to zero and the minimal period to the whole tail. Measured: on a
    # corpus of eight real loops this detector fired zero times, and the single
    # unit "We'll also protect the" was the entire reason.
    for drop in range(0, 3):
        units_ = units[: len(units) - drop] if drop else units
        n = len(units_)
        for length in range(2, min(n, max_tail) + 1):
            window = units_[n - length :]
            tail = [u for _, u in window]
            p = minimal_period(tail)
            # Divisibility is deliberately not required either. n - border is
            # the minimal period in the general sense — seq[i] == seq[i+p]
            # wherever both exist — so a final repetition cut short still counts.
            if p == 0 or length // p < min_repeats:
                continue
            repeats = length // p
            unit = " ".join(tail[:p])
            if len(unit) < min_unit:  # scaffolding, not prose — see detect_run
                continue
            if best is None or repeats > best.severity:
                best = Finding(
                    "period",
                    repeats,
                    f"the last {length} sentences repeat a {p}-sentence block {repeats}x"
                    + (f", cut {drop} unit(s) short" if drop else ""),
                    unit[:300],
                    window[0][0],
                )
    return best


def detect_run(units: list[str], min_run: int, min_unit: int) -> Finding | None:
    """Longest run of consecutive identical sentences.

    The repeated unit has to be substantial. A real answer full of aider
    SEARCH/REPLACE blocks repeats the bare filename above every one of them, and
    "main.go" 43 times was the only false positive this tool produced on a real
    corpus. A repeated short token is what scaffolding does; a repeated sentence
    is what a loop does. Word-level stutter is not lost by this — detect_word_run
    covers it and answers to its own threshold.
    """
    best_len, best_unit, best_off = 0, "", 0
    run_len, prev, start = 0, None, 0
    for off, u in units:
        if u == prev:
            run_len += 1
        else:
            run_len, prev, start = 1, u, off
        if run_len > best_len and len(u) >= min_unit:
            best_len, best_unit, best_off = run_len, u, start
    if best_len < min_run:
        return None
    return Finding("run", best_len, f"one sentence repeated {best_len}x in a row", best_unit[:300], best_off)


def detect_word_run(text: str, min_run: int) -> Finding | None:
    """Longest run of one repeated word.

    The oldest and most literal degeneration — "the the the the" — and the
    sentence detectors cannot see it, because the whole stutter is one sentence
    with no terminator. Found by the self-test, which is what the self-test is
    for.

    finditer rather than findall, so the run's position is known. The offset is
    what the report turns into a line number, and a detector that finds the
    right thing at the wrong place sends the reader to the wrong screen.
    """
    best_len, best_word, best_off = 0, "", 0
    run_len, prev, start = 0, None, 0
    for m in re.finditer(r"\w+", text):
        w = m.group(0).lower()
        if w == prev:
            run_len += 1
        else:
            run_len, prev, start = 1, w, m.start()
        if run_len > best_len:
            best_len, best_word, best_off = run_len, w, start
    if best_len < min_run:
        return None
    return Finding("word", best_len, f'the word "{best_word}" repeats {best_len}x in a row', best_word, best_off)


def detect_chunk(text: str, size: int, min_count: int, max_avg_gap: float) -> Finding | None:
    """Gemini CLI's shape: a window that recurs often at short average spacing.

    Positions are bucketed by hash rather than by the substring itself, so the
    memory is O(n) integers rather than O(n * size) characters; the winner is
    re-sliced and compared before it is reported, so a hash collision cannot
    produce a finding.
    """
    if len(text) < size * 2:
        return None
    buckets: dict[int, list[int]] = defaultdict(list)
    for i in range(len(text) - size + 1):
        buckets[hash(text[i : i + size])].append(i)

    best: Finding | None = None
    for positions in buckets.values():
        if len(positions) < min_count:
            continue
        first = text[positions[0] : positions[0] + size]
        # A window has to carry text. Blanking stripped lines keeps the line
        # numbering exact (see strip_code_blocks) at the cost of long runs of
        # newlines where a fenced block used to be, and a window of nothing but
        # whitespace recurs at one-character spacing forever. That regression
        # arrived with the line numbers and was caught by re-running the corpus,
        # not by the self-test — an answer made of forty edit blocks became two
        # new "loops" reported at 871x and 385x.
        if len(first.strip()) < size // 4:
            continue
        # Verify rather than trust the hash, and drop any colliding positions.
        positions = [i for i in positions if text[i : i + size] == first]
        if len(positions) < min_count:
            continue
        gaps = [b - a for a, b in zip(positions, positions[1:])]
        avg = sum(gaps) / len(gaps)
        if avg > max_avg_gap:
            continue
        if best is None or len(positions) > best.severity:
            best = Finding(
                "chunk",
                len(positions),
                f"a {size}-char window recurs {len(positions)}x, {avg:.0f} chars apart on average",
                first,
                positions[0],
            )
    return best


# -------------------------------------------------------------------- report


@dataclass
class Scan:
    blocks: int = 0
    chars: int = 0
    files: int = 0
    findings: list[dict] = field(default_factory=list)


def line_of(text: str, offset: int, base: int) -> int:
    """The file line an offset in the stripped block text falls on.

    Exact only because strip_code_blocks blanks lines instead of deleting them.

    Leading whitespace is skipped first. A chunk window is character-aligned, so
    it can begin on the newline that ends the previous line; reporting that line
    sends the reader one line above the text they are looking for.
    """
    offset = max(0, min(offset, len(text)))
    while offset < len(text) and text[offset].isspace():
        offset += 1
    return base + text.count("\n", 0, offset)


def analyze(block: Block, args) -> list[Finding]:
    text = block.text if args.keep_code else strip_code_blocks(block.text)
    # A word stutter is a finding at any length; the others need enough text to
    # tell repetition from ordinary structure.
    word = detect_word_run(text, args.min_word_run)
    if len(text.strip()) < args.min_chars:
        return [word] if word else []
    units = sentences(text)
    out = []
    for f in (
        word,
        detect_run(units, args.min_run, args.min_unit_chars),
        detect_period(units, args.min_repeats, args.max_tail, args.min_unit_chars),
        detect_chunk(text, args.chunk_size, args.min_chunk_count, args.max_avg_gap),
    ):
        if f:
            out.append(f)
    return out


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("paths", nargs="*", help="files or directories to scan")
    ap.add_argument("--json", action="store_true", help="emit findings as JSON")
    ap.add_argument("--show", action="store_true", help="print the repeating text")
    ap.add_argument("--self-test", action="store_true", help="check the detectors discriminate, then exit")
    ap.add_argument("--keep-code", action="store_true", help="do not strip fenced code and tables")
    ap.add_argument("--min-chars", type=int, default=200, help="skip blocks shorter than this")
    ap.add_argument("--min-run", type=int, default=3, help="run: identical sentences in a row")
    ap.add_argument("--min-word-run", type=int, default=6, help="word: identical words in a row")
    ap.add_argument("--min-repeats", type=int, default=3, help="period: repetitions of the tail unit")
    ap.add_argument("--max-tail", type=int, default=200, help="period: longest suffix considered")
    ap.add_argument("--min-unit-chars", type=int, default=24, help="run/period: shortest repeating unit worth reporting")
    ap.add_argument("--chunk-size", type=int, default=50, help="chunk: window width in characters")
    ap.add_argument("--min-chunk-count", type=int, default=10, help="chunk: occurrences to report")
    ap.add_argument("--max-avg-gap", type=float, default=250, help="chunk: mean spacing allowed")
    args = ap.parse_args(argv)

    if args.self_test:
        return self_test(args)
    if not args.paths:
        ap.error("give a path to scan, or --self-test")

    scan = Scan()
    for path, fmt in walk(args.paths):
        scan.files += 1
        try:
            blocks = READERS[fmt](path)
        except OSError as e:
            print(f"{path}: {e}", file=sys.stderr)
            continue
        for b in blocks:
            scan.blocks += 1
            scan.chars += len(b.text)
            stripped = b.text if args.keep_code else strip_code_blocks(b.text)
            for f in analyze(b, args):
                rec = {
                    "file": b.path,
                    "line": line_of(stripped, f.offset, b.line),
                    "session": b.session,
                    "block": b.index,
                    "kind": b.kind,
                    **asdict(f),
                }
                scan.findings.append(rec)

    if args.json:
        json.dump(
            {"files": scan.files, "blocks": scan.blocks, "chars": scan.chars, "findings": scan.findings},
            sys.stdout,
            indent=2,
        )
        print()
        return 1 if scan.findings else 0

    # Grouped by block, worst first. One loop trips several detectors, and three
    # near-identical lines per block buries the shape of a corpus scan.
    by_block: dict[tuple, list[dict]] = defaultdict(list)
    for r in scan.findings:
        by_block[(r["file"], r["block"], r["kind"], r["session"])].append(r)
    ordered = sorted(by_block.items(), key=lambda kv: -max(x["severity"] for x in kv[1]))
    for (path, index, kind, session), rows in ordered:
        # path:line first, so the line is clickable and greppable.
        print(f"{path}:{min(r['line'] for r in rows)}  ({kind})  [{session}]")
        for r in sorted(rows, key=lambda x: -x["severity"]):
            print(f"  line {r['line']}  {r['detector']}: {r['detail']}")
        if args.show:
            sample = next((r["sample"] for r in rows if r["sample"]), "")
            if sample:
                print(f"  > {sample!r}")
    # The denominator, always. A zero next to no denominator is the shape a
    # broken parser makes, and it reads exactly like good news.
    print(
        f"\n{len(by_block)} suspect block(s), {len(scan.findings)} finding(s), "
        f"out of {scan.blocks} block(s) ({scan.chars:,} chars) across {scan.files} file(s)."
    )
    if scan.files and not scan.blocks:
        print("No blocks were extracted. The format sniffer or a reader is wrong.", file=sys.stderr)
        return 2
    return 1 if scan.findings else 0


# ----------------------------------------------------------------- self-test


def self_test(args) -> int:
    """Check each detector fires on a loop and stays quiet on real writing.

    Both halves, because a detector that fires on everything and one that fires
    on nothing both report a number, and only the negative cases tell them
    apart. The legitimate-repetition cases are the ones worth watching: a
    numbered list and a diff repeat by nature.
    """
    loop = "I'll check the file. " * 30
    stutter = "The file contains the the the the the the the the the the the the the the the"
    tail_loop = (
        "Let me look at the configuration to understand the failure mode here. "
        + "Actually, let me reconsider. Let me try again. " * 12
    )
    prose = (
        "The sandbox confines writes to the project and a handful of caches. "
        "Landlock is inherited across fork and exec, so every command a tool starts "
        "is covered by the same ruleset. Reads are untouched, which is why the cost "
        "of running under it is close to nothing for an ordinary session. "
        "Removing a path from the writable set is a one-line change in the config."
    )
    listy = "\n".join(f"{i}. Renamed verify to check in module {i}." for i in range(1, 15))
    # A real diff alternates - and + but the content differs every time, which is
    # the whole difference between a diff and a loop. The first version of this
    # fixture repeated two identical lines twelve times and was correctly
    # flagged — the fixture was wrong, not the detector.
    diffish = "\n".join(
        f"- {name} = {i}\n+ {name} = {i + 1}"
        for i, name in enumerate(
            ["timeout", "retries", "budget", "window", "depth", "width", "limit", "stride"], start=1
        )
    )

    # Every loop in the real corpus ended in a fragment, and none of the
    # fixtures did — so the period detector passed this self-test and found
    # nothing at all in the wild. Two of these now stop mid-unit on purpose.
    truncated = "Actually, the age package uses Recipient. " * 14 + "Actually, the age package uses"
    cycle = ("We protect the rows. We protect the flags. We protect the query. " * 9) + "We protect the"

    # A real answer made of aider SEARCH/REPLACE blocks: the filename heads every
    # one of them. This was the tool's only false positive on a real corpus.
    editblocks = "Here are the changes:\n\n" + "\n\n".join(
        f"main.go\n```go\n<<<<<<< SEARCH\nold{i}\n=======\nnew{i}\n>>>>>>> REPLACE\n```"
        for i in range(1, 20)
    )

    cases = [
        ("aider edit blocks in an answer", editblocks, False),
        ("sentence loop", loop, True),
        ("loop cut off mid-sentence", truncated, True),
        ("multi-sentence cycle, cut off", cycle, True),
        ("word stutter", stutter, True),
        ("loop in the tail only", tail_loop, True),
        ("ordinary prose", prose, False),
        ("numbered list of similar items", listy, False),
        ("diff-like alternation", diffish, False),
    ]

    failures = 0
    for name, text, want in cases:
        block = Block("<self-test>", name, 0, "answer", text)
        found = analyze(block, args)
        got = bool(found)
        mark = "ok  " if got == want else "FAIL"
        if got != want:
            failures += 1
        detail = ", ".join(f"{f.detector}={f.severity:g}" for f in found) or "nothing"
        print(f"{mark} {name:32} want={'loop' if want else 'clean':5} got: {detail}")

    print()
    if failures:
        print(f"{failures} self-test case(s) failed. Do not trust a scan until these pass.")
        return 2
    print("Detectors discriminate on all cases. A zero from a scan is now worth something.")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
