"""Per-trial rows and per-arm summaries for the effort trial."""
import glob, json, os, re, statistics as st, sys

OUT = "/tmp/fh/runs/effort"
rows = []
for res in sorted(glob.glob(f"{OUT}/*/*/result.json")):
    d = os.path.dirname(res)
    r = json.load(open(res))
    arm = d.split("/")[-2]
    recs = [json.loads(l) for f in glob.glob(f"{d}/upper/fh/state/strument/projects/*/sessions/*/log/*.jsonl")
            for l in open(f, errors="replace")]
    turn = [x for x in recs if x.get("type") == "turn"]
    t = turn[-1] if turn else {}
    reasoning = sum(len(x.get("text") or "") for x in recs if x.get("type") == "reasoning")
    out = open(f"{d}/agent.out", errors="replace").read()
    m = re.findall(r"([\d.]+)k cache hit", out)
    rows.append({"arm": arm, "trial": os.path.basename(d), "task": r["task"], "reward": r["reward"],
                 "outcome": t.get("outcome"), "steps": t.get("steps"), "sent": t.get("sent"),
                 "cache_hit_k": float(m[-1]) if m else None, "received": t.get("received"),
                 "reasoning_chars": reasoning, "cost": t.get("cost"), "agent_seconds": r["agent_seconds"],
                 "rate_limit_errors": out.count("rate_limit"), "files": t.get("files")})

if len(sys.argv) > 1 and sys.argv[1] == "jsonl":
    for x in rows:
        print(json.dumps(x))
    sys.exit()

print("| arm | task | passed | median cost | median received | median reasoning chars | median steps | median seconds |")
print("|---|---|---|---|---|---|---|---|")
for arm in ("k3", "k3-high", "k3-low"):
    for task in sorted({x["task"] for x in rows}):
        g = [x for x in rows if x["arm"] == arm and x["task"] == task]
        if not g:
            continue
        med = lambda k: st.median([x[k] for x in g if x[k] is not None] or [0])
        print(f"| {arm} | {task} | {sum(x['reward']=='1' for x in g)}/{len(g)} | ${med('cost'):.3f} | {med('received'):.0f} "
              f"| {med('reasoning_chars'):.0f} | {med('steps'):.0f} | {med('agent_seconds'):.0f} |")
print()
print("| arm | passed | total cost | mean received | mean steps | rate-limit errors |")
print("|---|---|---|---|---|---|")
for arm in ("k3", "k3-high", "k3-low"):
    g = [x for x in rows if x["arm"] == arm]
    if g:
        print(f"| {arm} | {sum(x['reward']=='1' for x in g)}/{len(g)} | ${sum(x['cost'] or 0 for x in g):.2f} "
              f"| {st.mean([x['received'] or 0 for x in g]):.0f} | {st.mean([x['steps'] or 0 for x in g]):.1f} "
              f"| {sum(x['rate_limit_errors'] for x in g)} |")
