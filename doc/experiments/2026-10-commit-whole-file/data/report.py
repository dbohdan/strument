import json, os, sys, math
E = sys.argv[1]
sys.path.insert(0, os.path.join(E, "tools"))
from scorer import score
runs = {json.loads(l)["id"]: json.loads(l) for l in open(f"{E}/main/runs.jsonl")}
rows = []
for rid, r in sorted(runs.items()):
    s = score(f"{E}/main/{rid}")
    rows.append(dict(r, **s))
json.dump(rows, open(f"{E}/scored.json", "w"), indent=1)
def fisher(a, n1, b, n2):
    # two-sided Fisher exact on [[a, n1-a], [b, n2-b]]
    k = a + b; N = n1 + n2
    def p(x): return math.comb(n1, x) * math.comb(n2, k - x) / math.comb(N, k)
    p0 = p(a); lo = max(0, k - n2); hi = min(k, n1)
    return min(1.0, sum(p(x) for x in range(lo, hi + 1) if p(x) <= p0 * (1 + 1e-9)))
cols = ["success", "finished", "rename_whole", "greet_done", "separate", "user_line_lost", "other_kept", "other_lost"]
print("| model | metric | baseline | treatment | p |\n|---|---|---|---|---|")
for m in ["qwen", "mimo", "glm"]:
    B = [x for x in rows if x["model"] == m and x["arm"] == "baseline"]
    T = [x for x in rows if x["model"] == m and x["arm"] == "treatment"]
    for c in cols:
        a = sum(bool(x[c]) for x in B); b = sum(bool(x[c]) for x in T)
        print(f"| {m} | {c} | {a}/{len(B)} | {b}/{len(T)} | {fisher(a, len(B), b, len(T)):.3f} |")
    for c in ["history_rewrites", "steps", "received", "cost", "wall"]:
        def mean(xs):
            v = [x.get(c) for x in xs if x.get(c) is not None]; return (sum(v) / len(v), len(v)) if v else (float("nan"), 0)
        (ma, na), (mb, nb) = mean(B), mean(T)
        print(f"| {m} | mean {c} | {ma:.4g} (n={na}) | {mb:.4g} (n={nb}) | |")
print("exits:", sorted({(x["model"], x["arm"], x["exit"]) for x in rows if x["exit"] != 0}))
