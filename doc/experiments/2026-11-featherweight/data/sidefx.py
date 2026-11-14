#!/usr/bin/env python3
"""Side effects per arm, and the fix tasks' failures, from score.py's rows.

    sidefx.py SCORES
"""
import collections, json, math, sys

rows = [json.loads(l) for l in open(sys.argv[1]) if l.startswith("{")]


def fisher(a, b, c, d):
    n, r1, c1 = a + b + c + d, a + b, a + c
    p = lambda x: math.comb(r1, x) * math.comb(n - r1, c1 - x) / math.comb(n, c1)  # noqa: E731
    o = p(a)
    return min(1, sum(p(x) for x in range(max(0, c1 - (n - r1)), min(r1, c1) + 1) if p(x) <= o * (1 + 1e-9)))


TESTS = (("out of scope", lambda r: bool(r["out_of_scope"])),
         ("left files", lambda r: bool(r["untracked"])),
         ("own tests broken", lambda r: r["own_tests_pass"] is False))
for kind, ts in (("fix", ("tally-fix", "pricetag-fix")), ("ask", ("tally-ask", "pricetag-ask"))):
    print(f"\n### {kind} tasks (n = 80 per arm)\n")
    print("| side effect | S | S-fw | FW | FW vs S p | S-fw vs S p |")
    print("|---|---|---|---|---|---|")
    for name, f in TESTS:
        c = {a: sum(1 for r in rows if r["task"] in ts and r["arm"] == a and f(r)) for a in ("S", "S-fw", "FW")}
        print(f"| {name} | {c['S']} | {c['S-fw']} | {c['FW']} | {fisher(c['FW'], 80 - c['FW'], c['S'], 80 - c['S']):.4f} | "
              f"{fisher(c['S-fw'], 80 - c['S-fw'], c['S'], 80 - c['S']):.4f} |")
print()
for a in ("S", "S-fw", "FW"):
    left = collections.Counter(f for r in rows if r["arm"] == a for f in r["untracked"])
    scope = collections.Counter(f for r in rows if r["arm"] == a for f in r["out_of_scope"])
    print(f"- {a}: left {dict(left.most_common(8))}; out of scope {dict(scope.most_common(6))}")
print("\nfix failures:", [(r["id"], r["grade"]) for r in rows if r["task"].endswith("fix") and not r["success"]])
