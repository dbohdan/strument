"""Score Strument's trials against the published FrontierHarness runs of the
same tasks: pass/fail, and cost and turns against the 12 configurations."""
import glob, json, os, statistics as st, sys

PUB = json.load(open("/tmp/harness/fh-eval/results/eval-data.json"))
published = {}
for h in PUB["harnesses"]:
    for t in h["task_details"]:
        published.setdefault(t["id"].split("/", 1)[1], {})[h["name"]] = t


def strument_turn(trial):
    logs = glob.glob(os.path.join(trial, "upper/fh/state/strument/projects/*/sessions/*/log/*.jsonl"))
    turns = [json.loads(l) for f in logs for l in open(f) if '"type": "turn"' in l or '"type":"turn"' in l]
    return turns[-1] if turns else {}


def main(model):
    rows = []
    for res in sorted(glob.glob(f"/tmp/fh/runs/{model}/*/result.json")):
        trial = os.path.dirname(res)
        r = json.load(open(res))
        t = strument_turn(trial)
        pub = published.get(r["task"], {})
        costs = sorted(v["cost_first_cold_usd"] for v in pub.values() if v.get("cost_first_cold_usd") is not None)
        cost = t.get("cost")
        rank = 1 + sum(1 for c in costs if cost is not None and c < cost)
        rows.append((r["task"], os.path.basename(trial), r["reward"], r["agent_status"], t.get("steps"),
                     cost, rank, len(costs) + 1, st.median(costs) if costs else None,
                     min(costs) if costs else None, sum(1 for v in pub.values() if v.get("success"))))
    print("| task | reward | exit | steps | Strument cost | rank by cost (1 = cheapest) | published median | cheapest published | published passes |")
    print("|---|---|---|---|---|---|---|---|---|")
    for x in rows:
        cost = f"${x[5]:.3f}" if x[5] is not None else "?"
        print(f"| {x[1]} | {x[2]} | {x[3]} | {x[4]} | {cost} | {x[6]}/{x[7]} | ${x[8]:.3f} | ${x[9]:.3f} | {x[10]}/12 |")
    passed = sum(1 for x in rows if x[2] == "1")
    total = sum(x[5] or 0 for x in rows)
    print(f"\n{passed}/{len(rows)} passed; Strument total ${total:.2f}")
    # Each published configuration's total on the same tasks, for the cost comparison.
    tasks = {x[0] for x in rows}
    print("\n| configuration | passes on these tasks | cost on these tasks |")
    print("|---|---|---|")
    for h in sorted(PUB["harnesses"], key=lambda h: sum((published[t][h["name"]].get("cost_first_cold_usd") or 0) for t in tasks if h["name"] in published.get(t, {}))):
        c = sum((published[t][h["name"]].get("cost_first_cold_usd") or 0) for t in tasks if h["name"] in published.get(t, {}))
        p = sum(1 for t in tasks if published.get(t, {}).get(h["name"], {}).get("success"))
        print(f"| {h['name']} | {p}/{len(tasks)} | ${c:.2f} |")
    print(f"| **strument** | {passed}/{len(rows)} | ${total:.2f} |")


if __name__ == "__main__":
    main(sys.argv[1])
