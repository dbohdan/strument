"""What context window does a realistic side model actually have?

summaryFallbackInput is the number used when a model's `context` is unset, and
4096 was inherited rather than measured. The honest way to pick it is to look at
what people run: this takes one user's Strument model history, weighted by how
often each model was used, and reads each model's real context_length from
OpenRouter's catalogue.

The names in the history are display names — the slug reduced to its core, with
the provider prefix and any :variant suffix stripped (config.model's
display_name default) — so they are matched back against the catalogue by that
same reduction rather than by exact id.
"""

import collections
import json
import pathlib

HERE = pathlib.Path(__file__).parent

# From the user's ~/.local/state/strument history, most-used first.
USAGE = [
    (150, "mimo-v2.5"),
    (140, "glm-5.3-flash"),
    (92, "gpt-5.6-luna"),
    (85, "deepseek-v4-flash-0731"),
    (76, "mimo-v2.5-pro"),
    (49, "qwen3.8"),
    (39, "qwen3.6-uncensored"),
    (34, "glm-5.3"),
    (23, "hy3"),
    (22, "deepseek-v4-pro:nitro"),
    (20, "claude-haiku-4.5"),
    (18, "glm-5.2"),
    (14, "qwen3.6-35b-a3b"),
    (13, "ling-3.0-tiny"),
    (12, "deepseek-v4.1-flash"),
    (11, "deepseek-v4-flash:nitro"),
    (11, "claude-sonnet-5"),
    (9, "bonsai-8b"),
    (7, "minicpm5-2b"),
    (7, "laguna-s-2.1"),
    (7, "kimi-k3"),
    (5, "ling-3.0-flash"),
    (4, "glm-4.7"),
    (3, "lfm2.5-2.6b"),
    (3, "granite-4.3-3b"),
    (3, "gemini-3.7-flash"),
    (2, "qwen3.5-9b"),
    (2, "minimax-m3"),
    (2, "maple-preview"),
    (2, "kimi-k2.7-code"),
    (2, "granite-4.2-8b"),
    (2, "deepseek-v4-pro-0813"),
    (1, "north-mini-code-1.0"),
    (1, "gpt-5.6-terra"),
    (1, "gemini-3.6-flash"),
    (1, "claude-fable-5"),
    (1, "bonsai-27b"),
]


def core(slug):
    """The display_name default: drop the provider prefix and :variant suffix."""
    s = slug.split("/")[-1]
    return s.split(":")[0]


def main():
    cat = json.loads((HERE / "models.json").read_text())["data"]
    by_core = {}
    for m in cat:
        c = core(m["id"])
        ctx = m.get("context_length") or 0
        # Several ids can reduce to one core (a :free or :nitro variant beside
        # the base). Keep the largest, which is what the base model offers.
        by_core[c] = max(by_core.get(c, 0), ctx)

    rows = []
    missing = []
    for n, name in USAGE:
        ctx = by_core.get(core(name))
        if not ctx:
            missing.append((n, name))
            continue
        rows.append((n, name, ctx))

    total = sum(n for n, _, _ in rows)
    print(f"matched {len(rows)} of {len(USAGE)} models, {total} of {sum(n for n, _ in USAGE)} sessions")
    if missing:
        print("unmatched:", ", ".join(f"{name} ({n})" for n, name in missing))
    print()

    print(f"{'model':28s} {'uses':>5s} {'context':>9s} {'ctx/8':>8s}")
    for n, name, ctx in rows:
        print(f"{name:28s} {n:5d} {ctx:9d} {ctx // 8:8d}")

    # Usage-weighted percentiles: one entry per session, so a model run 150
    # times counts 150 times. The question is "what window does a call
    # typically have", not "what window does a catalogue entry have".
    weighted = []
    for n, _, ctx in rows:
        weighted.extend([ctx] * n)
    weighted.sort()

    def pct(p):
        return weighted[int(len(weighted) * p)]

    print("\n=== usage-weighted context window")
    for p, label in [(0.0, "min"), (0.05, "p5"), (0.1, "p10"), (0.25, "p25"), (0.5, "median")]:
        v = weighted[0] if p == 0 else pct(p)
        print(f"{label:>7s} {v:9d} tokens   ({v // 8:7d} at ctx/8)")

    print("\n=== how many sessions fall below a candidate floor")
    for floor in (4096, 8192, 16384, 24576, 32768, 65536):
        below = sum(1 for c in weighted if c < floor)
        print(f"  floor {floor:6d}: {below:4d}/{len(weighted)} sessions ({100 * below / len(weighted):.0f}%) have a smaller window")

    print("\n=== how many sessions fall below a candidate floor, at ctx/8")
    for floor in (2048, 4096, 8192, 16384, 24576):
        below = sum(1 for c in weighted if c // 8 < floor)
        print(f"  floor {floor:6d}: {below:4d}/{len(weighted)} sessions ({100 * below / len(weighted):.0f}%) have a smaller ctx/8")

    small = sorted({(c, name) for _, name, c in rows if c < 65536})
    print("\n=== the small-window models, which are what a floor is for")
    for c, name in small:
        print(f"  {name:28s} {c:8d} ({c // 8} at ctx/8)")


if __name__ == "__main__":
    main()
