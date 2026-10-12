"""Scores one compaction-threshold session from its transcript and proxy log.

usage: python3 -I scorer.py <session-dir>   |   python3 -I scorer.py --selftest
Counts only: answers by exact token, folds, tool calls in the question turns,
and OpenRouter's own per-request cost.
"""
import json, os, re, sys

EXPECT = [("codename", "marigold-417"), ("flag", "lantern_ridge_v3"), ("dbhost", "db-quill-82.internal"),
          ("budget", "bx-5528"), ("oncall", "ilse varga"), ("cohort", "cohort-teal-29")]
OLD = {"codename", "dbhost", "oncall"}
ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]|\r")
ECHO = "Two questions about things I told you"
ANS = re.compile(r"^\W*ANSWER([12])\W*:\s*(.*)$", re.M)
TOOL = re.compile(r"^(Read|Running|Searched|Listed|Found|Globbed) ", re.M)
UNKNOWN = re.compile(r"don.t know|do not know|not sure|unknown|wasn.t told|no record|not (?:mentioned|given|provided)", re.I)

def norm(s):
    return re.sub(r"[`*\"'“”]", "", s).strip().strip(".").lower()

def score_text(text):
    segs = text.split(ECHO)[1:]
    out = {}
    for k in range(3):
        seg = segs[k] if k < len(segs) else ""
        answers = {}
        for m in ANS.finditer(seg):
            if "Put the answers" in m.group(0):
                continue
            answers[m.group(1)] = m.group(2)
        for j, idx in enumerate("12"):
            key, want = EXPECT[k * 2 + j]
            if k >= len(segs):
                verdict = "no_turn"
            elif idx not in answers:
                verdict = "no_answer"
            elif want in norm(answers[idx]):
                verdict = "correct"
            elif UNKNOWN.search(answers[idx]):
                verdict = "unknown"
            else:
                verdict = "wrong"
            out[key] = verdict
        out[f"q{k}_tool_calls"] = len(TOOL.findall(seg))
    out["folds"] = text.count("Chat history compacted")
    out["recall_old"] = sum(out[k] == "correct" for k in OLD)
    out["recall_middle"] = sum(out[k] == "correct" for k, _ in EXPECT if k not in OLD)
    out["question_tool_calls"] = sum(out[f"q{k}_tool_calls"] for k in range(3))
    return out

def score(d):
    text = ANSI.sub("", open(os.path.join(d, "transcript.raw"), "rb").read().decode("utf-8", "replace"))
    r = score_text(text)
    main = side = 0.0; prompt = cached = 0; n = 0
    p = os.path.join(d, "proxy.jsonl")
    if os.path.exists(p):
        for l in open(p):
            q = json.loads(l); u = q.get("usage") or {}; c = u.get("cost") or 0
            if q["marks"]:
                main += c; prompt += u.get("prompt_tokens") or 0
                cached += (u.get("prompt_tokens_details") or {}).get("cached_tokens") or 0; n += 1
            else:
                side += c
    r.update(cost=round(main + side, 5), side_cost=round(side, 5), requests=n, prompt_tokens=prompt,
             hit_rate=round(cached / prompt, 4) if prompt else None)
    return r

def selftest():
    q = ECHO + " ... Put the answers on their own lines as ANSWER1: and ANSWER2:.\n"
    t = ("noise\n" + q + "Read std/x.go (10 lines)\nANSWER1: **Marigold-417**\nANSWER2: lantern_ridge_v2\n"
         + q + "ANSWER1: I don't know\n"
         + q + "**ANSWER1:** Ilse Varga.\nANSWER2: `cohort-teal-29`\nChat history compacted: 1 -> 2\n")
    r = score_text(t)
    want = {"codename": "correct", "flag": "wrong", "dbhost": "unknown", "budget": "no_answer",
            "oncall": "correct", "cohort": "correct", "q0_tool_calls": 1, "folds": 1,
            "recall_old": 2, "recall_middle": 1}
    bad = {k: (r[k], v) for k, v in want.items() if r[k] != v}
    r2 = score_text("no questions here\nANSWER1: Marigold-417\n")
    if r2["codename"] != "no_turn":
        bad["no_turn"] = r2["codename"]
    print("selftest", "FAILED " + repr(bad) if bad else "ok"); sys.exit(1 if bad else 0)

if __name__ == "__main__":
    if sys.argv[1] == "--selftest":
        selftest()
    print(json.dumps(score(sys.argv[1])))
