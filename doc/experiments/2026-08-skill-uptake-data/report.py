#!/usr/bin/env python3
"""Summarise the pilot. Counter-metrics sit beside the primary, never below it."""
import json
import sys
from collections import defaultdict

PALETTE = ["#A5415A", "#7A490D", "#648C43", "#00A49B", "#007DB7", "#54397D"]
ARMS = ["none", "skill", "inline"]
RULES = ["R1_palette", "R2_chartjunk", "R3_unit", "R4_direct_labels", "R5_gridlines"]


def main():
    rows = json.load(open(sys.argv[1]))
    # Every chart fixture, not just the first one. The hardcoded names here
    # silently dropped latency, storage and links from a 234-run report -- a
    # scorer that sees a third of the data and says nothing about it.
    CHARTS = ("revenue", "latency", "storage")
    chart = [r for r in rows if r["fixture"] in CHARTS]
    decoy = [r for r in rows if r["fixture"] not in CHARTS]

    print("=" * 78)
    print("PRIMARY -- chart task")
    print("=" * 78)
    print(f"{'arm':<8}{'n':>3}{'loaded':>8}{'edited':>8}{'rules/5':>9}"
          + "".join(f"{r.split('_')[0]:>5}" for r in RULES)
          + f"{'wellfm':>8}{'dataOK':>8}{'creep':>7}{'cost':>9}")
    for arm in ARMS:
        rs = [r for r in chart if r["arm"] == arm and not r["empty"]
              and not r["timeout"] and not r.get("loop_stopped")]
        if not rs:
            continue
        n = len(rs)
        loaded = sum(1 for r in rs if r["skill_calls"] > 0)
        edited = sum(1 for r in rs if r["edited"])
        mean = sum(r["n_rules"] for r in rs) / n
        per = [sum(1 for r in rs if r.get("rules", {}).get(k)) for k in RULES]
        wf = sum(1 for r in rs if r["wellformed"])
        dok = sum(1 for r in rs if r["data_ok"] is True)
        creep = sum(1 for r in rs if r["scope_creep"])
        cost = sum(r["cost"] for r in rs)
        print(f"{arm:<8}{n:>3}{loaded:>8}{edited:>8}{mean:>9.2f}"
              + "".join(f"{v:>5}" for v in per)
              + f"{wf:>8}{dok:>8}{creep:>7}{cost:>9.4f}")

    print("\nper fixture, mean rules/5")
    print(f"{'fixture':<10}" + "".join(f"{a:>14}" for a in ARMS))
    for fx in sorted({r["fixture"] for r in chart}):
        cells = []
        for arm in ARMS:
            rs = [r for r in chart if r["fixture"] == fx and r["arm"] == arm
                  and not r["empty"] and not r["timeout"] and not r.get("loop_stopped")]
            cells.append(f"{sum(r['n_rules'] for r in rs)/len(rs):.2f} ({len(rs)})" if rs else "-")
        print(f"{fx:<10}" + "".join(f"{c:>14}" for c in cells))

    print("\nper model, mean rules/5 (n per cell in brackets)")
    models = sorted({r["model"] for r in chart})
    print(f"{'model':<10}" + "".join(f"{a:>14}" for a in ARMS) + f"{'B loaded':>10}")
    for m in models:
        cells = []
        for arm in ARMS:
            rs = [r for r in chart if r["model"] == m and r["arm"] == arm
                  and not r["empty"] and not r["timeout"] and not r.get("loop_stopped")]
            cells.append(f"{sum(r['n_rules'] for r in rs)/len(rs):.1f} ({len(rs)})" if rs else "-")
        b = [r for r in chart if r["model"] == m and r["arm"] == "skill"]
        ld = f"{sum(1 for r in b if r['skill_calls']>0)}/{len(b)}"
        print(f"{m:<10}" + "".join(f"{c:>14}" for c in cells) + f"{ld:>10}")

    print("\n" + "=" * 78)
    print("COUNTER-METRICS")
    print("=" * 78)
    for arm in ARMS:
        rs = [r for r in chart if r["arm"] == arm]
        if not rs:
            continue
        broke = [r["tag"] for r in rs if not r["wellformed"]]
        lost = [r["tag"] for r in rs if r["data_ok"] is False]
        dropped = [r["tag"] for r in rs if r["data_attrs"] == "dropped"]
        creep = [(r["tag"], r["scope_creep"]) for r in rs if r["scope_creep"]]
        noedit = [r["tag"] for r in rs if not r["edited"]]
        print(f"\narm {arm}: n={len(rs)}")
        print(f"  malformed SVG      : {len(broke)} {broke or ''}")
        print(f"  data values changed: {len(lost)} {lost or ''}")
        print(f"  data attrs dropped : {len(dropped)} {dropped or ''}   (counted apart: not corruption)")
        print(f"  scope creep        : {len(creep)} {creep or ''}")
        print(f"  no edit at all     : {len(noedit)} {noedit or ''}")

    if decoy:
        print("\n" + "=" * 78)
        print("DECOY -- the skill does not apply here")
        print("=" * 78)
        print(f"{'fixture':<9}{'arm':<8}{'n':>3}{'loaded':>8}{'fixed':>8}{'creep':>7}{'cost':>9}")
        for fx in sorted({r["fixture"] for r in decoy}):
            for arm in ("none", "skill"):
                rs = [r for r in decoy if r["fixture"] == fx and r["arm"] == arm
                      and not r["empty"] and not r["timeout"]]
                if not rs:
                    continue
                print(f"{fx:<9}{arm:<8}{len(rs):>3}"
                      f"{sum(1 for r in rs if r['skill_calls']>0):>8}"
                      f"{sum(1 for r in rs if r.get('decoy_fixed')):>8}"
                      f"{sum(1 for r in rs if r['scope_creep']):>7}"
                      f"{sum(r['cost'] for r in rs):>9.4f}")

    bad = [r["tag"] for r in rows if r["empty"] or r["timeout"]]
    loops = [r["tag"] for r in rows if r.get("loop_stopped")]
    if bad:
        print(f"\ndropped (empty response / timeout): {len(bad)} {bad}")
    if loops:
        print(f"\nNO ANSWER -- harness stopped the turn (loop detector): {len(loops)}")
        for t in loops:
            print(f"   {t}")
        print("   Excluded from rules/5. Each had planned the edit correctly and")
        print("   was cut off mid-transcription; scoring them 0 would be a")
        print("   mechanical failure charged to the arm they happened to land in.")

    print("\n" + "=" * 78)
    print("STRUCTURAL CHECKS (an assertion that cannot fail is not a check)")
    print("=" * 78)
    a_loaded = sum(1 for r in rows if r["arm"] == "none" and r["skill_calls"] > 0)
    c_loaded = sum(1 for r in rows if r["arm"] == "inline" and r["skill_calls"] > 0)
    print(f"  arm none loaded a skill  : {a_loaded}  (must be 0 -- no tool was offered)")
    print(f"  arm inline loaded a skill: {c_loaded}  (must be 0 -- no tool was offered)")
    a_pal = [r["tag"] for r in chart if r["arm"] == "none"
             and r.get("detail", {}).get("R1_palette", {}).get("palette_used")]
    print(f"  arm none emitted a palette hex: {len(a_pal)} {a_pal or ''}")
    print("     (any hit contaminates R1: the palette would not be unguessable)")
    total = sum(r["cost"] for r in rows)
    print(f"\ntotal spend: ${total:.4f} over {len(rows)} runs")
    return 0


if __name__ == "__main__":
    sys.exit(main())
