#!/usr/bin/env python3
"""Score the String.raw trial.

    score.py OUTDIR [--rows]

Per session, from done.json and the session log: the target file's outcome
(correct, unchanged, wrong), the run_code programs, whether any program called
edit() or write() (the mechanism fired), whether any used String.raw, how many
programs failed and how many of those on an escape. Then the table per arm,
model and task, and Fisher's exact test on correct sessions, pooled over the
three block tasks (the primary), the three line tasks of pilot 1, and the
counter-metric task.
"""

import glob
import json
import math
import os
import re
import sys

ESCAPE = re.compile(r"Octal escape sequences are not allowed|invalid escape: |are not allowed in template strings")
PROVIDER_ERROR = re.compile(r"HTTP [45]\d\d|Empty response received|rate_limit|stream ended|connection reset")
LINE_TASKS = ["c", "py", "sh"]
BLOCK_TASKS = ["cb", "pyb", "shb"]
COUNTER = "csv"


def blob(d, digest):
    hits = glob.glob(os.path.join(d, "state", "**", "blobs", digest), recursive=True)
    if not hits:
        raise FileNotFoundError(f"{d}: blob {digest} is missing")
    return open(hits[0], errors="replace").read()


def session(d):
    done = json.load(open(os.path.join(d, "done.json")))
    row = {k: done.get(k) for k in ("id", "arm", "task", "model", "order", "seconds", "status", "outcome")}
    programs, failed, escape, steps, cost, errors = [], 0, 0, 0, 0.0, 0
    for lg in glob.glob(os.path.join(d, "session-2*.jsonl")):
        for line in open(lg):
            r = json.loads(line)
            if r["type"] == "turn":
                steps = r.get("steps", 0)
            elif r["type"] == "request":
                cost += r.get("cost", 0) or 0
            elif r["type"] == "message" and r.get("role") == "assistant":
                for tc in r.get("tool_calls") or []:
                    if tc.get("name") != "run_code":
                        continue
                    try:
                        # Large arguments are stored as a blob; the log keeps
                        # only its hash and a cut summary.
                        args = json.loads(blob(d, tc["blob"]) if tc.get("blob") else tc.get("arguments") or "{}")
                    except ValueError:
                        args = {}
                    programs.append(args.get("code", "") if isinstance(args, dict) else "")
            elif r["type"] == "message" and r.get("role") == "tool":
                text = r.get("text") or (blob(d, r["blob"]) if r.get("blob") else "")
                if text.startswith("The program failed"):
                    failed += 1
                    escape += bool(ESCAPE.search(text))
    # A turn the provider cut short ends like any other turn (handbook §29).
    out = open(os.path.join(d, "stdout.txt"), errors="replace").read() + \
        open(os.path.join(d, "stderr.txt"), errors="replace").read()
    for m in PROVIDER_ERROR.finditer(out):
        if "Retrying" not in out[m.end():m.end() + 300]:
            errors += 1
    row.update(programs=len(programs),
               fired=any(re.search(r"\b(edit|write)\s*\(", p) for p in programs),
               raw=any("String.raw" in p for p in programs),
               raw_first=bool(programs) and "String.raw" in programs[0],
               failed=failed, escape=escape, steps=steps, cost=round(cost, 6), provider_errors=errors)
    return row


def fisher(a, b, c, d):
    """Two-sided Fisher's exact test on [[a, b], [c, d]]."""
    n, r1, c1 = a + b + c + d, a + b, a + c
    def p(x):
        return math.comb(r1, x) * math.comb(n - r1, c1 - x) / math.comb(n, c1)
    obs = p(a)
    lo, hi = max(0, c1 - (n - r1)), min(r1, c1)
    return min(1.0, sum(p(x) for x in range(lo, hi + 1) if p(x) <= obs * (1 + 1e-9)))


def main():
    outdir = sys.argv[1]
    rows = [session(d) for d in sorted(glob.glob(os.path.join(outdir, "*"))) if os.path.exists(os.path.join(d, "done.json"))]
    if "--rows" in sys.argv:
        for r in rows:
            print(json.dumps(r))
        return
    w = print
    w(f"{len(rows)} sessions\n")
    w("| arm | model | task | n | correct | wrong | unchanged | fired | String.raw | escape errors | failed programs |")
    w("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
    for arm in ("B", "R"):
        for model in sorted({r["model"] for r in rows}):
            for task in LINE_TASKS + BLOCK_TASKS + [COUNTER]:
                rs = [r for r in rows if (r["arm"], r["model"], r["task"]) == (arm, model, task)]
                if not rs:
                    continue
                c = lambda k, v=True: sum(1 for r in rs if r[k] == v)  # noqa: E731
                w(f"| {arm} | {model} | {task} | {len(rs)} | {c('outcome', 'correct')} | {c('outcome', 'wrong')} | "
                  f"{c('outcome', 'unchanged')} | {c('fired')} | {c('raw')} | {sum(r['escape'] for r in rs)} | "
                  f"{sum(r['failed'] for r in rs)} |")
    w("")
    for label, tasks in (("block tasks (primary)", BLOCK_TASKS), ("line tasks (pilot 1)", LINE_TASKS),
                         ("csv (counter-metric)", [COUNTER])):
        for model in sorted({r["model"] for r in rows}) + ["all"]:
            sel = lambda arm: [r for r in rows if r["arm"] == arm and r["task"] in tasks and model in ("all", r["model"])]  # noqa: E731
            b, rr = sel("B"), sel("R")
            if not b or not rr:
                continue
            bc, rc = sum(r["outcome"] == "correct" for r in b), sum(r["outcome"] == "correct" for r in rr)
            w(f"{label}, {model}: B {bc}/{len(b)}, R {rc}/{len(rr)}, p = {fisher(bc, len(b) - bc, rc, len(rr) - rc):.3g}")
    bad = [r["id"] for r in rows if r["status"] != "ok" or r["provider_errors"]]
    w(f"\nsessions with a timeout or an unretried provider error: {len(bad)} {bad}")


if __name__ == "__main__":
    main()
