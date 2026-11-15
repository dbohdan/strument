#!/usr/bin/env python3
"""Score the judge check.

    score.py DIR    tables in Markdown, from DIR/gen and DIR/judge

Fact checks are recomputed from each saved block rather than read from the
file, so a corrected check rescores without re-running (experimenting.md §4).

A pair judged in both orders gives one of:
  first / second   the same member won in both orders: a consistent verdict
  split            each order picked the version shown in one position
  missing          a call failed or its verdict did not parse; counted apart,
                   never as a split (§3)
"""

import collections
import glob
import json
import math
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import defects as D  # noqa: E402
import judge as J  # noqa: E402
import tasks as T  # noqa: E402


def load(outdir, sub):
    return [json.load(open(p)) for p in sorted(glob.glob(os.path.join(outdir, sub, "*.json")))]


def winner_member(rec):
    """Which member of the pair the judge picked: 0 (the pair's first), 1, or None."""
    if rec.get("verdict") not in ("A", "B"):
        return None
    shown_first = rec["order"] == "ab"
    picked_a = rec["verdict"] == "A"
    return 0 if picked_a == shown_first else 1


def outcomes(judged):
    by = collections.defaultdict(dict)
    for r in judged:
        by[(r["task"], r["pair"], r["judge"])][r["order"]] = r
    out = {}
    for key, orders in by.items():
        ws = [winner_member(orders[o]) if o in orders else None for o in J.ORDERS]
        if None in ws:
            out[key] = "missing"
        elif ws[0] == ws[1]:
            out[key] = "first" if ws[0] == 0 else "second"
        else:
            out[key] = "split"
    return out


def binom_two_sided(k, n):
    """Exact two-sided binomial p against 1/2."""
    if n == 0:
        return float("nan")
    probs = [math.comb(n, i) / 2 ** n for i in range(n + 1)]
    return min(1.0, sum(p for p in probs if p <= probs[k] + 1e-12))


def main(outdir):
    w = print
    gens = load(outdir, "gen")
    judged = load(outdir, "judge")

    w("## Candidates\n")
    w("| model | candidates | fenced block | facts ok | mean cost | reasoning tokens (median) |")
    w("|---|---|---|---|---|---|")
    for m in ("mimo", "terra"):
        rs = [r for r in gens if r["model"] == m]
        if not rs:
            continue
        blocks = [r for r in rs if r.get("block") is not None]
        ok = sum(1 for r in blocks if T.facts(r["task"], r["block"])["ok"])
        rt = sorted(r.get("reasoning_tokens") or 0 for r in rs)
        w(f"| {m} | {len(rs)} | {len(blocks)} | {ok} | ${sum(r.get('cost') or 0 for r in rs) / len(rs):.5f} | "
          f"{rt[len(rt) // 2]} |")

    defect_tables(load(outdir, "defects"))
    if not judged:
        return
    oc = outcomes(judged)
    judges = sorted({r["judge"] for r in judged})

    w("\n## Verdicts per pair kind and judge\n")
    w("Pair kinds: **tm** is Terra against MiMo (tm1 and tm2 pooled), first member Terra; **mm** and "
      "**tt** are null pairs of one writer's two samples.\n")
    w("| judge | pair kind | pairs | consistent | first won | second won | split | missing | "
      "first won of consistent | p (vs ½) |")
    w("|---|---|---|---|---|---|---|---|---|---|")
    for j in judges:
        for kind, pairs in (("tm", ("tm1", "tm2")), ("mm", ("mm",)), ("tt", ("tt",))):
            vals = [v for (task, pair, jj), v in oc.items() if jj == j and pair in pairs]
            c = collections.Counter(vals)
            cons = c["first"] + c["second"]
            share = f"{c['first'] / cons:.0%}" if cons else "—"
            w(f"| {j} | {kind} | {len(vals)} | {cons} ({cons / len(vals):.0%}) | {c['first']} | {c['second']} | "
              f"{c['split']} | {c['missing']} | {share} | {binom_two_sided(c['first'], cons):.3f} |")

    w("\n## Position: share of parsed verdicts that picked A\n")
    w("| judge | parsed | picked A | unparsed or failed |")
    w("|---|---|---|---|")
    for j in judges:
        rs = [r for r in judged if r["judge"] == j]
        parsed = [r for r in rs if r.get("verdict") in ("A", "B")]
        a = sum(1 for r in parsed if r["verdict"] == "A")
        w(f"| {j} | {len(parsed)} | {a} ({a / len(parsed):.0%}) | {len(rs) - len(parsed)} |" if parsed else
          f"| {j} | 0 | — | {len(rs)} |")

    if len(judges) >= 2:
        w("\n## Agreement between judges on Terra-against-MiMo pairs both judged consistently\n")
        a_j, b_j = judges[:2]
        both = [(oc.get((t, p, a_j)), oc.get((t, p, b_j))) for (t, p, j) in oc if j == a_j and p in ("tm1", "tm2")]
        both = [(x, y) for x, y in both if x in ("first", "second") and y in ("first", "second")]
        agree = sum(1 for x, y in both if x == y)
        w(f"{agree} of {len(both)} pairs ({agree / len(both):.0%})." if both else "No pair was consistent "
          "for both judges.")

    w("\n## Terra-against-MiMo verdicts per task (consistent verdicts; T = Terra, M = MiMo, s = split)\n")
    w("| task | " + " | ".join(judges) + " |")
    w("|---|" + "---|" * len(judges))
    sym = {"first": "T", "second": "M", "split": "s", "missing": "?"}
    for task in T.TASKS:
        cells = []
        for j in judges:
            cells.append(" ".join(sym.get(oc.get((task, p, j)), "·") for p in ("tm1", "tm2")))
        w(f"| {task} | " + " | ".join(cells) + " |")

    w("\nJudge calls: " + ", ".join(
        f"{j} {sum(1 for r in judged if r['judge'] == j)} (${sum(r.get('cost') or 0 for r in judged if r['judge'] == j):.3f})"
        for j in judges) + ".")
    prov = collections.Counter((r["judge"], r.get("provider")) for r in judged)
    w(f"Providers: {dict(prov)}.")


def defect_tables(recs):
    """The position-free judge: defects per candidate, Terra against MiMo paired
    by task and sample, and how often a judge repeats its own count."""
    if not recs:
        return
    w = print
    counts = collections.defaultdict(dict)  # (task, writer, n, judge) -> rep -> count or None
    for r in recs:
        items = D.parse_defects(r.get("text")) if r.get("ok") else None
        counts[(r["task"], r["writer"], r["n"], r["judge"])][r["rep"]] = None if items is None else len(items)
    judges = sorted({r["judge"] for r in recs})
    w("\n## Defects per candidate (single-candidate judge)\n")
    w("Each candidate is judged alone, twice. A candidate's count is the mean of its parsed reps. **Paired** "
      "compares Terra's sample n with MiMo's sample n on the same task: Terra fewer, tie, or MiMo fewer.\n")
    w("| judge | MiMo mean | Terra mean | Terra fewer | tie | MiMo fewer | p (sign, vs ½) | same count on both reps | unparsed |")
    w("|---|---|---|---|---|---|---|---|---|")
    for j in judges:
        def mean(task, writer, n):
            vs = [v for v in counts.get((task, writer, n, j), {}).values() if v is not None]
            return sum(vs) / len(vs) if vs else None
        per = {m: [x for x in (mean(t, m, n) for t in T.TASKS for n in (1, 2)) if x is not None] for m in ("mimo", "terra")}
        fewer = tie = more = 0
        for t in T.TASKS:
            for n in (1, 2):
                a, b = mean(t, "terra", n), mean(t, "mimo", n)
                if a is None or b is None:
                    continue
                if a < b:
                    fewer += 1
                elif a > b:
                    more += 1
                else:
                    tie += 1
        reps = [v for k, v in counts.items() if k[3] == j]
        both = [v for v in reps if v.get(1) is not None and v.get(2) is not None]
        same = sum(1 for v in both if v[1] == v[2])
        unparsed = sum(1 for v in reps for x in v.values() if x is None)
        mm = f"{sum(per['mimo']) / len(per['mimo']):.2f}" if per["mimo"] else "—"
        tm = f"{sum(per['terra']) / len(per['terra']):.2f}" if per["terra"] else "—"
        w(f"| {j} | {mm} | {tm} | {fewer} | {tie} | {more} | {binom_two_sided(fewer, fewer + more):.3f} | "
          f"{same} of {len(both)} | {unparsed} |")


if __name__ == "__main__":
    main(sys.argv[1])
