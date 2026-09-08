"""Analysis. No scipy here, so CMH and Fisher are implemented directly."""

import json
import math
import pathlib
from collections import defaultdict

EXP = pathlib.Path(__file__).parent
rows = [json.loads(l) for l in open(EXP / "results.jsonl")]

# Infrastructure failures are separated BEFORE anything is counted. A timeout
# or an empty provider response is not a behavioral failure, and the previous
# experiment's worst hour went on exactly this confusion.
infra = [r for r in rows if r["returncode"] != 0 or r["empty_response"]]
good = [r for r in rows if r not in infra]

print(f"n={len(rows)}  infrastructure={len(infra)}  analyzed={len(good)}")
if infra:
    by = defaultdict(int)
    for r in infra:
        by[(r["arm"], "timeout" if r["returncode"] == -9 else f"rc={r['returncode']}"
            if not r["empty_response"] else "empty")] += 1
    print("  infra by arm/kind:", dict(by))
print(f"  total cost=${sum(r['cost'] or 0 for r in rows):.4f}")


def rate(rs):
    return sum(r["passed"] for r in rs) / len(rs) if rs else float("nan")


print("\n=== PRIMARY: task success ===")
for arm in "AB":
    a = [r for r in good if r["arm"] == arm]
    print(f"  {arm}: {sum(r['passed'] for r in a)}/{len(a)} = {rate(a):.3f}")

# Cochran-Mantel-Haenszel, stratified by model.
num = den = 0.0
var = 0.0
print("\n  per model (a=B pass, b=B fail, c=A pass, d=A fail):")
for m in ["mimo", "luna", "v4flash"]:
    B = [r for r in good if r["model"] == m and r["arm"] == "B"]
    A = [r for r in good if r["model"] == m and r["arm"] == "A"]
    a, b = sum(r["passed"] for r in B), len(B) - sum(r["passed"] for r in B)
    c, d = sum(r["passed"] for r in A), len(A) - sum(r["passed"] for r in A)
    n = a + b + c + d
    if n == 0:
        continue
    num += a - (a + b) * (a + c) / n
    var += ((a + b) * (c + d) * (a + c) * (b + d)) / (n * n * (n - 1)) if n > 1 else 0
    print(f"    {m:<8} B {a}/{a+b}={a/(a+b):.3f}   A {c}/{c+d}={c/(c+d):.3f}   diff {a/(a+b)-c/(c+d):+.3f}")

cmh = (abs(num) - 0.5) ** 2 / var if var > 0 else 0
print(f"\n  CMH chi2 = {cmh:.3f}  (p ~ {math.erfc(math.sqrt(cmh/2)):.3f})")

# Pooled difference and a one-sided bound on harm (B - A).
Ba = [r for r in good if r["arm"] == "B"]
Aa = [r for r in good if r["arm"] == "A"]
pB, pA = rate(Ba), rate(Aa)
se = math.sqrt(pB * (1 - pB) / len(Ba) + pA * (1 - pA) / len(Aa))
diff = pB - pA
print(f"  pooled diff (B-A) = {diff:+.4f}  SE={se:.4f}")
print(f"  95% CI = [{diff-1.96*se:+.4f}, {diff+1.96*se:+.4f}]")
print(f"  one-sided 95% worst case for B = {diff-1.645*se:+.4f}")

print("\n=== per task ===")
for t in sorted({r["task"] for r in good}):
    B = [r for r in good if r["task"] == t and r["arm"] == "B"]
    A = [r for r in good if r["task"] == t and r["arm"] == "A"]
    print(f"  {t:<18} A {rate(A):.3f} (n={len(A)})   B {rate(B):.3f} (n={len(B)})   diff {rate(B)-rate(A):+.3f}")

print("\n=== COUNTER-METRIC: reads of files already in the chat ===")
for arm in "AB":
    a = [r["redundant_reads"] for r in good if r["arm"] == arm]
    a_sorted = sorted(a)
    print(f"  {arm}: total={sum(a)} mean={sum(a)/len(a):.3f} median={a_sorted[len(a)//2]} max={max(a)} "
          f"nonzero={sum(1 for x in a if x)}/{len(a)}")

print("\n=== descriptive (no longer confounded: cache prefix unmoved) ===")
for arm in "AB":
    a = [r for r in good if r["arm"] == arm]
    tok = sorted(r["tokens_sent_k"] for r in a if r["tokens_sent_k"])
    st = sorted(r["steps"] for r in a if r["steps"])
    el = sorted(r["elapsed"] for r in a)
    print(f"  {arm}: med_tokens={tok[len(tok)//2]:.1f}k  med_steps={st[len(st)//2]}  "
          f"med_s={el[len(el)//2]:.0f}  cost=${sum(r['cost'] or 0 for r in a):.4f}")
