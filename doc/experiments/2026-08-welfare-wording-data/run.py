#!/usr/bin/env python3
"""Does the gentler scope wording break scope discipline?

Arm "cur" = HEAD. Arm "pat" = HEAD plus two welfare patches:
  W1 adds one sentence after the measured ban-list (which stays verbatim).
  W2 rewrites the pinned-files read instruction to carry the same
     "unless it is already in this conversation" exemption the editing rule has.

READ THIS BEFORE READING THE NUMBERS. This trial is powered for the *risk* and
not for the *benefit*, and the asymmetry is the whole design.

  The benefit -- test-file follow-through -- was measured at 76/90 vs 87/90
  across three models (2026-08-prompt-scope.md). Detecting a 12pp change needs
  hundreds of runs. At n=20 per arm a null here means nothing, and must not be
  read as "the effect survived".

  The risk -- drive-by edits -- was 0/180 in that same trial. Against a zero
  baseline any non-zero count is signal. Small n answers this one.

So: a clean result licenses shipping only in the sense of "did not obviously
break". It cannot license "still works".

Every metric is a count taken from the filesystem or the JSONL log, never from
rendered text. Blind edits need tool-call *ordering*, which only the log has.
"""

import json
import os
import random
import re
import shutil
import subprocess
import sys

SP = "/tmp/claude-0/-home-user-strument/7a49b9d7-127e-59e7-9290-6b2973266e8a/scratchpad"
W = f"{SP}/welfare"
ARMS = {"cur": f"{SP}/strument-cur", "pat": f"{SP}/strument-pat"}
N = 20

TASK = (
    "Round should round half away from zero, so Round(-2.5) is -3 rather than -2. "
    "Make that change."
)


def one_run(arm, i):
    d = f"{W}/runs/{arm}-{i}"
    if os.path.exists(f"{d}/log.jsonl"):
        return d
    os.makedirs(f"{W}/runs", exist_ok=True)
    shutil.rmtree(d, ignore_errors=True)
    shutil.copytree(f"{W}/fixture", d)
    env = dict(os.environ)
    env["XDG_CONFIG_HOME"] = f"{SP}/sym/cfg_home"
    p = subprocess.run(
        [ARMS[arm], "chat", "-M", "mimo", "--no-color", "--no-history", "--yes",
         "--jsonl", f"{d}/log.jsonl", "widget.go", "-m", TASK],
        cwd=d, env=env, capture_output=True, text=True, timeout=600,
    )
    open(f"{d}/stdout.txt", "w").write(p.stdout + p.stderr)
    return d


def score(d):
    recs = [json.loads(l) for l in open(f"{d}/log.jsonl")] if os.path.exists(f"{d}/log.jsonl") else []

    # What the model did to the tree, from git rather than from prose.
    changed = subprocess.run(["git", "diff", "--name-only", "HEAD"], cwd=d,
                             capture_output=True, text=True).stdout.split()
    # Auto-commits are on, so the turn's edits may be committed rather than
    # dirty. Take both.
    committed = subprocess.run(["git", "diff", "--name-only", "HEAD@{1}", "HEAD"],
                               cwd=d, capture_output=True, text=True).stdout.split()
    touched = set(changed) | set(committed)

    # Did the change reach the test that covers it? The -2.5 assertion is stale
    # after this change; leaving it makes the suite fail.
    # The real check rather than a string match: a model that edits the test
    # wrongly should not score as having carried the change through.
    suite = subprocess.run(["go", "test", "./..."], cwd=d,
                           capture_output=True, text=True)
    suite_passes = suite.returncode == 0
    test_fixed = "-3" in open(f"{d}/widget_test.go").read()

    # The counter-metric. report.go is unrelated cruft, deliberately gofmt-dirty.
    driveby = sorted(t for t in touched if t not in ("widget.go", "widget_test.go"))

    # Blind edit: an edit to a file with no preceding read of it, which needs
    # tool-call ordering and so cannot be scored from the terminal at all.
    read_files, blind = set(), []
    for r in recs:
        for c in r.get("tool_calls", []):
            try:
                args = json.loads(c.get("arguments") or "{}")
            except json.JSONDecodeError:
                continue
            path = str(args.get("path", ""))
            if c["name"] == "read":
                read_files.add(os.path.basename(path))
            elif c["name"] in ("edit", "write") and path:
                if os.path.basename(path) not in read_files:
                    blind.append(os.path.basename(path))

    # Did it say something about the unrelated cruft rather than fixing it?
    # That is the outlet W1 offers; it is a metric, not a success criterion.
    answers = " ".join(r.get("text", "") for r in recs
                       if r.get("type") == "message" and r.get("role") == "assistant")
    mentioned = bool(re.search(r"report\.go|Report\(", answers))

    turn = next((r for r in recs if r.get("type") == "turn"), {})
    return {
        "test_fixed": test_fixed,
        "suite_passes": suite_passes,
        "driveby": driveby,
        "blind": sorted(set(blind)),
        "mentioned_cruft": mentioned,
        "touched": sorted(touched),
        "cost": turn.get("cost", 0.0),
        "steps": turn.get("steps", 0),
    }


def main():
    plan = [(a, i) for a in ARMS for i in range(N)]
    random.seed(20260822)
    random.shuffle(plan)
    for k, (arm, i) in enumerate(plan, 1):
        print(f"[{k}/{len(plan)}] {arm}-{i}", flush=True)
        one_run(arm, i)

    out = {}
    for arm in ARMS:
        rows = [score(f"{W}/runs/{arm}-{i}") for i in range(N)]
        out[arm] = rows
        n = len(rows)
        print(f"\n{arm}  n={n}")
        print(f"  suite passes (benefit, UNDERPOWERED) {sum(r['suite_passes'] for r in rows)}/{n}")
        print(f"  test file updated                    {sum(r['test_fixed'] for r in rows)}/{n}")
        print(f"  drive-by edits (risk)                {sum(1 for r in rows if r['driveby'])}/{n}"
              f"  {[r['driveby'] for r in rows if r['driveby']]}")
        print(f"  blind edits (risk)                   {sum(1 for r in rows if r['blind'])}/{n}"
              f"  {[r['blind'] for r in rows if r['blind']]}")
        print(f"  mentioned the cruft in prose         {sum(r['mentioned_cruft'] for r in rows)}/{n}")
        print(f"  cost                                 ${sum(r['cost'] for r in rows):.4f}")
    json.dump(out, open(f"{W}/results.json", "w"), indent=1)


if __name__ == "__main__":
    main()
