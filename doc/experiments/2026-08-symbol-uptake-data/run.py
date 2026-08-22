#!/usr/bin/env python3
"""Does the improved symbol tool get chosen?

Arm A = strument-base (e1ea92f, symbol before the change).
Arm B = strument-new  (121e1a6, source lines + fields + honest miss).

Both arms run the same task against the same tree, so a difference in symbol
usage is a difference in *choice*: base-arm symbol answers this task fine, it
just answers with coordinates instead of code.

Order is randomized across the whole run. Running every A then every B
confounds the arm with the time it ran, and providers drift across that window
(doc/experiments/2026-08-prompt-scope.md: shuffling alone moved a baseline from
65% to 84%).
"""

import json
import os
import random
import re
import subprocess
import sys
import time

SP = "/tmp/claude-0/-home-user-strument/7a49b9d7-127e-59e7-9290-6b2973266e8a/scratchpad"
REPO = "/home/user/strument"
OUT = f"{SP}/sym/runs"

# Neutral: names neither tool. The model picks its own path.
TASK = (
    "Which non-test functions call settleEdits, and what does each one pass as "
    "the message argument? Keep it brief."
)

# Pre-registered answer key. The three production call sites, and what each
# passes -- verified by hand against the tree before any run.
CALLERS = {
    "runOne": "empty",
    "afterInterrupt": "empty",
    "runCommitTool": "args.message",
}

ARMS = {"base": f"{SP}/strument-base", "new": f"{SP}/strument-new"}
MODELS = {"mimo": 12, "glm": 6, "kimi": 6}


def score(text):
    """Counts, not judgments."""
    # The transcript prints one line per tool call; these are its verbs.
    symbol_calls = len(re.findall(r"^Looked up ", text, re.M))
    reads = len(re.findall(r"^Read ", text, re.M))
    greps = len(re.findall(r"^Searched for ", text, re.M))
    globs = len(re.findall(r"^(Matched|Listed) ", text, re.M))

    # The answer region. Two things must come out of it first, and a pilot run
    # settled which: tool *results* never reach the transcript (only the
    # one-line summary does), but the reasoning block does -- and reasoning is
    # not an answer. A model that works the callers out in ‹thinking› and then
    # says "I could not determine them" has not answered, and a scorer that
    # searched the whole transcript would score it 3/3. That is the bug family
    # that produced nine bad scorers on this project.
    # The renderer has TWO forms and conflating them silently ate real answers:
    # a multi-line block opens with the marker alone on its line and closes with
    # ‹/›, while a one-line aside is "‹thinking› text" ending at the newline and
    # never closed. Treating every unclosed marker as running to the end of the
    # output deleted the final answer of any run whose last aside was one-line —
    # which deflated recall in both arms until a cross-check disagreed with a
    # transcript I had read with my own eyes.
    answer = re.sub(r"‹thinking›\n.*?(?:‹/›|\Z)", "", text, flags=re.S)
    answer = re.sub(r"‹thinking›[^\n]*\n?", "", answer)
    answer = "\n".join(
        ln for ln in answer.splitlines()
        if not re.match(r"^(Looked up |Read |Searched for |Matched |Listed |Tokens: |strument: )", ln)
    )

    named = {c for c in CALLERS if c in answer}
    # "empty string" / `""` for the two, and args.message() for the third.
    right_msg = 0
    if "runCommitTool" in named and re.search(r"args\.message|message\(\)", answer):
        right_msg += 1
    for c in ("runOne", "afterInterrupt"):
        if c in named and re.search(r'""|empty', answer):
            right_msg += 1

    cost = 0.0
    m = re.search(r"Cost: \$([0-9.]+) turn", text)
    if m:
        cost = float(m.group(1))

    return {
        "symbol": symbol_calls,
        "read": reads,
        "grep": greps,
        "glob": globs,
        "tools": symbol_calls + reads + greps + globs,
        "named": sorted(named),
        "recall": len(named),
        "right_msg": right_msg,
        "cost": cost,
        "empty": "Empty response received" in text,
    }


def main():
    os.makedirs(OUT, exist_ok=True)
    plan = []
    for model, n in MODELS.items():
        for arm in ARMS:
            for i in range(n):
                plan.append((model, arm, i))
    random.seed(20260822)
    random.shuffle(plan)

    env = dict(os.environ)
    env["XDG_CONFIG_HOME"] = f"{SP}/sym/cfg_home"

    results = []
    for k, (model, arm, i) in enumerate(plan, 1):
        tag = f"{model}-{arm}-{i}"
        path = f"{OUT}/{tag}.txt"
        if os.path.exists(path):
            text = open(path).read()
        else:
            print(f"[{k}/{len(plan)}] {tag}", flush=True)
            p = subprocess.run(
                [ARMS[arm], "chat", "-M", model, "--dry-run", "--no-color",
                 "--no-history", "--yes", "-m", TASK],
                cwd=REPO, env=env, capture_output=True, text=True, timeout=900,
            )
            text = p.stdout + p.stderr
            open(path, "w").write(text)
            time.sleep(1)
        s = score(text)
        s.update(model=model, arm=arm, run=i)
        results.append(s)

    json.dump(results, open(f"{SP}/sym/results.json", "w"), indent=1)

    print("\n{:<6} {:<5} {:>4} {:>5} {:>5} {:>6} {:>7} {:>8}".format(
        "model", "arm", "sym", "read", "grep", "tools", "recall", "cost"))
    for model in MODELS:
        for arm in ARMS:
            rows = [r for r in results if r["model"] == model and r["arm"] == arm and not r["empty"]]
            if not rows:
                continue
            n = len(rows)
            avg = lambda k: sum(r[k] for r in rows) / n
            print("{:<6} {:<5} {:>4.1f} {:>5.1f} {:>5.1f} {:>6.1f} {:>5.1f}/3 {:>8.3f}  n={}".format(
                model, arm, avg("symbol"), avg("read"), avg("grep"),
                avg("tools"), avg("recall"), sum(r["cost"] for r in rows), n))

    for arm in ARMS:
        rows = [r for r in results if r["arm"] == arm and not r["empty"]]
        used = sum(1 for r in rows if r["symbol"] > 0)
        print(f"\narm {arm}: symbol used in {used}/{len(rows)} runs; "
              f"mean tools {sum(r['tools'] for r in rows)/len(rows):.1f}; "
              f"full recall in {sum(1 for r in rows if r['recall'] == 3)}/{len(rows)}")
    bad = [r for r in results if r["empty"]]
    if bad:
        print(f"\ndropped {len(bad)} empty-response runs: "
              f"{[(r['model'], r['arm'], r['run']) for r in bad]}")


if __name__ == "__main__":
    main()
