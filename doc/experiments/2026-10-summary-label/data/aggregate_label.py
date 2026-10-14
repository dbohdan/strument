"""Aggregates the recall trial: python3 -I aggregate_recall.py <batch-dir> <files.txt> [--json out]

Scores each session's latest runs.jsonl record with scorer_recall, then reports
per arm. A derived answer is wrong when it is answered, not true, and not
"don't know": the confident failure the decision rule counts against an arm.
"""
import json, os, statistics, subprocess, sys

ARMS = ("summary+recall", "summary+recall+label")
d, files = sys.argv[1], sys.argv[2]
here = os.path.dirname(os.path.abspath(__file__))
latest = {}
for l in open(os.path.join(d, "runs.jsonl")):
    r = json.loads(l); latest[r["id"]] = r
rows = []
for sid in sorted(latest):
    rec = latest[sid]
    s = json.loads(subprocess.run([sys.executable, "-I", os.path.join(here, "scorer_recall.py"), os.path.join(d, sid), files],
                                  capture_output=True, text=True, check=True).stdout)
    wrong = sum(1 for v in s["derived"].values() if v and v["answered"] and not v["truth"] and not v["unknown"])
    rows.append(dict(id=sid, arm=rec["mode"], done=rec["done"], turns=len(rec["turns"]),
                     loops=rec.get("loops", 0), declined=rec.get("declined", 0),
                     user=s["recall_old"] + s["recall_middle"], dtruth=s["derived_truth"],
                     dunk=s["derived_unknown"], dwrong=wrong, rolls=s["rolls"], folds=s.get("folds", 0),
                     fail=s["fold_failures"], qtools=s.get("question_tool_calls", 0),
                     rcalls=s["recall_calls"], rq=s["recall_in_questions"], rbefore=s["recall_before_questions"],
                     rall=s["recall_from_all"], rargs=s["recall_args"], cost=s["cost"]))
for r in rows:
    print(r["id"], r["arm"].ljust(19), "done" if r["done"] else f"INCOMPLETE@{r['turns']}", "user", r["user"],
          "| derived true", r["dtruth"], "unk", r["dunk"], "wrong", r["dwrong"],
          "| recall", r["rcalls"], "in q", r["rq"], "before", r["rbefore"], "all", r["rall"],
          "| rolls", r["rolls"], "folds", r["folds"], "fail", r["fail"], "| qtools", r["qtools"], "loops", r["loops"],
          "declined", r["declined"], "| $%.4f" % r["cost"])
print()
for arm in ARMS:
    a = [r for r in rows if r["arm"] == arm and r["done"]]
    if not a:
        continue
    t = lambda k: sum(r[k] for r in a)
    print(arm.ljust(19), "n", len(a), "| user", t("user"), "/", 6 * len(a), "| derived true", t("dtruth"), "unk", t("dunk"),
          "wrong", t("dwrong"), "/", 4 * len(a), "| mean true %.2f" % statistics.mean(r["dtruth"] for r in a),
          "| sessions calling recall in questions", sum(r["rq"] > 0 for r in a),
          "| recall calls", t("rcalls"), "| $%.3f" % t("cost"))
if "--json" in sys.argv:
    json.dump(rows, open(sys.argv[sys.argv.index("--json") + 1], "w"), indent=1)
