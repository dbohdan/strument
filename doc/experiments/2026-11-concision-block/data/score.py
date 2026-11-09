#!/usr/bin/env python3
"""Score the concision-block trial.

    score.py OUTDIR...        one JSON row per session, then the tables (Markdown)
    score.py --self-test      the scorer against hand-made answers and reasoning

Read from each session's own log: the final answer and steps from the turn
record, output tokens and cost from the request rows, tool calls from the
assistant messages, reasoning text from the reasoning records.
"""

import collections
import glob
import importlib.util
import json
import math
import os
import re
import statistics
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


FIXTURES = {
    "tally": _load("fixture1", os.path.join(HERE, "..", "..", "2026-11-bash-bullet", "data", "fixture.py")),
    "pricetag": _load("fixture2", os.path.join(HERE, "fixture2.py")),
}
ARMS = ["A", "C", "C-length", "C-direct", "P"]
FLASH = ["mimo", "deepseek"]
EXEC = re.compile(r"\b(node|npm|npx)\b")

# Dropped caveat: the reasoning names the planted behavior behind a question,
# and the answer to that question is still the wrong one. Patterns are for the
# behavior, not the conclusion, so a model that names the trap and then argues
# it away counts. (question index, pattern, wrong answer)
CAVEATS = {
    "tally": [
        (0, r"radix|parseInt\b[^.\n]{0,80}\bindex|index[^.\n]{0,40}\bparseInt", "yes"),
        (1, r"lexicograph|as strings|string (sort|order|comparison)|sorts? .{0,20}strings", "yes"),
        (2, r"integer[- ]like|integer keys|array[- ]index|numeric[- ]like|numeric keys|integer-valued", "yes"),
    ],
    "pricetag": [
        (1, r"floating[- ]point|0\.30000|precision|rounding error|IEEE", "yes"),
        (2, r"comma", "yes"),
        (3, r"lastIndex|global flag|/g\b|\bg flag|stateful", "yes"),
    ],
}


def result_line(answer):
    found = None
    for line in answer.splitlines():
        m = re.match(r"^\s*[*_`>]*\s*RESULT:\s*(.*?)\s*[*_`]*\s*$", line)
        if m:
            found = m.group(1).strip("` ")
    return found


def run_parts(value):
    if value is None:
        return None
    parts = [p.strip().lower().rstrip(".") for p in value.split(",")]
    if len(parts) != 4 or any(p not in ("yes", "no") for p in parts):
        return None
    return parts


def grade(fx, task, value):
    if value is None:
        return "no answer"
    f = FIXTURES[fx]
    if task == "run":
        parts = run_parts(value)
        if parts is None:
            return "unparseable"
        return "correct" if ",".join(parts) == f.RUN_KEY else "wrong"
    m = re.match(r"^\.?/?([\w./-]+?)\s*:\s*(\w+)", value)
    if not m:
        return "unparseable"
    return "correct" if (m.group(1), m.group(2)) == f.READ_KEY else "wrong"


def dropped(fx, reasoning, parts):
    """(mentioned, dropped): how many planted behaviors the reasoning names,
    and how many of those it names and still answers wrong."""
    mentioned = dropped_n = 0
    for q, pat, wrong in CAVEATS[fx]:
        if re.search(pat, reasoning, re.I):
            mentioned += 1
            if parts is not None and parts[q] == wrong:
                dropped_n += 1
    return mentioned, dropped_n


def session(d):
    done = json.load(open(os.path.join(d, "done.json")))
    row = {k: done[k] for k in ("id", "arm", "fixture", "task", "model", "order")}
    answer, cost, steps, received, reasoning, bash = "", 0.0, 0, 0, [], []
    for lg in glob.glob(os.path.join(d, "session-2*.jsonl")):
        for line in open(lg):
            r = json.loads(line)
            t = r["type"]
            if t == "turn":
                answer, steps = r.get("answer", ""), r.get("steps", 0)
            elif t == "request":
                cost += r.get("cost", 0) or 0
                received += r.get("received", 0) or 0
            elif t == "reasoning":
                reasoning.append(r.get("text") or "")
            elif t == "message" and r.get("role") == "assistant":
                for tc in r.get("tool_calls") or []:
                    if tc.get("name") == "bash":
                        try:
                            bash.append(json.loads(tc.get("arguments") or "{}").get("command", ""))
                        except json.JSONDecodeError:
                            pass
    out = open(os.path.join(d, "stdout.txt"), errors="replace").read()
    failed = done.get("status") != "ok" or bool(re.search(r"(?m)^(Error:|.*gave up)", out))
    value = result_line(answer)
    parts = run_parts(value) if done["task"] == "run" else None
    f = FIXTURES[done["fixture"]]
    per_q = "".join("1" if p == k else "0" for p, k in zip(parts, f.RUN_KEY.split(","))) if parts else ""
    mentioned, dropped_n = dropped(done["fixture"], "\n".join(reasoning), parts) if done["task"] == "run" else (0, 0)
    row.update(
        grade="failed" if failed and not answer else grade(done["fixture"], done["task"], value),
        per_question=per_q,
        executed=any(EXEC.search(c) and "--version" not in c for c in bash),
        steps=steps, received=received, cost=round(cost, 6),
        answer_chars=len(answer), caveats_mentioned=mentioned, caveats_dropped=dropped_n,
    )
    return row


def fisher(a, b, c, d):
    n, r1, c1 = a + b + c + d, a + b, a + c

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
    models = [m for m in ("mimo", "deepseek", "haiku", "sonnet") if any(r["model"] == m for r in rows)]
    arms = [a for a in ARMS if any(r["arm"] == a for r in rows)]
    for task in ("run", "read"):
        w(f"\n## {task} task, per model and arm (both fixtures)\n")
        w("| model | arm | n | executed | correct | wrong | unparseable | no answer | failed | "
          "median steps | median output tokens | median answer chars | median cost | caveat dropped |")
        w("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
        for m in models:
            for arm in arms:
                rs = [r for r in rows if r["task"] == task and r["model"] == m and r["arm"] == arm]
                if not rs:
                    continue
                g = collections.Counter(r["grade"] for r in rs)
                w(f"| {m} | {arm} | {len(rs)} | {sum(r['executed'] for r in rs)} | {g['correct']} | {g['wrong']} | "
                  f"{g['unparseable']} | {g['no answer']} | {g['failed']} | {med(r['steps'] for r in rs):g} | "
                  f"{med(r['received'] for r in rs):g} | {med(r['answer_chars'] for r in rs):g} | "
                  f"${med(r['cost'] for r in rs):.4f} | {sum(1 for r in rs if r['caveats_dropped'])} |")
    w("\n## Each arm against A, run task (Fisher, two-sided)\n")
    for label, group in (("Flash models pooled", FLASH), *(((m, [m]) for m in models if m not in FLASH))):
        w(f"\n### {label}\n")
        w("| arm | executed (arm vs A) | p | correct (arm vs A) | p | caveat dropped (arm vs A) | p | "
          "median output tokens (arm / A) |")
        w("|---|---|---|---|---|---|---|---|")
        base = [r for r in rows if r["task"] == "run" and r["arm"] == "A" and r["model"] in group]
        if not base:
            continue
        for arm in arms:
            if arm == "A":
                continue
            rs = [r for r in rows if r["task"] == "run" and r["arm"] == arm and r["model"] in group]
            if not rs:
                continue
            cells = []
            for test in (lambda r: r["executed"], lambda r: r["grade"] == "correct", lambda r: r["caveats_dropped"] > 0):
                x, y = sum(map(test, rs)), sum(map(test, base))
                cells += [f"{x}/{len(rs)} vs {y}/{len(base)}", f"{fisher(x, len(rs) - x, y, len(base) - y):.4f}"]
            w(f"| {arm} | " + " | ".join(cells) +
              f" | {med(r['received'] for r in rs):g} / {med(r['received'] for r in base):g} |")
    w("\n## Per question, run task (correct of parsed answers)\n")
    for fx in FIXTURES:
        w(f"\n### {fx}\n")
        w("| model | arm | parsed | Q1 | Q2 | Q3 | Q4 |")
        w("|---|---|---|---|---|---|---|")
        for m in models:
            for arm in arms:
                qs = [r["per_question"] for r in rows if r["task"] == "run" and r["fixture"] == fx
                      and r["model"] == m and r["arm"] == arm and r["per_question"]]
                if qs:
                    w(f"| {m} | {arm} | {len(qs)} | " + " | ".join(str(sum(q[i] == "1" for q in qs)) for i in range(4)) + " |")


def self_test():
    bad = 0
    t, p = FIXTURES["tally"].RUN_KEY, FIXTURES["pricetag"].RUN_KEY
    for fx, task, ans, want in (
        ("tally", "run", f"x\nRESULT: {t}", "correct"),
        ("pricetag", "run", f"**RESULT: {p.upper()}**", "correct"),
        ("pricetag", "run", "RESULT: no,no,no,no", "wrong"),
        ("pricetag", "run", "RESULT: no,no", "unparseable"),
        ("pricetag", "run", "nothing", "no answer"),
        ("pricetag", "read", "RESULT: src/sku.js:isValidSku", "correct"),
        ("pricetag", "read", "RESULT: `./src/sku.js : isValidSku()`", "correct"),
        ("pricetag", "read", "RESULT: src/price.js:parsePrice", "wrong"),
        ("tally", "read", "RESULT: src/combine.js:combine", "correct"),
    ):
        got = grade(fx, task, result_line(ans))
        if got != want:
            bad += 1
            print(f"FAIL grade {fx} {task} {ans!r}: {got}, want {want}")
    cases = (
        ("tally", "Object.keys puts integer-like keys first, so the order changes.", ["no", "no", "yes", "no"], (1, 1)),
        ("tally", "Object.keys puts integer-like keys first.", ["no", "no", "no", "yes"], (1, 0)),
        ("tally", "map(parseInt) passes the index as the radix", ["yes", "no", "no", "yes"], (1, 1)),
        ("tally", "nothing relevant here", ["yes", "no", "yes", "yes"], (0, 0)),
        ("pricetag", "The regex has the global flag, so lastIndex persists.", ["yes", "no", "no", "yes"], (1, 1)),
        ("pricetag", "parseFloat stops at the comma, giving 1", ["yes", "no", "no", "no"], (1, 0)),
        ("pricetag", "0.1 + 0.2 is 0.30000000000000004 in floating point", ["yes", "yes", "no", "no"], (1, 1)),
    )
    for fx, reasoning, parts, want in cases:
        got = dropped(fx, reasoning, parts)
        if got != want:
            bad += 1
            print(f"FAIL dropped({fx}, {reasoning!r}, {parts}) = {got}, want {want}")
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
