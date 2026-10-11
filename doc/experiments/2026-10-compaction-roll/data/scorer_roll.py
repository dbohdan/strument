"""Scores one roll-trial session: python3 -I scorer_roll.py <session-dir> | --selftest

User facts as in the threshold trial's scorer; derived facts are the longest of a
turn's five files and its line count, checked against the files themselves (the
truth) and against what the model said at the time (its own result). Counts only.
"""
import json, os, re, sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from scorer import score_text as score_user, ANSI  # noqa: E402

DERIVED = [3, 4, 11, 12]
WORK = "Two questions about work you did earlier"
ANS = re.compile(r"^\W*ANSWER([12])\W*:\s*(.*)$", re.M)
UNKNOWN = re.compile(r"don.t know|do not know|not sure|unknown|cannot recall|can.t recall|no record|not (?:in|available)", re.I)
PROMPT = re.compile(r"Read std/(\S+), std/(\S+), std/(\S+), std/(\S+) and std/(\S+), one file per step")

def lines_of(path):
    with open(path, encoding="utf-8", errors="replace") as f:
        return len(f.read().splitlines())

def truth(proj, files):
    counts = {f: lines_of(os.path.join(proj, "std", f)) for f in files}
    longest = max(counts, key=counts.get)
    return longest, counts[longest]

def own_claim(seg):
    """The line count the model gave in its turn answer: the last 'N lines' after its reasoning."""
    ans = seg.rsplit("‹/›", 1)[-1].split("Tokens:")[0]
    m = re.findall(r"(\d[\d,]*)\s*lines", ans)
    return int(m[-1].replace(",", "")) if m else None

def judge(answer, want_file, want_n, claim):
    nums = [int(x.replace(",", "")) for x in re.findall(r"\d[\d,]*", answer)]
    base = os.path.basename(want_file)
    return {"truth": want_n in nums and base in answer,
            "own": claim is not None and claim in nums,
            "unknown": bool(UNKNOWN.search(answer)) and want_n not in nums,
            "answered": bool(answer.strip())}

def score_text(text, batches, proj_truth):
    # User-fact answers end where the work questions begin, or theirs would be read as these.
    r = score_user(text.split(WORK)[0])
    # Turn i is the i-th stretch ending in a usage line. The echoed prompt wraps
    # at the terminal's width, mid-path, so it cannot be matched reliably; the
    # usage line ends every turn and does not wrap.
    segs = re.split(r"Tokens: [^\n]*\n", text)
    claims = {t: own_claim(segs[t]) for t in DERIVED if t < len(segs)}
    work = text.split(WORK)[1:]
    out = {}
    for k, pair in enumerate(((3, 11), (4, 12))):
        seg = work[k] if k < len(work) else ""
        answers = {m.group(1): m.group(2) for m in ANS.finditer(seg) if "giving the file" not in m.group(0)}
        for j, t in enumerate(pair):
            f, n = proj_truth[t]
            out[t] = judge(answers.get(str(j + 1), ""), f, n, claims.get(t)) if k < len(work) else None
    r["derived"] = {str(t): v for t, v in out.items()}
    r["derived_truth"] = sum(bool(v and v["truth"]) for v in out.values())
    r["derived_own"] = sum(bool(v and v["own"]) for v in out.values())
    r["derived_unknown"] = sum(bool(v and v["unknown"]) for v in out.values())
    r["claims_correct"] = sum(1 for t in DERIVED if claims.get(t) == proj_truth[t][1])
    r["rolls"] = text.count("Dropped the oldest")
    r["cleared"] = text.count("old tool result")
    r["fold_failures"] = text.count("Could not summarize")
    r["work_tool_calls"] = sum(len(re.findall(r"^(Read|Running|Searched|Listed|Found|Globbed) ", s, re.M)) for s in work)
    return r

def score(d, files):
    text = ANSI.sub("", open(os.path.join(d, "transcript.raw"), "rb").read().decode("utf-8", "replace"))
    batches = {t: files[t * 5:(t + 1) * 5] for t in DERIVED}
    proj_truth = {t: truth(os.path.join(d, "proj"), batches[t]) for t in DERIVED}
    r = score_text(text, batches, proj_truth)
    main = side = 0.0
    for l in open(os.path.join(d, "proxy.jsonl")):
        q = json.loads(l); c = (q.get("usage") or {}).get("cost") or 0
        if q["marks"]: main += c
        else: side += c
    r.update(cost=round(main + side, 5), side_cost=round(side, 5))
    return r

def selftest():
    batches = {3: ["a/x.go", "b", "c", "d", "e"], 4: ["f.go", "g", "h", "i", "j"],
               11: ["k.go", "l", "m", "n", "o"], 12: ["p.go", "q", "r", "s", "t"]}
    truth_ = {3: ("a/long.go", 700), 4: ("g.go", 50), 11: ("m.go", 300), 12: ("t.go", 99)}
    def turn(first):
        return f"Read std/{first}, std/b, std/c, std/d and std/e, one file per step, whole."
    filler = "".join(f"turn {i}\nTokens: 1\n" for i in range(3))
    text = (filler + turn("a/x.go") + "\n‹/›\nThe longest is long.go with 700 lines.\nTokens: 1\n"
            + turn("f.go") + "\n‹/›\ng.go is longest, 51 lines.\nTokens: 1\n"
            + WORK + " ... giving the file and the line count.\nANSWER1: long.go, 700 lines\nANSWER2: m.go — I don't know the count\n"
            + WORK + " ...\nANSWER1: g.go, 51 lines\nANSWER2: t.go, 99 lines\n")
    r = score_text(text, batches, truth_)
    d = r["derived"]
    assert d["3"]["truth"] and d["3"]["own"], d["3"]
    assert not d["11"]["truth"] and d["11"]["unknown"], d["11"]
    assert not d["4"]["truth"] and d["4"]["own"], d["4"]          # recalled its own wrong count
    assert d["12"]["truth"] and not d["12"]["own"], d["12"]        # no claim recorded for 12
    assert r["claims_correct"] == 1 and r["derived_truth"] == 2, r
    u = score_text("Two questions about things I told you ... Put the answers on their own lines as ANSWER1: and ANSWER2:.\n"
                   "ANSWER1: Marigold-417\nANSWER2: lantern_ridge_v3\n" + WORK + "\nANSWER1: x.go, 9\nANSWER2: y.go, 8\n",
                   batches, truth_)
    assert u["codename"] == "correct" and u["flag"] == "correct", u
    print("selftest ok")

if __name__ == "__main__":
    if sys.argv[1] == "--selftest":
        selftest()
    else:
        files = [l.strip() for l in open(sys.argv[2]) if l.strip()]
        print(json.dumps(score(sys.argv[1], files)))
