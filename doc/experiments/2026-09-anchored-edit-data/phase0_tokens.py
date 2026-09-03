#!/usr/bin/env python3
"""Phase 0 of the anchored-edit trial: what does each read format cost?

The input half of the token question needs no models. Render a corpus in each
arm's read format, tokenize, compare. See
doc/experiments/2026-09-anchored-edit-preregistration.md.

Four formats, chosen so the two changes yoneda makes are separable:

    A       12<TAB>\tfunc f() {              today (toolobserve.go:105)
    C       12<TAB>1 tab<TAB>func f() {      + the indent column
    D_tab   clever-torrent<TAB>1 tab<TAB>…   + anchors instead of line numbers
    D       clever-torrent ║ 1 tab ║ …       + yoneda's heavy-bar separator

A->C isolates the indent column, C->D_tab the address, D_tab->D the separator.
Arm B of the ladder changes no format, so B == A here by construction; it is
listed in the output as a reminder that its cost is zero.

Two tokenizers, because a conclusion that depends on one is not a conclusion:
OpenAI's o200k_base (gpt-5.6-luna) and Qwen's BPE (representative of the rest
of the panel, which is mostly Qwen-family). Calibration against what a provider
actually meters is rig check 5 and is still pending — this script measures
relative overhead, which is what the decision turns on.

The word list is written here rather than borrowed, so nothing of yoneda's is
copied into this repository. What matters for the measurement is only that
anchors are dash-joined common short English words, which is the property that
makes them tokenize well.
"""

import argparse
import json
import os
import random
import sys

# 128 common short words: two-word anchors give 16k unique ids per file, which
# is more than any file here needs, and every word is frequent enough to be a
# single token in both tokenizers.
WORDS = """able acorn amber anchor apple arbor arrow autumn basin beacon
bell birch black blue bold brass brave brick bright bronze brook calm cedar
chalk cherry clay clever cliff cloud clover coal cobalt copper coral cotton
crane creek crisp crown dawn deep dew dusk eagle east ember fair fern flat
flint fog forest fox glass gleam glossy gold grand granite grass green grove
harbor hardy haze hill iron ivory jade juniper lake lantern lark leaf light
lily linen loam maple marsh meadow mint misty moss noble north oak ocean olive
onyx opal orchid otter palm pearl pebble petal pine plum polite quartz quiet
rapid reed ridge river rose rust sage sand shale silver slate slow soft spruce
stone storm swift thicket tide torrent trail vale vast vine violet volcano
white wide willow wise""".split()


def split_indent(line):
    """The leading run of spaces and tabs, and the rest."""
    i = 0
    while i < len(line) and line[i] in " \t":
        i += 1
    return line[:i], line[i:]


def encode_indent(run):
    """Render a whitespace run as words: '3 tabs', '1 tab 2 spaces'.

    The empty run is '0 spaces' so the column is always present, which is what
    gives the row a fixed arity for the reader.
    """
    if not run:
        return "0 spaces"
    out = []
    i = 0
    while i < len(run):
        ch = run[i]
        n = 0
        while i < len(run) and run[i] == ch:
            n += 1
            i += 1
        unit = "tab" if ch == "\t" else "space"
        out.append(f"{n} {unit}" + ("" if n == 1 else "s"))
    return " ".join(out)


def mint_anchors(n, rng):
    """n distinct dash-joined word anchors."""
    seen, out = set(), []
    while len(out) < n:
        a = f"{rng.choice(WORDS)}-{rng.choice(WORDS)}"
        if a in seen:
            continue
        seen.add(a)
        out.append(a)
    return out


def render(fmt, lines, anchors):
    width = len(str(len(lines)))
    rows = []
    for i, line in enumerate(lines):
        num = f"{i + 1:>{width}}"
        run, rest = split_indent(line)
        if fmt == "A":
            rows.append(f"{num}\t{line}")
        elif fmt == "C":
            rows.append(f"{num}\t{encode_indent(run)}\t{rest}")
        elif fmt == "D_tab":
            rows.append(f"{anchors[i]}\t{encode_indent(run)}\t{rest}")
        elif fmt == "D":
            rows.append(f"{anchors[i]} ║ {encode_indent(run)} ║ {rest}")
        else:
            raise ValueError(fmt)
    return "\n".join(rows)


FORMATS = ["A", "C", "D_tab", "D"]

# $/M tokens, OpenRouter, 2026-09-03. Mid endpoint where a model has several.
PRICES = {
    "xiaomi/mimo-v2.5": (0.14, 0.28),
    "deepseek/deepseek-v4-flash-0731": (0.065, 0.18),
    "z-ai/glm-5.3-flash": (0.075, 0.25),
    "tencent/hy3": (0.132, 0.528),
    "openai/gpt-5.6-luna": (0.20, 1.20),
    "qwen/qwen3.8-27b": (0.425, 2.55),
}

# From 36 `turn` records across this directory's existing trials.
TURN_INPUT_MEDIAN = 32900
TURN_OUTPUT_MEDIAN = 800
OUTPUT_SAVING = 0.61  # oh-my-pi's headline, taken at face value for the sum


def load_tokenizers():
    import tiktoken
    from tokenizers import Tokenizer

    from huggingface_hub import hf_hub_download

    o200k = tiktoken.get_encoding("o200k_base")
    qwen = Tokenizer.from_file(
        hf_hub_download("Qwen/Qwen2.5-Coder-7B", "tokenizer.json")
    )
    return {
        "o200k": lambda s: len(o200k.encode(s, disallowed_special=())),
        "qwen": lambda s: len(qwen.encode(s, add_special_tokens=False).ids),
    }


def collect(paths, tokenizers, rng):
    per_file, totals = [], {}
    for path in paths:
        try:
            text = open(path, encoding="utf-8").read()
        except (UnicodeDecodeError, OSError):
            continue
        lines = text.split("\n")
        if lines and lines[-1] == "":
            lines.pop()
        if not lines:
            continue
        anchors = mint_anchors(len(lines), rng)
        rec = {"path": path, "lines": len(lines)}
        for fmt in FORMATS:
            body = render(fmt, lines, anchors)
            for tname, tok in tokenizers.items():
                n = tok(body)
                rec[f"{fmt}.{tname}"] = n
                totals[f"{fmt}.{tname}"] = totals.get(f"{fmt}.{tname}", 0) + n
        totals["lines"] = totals.get("lines", 0) + len(lines)
        per_file.append(rec)
    return per_file, totals


def breakeven_lines(extra_per_line, price_in, price_out):
    """How many lines a turn may read before the extra input costs more than
    a 61% output saving buys. Infinite when the format is not more expensive."""
    if extra_per_line <= 0:
        return float("inf")
    saved = OUTPUT_SAVING * TURN_OUTPUT_MEDIAN * price_out
    return saved / (extra_per_line * price_in)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=".")
    ap.add_argument("--ext", default=".go")
    ap.add_argument("--seed", type=int, default=20260903)
    ap.add_argument("--out", default=None)
    ap.add_argument("--label", default="go")
    args = ap.parse_args()

    rng = random.Random(args.seed)
    paths = []
    for dirpath, dirnames, filenames in os.walk(args.root):
        dirnames[:] = [
            d for d in dirnames if d not in {".git", "reference", "attic", "vendor"}
        ]
        for fn in sorted(filenames):
            if fn.endswith(tuple(args.ext.split(","))):
                paths.append(os.path.join(dirpath, fn))
    paths.sort()
    if not paths:
        sys.exit(f"no files matching {args.ext} under {args.root}")

    tokenizers = load_tokenizers()
    per_file, totals = collect(paths, tokenizers, rng)
    lines = totals["lines"]

    print(f"corpus: {len(per_file)} files, {lines} lines ({args.label})\n")
    print(f"{'format':8s} {'tokenizer':10s} {'tokens':>10s} {'tok/line':>9s} "
          f"{'vs A':>8s} {'extra/line':>11s}")
    summary = {}
    for tname in tokenizers:
        base = totals[f"A.{tname}"]
        for fmt in FORMATS:
            n = totals[f"{fmt}.{tname}"]
            extra = (n - base) / lines
            summary[f"{fmt}.{tname}"] = {
                "tokens": n,
                "tok_per_line": n / lines,
                "pct_vs_A": 100 * (n - base) / base,
                "extra_per_line": extra,
            }
            print(f"{fmt:8s} {tname:10s} {n:10d} {n / lines:9.2f} "
                  f"{100 * (n - base) / base:+7.1f}% {extra:+11.2f}")
        print()

    print("Arm B changes no format: its read cost is identical to A.\n")
    print("Lines a turn may read before the extra input outweighs a 61% output")
    print(f"saving, against a {TURN_INPUT_MEDIAN // 1000}k-input / "
          f"{TURN_OUTPUT_MEDIAN}-output median turn:\n")
    header = f"{'model':34s}" + "".join(f"{f:>12s}" for f in FORMATS[1:])
    print(header)
    breakevens = {}
    for model, (pin, pout) in PRICES.items():
        row = f"{model:34s}"
        for fmt in FORMATS[1:]:
            extra = summary[f"{fmt}.qwen"]["extra_per_line"]
            n = breakeven_lines(extra, pin, pout)
            breakevens[f"{model}.{fmt}"] = n
            row += f"{'never' if n == float('inf') else f'{n:.0f}':>12s}"
        print(row)

    if args.out:
        with open(args.out, "w") as f:
            json.dump(
                {
                    "label": args.label,
                    "seed": args.seed,
                    "files": len(per_file),
                    "lines": lines,
                    "summary": summary,
                    "breakeven_lines_qwen": breakevens,
                    "per_file": per_file,
                },
                f,
                indent=1,
                sort_keys=True,
            )
        print(f"\nwrote {args.out}")


if __name__ == "__main__":
    main()
