#!/usr/bin/env python3
"""Analyze the naming trial: counts only, no judge model.

    analyze.py RUNDIR            the report, as Markdown, on stdout
    analyze.py --self-test       the normalizer and the statistics on known inputs

RUNDIR holds one <model>.jsonl per model, as run.py collect writes them.
"""

import collections
import glob
import json
import math
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import prompts as P  # noqa: E402

KIND = {p[0]: p[1] for p in P.PROMPTS}
ORDER = ["claude-opus-5", "claude-opus-5.5", "gpt-6-sol"]
SHORT = {"claude-opus-5": "Opus 5", "claude-opus-5.5": "Opus 5.5", "gpt-6-sol": "GPT-6 Sol"}

# Titles stripped from the front of a name, so "Captain Elara Voss" and "Elara
# Voss" count as one name. A leading "the" is stripped too ("The Gilded
# Petrel"). Applied repeatedly: "The Lady ..." loses both.
TITLES = {"the", "captain", "capt", "capt.", "detective", "inspector", "commander", "admiral",
          "dr", "dr.", "doctor", "lord", "lady", "sir", "dame", "professor", "prof.", "mr.",
          "mrs.", "ms.", "miss", "mx.", "king", "queen", "general"}


# A leading thought block in the answer itself. The pilot found Opus 5, with
# reasoning set to "none", writing "<thought>The user wants me to ...</thought>riffle":
# the reasoning it was told to skip, inline. It is removed before scoring and
# counted in its own column.
THOUGHT = re.compile(r"^\s*<(thought|thinking)>.*?</\1>\s*", re.S | re.I)


def leaked(content):
    return bool(content and THOUGHT.match(content))


def normalize(content):
    """(name, compliant). The name is lowercased, unwrapped and title-stripped.
    A reply is compliant if its first line is a name of 1-6 words and nothing
    but blank lines follow it."""
    if content is None:
        return None, False
    content = THOUGHT.sub("", content)
    lines = [l for l in content.strip().splitlines() if l.strip()]
    if not lines:
        return None, False
    s = lines[0].strip()
    s = re.sub(r"^[#>\-\s]+", "", s)                     # heading, quote or bullet marks
    s = s.strip("*_`\"'“”‘’ ")
    s = re.sub(r"[.!;,:]+$", "", s).strip("*_`\"'“”‘’ ")
    words = s.split()
    while words and words[0].lower().rstrip(":") in TITLES and len(words) > 1:
        words = words[1:]
    name = " ".join(words).lower()
    compliant = len(lines) == 1 and 1 <= len(words) <= 6 and ":" not in s
    return (name or None), compliant


def entropy(counter):
    n = sum(counter.values())
    return -sum(c / n * math.log2(c / n) for c in counter.values()) if n else float("nan")


def sign_test(diffs):
    """Exact two-sided sign test on paired differences; ties dropped.
    Returns (positives, negatives, p)."""
    pos = sum(1 for d in diffs if d > 0)
    neg = sum(1 for d in diffs if d < 0)
    n = pos + neg
    if n == 0:
        return pos, neg, 1.0
    k = min(pos, neg)
    p = 2 * sum(math.comb(n, i) for i in range(k + 1)) / 2 ** n
    return pos, neg, min(1.0, p)


def load(rundir):
    rows = []
    for f in sorted(glob.glob(os.path.join(rundir, "*.jsonl"))):
        for line in open(f, encoding="utf-8"):
            r = json.loads(line)
            r["model"] = r["model"].split("/")[-1]
            r["name"], r["compliant"] = normalize(r["content"])
            rows.append(r)
    return rows


def cells(rows):
    """(model, arm, condition, prompt) -> Counter of compliant names."""
    out = collections.defaultdict(collections.Counter)
    for r in rows:
        if r["compliant"] and not r["error"]:
            out[(r["model"], r["arm"], r["condition"], r["prompt"])][r["name"]] += 1
    return out


def top1(c):
    n = sum(c.values())
    return c.most_common(1)[0][1] / n if n else float("nan")


def median(xs):
    xs = sorted(x for x in xs if x == x)
    if not xs:
        return float("nan")
    m = len(xs) // 2
    return xs[m] if len(xs) % 2 else (xs[m - 1] + xs[m]) / 2


def report(rundir):
    rows = load(rundir)
    C = cells(rows)
    models = [m for m in ORDER if any(r["model"] == m for r in rows)]
    pids = [p[0] for p in P.PROMPTS if any(r["prompt"] == p[0] for r in rows)]
    dids = [d for d in P.DELIBERATION_IDS if any(r["prompt"] == d and r["arm"] == "deliberate" for r in rows)]
    out = []
    w = out.append

    w("## Compliance and reasoning\n")
    w("| model | arm | n | errors | non-compliant | inline thought | reasoning > 0 | median reasoning tokens |")
    w("|---|---|---|---|---|---|---|---|")
    for m in models:
        for arm in ("off", "deliberate"):
            rs = [r for r in rows if r["model"] == m and r["arm"] == arm]
            if not rs:
                continue
            rt = [r["reasoning_tokens"] or 0 for r in rs]
            w(f"| {SHORT[m]} | {arm} | {len(rs)} | {sum(1 for r in rs if r['error'])} | "
              f"{sum(1 for r in rs if not r['error'] and not r['compliant'])} | "
              f"{sum(1 for r in rs if leaked(r['content']))} | "
              f"{sum(1 for x in rt if x > 0)} | {median(rt):g} |")

    w("\n## Concentration per cell (median over prompts)\n")
    w("Top-1 share: the most common name's share of a prompt's samples. Distinct: names per "
      "prompt. Entropy in bits; 20 samples cap it at 4.32.\n")
    w("| model | arm/condition | prompts | median top-1 | median distinct | median entropy |")
    w("|---|---|---|---|---|---|")
    for m in models:
        for arm, cond, ids in (("off", "bare", pids), ("off", "context", pids), ("deliberate", "bare", dids)):
            cs = [C[(m, arm, cond, p)] for p in ids if C[(m, arm, cond, p)]]
            if not cs:
                continue
            w(f"| {SHORT[m]} | {arm}/{cond} | {len(cs)} | {median([top1(c) for c in cs]):.2f} | "
              f"{median([len(c) for c in cs]):g} | {median([entropy(c) for c in cs]):.2f} |")

    w("\n## Paired comparisons (exact sign test on per-prompt top-1 share)\n")
    w("| comparison | prompts | first higher | second higher | p |")
    w("|---|---|---|---|---|")

    def cmp(label, a, b, ids):
        diffs = [top1(C[a(p)]) - top1(C[b(p)]) for p in ids if C[a(p)] and C[b(p)]]
        pos, neg, p = sign_test(diffs)
        w(f"| {label} | {len(diffs)} | {pos} | {neg} | {p:.4f} |")

    for i, m1 in enumerate(models):
        for m2 in models[i + 1:]:
            cmp(f"bare: {SHORT[m1]} vs {SHORT[m2]}", lambda p, m=m1: (m, "off", "bare", p),
                lambda p, m=m2: (m, "off", "bare", p), pids)
    for m in models:
        cmp(f"{SHORT[m]}: bare vs context", lambda p, m=m: (m, "off", "bare", p),
            lambda p, m=m: (m, "off", "context", p), pids)
    for m in models:
        cmp(f"{SHORT[m]}: off vs deliberate (bare)", lambda p, m=m: (m, "off", "bare", p),
            lambda p, m=m: (m, "deliberate", "bare", p), dids)

    w("\n## Does deliberation find new names, or new favorites?\n")
    w("Overlap: the share of deliberate samples whose name also occurs among the same model's "
      "20 bare samples for that prompt. Modal changed: prompts whose most common name differs "
      "between the two arms.\n")
    w("| model | prompts | overlap | modal changed | median top-1, off | median top-1, deliberate |")
    w("|---|---|---|---|---|---|")
    for m in models:
        tot = hit = changed = 0
        t_off, t_del = [], []
        for p in dids:
            off, dl = C[(m, "off", "bare", p)], C[(m, "deliberate", "bare", p)]
            if not off or not dl:
                continue
            tot += sum(dl.values())
            hit += sum(c for n, c in dl.items() if n in off)
            changed += off.most_common(1)[0][0] != dl.most_common(1)[0][0]
            t_off.append(top1(off))
            t_del.append(top1(dl))
        if tot:
            w(f"| {SHORT[m]} | {len(t_off)} | {hit / tot:.0%} | {changed}/{len(t_off)} | "
              f"{median(t_off):.2f} | {median(t_del):.2f} |")

    w("\n## Shared names across models (bare)\n")
    w("| pair | prompts with the same modal name | mean Jaccard of name sets |")
    w("|---|---|---|")
    for i, m1 in enumerate(models):
        for m2 in models[i + 1:]:
            same, jac, n = 0, [], 0
            for p in pids:
                a, b = C[(m1, "off", "bare", p)], C[(m2, "off", "bare", p)]
                if not a or not b:
                    continue
                n += 1
                same += a.most_common(1)[0][0] == b.most_common(1)[0][0]
                jac.append(len(set(a) & set(b)) / len(set(a) | set(b)))
            if n:
                w(f"| {SHORT[m1]} / {SHORT[m2]} | {same}/{n} | {sum(jac) / len(jac):.2f} |")

    w("\n## Modal name per prompt (bare), with its count of 20\n")
    w("| prompt | " + " | ".join(SHORT[m] for m in models) + " |")
    w("|---|" + "---|" * len(models))
    for p in pids:
        cellv = []
        for m in models:
            c = C[(m, "off", "bare", p)]
            cellv.append(f"{c.most_common(1)[0][0]} ({c.most_common(1)[0][1]})" if c else "")
        w(f"| {p} | " + " | ".join(cellv) + " |")

    w("\n## Character names: first names and surnames (bare and context)\n")
    for m in models:
        firsts, lasts = collections.Counter(), collections.Counter()
        for (mm, arm, cond, p), c in C.items():
            if mm != m or arm != "off" or KIND.get(p) != "character":
                continue
            for name, k in c.items():
                parts = name.split()
                if len(parts) >= 2:
                    firsts[parts[0]] += k
                    lasts[parts[-1]] += k
                elif parts:
                    firsts[parts[0]] += k
        total = sum(firsts.values())
        w(f"- **{SHORT[m]}** ({total} names). First: " +
          ", ".join(f"{n} {k}" for n, k in firsts.most_common(8)) +
          ". Last: " + ", ".join(f"{n} {k}" for n, k in lasts.most_common(8)) +
          f". Elara: {firsts['elara']}; Voss: {lasts['voss']}.")

    w("\n## Sound of the names (bare, all kinds)\n")
    w("| model | names | contains v | starts with v | contains th | ends in a | mean letters |")
    w("|---|---|---|---|---|---|---|")
    for m in models:
        names = [n for (mm, arm, cond, p), c in C.items() if mm == m and arm == "off" and cond == "bare"
                 for n, k in c.items() for _ in range(k)]
        if not names:
            continue
        f = lambda pred: f"{sum(1 for n in names if pred(n)) / len(names):.0%}"  # noqa: E731
        letters = [len(re.sub(r"[^a-z]", "", n)) for n in names]
        w(f"| {SHORT[m]} | {len(names)} | {f(lambda n: 'v' in n)} | "
          f"{f(lambda n: any(t.startswith('v') for t in n.split()))} | {f(lambda n: 'th' in n)} | "
          f"{f(lambda n: n.rstrip()[-1:] == 'a')} | {sum(letters) / len(letters):.1f} |")

    w("\n## Most common names overall (bare and context)\n")
    for m in models:
        allc = collections.Counter()
        for (mm, arm, cond, p), c in C.items():
            if mm == m and arm == "off":
                allc.update(c)
        w(f"- **{SHORT[m]}**: " + ", ".join(f"{n} {k}" for n, k in allc.most_common(12)))
    return "\n".join(out)


def self_test():
    bad = 0
    cases = [
        ("**Captain Elara Voss**", ("elara voss", True)),
        ("Elara Voss.", ("elara voss", True)),
        ("`quilm`", ("quilm", True)),
        ("*Thrice Fortunate*", ("thrice fortunate", True)),
        ("The Gilded Petrel", ("gilded petrel", True)),
        ("“Steadfast Hope”", ("steadfast hope", True)),
        ("Name: Elara Voss", ("name: elara voss", False)),
        ("Elara Voss\n\nShe is a cartographer who...", ("elara voss", False)),
        ("Here is a name for your captain, one that suggests weathered authority and humor", (None, False)),
        ("", (None, False)),
        ("Detective Inspector Mara Quayle", ("mara quayle", True)),
        ("The", ("the", True)),
        ("<thought>The user wants a name. Just the name.</thought>riffle", ("riffle", True)),
        ("<thinking>\nhm\n</thinking>\n\nElara Voss", ("elara voss", True)),
    ]
    for raw, want in cases:
        got = normalize(raw)
        if want[0] is None and raw:
            ok = got[1] is False
        else:
            ok = got == want
        if not ok:
            bad += 1
            print(f"FAIL normalize({raw!r}) = {got}, want {want}")
    for diffs, want_p in (([1] * 10, 2 / 1024), ([1, -1] * 5, 1.0), ([0] * 5, 1.0), ([1] * 6 + [-1], 0.125)):
        p = sign_test(diffs)[2]
        if abs(p - want_p) > 1e-9:
            bad += 1
            print(f"FAIL sign_test({diffs}) p = {p}, want {want_p}")
    c = collections.Counter({"a": 10, "b": 10})
    if abs(entropy(c) - 1.0) > 1e-9 or abs(top1(c) - 0.5) > 1e-9:
        bad += 1
        print("FAIL entropy/top1")
    print("self-test:", "ok" if not bad else f"{bad} failures")
    return bad


if __name__ == "__main__":
    if sys.argv[1] == "--self-test":
        sys.exit(1 if self_test() else 0)
    print(report(sys.argv[1]))
