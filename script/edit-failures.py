#!/usr/bin/env python3
"""Classify what goes wrong with the edit tool, from Strument's JSONL logs.

The question this exists to answer is whether Strument needs a patch tool.
The argument for one is that a model can express a coordinated multi-file
mutation that `edit` makes awkward. That is a claim about *failures*, and the
logs already record every one of them, so it can be measured instead of
debated. See doc/experimenting.md; the point of counting is to avoid being
both author and judge.

Five classes, and each implies a different fix:

  not-found      the search text was not in the file. Better anchoring, or a
                 stale idea of the file. This is the one a version guard would
                 catch, so its rate is the case for adding one.
  ambiguous      the text was there several times. `replace_all` is the
                 feature that would answer this, if it were worth the cost.
  already-done   the replacement was already present. The model is redoing
                 work: a planning problem, not an editing one.
  refused        containment, a read-only pin, .git, or a gitignore rule said
                 no. A policy outcome, not a defect.
  rolled-back    the batch reached the disk and failed there.

And two shapes rather than failures, which are what a patch tool would
actually change:

  spread         edits in one assistant message touching several files. This
                 is the coordinated mutation, and `edit` already expresses it.
  interleave     a turn that edits file X, then some other file, then X
                 again. This is the shape a patch tool collapses, so a high
                 rate here is the real evidence for adding one.

                 Counting every return to an already-edited file was the first
                 version and it was wrong: three sequential edits to one file
                 scored two "revisits" and looked like coordination pressure,
                 when it is just a model making three changes in a row. A patch
                 would not collapse that. Only a return *across* another file
                 counts now.

Exercised against real recorded output, not just written: a fixture provider
drives the edit tool through one failure of each kind and the log it produces
is fed back through this. That is how the ambiguity bug in coder/tools.go was
found — the fixture aimed at that class and the harness reported success. Two
branches, already-done and rolled-back, are not covered by that fixture and are
matched on their message text alone.

Usage:
    script/edit-failures.py LOG.jsonl [LOG.jsonl ...]
    script/edit-failures.py --json ~/.local/state/strument/**/*.jsonl
    script/edit-failures.py --show not-found LOG.jsonl
"""

import argparse
import collections
import json
import os
import sys

CLASSES = ["not-found", "ambiguous", "already-done", "refused", "rolled-back", "ok"]

# The tool results these come from are written in coder/tools.go; the phrases
# are matched rather than a status code because the result the model reads is
# the only record there is.
MARKERS = [
    ("rolled-back", "whole batch was rolled back"),
    ("ambiguous", "so it is ambiguous"),
    ("already-done", "may not be needed"),
    ("not-found", "search text was not found"),
    ("refused", "Skipped "),
]


def classify(result):
    for name, marker in MARKERS:
        if marker in result:
            return name
    return "ok"


def read_log(path):
    """Yield (kind, payload) for the records this cares about."""
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                continue
            yield rec


def scan(path):
    """Return per-call classifications and the per-message edit shape."""
    calls = {}       # tool_call_id -> {"path": ..., "tool": ...}
    rows = []        # (class, tool, path, result, call_id)
    spread = []      # files touched per assistant message that edited
    interleaved = 0  # returns to a file after editing a different one
    seen_files = set()
    last_file = None

    for rec in read_log(path):
        if rec.get("type") == "message" and rec.get("role") == "assistant":
            batch = []
            for tc in rec.get("tool_calls") or []:
                if tc.get("name") not in ("edit", "write"):
                    continue
                try:
                    args = json.loads(tc.get("arguments") or "{}")
                except json.JSONDecodeError:
                    args = {}
                p = args.get("path", "?")
                calls[tc.get("id")] = {"path": p, "tool": tc["name"]}
                batch.append(p)
            if batch:
                spread.append(len(set(batch)))
                for p in batch:
                    if p in seen_files and last_file is not None and p != last_file:
                        interleaved += 1
                    seen_files.add(p)
                    last_file = p
        elif rec.get("type") == "message" and rec.get("role") == "tool":
            info = calls.get(rec.get("tool_call_id"))
            if not info:
                continue
            text = rec.get("text", "")
            rows.append((classify(text), info["tool"], info["path"], text, rec.get("tool_call_id")))

    return rows, spread, interleaved


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("logs", nargs="+")
    ap.add_argument("--json", action="store_true", help="machine-readable summary")
    ap.add_argument("--show", metavar="CLASS", help="print the results in one class")
    args = ap.parse_args()

    counts = collections.Counter()
    by_tool = collections.Counter()
    spread_all = []
    interleaved_all = 0
    shown = []
    files_seen = 0

    for path in args.logs:
        if not os.path.exists(path):
            print(f"{path}: no such file", file=sys.stderr)
            continue
        files_seen += 1
        rows, spread, interleaved = scan(path)
        spread_all.extend(spread)
        interleaved_all += interleaved
        for cls, tool, p, text, _ in rows:
            counts[cls] += 1
            by_tool[(tool, cls)] += 1
            if args.show and cls == args.show:
                shown.append((path, p, text))

    total = sum(counts.values())
    multi = sum(1 for n in spread_all if n > 1)

    if args.json:
        print(json.dumps({
            "logs": files_seen,
            "edit_calls": total,
            "classes": dict(counts),
            "messages_with_edits": len(spread_all),
            "messages_touching_several_files": multi,
            "max_files_in_one_message": max(spread_all) if spread_all else 0,
            "interleaved_returns": interleaved_all,
        }, indent=2))
        return

    if args.show:
        for path, p, text in shown:
            print(f"--- {os.path.basename(path)}  {p}")
            print(text.rstrip())
            print()
        print(f"{len(shown)} result(s) in class {args.show!r}")
        return

    print(f"{files_seen} log(s), {total} edit/write call(s)\n")
    if not total:
        print("Nothing to classify.")
        return
    for cls in CLASSES:
        n = counts.get(cls, 0)
        if n:
            print(f"  {cls:14} {n:5}  {100 * n / total:5.1f}%")
    print()
    print("Coordination, which is what a patch tool would change:")
    print(f"  messages that edited                {len(spread_all):5}")
    print(f"  ... touching more than one file     {multi:5}"
          f"  ({100 * multi / len(spread_all):.0f}%)" if spread_all else "")
    print(f"  most files in one message           {max(spread_all) if spread_all else 0:5}")
    print(f"  returns to a file across another    {interleaved_all:5}")
    print()
    print("Read this as: a high not-found rate argues for a read-version guard;")
    print("a high ambiguous rate argues for replace_all; a high interleave count is")
    print("the case for a patch tool. Anything else argues for leaving edit alone.")


if __name__ == "__main__":
    main()
