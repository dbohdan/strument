#!/usr/bin/env python3
"""Score the Featherweight-vs-Strument trial.

    score.py OUTDIR...      one JSON row per session, then the tables (Markdown)
    score.py --self-test    the extraction and grading on hand-made inputs

A session is read the same way whatever the harness, into one record:
  - Strument arms: the session log (answer, steps, tool calls, cost, output
    tokens), as the earlier trials read it;
  - Featherweight: stdout (the answer and steps `debug turn` prints) and the
    wire, whose responses carry the tool calls, usage and cost.
Providers come from the wire for both, to check that the pin held.
"""

import collections
import glob
import json
import math
import os
import re
import statistics
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tasks as T  # noqa: E402

ARMS = ["S", "S-fw", "FW"]
RUNS_CODE = re.compile(r"\b(node|npm|npx)\b")
RUNS_TESTS = re.compile(r"node\s+--test|npm\s+(run\s+)?test\b")


def result_line(answer):
    found = None
    for line in answer.splitlines():
        m = re.match(r"^\s*[*_`>]*\s*RESULT:\s*(.*?)\s*[*_`]*\s*$", line)
        if m:
            found = m.group(1).strip("` ")
    return found


def grade_ask(task, answer):
    value = result_line(answer or "")
    if value is None:
        return "no answer", ""
    parts = [p.strip().lower().rstrip(".") for p in value.split(",")]
    if len(parts) != 4 or any(p not in ("yes", "no") for p in parts):
        return "unparseable", ""
    key = T.TASKS[task]["key"].split(",")
    per_q = "".join("1" if p == k else "0" for p, k in zip(parts, key))
    return ("correct" if per_q == "1111" else "wrong"), per_q


def wire(d):
    """(responses as dicts, providers seen). Non-streaming bodies are JSON;
    Strument's streamed ones are SSE, from which only the provider is read."""
    out, providers = [], collections.Counter()
    path = os.path.join(d, "wire.jsonl")
    if not os.path.exists(path):
        return out, providers
    for line in open(path):
        r = json.loads(line)
        if r.get("kind") != "raw_response":
            continue
        body = r.get("body")
        if isinstance(body, str) and body.lstrip().startswith("{"):
            try:
                body = json.loads(body)
            except ValueError:
                continue
        if isinstance(body, dict):
            out.append(body)
            providers[body.get("provider")] += 1
        elif isinstance(body, str):
            for m in re.finditer(r'"provider"\s*:\s*"([^"]+)"', body):
                providers[m.group(1)] += 1
                break
    return out, providers


def commands(calls):
    """The shell-ish text of a session's tool calls: Strument's bash commands
    and Featherweight's run_code Tcl (whose exec runs commands)."""
    out = []
    for name, args in calls:
        if name == "bash":
            out.append(args.get("command", ""))
        elif name == "run_code" and "exec" in args.get("code", ""):
            out.append(args.get("code", ""))
    return out


def session(d):
    done = json.load(open(os.path.join(d, "done.json")))
    row = {k: done[k] for k in ("id", "arm", "task", "model", "order", "seconds", "status")}
    responses, providers = wire(d)
    calls, answer, steps, cost, received = [], "", 0, 0.0, 0
    if done["arm"] == "FW":
        out = open(os.path.join(d, "stdout.txt"), errors="replace").read()
        m = re.search(r"^steps: (\d+)$", out, re.M)
        steps = int(m.group(1)) if m else 0
        i = out.find("\nanswer:\n")
        answer = out[i + 9:] if i >= 0 else ""
        for b in responses:
            u = b.get("usage") or {}
            cost += u.get("cost") or 0
            received += u.get("completion_tokens") or 0
            msg = ((b.get("choices") or [{}])[0]).get("message") or {}
            for tc in msg.get("tool_calls") or []:
                fn = tc.get("function") or {}
                try:
                    args = json.loads(fn.get("arguments") or "{}")
                except ValueError:
                    args = {}
                calls.append((fn.get("name"), args if isinstance(args, dict) else {}))
    else:
        for lg in glob.glob(os.path.join(d, "session-2*.jsonl")):
            for line in open(lg):
                r = json.loads(line)
                if r["type"] == "turn":
                    answer, steps = r.get("answer", ""), r.get("steps", 0)
                elif r["type"] == "request":
                    cost += r.get("cost", 0) or 0
                    received += r.get("received", 0) or 0
                elif r["type"] == "message" and r.get("role") == "assistant":
                    for tc in r.get("tool_calls") or []:
                        try:
                            args = json.loads(tc.get("arguments") or "{}")
                        except ValueError:
                            args = {}
                        calls.append((tc.get("name"), args if isinstance(args, dict) else {}))
    cmds = commands(calls)
    row.update(steps=steps, cost=round(cost, 6), received=received, tool_calls=len(calls),
               executed=any(RUNS_CODE.search(c) for c in cmds),
               ran_tests=any(RUNS_TESTS.search(c) for c in cmds),
               providers=dict(providers), answer_chars=len(answer or ""))
    kind = T.TASKS[done["task"]]["kind"]
    if kind == "ask":
        row["grade"], row["per_question"] = grade_ask(done["task"], answer)
        row["success"] = row["grade"] == "correct"
    else:
        hidden = done.get("hidden") or []
        row["hidden_passed"] = sum(1 for _, ok in hidden if ok)
        row["hidden_total"] = len(hidden)
        row["success"] = bool(hidden) and all(ok for _, ok in hidden)
        row["grade"] = "all pass" if row["success"] else f"{row['hidden_passed']}/{len(hidden)}"
    row["own_tests_pass"] = done.get("own_tests_pass")
    row["out_of_scope"] = [f for f in done.get("changed", []) if not f.startswith(("src/", "test/"))]
    row["untracked"] = done.get("untracked", [])
    row["commits"] = done.get("commits", 0)
    return row


def fisher(a, b, c, d):
    n, r1, c1 = a + b + c + d, a + b, a + c
    if n == 0:
        return 1.0

    def p(x):
        return math.comb(r1, x) * math.comb(n - r1, c1 - x) / math.comb(n, c1)
    obs = p(a)
    lo, hi = max(0, c1 - (n - r1)), min(r1, c1)
    return min(1.0, sum(p(x) for x in range(lo, hi + 1) if p(x) <= obs * (1 + 1e-9)))


def med(xs):
    xs = list(xs)
    return statistics.median(xs) if xs else float("nan")


def tables(rows):
    w = print
    models = [m for m in ("mimo", "deepseek") if any(r["model"] == m for r in rows)]
    arms = [x for x in ARMS if any(r["arm"] == x for r in rows)]
    w("\n## Per task, arm and model\n")
    w("| task | model | arm | n | success | ran code | ran tests | median steps | median output tokens | "
      "median cost | median seconds | own tests broken | out of scope | left files | commits |")
    w("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
    for task in T.TASKS:
        for m in models:
            for arm in arms:
                rs = [r for r in rows if r["task"] == task and r["model"] == m and r["arm"] == arm]
                if not rs:
                    continue
                w(f"| {task} | {m} | {arm} | {len(rs)} | {sum(r['success'] for r in rs)} | "
                  f"{sum(r['executed'] for r in rs)} | {sum(r['ran_tests'] for r in rs)} | "
                  f"{med(r['steps'] for r in rs):g} | {med(r['received'] for r in rs):g} | "
                  f"${med(r['cost'] for r in rs):.4f} | {med(r['seconds'] for r in rs):g} | "
                  f"{sum(1 for r in rs if r['own_tests_pass'] is False)} | "
                  f"{sum(1 for r in rs if r['out_of_scope'])} | {sum(1 for r in rs if r['untracked'])} | "
                  f"{sum(r['commits'] for r in rs)} |")
    w("\n## Success, pooled over models (Fisher, two-sided)\n")
    w("| task | S | S-fw | FW | S vs S-fw (prompt) p | S-fw vs FW (harness) p | S vs FW p |")
    w("|---|---|---|---|---|---|---|")
    groups = [(t, [t]) for t in T.TASKS] + [("all ask", ["tally-ask", "pricetag-ask"]),
                                            ("all fix", ["tally-fix", "pricetag-fix"]), ("all", list(T.TASKS))]
    for label, ts in groups:
        cnt = {}
        for arm in arms:
            rs = [r for r in rows if r["task"] in ts and r["arm"] == arm]
            cnt[arm] = (sum(r["success"] for r in rs), len(rs))
        if len(cnt) < 3:
            continue

        def p(x, y):
            (s1, n1), (s2, n2) = cnt[x], cnt[y]
            return f"{fisher(s1, n1 - s1, s2, n2 - s2):.4f}"
        w(f"| {label} | " + " | ".join(f"{cnt[x][0]}/{cnt[x][1]}" for x in ARMS) +
          f" | {p('S', 'S-fw')} | {p('S-fw', 'FW')} | {p('S', 'FW')} |")
    w("\n## Cost and work, pooled over models (medians)\n")
    w("| task | arm | steps | tool calls | output tokens | cost | seconds |")
    w("|---|---|---|---|---|---|---|")
    for task in T.TASKS:
        for arm in arms:
            rs = [r for r in rows if r["task"] == task and r["arm"] == arm]
            if rs:
                w(f"| {task} | {arm} | {med(r['steps'] for r in rs):g} | {med(r['tool_calls'] for r in rs):g} | "
                  f"{med(r['received'] for r in rs):g} | ${med(r['cost'] for r in rs):.4f} | "
                  f"{med(r['seconds'] for r in rs):g} |")
    bad = [(r["id"], r["providers"]) for r in rows
           if set(r["providers"]) - {"Xiaomi", "Wafer", None}]
    w(f"\nProvider pin: {len(bad)} sessions saw a provider other than Xiaomi or Wafer" +
      (f": {bad[:5]}" if bad else "."))


def self_test():
    bad = 0
    k = T.TASKS["tally-ask"]["key"]
    for ans, want in ((f"x\nRESULT: {k}", "correct"), ("RESULT: yes,yes,yes,yes", "wrong"),
                      ("RESULT: no", "unparseable"), ("nothing", "no answer")):
        got = grade_ask("tally-ask", ans)[0]
        if got != want:
            bad += 1
            print(f"FAIL grade {ans!r}: {got}, want {want}")
    cases = [
        ([("bash", {"command": "node --test"})], True, True),
        ([("bash", {"command": "npm test 2>&1 | tail"})], True, True),
        ([("bash", {"command": "node -e 'x'"})], True, False),
        ([("run_code", {"code": "exec node --test"})], True, True),
        ([("run_code", {"code": "puts [exec node -e {1}]"})], True, False),
        ([("run_code", {"code": "set f [open src/a.js]; read $f"})], False, False),
        ([("read", {"path": "test/node.test.js"})], False, False),
    ]
    for calls, ex, tst in cases:
        cmds = commands(calls)
        got = (any(RUNS_CODE.search(c) for c in cmds), any(RUNS_TESTS.search(c) for c in cmds))
        if got != (ex, tst):
            bad += 1
            print(f"FAIL commands {calls}: {got}, want {(ex, tst)}")
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
