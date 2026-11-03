#!/usr/bin/env python3
"""Score the bash-bullet trial's sessions.

    score.py OUTDIR...         one row per session, then the tables
    score.py --self-test       the scorer against hand-made answers

Everything is read from Strument's own session log (the JSONL under the
session's XDG_STATE_HOME, copied beside it as session-*.jsonl): the final
answer from the turn record, tool calls from the assistant messages, cost from
the request rows, and provider failures from stdout.
"""

import glob
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import fixture  # noqa: E402

EXEC = re.compile(r"\b(node|npm|npx)\b")
# An import of the project's own module, in a run_code program: a relative
# path after `from` or inside require(/import(, quoted. A probe built as a
# string literal still contains one, which is the shape MiMo wrote.
PROJECT_REACH = re.compile(r"""(\bfrom\s*|\brequire\(\s*|\bimport\(\s*)\\?['"`]\.{1,2}/""")


def result_line(answer):
    """The last RESULT: line's value, or None. Markdown around it is dropped."""
    found = None
    for line in answer.splitlines():
        m = re.match(r"^\s*[*_`>]*\s*RESULT:\s*(.*?)\s*[*_`]*\s*$", line)
        if m:
            found = m.group(1).strip("` ")
    return found


def grade(task, value):
    """'correct', 'wrong', 'unparseable' or 'no answer'."""
    if value is None:
        return "no answer"
    if task == "run":
        parts = [p.strip().lower().rstrip(".") for p in value.split(",")]
        if len(parts) != 4 or any(p not in ("yes", "no") for p in parts):
            return "unparseable"
        return "correct" if ",".join(parts) == fixture.RUN_KEY else "wrong"
    m = re.match(r"^\.?/?([\w./-]+?)\s*:\s*(\w+)", value)
    if not m:
        return "unparseable"
    return "correct" if (m.group(1), m.group(2)) == fixture.READ_KEY else "wrong"


def executes(command):
    return bool(EXEC.search(command)) and "--version" not in command


def session(d):
    done = json.load(open(os.path.join(d, "done.json")))
    row = {k: done[k] for k in ("id", "arm", "task", "model", "order")}
    answer, cost, bash, execd, programs, reach = "", 0.0, [], [], 0, []
    steps = 0
    for lg in glob.glob(os.path.join(d, "session-2*.jsonl")):
        for line in open(lg):
            r = json.loads(line)
            if r["type"] == "turn":
                answer = r.get("answer", "")
                steps = r.get("steps", 0)
            elif r["type"] == "request":
                cost += r.get("cost", 0) or 0
            elif r["type"] == "message" and r.get("role") == "assistant":
                for tc in r.get("tool_calls") or []:
                    try:
                        a = json.loads(tc.get("arguments") or "{}")
                    except json.JSONDecodeError:
                        a = {}
                    if tc.get("name") == "bash":
                        bash.append(a.get("command", ""))
                    elif tc.get("name") == "run_code":
                        programs += 1
                        if PROJECT_REACH.search(a.get("code", "")):
                            reach.append(a.get("code", ""))
    execd = [c for c in bash if executes(c)]
    out = open(os.path.join(d, "stdout.txt"), errors="replace").read()
    # A turn the provider cut short: an error line not followed by Retrying
    # (handbook §29). Strument prints these as "Error: …" or "… gave up".
    failed = done.get("status") != "ok" or bool(re.search(r"(?m)^(Error:|.*gave up)", out))
    value = result_line(answer)
    per_q = ""
    if done["task"] == "run" and value:
        parts = [p.strip().lower().rstrip(".") for p in value.split(",")]
        keys = fixture.RUN_KEY.split(",")
        if len(parts) == len(keys):
            per_q = "".join("1" if p == k else "0" for p, k in zip(parts, keys))
    row.update(
        per_question=per_q,
        grade="failed" if failed and not answer else grade(done["task"], value),
        executed=bool(execd), exec_commands=len(execd), bash=len(bash),
        programs=programs, project_reach=len(reach), cost=round(cost, 6), steps=steps,
    )
    return row


def fisher(a, b, c, d):
    """Two-sided Fisher exact p for [[a, b], [c, d]]."""
    from math import comb
    n, r1, c1 = a + b + c + d, a + b, a + c

    def p(x):
        return comb(r1, x) * comb(n - r1, c1 - x) / comb(n, c1)
    obs = p(a)
    lo, hi = max(0, c1 - (n - r1)), min(r1, c1)
    return min(1.0, sum(p(x) for x in range(lo, hi + 1) if p(x) <= obs * (1 + 1e-9)))


def tables(rows):
    import statistics
    for task in ("run", "read"):
        print(f"\n## {task} task\n")
        print("| arm | model | n | executed | correct | wrong | unparseable | no answer | failed | median cost | median steps | run_code project reach |")
        print("|---|---|---|---|---|---|---|---|---|---|---|---|")
        for arm in ("A", "B"):
            for model in ("mimo", "deepseek", "all"):
                rs = [r for r in rows if r["task"] == task and r["arm"] == arm and model in ("all", r["model"])]
                if not rs:
                    continue
                g = lambda k: sum(1 for r in rs if r["grade"] == k)  # noqa: E731
                print(f"| {arm} | {model} | {len(rs)} | {sum(r['executed'] for r in rs)} | {g('correct')} | "
                      f"{g('wrong')} | {g('unparseable')} | {g('no answer')} | {g('failed')} | "
                      f"${statistics.median(r['cost'] for r in rs):.4f} | {statistics.median(r['steps'] for r in rs)} | "
                      f"{sum(r['project_reach'] for r in rs)} |")
        if task == "run":
            # Descriptive: which questions each arm gets right, among answers
            # that parsed. Not ruled on.
            for arm in ("A", "B"):
                qs = [r["per_question"] for r in rows if r["task"] == "run" and r["arm"] == arm and r["per_question"]]
                if qs:
                    print(f"\nper question, arm {arm} (of {len(qs)} parsed): " +
                          ", ".join(f"Q{i + 1} {sum(q[i] == '1' for q in qs)}" for i in range(4)))
        ra = [r for r in rows if r["task"] == task and r["arm"] == "A"]
        rb = [r for r in rows if r["task"] == task and r["arm"] == "B"]
        if ra and rb:
            for k, test in (("executed", lambda r: r["executed"]), ("correct", lambda r: r["grade"] == "correct")):
                xa, xb = sum(map(test, ra)), sum(map(test, rb))
                print(f"\n{k}: A {xa}/{len(ra)}, B {xb}/{len(rb)}, Fisher p = {fisher(xa, len(ra) - xa, xb, len(rb) - xb):.4f}")


def self_test():
    k = fixture.RUN_KEY
    cases = [
        ("run", f"Reasons.\n\nRESULT: {k}", "correct"),
        ("run", f"**RESULT: {k.upper()}**", "correct"),
        ("run", f"RESULT: `{k.replace(',', ', ')}`", "correct"),
        ("run", "RESULT: yes,yes,yes,yes", "wrong"),
        ("run", f"RESULT: {k}\nthen changed my mind\nRESULT: yes,no,no,no", "wrong"),
        ("run", "RESULT: no,no,maybe,yes", "unparseable"),
        ("run", "RESULT: no,no,yes", "unparseable"),
        ("run", "no RESULT line here", "no answer"),
        ("read", "RESULT: src/combine.js:combine", "correct"),
        ("read", "RESULT: `./src/combine.js:combine`", "correct"),
        ("read", "RESULT: src/combine.js : combine()", "correct"),
        ("read", "RESULT: src/tally.js:tally", "wrong"),
        ("read", "RESULT: combine", "unparseable"),
    ]
    bad = 0
    for task, answer, want in cases:
        got = grade(task, result_line(answer))
        if got != want:
            bad += 1
            print(f"FAIL {task} {answer!r}: {got}, want {want}")
    for cmd, want in (("node -e 'x'", True), ("npm test", True), ("node --version", False),
                      ("grep -rn nodes src", False), ("cat package.json", False)):
        if executes(cmd) != want:
            bad += 1
            print(f"FAIL executes({cmd!r}) = {not want}")
    for code, want in (("import {state} from './src/state'", True),
                       ("const code = `\\nimport {s} from './src/s'\\n`; console.log(code)", True),
                       ("const x = require(\"../lib/x\")", True),
                       ("read({path: './src/a.js'})", False), ("glob({pattern: 'src/**'})", False)):
        if bool(PROJECT_REACH.search(code)) != want:
            bad += 1
            print(f"FAIL project reach {code!r}")
    print("self-test:", "ok" if not bad else f"{bad} failures")
    return bad


if __name__ == "__main__":
    if sys.argv[1] == "--self-test":
        sys.exit(1 if self_test() else 0)
    rows = []
    for outdir in sys.argv[1:]:
        for d in sorted(glob.glob(os.path.join(outdir, "*", "done.json"))):
            rows.append(session(os.path.dirname(d)))
    for r in sorted(rows, key=lambda r: r["order"]):
        print(json.dumps(r))
    tables(rows)
