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


CODE_FENCE = re.compile(r"^\s*(```|~~~)")


def strip_code_blocks(text: str) -> str:
    """Drop fenced code and table rows.

    Not squeamishness: a table, a long list, and generated code legitimately
    repeat, and Gemini CLI resets its detector on exactly these for the same
    reason. Leaving them in makes the detector loudest on the output most likely
    to be correct.
    """
    out, in_fence = [], False
    for line in text.split("\n"):
        if CODE_FENCE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        if line.lstrip().startswith("|"):
            continue
        out.append(line)
    return "\n".join(out)


def read_aider(path: str) -> list[Block]:
    """aider's chat history: '#### ' is the user, '> ' is aider, rest is model."""
    blocks: list[Block] = []
    session = "(before any session header)"
    current: list[str] = []

    def flush() -> None:
        if current:
            body = "\n".join(current).strip()
            if body:
                blocks.append(Block(path, session, len(blocks), "answer", body))
            current.clear()

    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            line = line.rstrip("\n")
            if line.startswith("# aider chat started at"):
                flush()
                session = line[2:].strip()
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

    def flush() -> None:
        nonlocal in_response
        if current:
            body = "\n".join(current).strip()
            if body:
                blocks.append(Block(path, session, len(blocks), "answer", body))
            current.clear()
        in_response = False

    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            line = line.rstrip("\n")
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
                blocks.append(Block(path, session, len(blocks), kind, text))
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


def sentences(text: str) -> list[str]:
    out = []
    for piece in SENTENCE_SPLIT.split(text):
        piece = " ".join(piece.split())
        if piece:
            out.append(piece)
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


def detect_period(units: list[str], min_repeats: int, max_tail: int) -> Finding | None:
    """The longest suffix that is one unit sequence repeated min_repeats+ times.

    Suffixes, not the whole block, because the failure mode is a response that
    starts fine and never stops. Requiring the *whole* block to be periodic
    would find almost nothing.
    """
    n = len(units)
    best: Finding | None = None
    for length in range(2, min(n, max_tail) + 1):
        tail = units[n - length :]
        p = minimal_period(tail)
        if p == 0 or length % p or length // p < min_repeats:
            continue
        repeats = length // p
        if best is None or repeats > best.severity:
            unit = " ".join(tail[:p])
            best = Finding(
                "period",
                repeats,
                f"the last {length} sentences are one block of {p} repeated {repeats}x",
                unit[:300],
            )
    return best


def detect_run(units: list[str], min_run: int) -> Finding | None:
    """Longest run of consecutive identical sentences."""
    best_len, best_unit = 0, ""
    run_len, prev = 0, None
    for u in units:
        if u == prev:
            run_len += 1
        else:
            run_len, prev = 1, u
        if run_len > best_len:
            best_len, best_unit = run_len, u
    if best_len < min_run:
        return None
    return Finding("run", best_len, f"one sentence repeated {best_len}x in a row", best_unit[:300])


def detect_word_run(text: str, min_run: int) -> Finding | None:
    """Longest run of one repeated word.

    The oldest and most literal degeneration — "the the the the" — and the
    sentence detectors cannot see it, because the whole stutter is one sentence
    with no terminator. Found by the self-test, which is what the self-test is
    for.
    """
    words = re.findall(r"\w+", text.lower())
    best_len, best_word = 0, ""
    run_len, prev = 0, None
    for w in words:
        run_len = run_len + 1 if w == prev else 1
        prev = w
        if run_len > best_len:
            best_len, best_word = run_len, w
    if best_len < min_run:
        return None
    return Finding("word", best_len, f'the word "{best_word}" repeats {best_len}x in a row', best_word)


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
            )
    return best


# -------------------------------------------------------------------- report


@dataclass
class Scan:
    blocks: int = 0
    chars: int = 0
    files: int = 0
    findings: list[dict] = field(default_factory=list)


def analyze(block: Block, args) -> list[Finding]:
    text = block.text if args.keep_code else strip_code_blocks(block.text)
    text = text.strip()
    # A word stutter is a finding at any length; the others need enough text to
    # tell repetition from ordinary structure.
    word = detect_word_run(text, args.min_word_run)
    if len(text) < args.min_chars:
        return [word] if word else []
    units = sentences(text)
    out = []
    for f in (
        word,
        detect_run(units, args.min_run),
        detect_period(units, args.min_repeats, args.max_tail),
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
            for f in analyze(b, args):
                rec = {"file": b.path, "session": b.session, "block": b.index, "kind": b.kind, **asdict(f)}
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
        print(f"{path}  block {index} ({kind})  [{session}]")
        for r in sorted(rows, key=lambda x: -x["severity"]):
            print(f"  {r['detector']}: {r['detail']}")
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

    cases = [
        ("sentence loop", loop, True),
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
