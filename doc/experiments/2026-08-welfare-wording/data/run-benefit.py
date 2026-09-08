#!/usr/bin/env python3
"""Benefit trial: does the added scope sentence weaken the reach effect?

PRE-REGISTERED BEFORE ANY RUN. Written down first precisely because the
temptation to read a promising partial result and stop is what makes a
sequential peek dishonest.

Hypothesis. The shipped prompt ("pat" = W1 + W2) does not reduce test-file
follow-through relative to the previous one ("cur"). W1 appends a sentence after
the measured ban-list; the risk is that "if you notice something worth changing,
say so instead" is misread as applying to the stale test, which IS in scope --
the reach clause names "the tests that cover it" explicitly.

Design.
  n = 150 per arm, MiMo V2.5, fixed in advance. NO early stopping, no peeking
  at partial counts to decide whether to continue.
  Arms run interleaved and concurrently, which removes the time confound more
  thoroughly than sequential randomization does: both arms see the same
  provider weather.

Power. The fixture's baseline is ~45%, not the original trial's 97%. That is a
better operating point, not a worse one -- a ceiling leaves no room to fall.
At 45% and n=150/arm, a two-sided test has ~80% power for a 16-17 point
difference. It is still blind to a 5-point erosion, and that limit is stated
here rather than discovered later.

Primary metric: `go test` exit status. Not a string match -- a model that edits
the test wrongly must not score as having carried the change through.

Counter-metrics, reported at the same size: drive-by edits, blind edits, cost,
steps.

Decision rule, fixed now: if pat is significantly worse (two-sided Fisher
p < 0.05 with pat < cur), revert W1 and decompose. Otherwise keep the patch and
report the interval.
"""

import json
import os
import random
import re
import shutil
import subprocess
from concurrent.futures import ThreadPoolExecutor

SP = "/tmp/claude-0/-home-user-strument/7a49b9d7-127e-59e7-9290-6b2973266e8a/scratchpad"
W = f"{SP}/welfare2"
FIXTURE = f"{SP}/welfare/fixture"
ARMS = {"cur": f"{SP}/strument-cur", "pat": f"{SP}/strument-pat"}
N = 150
WORKERS = 12

TASK = (
    "Round should round half away from zero, so Round(-2.5) is -3 rather than -2. "
    "Make that change."
)


def one_run(job):
    arm, i = job
    d = f"{W}/runs/{arm}-{i}"
    if os.path.exists(f"{d}/log.jsonl"):
        return d
    shutil.rmtree(d, ignore_errors=True)
    shutil.copytree(FIXTURE, d)
    env = dict(os.environ)
    env["XDG_CONFIG_HOME"] = f"{SP}/sym/cfg_home"
    try:
        p = subprocess.run(
            [ARMS[arm], "chat", "-M", "mimo", "--no-color", "--no-history", "--yes",
             "--jsonl", f"{d}/log.jsonl", "widget.go", "-m", TASK],
            cwd=d, env=env, capture_output=True, text=True, timeout=600,
        )
        open(f"{d}/stdout.txt", "w").write(p.stdout + p.stderr)
    except subprocess.TimeoutExpired:
        open(f"{d}/stdout.txt", "w").write("TIMEOUT")
    return d


def score(d):
    log = f"{d}/log.jsonl"
    recs = [json.loads(l) for l in open(log)] if os.path.exists(log) else []

    touched = set()
    for args in (["git", "diff", "--name-only", "HEAD"],
                 ["git", "diff", "--name-only", "HEAD@{1}", "HEAD"]):
        touched |= set(subprocess.run(args, cwd=d, capture_output=True,
                                      text=True).stdout.split())

    suite = subprocess.run(["go", "test", "./..."], cwd=d, capture_output=True, text=True)
    driveby = sorted(t for t in touched if t not in ("widget.go", "widget_test.go"))

    read_files, blind = set(), []
    for r in recs:
        for c in r.get("tool_calls", []):
            try:
                a = json.loads(c.get("arguments") or "{}")
            except json.JSONDecodeError:
                continue
            path = os.path.basename(str(a.get("path", "")))
            if c["name"] == "read":
                read_files.add(path)
            elif c["name"] in ("edit", "write") and path and path not in read_files:
                blind.append(path)

    answers = " ".join(r.get("text", "") for r in recs
                       if r.get("type") == "message" and r.get("role") == "assistant")
    turn = next((r for r in recs if r.get("type") == "turn"), {})
    return {
        "suite_passes": suite.returncode == 0,
        "driveby": driveby,
        "blind": sorted(set(blind)),
        "mentioned_cruft": bool(re.search(r"report\.go|Report\(", answers)),
        "cost": turn.get("cost", 0.0),
        "steps": turn.get("steps", 0),
        # A run that never produced a turn record did not complete; it is not a
        # failure of the prompt and is counted separately rather than as one.
        "ran": bool(turn),
    }


def main():
    os.makedirs(f"{W}/runs", exist_ok=True)
    plan = [(a, i) for a in ARMS for i in range(N)]
    random.seed(20260822)
    random.shuffle(plan)
    done = 0
    with ThreadPoolExecutor(max_workers=WORKERS) as ex:
        for _ in ex.map(one_run, plan):
            done += 1
            if done % 25 == 0:
                print(f"{done}/{len(plan)}", flush=True)

    out = {a: [score(f"{W}/runs/{a}-{i}") for i in range(N)] for a in ARMS}
    json.dump(out, open(f"{W}/results.json", "w"), indent=1)
    for arm, rows in out.items():
        ok = [r for r in rows if r["ran"]]
        n = len(ok)
        print(f"\n{arm}  n={n} (of {len(rows)}; {len(rows)-n} did not complete)")
        print(f"  suite passes      {sum(r['suite_passes'] for r in ok)}/{n}")
        print(f"  drive-by edits    {sum(1 for r in ok if r['driveby'])}/{n}")
        print(f"  blind edits       {sum(1 for r in ok if r['blind'])}/{n}")
        print(f"  mentioned cruft   {sum(r['mentioned_cruft'] for r in ok)}/{n}")
        print(f"  mean steps        {sum(r['steps'] for r in ok)/max(n,1):.1f}")
        print(f"  cost              ${sum(r['cost'] for r in ok):.4f}")


if __name__ == "__main__":
    main()
