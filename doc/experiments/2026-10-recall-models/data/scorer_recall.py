"""Scores one recall-trial session: python3 -I scorer_recall.py <session-dir> <files.txt> | --selftest

The roll trial's scores (scorer_roll), plus the recall calls read from the
session record, which keeps each call's arguments: how many, in which turns,
with which pattern and scope. Counts only.
"""
import glob, json, os, sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import scorer_roll  # noqa: E402

QUESTION_TURNS = range(16, 21)  # 0-based: three user-fact turns, two derived

def recall_calls(records):
    """(turn, args) for every recall call; turn is 0-based, counted by turn records."""
    out, turn, seen = [], 0, False
    for r in records:
        if r.get("type") == "turn":
            if seen:
                turn += 1
                seen = False
        elif r.get("type") == "message":
            seen = True
            for tc in r.get("tool_calls") or []:
                if tc.get("name") == "recall":
                    try:
                        args = json.loads(tc.get("arguments") or "{}")
                    except ValueError:
                        args = {"unparsed": tc.get("arguments")}
                    out.append((turn, args))
    return out

def records_of(d):
    recs = []
    for p in sorted(glob.glob(os.path.join(d, "state", "strument", "projects", "*", "sessions", "*", "log", "*.jsonl"))):
        recs += [json.loads(l) for l in open(p) if l.strip()]
    return recs

def prose_unknowns(d, r):
    """Post-hoc fix (doc/experiments/2026-10-recall, Results): an answer of "I
    don't know" in prose, without ANSWER lines, was scored unanswered. Where a
    work-question turn has no ANSWER lines and its final text says it does not
    know, both of its questions count as "don't know". True and wrong counts
    cannot change: neither depends on an unparsed answer."""
    text = scorer_roll.ANSI.sub("", open(os.path.join(d, "transcript.raw"), "rb").read().decode("utf-8", "replace"))
    work = text.split(scorer_roll.WORK)[1:]
    for k, pair in enumerate(((3, 11), (4, 12))):
        if k >= len(work) or scorer_roll.ANS.search(work[k]):
            continue
        final = work[k].rsplit("\u2039/\u203a", 1)[-1].split("Tokens:")[0]
        if scorer_roll.UNKNOWN.search(final):
            for t in pair:
                v = r["derived"].get(str(t))
                if v and not v["answered"]:
                    v["unknown"] = True
    r["derived_unknown"] = sum(bool(v and v["unknown"]) for v in r["derived"].values())

def score(d, files):
    r = scorer_roll.score(d, files)
    prose_unknowns(d, r)
    calls = recall_calls(records_of(d))
    r["recall_calls"] = len(calls)
    r["recall_in_questions"] = sum(1 for t, _ in calls if t in QUESTION_TURNS)
    r["recall_before_questions"] = sum(1 for t, _ in calls if t < QUESTION_TURNS.start)
    r["recall_from_all"] = sum(1 for _, a in calls if a.get("from") in ("all", "tools"))
    r["recall_args"] = [[t, a] for t, a in calls]
    return r

def selftest():
    recs = [{"type": "message", "role": "user"}, {"type": "turn"},
            {"type": "message", "role": "assistant", "tool_calls": [{"name": "recall", "arguments": '{"pattern":"x"}'}]},
            {"type": "message", "role": "tool"}, {"type": "turn"},
            {"type": "message", "role": "assistant", "tool_calls": [{"name": "read", "arguments": "{}"},
                                                                   {"name": "recall", "arguments": '{"pattern":"y","from":"all"}'}]}]
    got = recall_calls(recs)
    assert got == [(1, {"pattern": "x"}), (2, {"pattern": "y", "from": "all"})], got
    print("selftest ok")

if __name__ == "__main__":
    if sys.argv[1] == "--selftest":
        selftest()
    else:
        files = [l.strip() for l in open(sys.argv[2]) if l.strip()]
        print(json.dumps(score(sys.argv[1], files)))
