"""replaydelivery.py RECORD.jsonl...: did a session's route take the reasoning Strument replayed?

Each request row has the provider's prompt count (sent), Strument's estimate of the request
(estimated) and the part of that estimate that was replayed reasoning (replayed). Between two
consecutive steps of one turn, the request grows by one assistant message and its tool results, so
the growth is fitted as

    d_sent = a * d(estimated - replayed) + b * d_replayed + c

by least squares, and f = b / a is the fraction of the newly replayed reasoning the provider
counted: near 1 delivered, near 0 discarded. Fitting the levels instead does not work: each step
adds about a hundred tokens of template and tool-call overhead the estimate does not see, and
replayed reasoning grows in step with everything else, so it cannot be told apart from the rest.
In differences the overhead is the constant c, and what identifies f is that some steps reason
more than others. A bootstrap over the steps gives the interval; a session whose steps all reason
alike gets a wide one and decides nothing. A record from before the field existed reads as one that
replayed nothing.

Validated on 2026-11-27 against routes whose delivery a paired probe had just measured (strip.py,
strip_or.py); see the README's check of that date.
"""
import json, random, sys


def solve3(rows):
    """Least squares for sent ~ x1, x2, 1 by the normal equations; None if singular."""
    s = [[0.0] * 4 for _ in range(3)]
    for x1, x2, y in rows:
        v = (x1, x2, 1.0)
        for i in range(3):
            for j in range(3):
                s[i][j] += v[i] * v[j]
            s[i][3] += v[i] * y
    for col in range(3):
        piv = max(range(col, 3), key=lambda r: abs(s[r][col]))
        if abs(s[piv][col]) < 1e-9:
            return None
        s[col], s[piv] = s[piv], s[col]
        for r in range(3):
            if r != col:
                k = s[r][col] / s[col][col]
                s[r] = [a - k * b for a, b in zip(s[r], s[col])]
    return [s[i][3] / s[i][i] for i in range(3)]


def fraction(rows):
    sol = solve3(rows)
    if sol is None or sol[0] <= 0:
        return None
    return sol[1] / sol[0]


for path in sys.argv[1:]:
    reqs = []
    for line in open(path):
        r = json.loads(line)
        if r.get("type") == "request" and r.get("call") == "turn" and r.get("sent") and r.get("estimated") \
                and r.get("outcome") in ("done", None):
            reqs.append(r)
    rows = []
    for prev, cur in zip(reqs, reqs[1:]):
        if cur.get("step", 0) != prev.get("step", 0) + 1:
            continue  # a new turn, a retry or a continuation: not one step's growth
        dp, dc = prev.get("replayed", 0), cur.get("replayed", 0)
        rows.append(((cur["estimated"] - dc) - (prev["estimated"] - dp), dc - dp, cur["sent"] - prev["sent"]))
    total = sum(x2 for _, x2, _ in rows)
    if total <= 0:
        print(f"{path}: {len(rows)} steps, nothing newly replayed (or recorded before the field existed)")
        continue
    f = fraction(rows)
    random.seed(0)
    boot = sorted(b for b in (fraction([random.choice(rows) for _ in rows]) for _ in range(2000)) if b is not None)
    lo, hi = (boot[int(0.025 * len(boot))], boot[int(0.975 * len(boot)) - 1]) if boot else (None, None)
    verdict = "undecided"
    if lo is not None and lo > 0.5:
        verdict = "delivered"
    elif hi is not None and hi < 0.5:
        verdict = "discarded"
    fs = "n/a" if f is None else f"{f:.2f}"
    ci = "n/a" if lo is None else f"{lo:.2f}..{hi:.2f}"
    print(f"{path}: {len(rows)} steps, {total} tokens newly replayed, f = {fs} (95% {ci}): {verdict}")
