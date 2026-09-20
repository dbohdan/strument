"""Turn side_timing.jsonl into the two tables the budget question needs.

Per arm: the worst total (what a duration budget has to cover) and the worst
inter-byte gap including the wait for the first byte (what an idle timeout has
to cover). Those are different numbers and the whole design choice is which one
to bound, so they are reported side by side rather than one summarised away.

The first byte counts as a gap. It is the longest silence in most of these
calls — a large prompt is prefilled before anything streams — and an idle
timeout that ignored it would fire during normal prefill on every large input.
"""

import collections
import json
import pathlib
import statistics
import sys

HERE = pathlib.Path(__file__).parent


def main():
    rows = [json.loads(l) for l in (HERE / "side_timing.jsonl").read_text().splitlines() if l.strip()]
    if not rows:
        print("no rows")
        return 1

    cached = [r for r in rows if (r.get("cached_tokens") or 0) > 0]
    print(f"{len(rows)} calls, {len(cached)} with a non-zero cached-token count")
    if cached:
        print("  cache hits present — the salt did not make every call cold:")
        for r in cached[:10]:
            print(f"    {r['arm']:18s} {r['model']:32s} cached={r['cached_tokens']}")
    print()

    by_arm = collections.defaultdict(list)
    for r in rows:
        by_arm[r["arm"]].append(r)

    order = [
        "notes/capped",
        "commit/small",
        "commit/mid",
        "commit/big",
        "summary/200k-ctx",
        "summary/1m-ctx",
    ]

    def gap(r):
        # Already the largest silence on the wire, prefill included: the first
        # line's gap is measured from the request, so no separate first-byte
        # term is needed. Deliberately not the gap between content tokens —
        # see the note in side_timing.timed.
        return r["gap"]

    print("=== per arm, across all models")
    print(f"{'arm':18s} {'n':>3s} {'in tok':>8s} {'median':>8s} {'worst':>8s} {'worst gap':>10s}")
    for a in order:
        rs = by_arm.get(a)
        if not rs:
            continue
        print(
            f"{a:18s} {len(rs):3d} {statistics.median(r['prompt_tokens'] or 0 for r in rs):8.0f} "
            f"{statistics.median(r['total'] for r in rs):7.1f}s {max(r['total'] for r in rs):7.1f}s "
            f"{max(gap(r) for r in rs):9.1f}s"
        )

    print("\n=== per arm and model")
    print(f"{'arm':18s} {'model':32s} {'n':>3s} {'worst':>8s} {'worst gap':>10s}")
    for a in order:
        for m in sorted({r["model"] for r in by_arm.get(a, [])}):
            rs = [r for r in by_arm[a] if r["model"] == m]
            print(
                f"{a:18s} {m:32s} {len(rs):3d} {max(r['total'] for r in rs):7.1f}s "
                f"{max(gap(r) for r in rs):9.1f}s"
            )

    print("\n=== reasoning: which arms had a model thinking on the wire")
    for a in order:
        for m in sorted({r["model"] for r in by_arm.get(a, [])}):
            rs = [r for r in by_arm[a] if r["model"] == m]
            think = max(r.get("reasoning_chars") or 0 for r in rs)
            if think:
                print(f"{a:18s} {m:32s} up to {think:6d} reasoning chars")

    print("\n=== how the worst gap grows with input")
    for a in order:
        rs = by_arm.get(a)
        if not rs:
            continue
        tok = statistics.median(r["prompt_tokens"] or 0 for r in rs)
        print(f"{a:18s} {tok:8.0f} tok -> worst gap {max(gap(r) for r in rs):5.1f}s")
    return 0


if __name__ == "__main__":
    sys.exit(main())
