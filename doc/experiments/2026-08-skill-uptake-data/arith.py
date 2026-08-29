#!/usr/bin/env python3
"""How much of the reasoning stream is arithmetic?

Not part of the design. Measured after the fact because reading transcripts
made it conspicuous, and written down because an impression ("qwen's transcript
is mostly arithmetic") is not a finding until it has a number attached.

Needs the raw transcripts, which are NOT committed -- only the handful the
writeup reasons about are, under transcripts/. Point --runs at a directory of
them:

    ./arith.py --runs <dir> --results results.json

The metric is a regex proxy: a reasoning line joining two numerals with an
operator, or assigning a number to a coordinate. It counts a line once however
much arithmetic is on it, so it under-reports, and it cannot tell recomputation
from first computation. Characters/4 is a token estimate. Directionally sound,
not precise -- which is why the writeup says so too.
"""

import argparse
import json
import pathlib
import re

# An operator BETWEEN two numerals. "6 quarters" must not count.
ARITH = re.compile(r"\d+(?:\.\d+)?\s*[-+*/]\s*\d+(?:\.\d+)?")
COORD = re.compile(r"\b(?:x|y|y1|y2|cx|cy|height|width|top)\s*=\s*\d")
CHARTS = ("revenue", "latency", "storage")


def reasoning_of(text):
    """Only the model's thinking, in both renderer forms (experimenting.md 15):
    a block opens with the marker alone and closes with the closing mark, while
    a one-line aside ends at its newline and is never closed."""
    out = [m.group(1) for m in re.finditer(r"‹thinking›\n(.*?)(?:‹/›|\Z)", text, re.S)]
    out += [m.group(1) for m in re.finditer(r"‹thinking›([^\n]*)\n", text)]
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--runs", required=True)
    ap.add_argument("--results", default="results.json")
    args = ap.parse_args()
    runs = pathlib.Path(args.runs)

    tot, worst = {}, []
    for r in json.load(open(args.results)):
        if r["fixture"] not in CHARTS or r.get("empty") or r.get("timeout"):
            continue
        p = runs / f"{r['tag']}.txt"
        if not p.exists():
            continue
        lines = [l for l in reasoning_of(p.read_text(errors="replace")).splitlines() if l.strip()]
        ar = [l for l in lines if ARITH.search(l) or COORD.search(l)]
        d = tot.setdefault(r["arm"], {"n": 0, "lines": 0, "arith": 0, "chars": 0})
        d["n"] += 1
        d["lines"] += len(lines)
        d["arith"] += len(ar)
        d["chars"] += sum(len(l) for l in ar)
        worst.append((sum(len(l) for l in ar), r["tag"], len(ar), len(lines)))

    if not tot:
        print(f"no transcripts found under {runs}")
        return 1
    print(f"{'arm':<8}{'n':>4}{'lines':>8}{'arith':>8}{'share':>8}{'chars':>9}{'~tokens':>9}")
    for arm in ("none", "skill", "inline"):
        d = tot.get(arm)
        if not d:
            continue
        print(f"{arm:<8}{d['n']:>4}{d['lines'] // d['n']:>8}{d['arith'] // d['n']:>8}"
              f"{100 * d['arith'] / max(d['lines'], 1):>7.0f}%"
              f"{d['chars'] // d['n']:>9}{d['chars'] // d['n'] // 4:>9}")
    worst.sort(reverse=True)
    print("\nheaviest single runs:")
    for ch, tag, a, l in worst[:6]:
        print(f"  {tag:<28} {a:>4}/{l:<4} lines  ~{ch // 4} tokens")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
