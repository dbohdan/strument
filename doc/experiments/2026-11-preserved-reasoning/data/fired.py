"""fired.py RUNS_DIR: did replay reach the wire? Per session, the growth of (provider count - Strument estimate)
from the first request to the last, against the reasoning tokens produced before the last request."""
import glob, json, os, sys
print("run\tgap_growth\tprior_reasoning\tratio")
for d in sorted(glob.glob(os.path.join(sys.argv[1], "*"))):
    reqs = []
    for lp in glob.glob(os.path.join(d, "state/strument/projects/*/sessions/*/log/*.jsonl")):
        for line in open(lp, errors="replace"):
            try: ev = json.loads(line)
            except ValueError: continue
            if ev.get("type") == "request" and ev.get("call") == "turn" and ev.get("estimated"):
                reqs.append(ev)
    if len(reqs) < 2: continue
    growth = (reqs[-1]["sent"] - reqs[-1]["estimated"]) - (reqs[0]["sent"] - reqs[0]["estimated"])
    prior = sum(r.get("reasoning", 0) for r in reqs[:-1])
    print(f"{os.path.basename(d)}\t{growth}\t{prior}\t{growth / prior:.2f}" if prior else f"{os.path.basename(d)}\t{growth}\t0\t-")
