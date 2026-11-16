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
    for m in ("mimo", "terra", "terra-default", "sol"):
        rs = [r for r in gens if r["model"] == m]
        if not rs:
            continue
        blocks = [r for r in rs if r.get("block") is not None]
        ok = sum(1 for r in blocks if T.facts(r["task"], r["block"])["ok"])
        rt = sorted(r.get("reasoning_tokens") or 0 for r in rs)
        w(f"| {m} | {len(rs)} | {len(blocks)} | {ok} | ${sum(r.get('cost') or 0 for r in rs) / len(rs):.5f} | "
          f"{rt[len(rt) // 2]} |")

    defect_tables(load(outdir, "defects"))
    censoring_tables(load(outdir, "defects"), gens)
    label_tables(load(outdir, "label"))
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
    """The position-free judge: defects per candidate, each writer against MiMo
    paired by task and sample, and how often a judge repeats its own count."""
    if not recs:
        return
    w = print
    counts = collections.defaultdict(dict)  # (task, writer, n, judge) -> rep -> count or None
    for r in recs:
        items = D.parse(r)
        counts[(r["task"], r["writer"], r["n"], r["judge"])][r["rep"]] = None if items is None else len(items)
    judges = sorted({r["judge"] for r in recs})
    ns = sorted({r["n"] for r in recs})
    writers = sorted({r["writer"] for r in recs}, key=lambda x: (x != "mimo", x))
    w("\n## Defects per candidate (single-candidate judge)\n")
    w("Each candidate is judged alone, twice. A candidate's count is the mean of its parsed reps. **Paired** "
      "compares a writer's sample n with MiMo's sample n on the same task: the writer fewer, tie, or MiMo "
      "fewer.\n")
    w("| judge | writer | mean defects | candidates with any | fewer than MiMo | tie | MiMo fewer | p (sign, vs ½) | "
      "same count on both reps | unparsed |")
    w("|---|---|---|---|---|---|---|---|---|---|")
    for j in judges:
        def mean(task, writer, n):
            vs = [v for v in counts.get((task, writer, n, j), {}).values() if v is not None]
            return sum(vs) / len(vs) if vs else None
        for wr in writers:
            ms = [x for x in (mean(t, wr, n) for t in T.TASKS for n in ns) if x is not None]
            fewer = tie = more = 0
            if wr != "mimo":
                for t in T.TASKS:
                    for n in ns:
                        a, b = mean(t, wr, n), mean(t, "mimo", n)
                        if a is None or b is None:
                            continue
                        fewer += a < b
                        more += a > b
                        tie += a == b
            reps = [v for k, v in counts.items() if k[3] == j and k[1] == wr]
            both = [v for v in reps if v.get(1) is not None and v.get(2) is not None]
            same = sum(1 for v in both if v[1] == v[2])
            unparsed = sum(1 for v in reps for x in v.values() if x is None)
            paired = (f"{fewer} | {tie} | {more} | {binom_two_sided(fewer, fewer + more):.3f}"
                      if wr != "mimo" else "— | — | — | —")
            w(f"| {j} | {wr} | {sum(ms) / len(ms):.2f} | {sum(1 for x in ms if x > 0)} of {len(ms)} | {paired} | "
              f"{same} of {len(both)} | {unparsed} |" if ms else f"| {j} | {wr} | — | — | {paired} | — | {unparsed} |")

    others = [x for x in writers if x != "mimo"]
    if len(others) < 2:
        return
    w("\n### Writer against writer (same judge, paired by task and sample)\n")
    w("| judge | writer | against | first fewer | tie | second fewer | p (sign, vs ½) |")
    w("|---|---|---|---|---|---|---|")
    for j in judges:
        def mean2(task, writer, n):
            vs = [v for v in counts.get((task, writer, n, j), {}).values() if v is not None]
            return sum(vs) / len(vs) if vs else None
        for i, x in enumerate(others):
            for y in others[i + 1:]:
                f = t_ = m = 0
                for task in T.TASKS:
                    for n in ns:
                        a, b = mean2(task, x, n), mean2(task, y, n)
                        if a is None or b is None:
                            continue
                        f += a < b
                        m += a > b
                        t_ += a == b
                w(f"| {j} | {x} | {y} | {f} | {t_} | {m} | {binom_two_sided(f, f + m):.3f} |")


def censoring_tables(recs, gens):
    """What the unparsed judgments could do, and the secondary measures.

    A candidate with no parsed rep drops its pair out of the sign test. The
    bounds credit every such pair to one side, then the other: if the result
    holds both ways the misses cannot overturn it. Defects per 100 words,
    because a longer page has more places for a defect.
    """
    if not recs:
        return
    w = print
    counts = collections.defaultdict(list)  # (task, writer, n, judge) -> parsed counts
    for r in recs:
        items = D.parse(r)
        if items is not None:
            counts[(r["task"], r["writer"], r["n"], r["judge"])].append(len(items))
    mean = {k: sum(v) / len(v) for k, v in counts.items()}
    words = {(g["task"], g["model"], g["n"]): len((g.get("block") or "").split()) for g in gens}
    judges = sorted({r["judge"] for r in recs})
    ns = sorted({r["n"] for r in recs})
    writers = sorted({r["writer"] for r in recs}, key=lambda x: (x != "mimo", x))

    w("\n### Unparsed judgments by writer\n")
    w("| judge | " + " | ".join(writers) + " |")
    w("|---|" + "---|" * len(writers))
    for j in judges:
        cells = []
        for wr in writers:
            rs = [r for r in recs if r["judge"] == j and r["writer"] == wr]
            bad = sum(1 for r in rs if D.parse(r) is None)
            cells.append(f"{bad} of {len(rs)}")
        w(f"| {j} | " + " | ".join(cells) + " |")

    w("\n### Bounds: every missing pair credited to one side\n")
    w("| judge | first | second | first fewer / tie / second fewer | missing pairs | p | all to first | "
      "all to second |")
    w("|---|---|---|---|---|---|---|---|")
    pairs = [(a, b) for i, a in enumerate(writers[::-1]) for b in writers[::-1][i + 1:]]
    for j in judges:
        for a, b in pairs:
            f = t_ = m = miss = 0
            for task in T.TASKS:
                for n in ns:
                    x, y = mean.get((task, a, n, j)), mean.get((task, b, n, j))
                    if x is None or y is None:
                        miss += 1
                        continue
                    f += x < y
                    m += x > y
                    t_ += x == y
            w(f"| {j} | {a} | {b} | {f} / {t_} / {m} | {miss} | {binom_two_sided(f, f + m):.3f} | "
              f"{binom_two_sided(f + miss, f + m + miss):.3f} | {binom_two_sided(f, f + m + miss):.3f} |")

    w("\n### Length and density (parsed candidates)\n")
    w("| writer | median words | " + " | ".join(f"defects per 100 words ({j})" for j in judges) + " |")
    w("|---|---|" + "---|" * len(judges))
    for wr in writers:
        ws = sorted(v for k, v in words.items() if k[1] == wr)
        if not ws:
            continue
        cells = []
        for j in judges:
            rs = [c / words[(t, wr, n)] * 100 for (t, ww, n, jj), c in mean.items()
                  if jj == j and ww == wr and words.get((t, wr, n))]
            cells.append(f"{sum(rs) / len(rs):.2f} (n={len(rs)})" if rs else "—")
        med = (ws[(len(ws) - 1) // 2] + ws[len(ws) // 2]) / 2
        w(f"| {wr} | {med:g} | " + " | ".join(cells) + " |")


def fisher_two_sided(a, b, c, d):
    """Exact two-sided Fisher test on [[a, b], [c, d]]."""
    n1, n2, k = a + b, c + d, a + c
    def p(x):
        return math.comb(n1, x) * math.comb(n2, k - x) / math.comb(n1 + n2, k)
    lo, hi = max(0, k - n2), min(k, n1)
    obs = p(a)
    return min(1.0, sum(p(x) for x in range(lo, hi + 1) if p(x) <= obs + 1e-12))


def label_tables(recs):
    """The label-swap probe: does MiMo's choice follow the writer or the label?"""
    if not recs:
        return
    w = print
    def picked(r):
        """(actual writer picked, labelled writer picked), or None."""
        if r.get("verdict") not in ("A", "B"):
            return None
        side = "a" if r["verdict"] == "A" else "b"
        return r["written_" + side], r["labelled_" + side]
    w("\n## Label swap: MiMo told who wrote each version\n")
    w("Per call: how often MiMo picked the text Terra actually wrote, and the text it was told Terra wrote. "
      "Per pair (both orders): a consistent winner by actual writer, or a split.\n")
    w("| labels | calls parsed | picked actual Terra | picked labelled Terra | picked A | pairs | consistent: actual Terra | "
      "consistent: actual MiMo | split | labelled Terra of consistent | p (vs ½) |")
    w("|---|---|---|---|---|---|---|---|---|---|---|")
    per_call = {}
    for lab in ("true", "false"):
        rs = [r for r in recs if r["label"] == lab]
        got = [(r, picked(r)) for r in rs]
        ok = [(r, g) for r, g in got if g]
        actual_t = sum(1 for _, g in ok if g[0] == "terra")
        lab_t = sum(1 for _, g in ok if g[1] == "terra")
        pick_a = sum(1 for r, _ in ok if r["verdict"] == "A")
        per_call[lab] = (actual_t, len(ok) - actual_t)
        by = collections.defaultdict(dict)
        for r, g in got:
            by[(r["task"], r["pair"])][r["order"]] = g
        cons_t = cons_m = split = 0
        for v in by.values():
            gs = [v.get(o) for o in J.ORDERS]
            if None in gs:
                continue
            if gs[0][0] == gs[1][0]:
                cons_t += gs[0][0] == "terra"
                cons_m += gs[0][0] == "mimo"
            else:
                split += 1
        cons = cons_t + cons_m
        lab_terra = cons_t if lab == "true" else cons_m  # consistent wins by the version labelled Terra
        w(f"| {lab} | {len(ok)} | {actual_t} | {lab_t} | {pick_a} | {len(by)} | {cons_t} | {cons_m} | {split} | "
          f"{f'{lab_terra / cons:.0%}' if cons else '—'} | {binom_two_sided(lab_terra, cons) if cons else float('nan'):.3f} |")
    (a, b), (c, d) = per_call["true"], per_call["false"]
    w(f"\nLabel effect on picking Terra's actual text, per call: {a} of {a + b} with true labels, {c} of {c + d} "
      f"with swapped labels (Fisher p = {fisher_two_sided(a, b, c, d):.3f}).")


if __name__ == "__main__":
    if len(sys.argv) > 2:
        T.use(sys.argv[2])
    main(sys.argv[1])
