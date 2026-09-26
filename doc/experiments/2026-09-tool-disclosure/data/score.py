"""Score probe answers: tools named that were not offered, and offered tools
left out. A reply that called a tool or said nothing is "no answer", a
separate column rather than an error of either kind.

Usage: python3 score.py PROBE.jsonl
"""

import collections
import json
import re
import sys


# Names a provider's serving layer adds on its own, which Strument never
# offered and the model did not invent: OpenAI's parallel-call wrapper.
PLATFORM = {"multi_tool_use.parallel"}


def names(answer):
    """The tool names in an answer: split on commas and newlines; strip list
    markup, backticks, quotes and trailing punctuation, and OpenAI's
    "functions." namespace, which GPT models put on every name."""
    parts = re.split(r"[,\n]+", answer)
    out = set()
    for p in parts:
        p = re.sub(r"^\s*(?:[-*]|\d+[.)])\s*", "", p)
        p = p.strip().strip("`'\":;*").strip().rstrip(".")
        if p.lower().startswith("functions."):
            p = p[len("functions."):]
        if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_.]*", p):
            out.add(p.lower())
    return out


def score(row):
    if row["error"] or row["tool_calls"] or not row["answer"].strip():
        return None
    got, offered = names(row["answer"]), {n.lower() for n in row["offered"]}
    return {"extra": sorted(got - offered - PLATFORM), "missing": sorted(offered - got),
            "platform": sorted(got & PLATFORM)}


def selftest():
    assert names("read, grep, `glob`, ls.") == {"read", "grep", "glob", "ls"}
    assert names("- read\n- run_code\n2) bash") == {"read", "run_code", "bash"}
    s = score({"error": None, "tool_calls": False, "answer": "read, ls, edit, write, shell",
               "offered": ["read", "ls", "edit", "write", "bash"]})
    assert s == {"extra": ["shell"], "missing": ["bash"], "platform": []}, s
    # The GPT form, found by the pilot: every name namespaced, plus a wrapper
    # the provider adds. Scored as a complete answer, not as fourteen misses.
    s = score({"error": None, "tool_calls": False, "offered": ["read", "run_code"],
               "answer": "functions.read, functions.run_code, multi_tool_use.parallel"})
    assert s == {"extra": [], "missing": [], "platform": ["multi_tool_use.parallel"]}, s
    assert score({"error": None, "tool_calls": True, "answer": "", "offered": ["read"]}) is None


def main():
    selftest()
    rows = [json.loads(line) for line in open(sys.argv[1])]
    cells = collections.defaultdict(lambda: {"n": 0, "none": 0, "wrong": 0, "extra": 0, "missing": 0})
    examples = collections.defaultdict(list)
    for r in rows:
        model = r["model"].split("/")[-1]
        c = cells[(model, r["depth"], r["arm"])]
        c["n"] += 1
        s = score(r)
        if s is None:
            c["none"] += 1
            continue
        if s["extra"] or s["missing"]:
            c["wrong"] += 1
            examples[(model, r["arm"])].append((r["session"], r["depth"], s))
        c["extra"] += len(s["extra"])
        c["missing"] += len(s["missing"])
    print(f"{'model':24} {'depth':6} arm   n  no-answer  wrong  extra  missing")
    for k in sorted(cells):
        c = cells[k]
        print(f"{k[0]:24} {k[1]:6} {k[2]:>3} {c['n']:3} {c['none']:10} {c['wrong']:6} {c['extra']:6} {c['missing']:8}")
    print()
    for k in sorted(examples):
        for e in examples[k][:3]:
            print(k, e)


if __name__ == "__main__":
    main()
