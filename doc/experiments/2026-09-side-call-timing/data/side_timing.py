"""How long do the commit-message and chat-summary side calls actually take?

Companion to notes_timing.py, which measured the third side call. The two here
differ from notes in the way that matters for a budget: notes has a hard input
cap (maxNotesInput = 24,000 chars), and these two do not.

  - The commit message is chatContext + "# Diffs:\n" + diffs. Only the prior
    turns are bounded (maxCommitHistory = 8000) and only tool-call *arguments*
    are clipped (maxCommitArgs = 300); the current turn's tool *results* and the
    diff itself go in whole. So the input scales with the turn.
  - The chat summary is renderForSummary(head), where the head is bounded by
    the side model's own context window (sideInputBound - 512) and, upstream, by
    the main model's history budget, max(context/8, 2048). A 200k-context main
    model compacts at 25k tokens; a 1M-context one at 125k.

Both are therefore measured at several input sizes, because a single number
would only describe whichever size was picked. Real commit diffs from this
repository, not synthetic ones — a diff's shape (hunk headers, repeated
context lines) is not something to guess at when it is a `git show` away.

Timed to first byte, to completion, and by largest inter-byte gap, for the same
reason as notes: the design question is whether to bound total duration or the
gap between bytes, and only the second distinguishes a slow call from a stalled
one.

Reasoning follows the code, not convenience: the commit call passes no
ReasoningEffort (commit.go says why — the user is waiting for their prompt
back), the summary call passes the side model's.
"""

import json
import os
import pathlib
import random
import sys
import time
import urllib.error
import urllib.request

KEY = os.environ["OPENROUTER_API_KEY"]
HERE = pathlib.Path(__file__).parent

# Verbatim from internal/prompts/prompts.go, with {language_instruction}
# resolved to "" as it is when no commit language is configured.
COMMIT_SYSTEM = (
    "Write the Git commit message for the changes below. "
    "You are given the request that prompted them, the work that followed, and the diff.\n\n"
    "Earlier turns are background. Take the reason for this change from them if it is "
    "there, and describe only what the diff does — work from an earlier turn is not part "
    "of this commit.\n\n"
    'The subject is one line, in the form "type(scope): description", '
    'e.g. "fix(workspace): stop counting cache writes twice".\n'
    "- Use feat for a new capability and fix for a bug. build, chore, ci, docs, perf, "
    "refactor, style, and test are also conventional.\n"
    "- The scope names the part of the codebase the change is confined to. Leave it out "
    "when the change spans several.\n"
    '- Imperative mood ("add feature", not "added" or "adding"), no trailing period, '
    "under 72 characters.\n"
    '- If the change breaks existing behavior, put "!" before the colon.\n\n'
    "A body is optional and usually empty. Add one only for something the diff cannot "
    "say: why this approach, what was rejected, the constraint or measurement behind the "
    "choice. The diff already says what changed, so a body that restates it is noise. "
    "When there is a body, leave one blank line after the subject. If the change breaks "
    "existing behavior, include a paragraph starting \"BREAKING CHANGE: \" saying what "
    "breaks.\n\n"
    "Reply with the commit message and nothing else — no preamble, no quotes, no code fence.\n"
)

SUMMARIZE = (
    "Briefly summarize this partial conversation about programming. "
    "Give more detail to the most recent messages and less to the older ones. "
    "Start a new paragraph whenever the topic changes.\n\n"
    "This is only part of a longer conversation, so don't end with a wrap-up phrase "
    'like "Finally, ..."; the conversation continues after your summary.\n\n'
    "Include the function, library, and package names under discussion, along with the "
    "filenames the assistant references inside fenced code blocks. Leave the fenced code "
    "blocks themselves out of the summary.\n\n"
    "Keep any reason the user gave for a decision, in their own terms. A choice can be "
    "read back from the code; the reason for it cannot.\n\n"
    'Do not attribute actions to anyone — no "I", no "you", no "the assistant". '
    "Say what happened."
)

MAX_COMMIT_HISTORY = 8000  # internal/coder/commit.go
SUMMARY_TOOL_BYTES = 2000  # internal/coder/summary.go
MAX_NOTES_INPUT = 24_000  # internal/coder/notes.go

# Carried over from notes_timing.py so the third side call is measured on the
# same instrument as the other two. Its earlier numbers were taken with the
# content-gap version and are not comparable with these.
SESSION_NOTES = (
    "Write notes on a programming session, to be read at the start of the next "
    "one by someone who was not present.\n\n"
    "Cover only what cannot be recovered from the code and its history:\n"
    "- What the work was for.\n"
    "- Decisions and their reasons, including approaches that were tried and abandoned.\n"
    "- Constraints or preferences the user stated.\n"
    "- What was in progress, and what came next.\n\n"
    "The record below already lists which files changed, and the reader can see the code. "
    "Do not restate the diff.\n\n"
    'Do not attribute actions to anyone — no "I", no "you", no "the assistant". Say what '
    "happened. Keep it under 300 words. Say less rather than guess: leave a thing out before "
    "inventing it.\n\n"
    "Reply with the notes and nothing else — no preamble, no heading, no code fence."
)

MODELS = [
    "deepseek/deepseek-v4.1-flash",  # the one that failed in the field
    "xiaomi/mimo-v2.5",
    "deepseek/deepseek-v4-flash-0731",
    "z-ai/glm-5.3-flash",
]
REPS = 3

# Real repository text, used as the tool results a turn's history is mostly
# made of. Reading files is what a turn does, and a read's whole result goes
# into the commit context uncut.
SOURCES = [
    "internal/coder/side.go",
    "internal/coder/summary.go",
    "internal/coder/commit.go",
    "internal/coder/notes.go",
    "internal/coder/send.go",
    "internal/client/client.go",
    "internal/prompts/prompts.go",
]


def repo_text():
    root = pathlib.Path("/home/user/strument")
    out = []
    for name in SOURCES:
        p = root / name
        if p.exists():
            out.append((name, p.read_text()))
    return out


FILES = repo_text()


def commit_context(n):
    """A renderCommitMessages-shaped history of about n characters.

    USER/ASSISTANT blocks with CALL lines, and TOOL blocks holding whole file
    contents — which is what they hold in the real thing, since only arguments
    are clipped.
    """
    parts = []
    size = 0
    i = 0
    while size < n:
        name, body = FILES[i % len(FILES)]
        block = (
            f"\nUSER: Look at {name} and tell me whether the retry ladder can "
            f"finish inside the deadline it runs under.\n"
            f"\nASSISTANT: Reading it now.\n"
            f"CALL: read {{\"path\": \"{name}\"}}\n"
            f"\nTOOL: {body}\n"
            f"\nASSISTANT: The sleeps total more than the budget, so the last "
            f"rung is unreachable. Lowering the cap fixes it.\n"
            f"CALL: edit {{\"path\": \"{name}\", \"old\": \"cap: retryTimeout\", "
            f"\"new\": \"cap: sideRetryCap\"}}\n"
        )
        parts.append(block)
        size += len(block)
        i += 1
    return "".join(parts)[:n]


def clip(s):
    if len(s) <= SUMMARY_TOOL_BYTES:
        return s
    return s[:SUMMARY_TOOL_BYTES] + "\n… (cut; the full result is not part of the summary input)"


def summary_input(n):
    """A renderForSummary-shaped history of about n characters.

    Tool results clipped at summaryToolBytes, as renderForSummary clips them;
    that is the reason a summary input of a given size holds many more messages
    than a commit context of the same size.
    """
    parts = []
    size = 0
    i = 0
    while size < n:
        name, body = FILES[i % len(FILES)]
        block = (
            f"# USER\nWork out whether {name} handles a deadline that expires "
            f"mid-retry, and say what it does instead.\n"
            f"# ASSISTANT\nReading {name}.\n"
            f"calls read {{\"path\": \"{name}\"}}\n"
            f"# TOOL\n{clip(body)}\n"
            f"# ASSISTANT\nThe context error is not a StreamError, so errors.As "
            f"fails and the call is treated as non-retryable. That is the bug.\n"
            f"calls edit {{\"path\": \"{name}\"}}\n"
        )
        parts.append(block)
        size += len(block)
        i += 1
    return "".join(parts)[:n]


def timed(model, system, user, reasoning, salt):
    # The salt leads the user message, and it is not cosmetic. Sending the same
    # prompt three times measures one cold call and two prefill-cache hits:
    # DeepSeek caches automatically, and in the first run of this probe the
    # second rep of commit/mid reached its first byte in 1.4s where the first
    # took 4.3s. A budget sized on that would be sized on a hit rate no real
    # commit call has — every commit has a different diff. Changing the first
    # bytes of the prompt makes every call a cold one.
    user = f"(run {salt})\n" + user
    payload = {
        "model": model,
        "messages": [
            {"role": "system", "content": system},
            {"role": "user", "content": user},
        ],
        "stream": True,
        "usage": {"include": True},
    }
    if reasoning is not None:
        payload["reasoning"] = reasoning
    req = urllib.request.Request(
        "https://openrouter.ai/api/v1/chat/completions",
        data=json.dumps(payload).encode(),
        headers={"Authorization": "Bearer " + KEY, "Content-Type": "application/json"},
    )
    start = time.time()
    first_content = None
    first_wire = None
    longest_gap = 0.0
    last = start
    chars = 0
    reasoning_chars = 0
    prompt_tokens = None
    cached_tokens = None
    with urllib.request.urlopen(req, timeout=600) as r:
        for raw in r:
            # Every line off the socket is bytes, and bytes are what the thing
            # being sized here actually watches: idleReader.Read resets its
            # timer on any n > 0 (internal/client/idle.go), so a keepalive
            # comment, a reasoning delta and a content token are the same event
            # to it. An earlier version of this function timed gaps between
            # *content* tokens only, and reported a 93.4s silence for a
            # reasoning model that had in fact been streaming reasoning the
            # whole time — a measurement that would have argued for a much
            # longer idle timeout than the real stream needs.
            now = time.time()
            if first_wire is None:
                first_wire = now - start
            longest_gap = max(longest_gap, now - last)
            last = now

            line = raw.decode("utf-8", "replace").strip()
            if not line or not line.startswith("data: "):
                continue
            if line == "data: [DONE]":
                break
            try:
                d = json.loads(line[6:])
            except json.JSONDecodeError:
                continue
            if d.get("usage"):
                prompt_tokens = d["usage"].get("prompt_tokens")
                # Recorded so a cache hit cannot hide inside a fast number.
                cached_tokens = (d["usage"].get("prompt_tokens_details") or {}).get(
                    "cached_tokens"
                )
            delta = (d.get("choices") or [{}])[0].get("delta") or {}
            reasoning_chars += len(delta.get("reasoning") or "")
            piece = delta.get("content") or ""
            if piece:
                if first_content is None:
                    first_content = time.time() - start
                chars += len(piece)
    return {
        "total": time.time() - start,
        "ttfb": first_content,
        "ttfw": first_wire,
        "gap": longest_gap,
        "chars": chars,
        "reasoning_chars": reasoning_chars,
        "prompt_tokens": prompt_tokens,
        "cached_tokens": cached_tokens,
    }


def arms():
    """The five input sizes, each a case the code can actually produce."""
    small = (HERE / "diff_small.txt").read_text()
    mid = (HERE / "diff_mid.txt").read_text()
    big = (HERE / "diff_big.txt").read_text()

    # Every arm sends no reasoning field at all, because that is what Strument
    # sends. All three side calls leave llm.Request.ReasoningEffort at "", and
    # client.go's switch treats "" as "defer to the provider default; send
    # nothing" — it does not disable reasoning. An earlier version of this
    # probe forced reasoning:{enabled:false} on the notes and commit arms,
    # which is a request the code never makes: it 400s outright on
    # glm-5.3-flash ("Reasoning is mandatory for this endpoint") and, on a
    # model that honours it, measures a faster call than the real one.
    out = []
    # Session notes: the one call with a hard input cap.
    out.append(("notes/capped", SESSION_NOTES, summary_input(MAX_NOTES_INPUT), None))

    for label, hist, diff in [
        ("commit/small", 12_000, small),
        ("commit/mid", 30_000, mid),
        ("commit/big", 80_000, big),
    ]:
        user = commit_context(hist) + "\n" + "# Diffs:\n" + diff
        out.append((label, COMMIT_SYSTEM, user, None))

    for label, n in [("summary/200k-ctx", 50_000), ("summary/1m-ctx", 400_000)]:
        out.append((label, SUMMARIZE, summary_input(n), None))
    return out


def main():
    plan = arms()
    print("input sizes:")
    for label, system, user, _ in plan:
        print(f"  {label:18s} {len(system) + len(user):8d} chars")
    print()

    # Randomized order, per the handbook: running every model's arms in a block
    # confounds the arm with the minute it ran, and providers drift across that
    # window.
    jobs = [(label, s, u, r, rep) for (label, s, u, r) in plan for rep in range(REPS) for _ in [0]]
    jobs = [(m, *j) for m in MODELS for j in jobs]
    random.seed(20260920)
    random.shuffle(jobs)

    print(
        f"{'arm':18s} {'model':32s} {'in tok':>7s} {'cached':>7s} "
        f"{'total':>7s} {'wire1':>7s} {'text1':>7s} {'max gap':>8s} {'out':>6s} {'think':>6s}"
    )
    results = []
    # The run id makes the salt unique per *run*, not only per call. A salt of
    # "{arm}-{rep}-{n}" alone is reproducible, which is the wrong property here:
    # rerunning the probe re-sent prompts the provider had already cached, and
    # one call came back with 11,674 of its 11,675 prompt tokens cached.
    run_id = f"{time.time():.3f}"
    for n, (model, label, system, user, reasoning, rep) in enumerate(jobs):
        try:
            r = timed(model, system, user, reasoning, f"{run_id}-{label}-{rep}-{n}")
        except urllib.error.HTTPError as e:
            print(f"{label:18s} {model:32s}  HTTP {e.code}: {e.read().decode()[:90]}")
            continue
        except Exception as e:  # noqa: BLE001
            print(f"{label:18s} {model:32s}  {e!r}")
            continue
        r.update(model=model, arm=label, rep=rep)
        results.append(r)
        ttfb = f"{r['ttfb']:.1f}s" if r["ttfb"] is not None else "-"
        ttfw = f"{r['ttfw']:.1f}s" if r["ttfw"] is not None else "-"
        pt = r["prompt_tokens"] if r["prompt_tokens"] is not None else -1
        ct = r["cached_tokens"] if r["cached_tokens"] is not None else -1
        print(
            f"{label:18s} {model:32s} {pt:7d} {ct:7d} {r['total']:6.1f}s {ttfw:>7s} {ttfb:>7s} "
            f"{r['gap']:7.1f}s {r['chars']:6d} {r['reasoning_chars']:6d}"
        )
        with (HERE / "side_timing.jsonl").open("a") as f:
            f.write(json.dumps(r) + "\n")

    print("\n=== worst case per arm (across all models)")
    for label, *_ in plan:
        rs = [r for r in results if r["arm"] == label]
        if not rs:
            continue
        print(
            f"{label:18s} slowest {max(x['total'] for x in rs):6.1f}s   "
            f"slowest first byte {max(x['ttfb'] or 0 for x in rs):5.1f}s   "
            f"largest gap {max(x['gap'] for x in rs):5.1f}s   "
            f"n={len(rs)}"
        )

    print("\n=== worst case per arm and model")
    for label, *_ in plan:
        for m in MODELS:
            rs = [r for r in results if r["arm"] == label and r["model"] == m]
            if not rs:
                continue
            print(
                f"{label:18s} {m:32s} slowest {max(x['total'] for x in rs):6.1f}s   "
                f"largest gap {max(x['gap'] for x in rs):5.1f}s   n={len(rs)}"
            )


if __name__ == "__main__":
    sys.exit(main())
