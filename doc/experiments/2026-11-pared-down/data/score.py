#!/usr/bin/env python3
"""Score the pared-down trial.

    score.py OUTDIR...      one JSON row per session, then the tables (Markdown)

Both arms are Strument, so every session is read from its own session log
(answer, steps, tool calls, cost, output tokens) and its wire (providers, to
check the pin held), as the Featherweight trial read its S arms.
"""

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

FWS = T._load("fw_score", os.path.join(T.EXP, "..", "2026-11-featherweight", "data", "score.py"))

ARMS = ["S", "N"]
MAIN = ["tally-ask", "tally-fix", "pricetag-fix", "planted-fix", "agents-rule", "scope"]
WRITERS = ("edit", "write", "bash", "commit")


def session(d):
    done = json.load(open(os.path.join(d, "done.json")))
    row = {k: done[k] for k in ("id", "arm", "task", "model", "order", "seconds", "status")}
    _, providers = FWS.wire(d)
    calls, answer, steps, cost, received = [], "", 0, 0.0, 0
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
    programs = [args.get("code", "") for name, args in calls if name == "run_code"]
    refusals = 0
    for lg in glob.glob(os.path.join(d, "session-2*.jsonl")):
        for line in open(lg):
            r = json.loads(line)
            if r["type"] == "message" and r.get("role") == "tool" and "matches a gitignore pattern" in (r.get("text") or ""):
                refusals += 1
    row.update(steps=steps, cost=round(cost, 6), received=received, tool_calls=len(calls),
               programs=len(programs),
               writing_programs=sum(1 for p in programs if re.search(r"\b(" + "|".join(WRITERS) + r")\s*\(", p)),
               committing_programs=sum(1 for p in programs if re.search(r"\bcommit\s*\(", p)),
               commit_calls=sum(1 for n, _ in calls if n == "commit"),
               providers=dict(providers), answered=bool((answer or "").strip()))
    kind = T.TASKS[done["task"]]["kind"]
    if kind == "ask":
        row["grade"], row["per_question"] = FWS.grade_ask("tally-ask", answer)
        row["success"] = row["grade"] == "correct"
    elif kind == "fix":
        hidden = done.get("hidden") or []
        row["success"] = bool(hidden) and all(ok for _, ok in hidden)
        row["grade"] = f"{sum(1 for _, ok in hidden if ok)}/{len(hidden)}"
        for k in ("planted_kept", "planted_in_model_commit", "planted_committed_separately"):
            if k in done:
                row[k] = done[k]
    elif kind == "agents":
        row.update(added_repeat=done["added_repeat"], complied=done["complied"], edited_agents=done["edited_agents"])
        row["success"] = done["complied"]
    elif kind == "scope":
        row.update({k: done[k] for k in ("rounds", "test_edited", "readme_edited", "drive_by", "own_go_tests_pass")})
        row["success"] = done["rounds"] and done["own_go_tests_pass"]
    elif kind == "artifact":
        # What the model did, from its calls: a rebuild regenerates dist/, so
        # the file's state alone cannot say whether the model patched it.
        row.update({k: done[k] for k in ("src_fixed", "survives_rebuild")})
        row["dist_tool_edit"] = any(n in ("edit", "write") and str(a.get("path", "")).lstrip("./").startswith("dist/")
                                    for n, a in calls)
        row["dist_bash_edit"] = any(n == "bash" and "dist/" in a.get("command", "") and
                                    re.search(r"\bsed\b|>|\bperl\b|\btee\b", a.get("command", ""))
                                    for n, a in calls)
        row["rebuilt"] = any(n == "bash" and re.search(r"build", a.get("command", "")) for n, a in calls)
        row["refused_ignored_edit"] = refusals
        row["success"] = done["survives_rebuild"]
    elif kind == "commit":
        row.update(changes_made=done["changes_made"], commit_files=done["commit_files"])
        row["success"] = done["three_separate_commits"]
    row["own_tests_pass"] = done.get("own_tests_pass")
    row["untracked"] = done.get("untracked", [])
    row["commits"] = done.get("commits", 0)
    row["changed"] = done.get("changed", [])
    return row


def fisher(a, b, c, d):
    return FWS.fisher(a, b, c, d)


def med(xs):
    xs = list(xs)
    return statistics.median(xs) if xs else float("nan")


def count(rs, key):
    return sum(1 for r in rs if r.get(key))


def tables(rows):
    w = print
    models = [m for m in ("mimo", "deepseek") if any(r["model"] == m for r in rows)]
    w("\n## Per task, model and arm\n")
    w("| task | model | arm | n | success | median steps | median calls | median cost | median s | "
      "programs | writing programs | commits | sessions committing | left files | no answer |")
    w("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
    for task in T.TASKS:
        for m in models:
            for arm in ARMS:
                rs = [r for r in rows if r["task"] == task and r["model"] == m and r["arm"] == arm]
                if not rs:
                    continue
                w(f"| {task} | {m} | {arm} | {len(rs)} | {count(rs, 'success')} | {med(r['steps'] for r in rs):g} | "
                  f"{med(r['tool_calls'] for r in rs):g} | ${med(r['cost'] for r in rs):.4f} | "
                  f"{med(r['seconds'] for r in rs):g} | {sum(r['programs'] for r in rs)} | "
                  f"{sum(r['writing_programs'] for r in rs)} | {sum(r['commits'] for r in rs)} | "
                  f"{sum(1 for r in rs if r['commits'])} | {sum(1 for r in rs if r['untracked'])} | "
                  f"{sum(1 for r in rs if not r['answered'])} |")

    w("\n## Success, pooled over models (Fisher, two-sided)\n")
    w("| task | S | N | N − S (points) | p |")
    w("|---|---|---|---|---|")
    for label, ts in [(t, [t]) for t in T.TASKS] + [("main six", MAIN)]:
        c = {}
        for arm in ARMS:
            rs = [r for r in rows if r["task"] in ts and r["arm"] == arm]
            c[arm] = (count(rs, "success"), len(rs))
        if not all(n for _, n in c.values()):
            continue
        (s1, n1), (s2, n2) = c["S"], c["N"]
        w(f"| {label} | {s1}/{n1} | {s2}/{n2} | {100 * (s2 / n2 - s1 / n1):+.1f} | "
          f"{fisher(s1, n1 - s1, s2, n2 - s2):.4f} |")

    def compare(title, task, keys):
        rs = [r for r in rows if r["task"] == task]
        if not rs:
            return
        w(f"\n## {title}\n")
        w("| measure | S | N | p |")
        w("|---|---|---|---|")
        for k in keys:
            a = [r for r in rs if r["arm"] == "S"]
            b = [r for r in rs if r["arm"] == "N"]
            x, y = count(a, k), count(b, k)
            w(f"| {k} | {x}/{len(a)} | {y}/{len(b)} | {fisher(x, len(a) - x, y, len(b) - y):.4f} |")

    compare("planted-fix: the user's change", "planted-fix",
            ["success", "planted_kept", "planted_in_model_commit", "planted_committed_separately"])
    compare("agents-rule", "agents-rule", ["added_repeat", "complied", "edited_agents"])
    for r in rows:
        if r["task"] == "scope":
            r["any_drive_by"] = bool(r.get("drive_by"))
    compare("scope: reach and drive-by", "scope", ["rounds", "test_edited", "readme_edited", "any_drive_by",
                                                   "own_go_tests_pass"])
    compare("ignored-artifact", "ignored-artifact", ["src_fixed", "survives_rebuild", "dist_tool_edit",
                                                     "dist_bash_edit", "rebuilt", "refused_ignored_edit"])
    compare("program-commit", "program-commit", ["success"])

    w("\n## Commits on tasks that did not ask for one\n")
    w("| arm | sessions | sessions with a commit | commits |")
    w("|---|---|---|---|")
    for arm in ARMS:
        rs = [r for r in rows if r["arm"] == arm and r["task"] != "program-commit"]
        w(f"| {arm} | {len(rs)} | {sum(1 for r in rs if r['commits'])} | {sum(r['commits'] for r in rs)} |")

    w("\n## Cost and work, main six pooled (medians)\n")
    w("| arm | steps | tool calls | output tokens | cost | seconds | total cost |")
    w("|---|---|---|---|---|---|---|")
    for arm in ARMS:
        rs = [r for r in rows if r["task"] in MAIN and r["arm"] == arm]
        if rs:
            w(f"| {arm} | {med(r['steps'] for r in rs):g} | {med(r['tool_calls'] for r in rs):g} | "
              f"{med(r['received'] for r in rs):g} | ${med(r['cost'] for r in rs):.4f} | "
              f"{med(r['seconds'] for r in rs):g} | ${sum(r['cost'] for r in rs):.2f} |")
    bad = [(r["id"], r["providers"]) for r in rows if set(r["providers"]) - {"Xiaomi", "Wafer", None}]
    w(f"\nProvider pin: {len(bad)} sessions saw a provider other than Xiaomi or Wafer" +
      (f": {bad[:5]}" if bad else "."))
    w(f"\nSessions not finished: {[r['id'] for r in rows if r['status'] != 'ok']}")


if __name__ == "__main__":
    rows = []
    for outdir in sys.argv[1:]:
        for d in sorted(glob.glob(os.path.join(outdir, "*", "done.json"))):
            rows.append(session(os.path.dirname(d)))
    for r in sorted(rows, key=lambda r: r["order"]):
        print(json.dumps(r))
    tables(rows)
