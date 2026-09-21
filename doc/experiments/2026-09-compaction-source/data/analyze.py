"""Tables for the write-up, from results.jsonl.

Written before the batch so the analysis cannot become a search for whichever
cut looks best. Exclusions, columns and the statistic are the ones the
preregistration names.
"""

import json
import pathlib
import statistics
import sys
from collections import Counter

MIN_FOLDS = 4
COLUMNS = ("recalled", "declined", "confabulated", "absent")


def fisher(a, b, c, d):
    """Two-sided Fisher exact on a 2x2, without scipy."""
    from math import comb
    n = a + b + c + d
    row1, col1 = a + b, a + c
    def p(x):
        return comb(row1, x) * comb(n - row1, col1 - x) / comb(n, col1)
    obs = p(a)
    lo = max(0, col1 - (n - row1))
    hi = min(row1, col1)
    return min(1.0, sum(p(x) for x in range(lo, hi + 1) if p(x) <= obs * (1 + 1e-9)))


def main(path):
    rows = [json.loads(l) for l in pathlib.Path(path).read_text().splitlines() if l.strip()]
    if not rows:
        print("no rows")
        return 1

    # Exclusions, reported rather than silent: a fixture that stopped forcing
    # compaction would otherwise look exactly like a null.
    kept, dropped = [], []
    for r in rows:
        (kept if r["folds"] >= MIN_FOLDS and r["returncode"] == 0 else dropped).append(r)

    print(f"{len(rows)} sessions, {len(kept)} kept, {len(dropped)} excluded")
    if dropped:
        why = Counter(
            "too few folds" if r["folds"] < MIN_FOLDS else f"exit {r['returncode']}"
            for r in dropped)
        for reason, n in sorted(why.items()):
            print(f"  {n} × {reason}")
    if not kept:
        return 1

    models = sorted({r["model"] for r in kept})
    for model in models:
        mine = [r for r in kept if r["model"] == model]
        print(f"\n## {model}  (n={len(mine)})")
        for probe, key in (("reason (turn 1)", "reason_score"),
                           ("middle (turn 6, counter-metric)", "name_score")):
            print(f"\n### {probe}")
            print(f"{'':14} " + " ".join(f"{c:>13}" for c in COLUMNS))
            counts = {}
            for arm in ("fold", "record"):
                a = [r for r in mine if r["arm"] == arm]
                c = Counter(r[key] for r in a)
                counts[arm] = (c, len(a))
                print(f"{arm:14} " + " ".join(f"{c[col]:>6}/{len(a):<6}" for col in COLUMNS))
            (cf, nf), (cr, nr) = counts["fold"], counts["record"]
            if nf and nr:
                p = fisher(cf["recalled"], nf - cf["recalled"],
                           cr["recalled"], nr - cr["recalled"])
                print(f"{'':14} recalled, two-sided Fisher: p = {p:.3f}")

        print("\n### cost of the change")
        for arm in ("fold", "record"):
            a = [r for r in mine if r["arm"] == arm]
            if not a:
                continue
            med = lambda k: statistics.median([r[k] for r in a])
            print(f"{arm:8} folds {med('folds'):5.1f}   "
                  f"compaction input {med('fold_input_tokens'):9.0f} tok   "
                  f"session ${med('session_cost'):.4f}   {med('elapsed'):6.1f}s")

        # reasoning="low" was requested and the config accepted it, but these
        # models still think. Reported per arm rather than against a threshold,
        # because what would confound an A/B is a *difference* between the
        # arms — the same volume on both sides is a property of the model.
        print("\n### reasoning volume (requested low)")
        for arm in ("fold", "record"):
            a = [r for r in mine if r["arm"] == arm]
            if not a:
                continue
            per_turn = [r["reasoning_chars"] / max(r["turns_recorded"], 1) for r in a]
            print(f"{arm:8} {statistics.median(per_turn):7.0f} chars per turn (median)")
        fails = sum(r["fold_failures"] for r in mine)
        if fails:
            print(f"  !! {fails} compaction call(s) did not return ok")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1] if len(sys.argv) > 1 else "results.jsonl"))
