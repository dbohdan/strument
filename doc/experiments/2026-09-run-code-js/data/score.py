"""Scores runs/ against the preregistered metrics. Refuses to report if the
classifier's self-test fails."""
import glob, json, math, os, sys
from collections import defaultdict

import classify

HERE = os.path.dirname(os.path.abspath(__file__))


def fisher(a, b, c, d):
    """Two-sided Fisher exact p for [[a, b], [c, d]]."""
    n1, n2, k = a + b, c + d, a + c
    n = n1 + n2
    def p(x):
        return math.comb(n1, x) * math.comb(n2, k - x) / math.comb(n, k)
    obs = p(a)
    lo, hi = max(0, k - n2), min(k, n1)
    return min(1.0, sum(p(x) for x in range(lo, hi + 1) if p(x) <= obs * (1 + 1e-9)))


def main():
    bad = classify.selftest()
    if bad:
        sys.exit(f"classifier self-test failed: {bad}")
    rows = [json.load(open(f)) for f in sorted(glob.glob(os.path.join(HERE, "runs", "*.json")))]
    rescoring = not rows
    if rescoring:
        # The raw transcripts are not kept in the repository; scored.jsonl
        # holds every first program, which is all the metrics read.
        rows = [json.loads(l) for l in open(os.path.join(HERE, "scored.jsonl"))]
    status = defaultdict(lambda: defaultdict(int))
    prog = defaultdict(list)
    cost = 0.0
    for r in rows:
        cost += r["cost"]
        status[r["arm"]][r["status"]] += 1
        if r["status"] == "program" and r["first_program"]:
            lang = "py" if r["arm"] == "monty" else "js"
            r["cls"] = classify.classify(r["first_program"], lang)
            prog[r["arm"]].append(r)

    print(f"{len(rows)} sessions, ${cost:.3f}\n")
    print("| arm | sessions | wrote a program | answered without | step limit | provider failure |")
    print("|---|---|---|---|---|---|")
    for arm in ("monty", "js"):
        s = status[arm]
        tot = sum(s.values())
        print(f"| {arm} | {tot} | {s['program']} | {s['answered_without_program']} | {s['step_limit']} | {s['provider_failure']} |")

    m = [r["cls"]["host"] for r in prog["monty"]]
    j = [r["cls"]["host"] for r in prog["js"]]
    a, b = sum(m), len(m) - sum(m)
    c, d = sum(j), len(j) - sum(j)
    print("\nPrimary: first program reaches for the host\n")
    print("| arm | first programs | host reach | rate |")
    print("|---|---|---|---|")
    for name, xs in (("monty", m), ("js", j)):
        print(f"| {name} | {len(xs)} | {sum(xs)} | {sum(xs)/len(xs):.1%} |" if xs else f"| {name} | 0 | 0 | — |")
    if m and j:
        p = fisher(a, b, c, d)
        rm, rj = a / len(m), c / len(j)
        verdict = "full trial" if (rj <= rm / 2 and p < 0.05) else "drop"
        print(f"\nFisher two-sided p = {p:.4f}. Decision rule: {verdict}.")

    print("\nPer model (host reach / first programs):\n")
    print("| model | monty | js |")
    print("|---|---|---|")
    for model in sorted({r["model"] for r in rows}):
        cells = []
        for arm in ("monty", "js"):
            xs = [r["cls"]["host"] for r in prog[arm] if r["model"] == model]
            cells.append(f"{sum(xs)}/{len(xs)}")
        print(f"| {model} | {cells[0]} | {cells[1]} |")

    print("\nPer task (programs written, monty / js):\n")
    print("| task | monty | js |")
    print("|---|---|---|")
    for task in ("walk", "total", "top5", "span", "longest", "cite"):
        cells = []
        for arm in ("monty", "js"):
            n = sum(1 for r in rows if r["arm"] == arm and r["task"] == task)
            k = sum(1 for r in prog[arm] if r["task"] == task)
            cells.append(f"{k}/{n}")
        print(f"| {task} | {cells[0]} | {cells[1]} |")

    print("\nSecondary hazards in first programs:\n")
    js = prog["js"]
    mo = prog["monty"]
    for key in ("kwargs", "ts", "bare_sort"):
        print(f"- js {key}: {sum(r['cls'][key] for r in js)}/{len(js)}")
    print(f"- monty missing construct: {sum(r['cls']['missing'] for r in mo)}/{len(mo)}")
    other = defaultdict(int)
    for r in rows:
        for n in r["other_calls"]:
            other[(r["arm"], n)] += 1
    print(f"- non-lookup calls answered 'not available': {dict(other)}")

    if rescoring:
        return
    with open(os.path.join(HERE, "scored.jsonl"), "w") as f:
        for r in rows:
            f.write(json.dumps({k: r.get(k) for k in ("model", "arm", "task", "rep", "status", "first_program", "program_step", "cls", "other_calls", "cost")}) + "\n")


if __name__ == "__main__":
    main()
