"""p3fired.py RUNS_DIR: per session, the provider-minus-estimate gap at turn 2's first request, less the gap
at turn 1's first, against turn 1's reasoning tokens. Under replay the jump should track the reasoning."""
import glob, json, os, sys
print("run\tgap_jump\tt1_reasoning")
for d in sorted(glob.glob(os.path.join(sys.argv[1], "*"))):
    reqs, t2 = [], None
    for lp in glob.glob(os.path.join(d, "state/strument/projects/*/sessions/*/log/*.jsonl")):
        for line in open(lp, errors="replace"):
            ev = json.loads(line)
            if ev.get("type") == "message" and ev.get("role") == "user" and "Staging has" in (ev.get("text") or ""):
                t2 = len(reqs)
            if ev.get("type") == "request" and ev.get("call") == "turn" and ev.get("estimated"):
                reqs.append(ev)
    if t2 is None or t2 >= len(reqs):
        print(f"{os.path.basename(d)}\t-\t-"); continue
    jump = (reqs[t2]["sent"] - reqs[t2]["estimated"]) - (reqs[0]["sent"] - reqs[0]["estimated"])
    print(f"{os.path.basename(d)}\t{jump}\t{sum(r.get('reasoning', 0) for r in reqs[:t2])}")
