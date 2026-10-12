"""Aggregates the roll trial: python3 -I aggregate_roll.py <batch-dir> <files.txt> [--json out]

Scores every session's latest runs.jsonl record with scorer_roll, then reports
per arm. "Wrong" derived answers are answered, not true and not "don't know":
the confident failure the decision rule singles out.
"""
import json, os, statistics, subprocess, sys

d, files = sys.argv[1], sys.argv[2]
here = os.path.dirname(os.path.abspath(__file__))
latest = {}
for l in open(os.path.join(d, "runs.jsonl")):
    r = json.loads(l); latest[r["id"]] = r
rows = []
for sid in sorted(latest):
    rec = latest[sid]
    out = subprocess.run([sys.executable, "-I", os.path.join(here, "scorer_roll.py"), os.path.join(d, sid), files],
                         capture_output=True, text=True, check=True).stdout
    s = json.loads(out)
    wrong = sum(1 for v in s["derived"].values() if v and v["answered"] and not v["truth"] and not v["unknown"])
    rows.append(dict(id=sid, arm=rec["mode"], done=rec["done"], turns=len(rec["turns"]), loops=rec.get("loops", 0),
                     declined=rec.get("declined", 0), user=s["recall_old"] + s["recall_middle"],
                     old=s["recall_old"], mid=s["recall_middle"], dtruth=s["derived_truth"], down=s["derived_own"],
                     dunk=s["derived_unknown"], dwrong=wrong, claims=s["claims_correct"], rolls=s["rolls"],
                     folds=s.get("folds", 0), fail=s["fold_failures"], cleared=s["cleared"],
                     wtools=s["work_tool_calls"], qtools=s.get("question_tool_calls", 0),
                     facts={k: s[k] for k in ("codename", "flag", "dbhost", "budget", "oncall", "cohort") if k in s}))
for r in rows:
    print(r["id"], r["arm"].ljust(9), "done" if r["done"] else f"INCOMPLETE@{r['turns']}",
          "user", r["user"], f"({r['old']}+{r['mid']})", "| derived truth", r["dtruth"], "own", r["down"],
          "unk", r["dunk"], "wrong", r["dwrong"], "claims", r["claims"], "| rolls", r["rolls"], "folds", r["folds"],
          "fail", r["fail"], "| wtools", r["wtools"], "qtools", r["qtools"])
print()
for arm in ("summary", "record", "roll", "roll-bare"):
    a = [r for r in rows if r["arm"] == arm and r["done"]]
    if not a: continue
    m = lambda k: round(statistics.mean(r[k] for r in a), 2)
    print(arm.ljust(9), "n", len(a), "| user", m("user"), "old", m("old"), "mid", m("mid"),
          "| derived truth", m("dtruth"), "own", m("down"), "unk", m("dunk"), "wrong", m("dwrong"),
          "| claims", m("claims"), "| rolls", m("rolls"), "folds", m("folds"), "fail", m("fail"),
          "| sessions with a wrong derived answer", sum(r["dwrong"] > 0 for r in a))
if "--json" in sys.argv:
    json.dump(rows, open(sys.argv[sys.argv.index("--json") + 1], "w"), indent=1)
