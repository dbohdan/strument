"""Scores the description-trims trials from each session's record.

Counts, not judgments. The answer parser is the run_code arms trial's, with
its self-test, extended by two single-lookup tasks.

Usage: score.py TRIAL_DIR code|ask
"""
import glob, json, math, os, re, sys

KEYS = {"walk": 5, "total": 512180, "span": 239, "longest": 263, "cite": 212,
        "top5": ["asset-106", "asset-377", "asset-052", "asset-197", "asset-024"]}
SINGLE = {"readme": r"message relay", "main": r"cmd/relay/main\.go"}
AMBIGUOUS = {"name", "port", "log"}


def answer_text(msg):
    if not msg:
        return None
    text = msg.replace("**", "").replace("__", "")
    idx = [m.start() for m in re.finditer(r"(?m)^[ \t>*-]*ANSWER:", text)]
    return text[idx[-1]:] if idx else None


def is_correct(task, ans):
    if ans is None:
        return None
    if task in SINGLE:
        return re.search(SINGLE[task], ans, re.I) is not None
    if task == "top5":
        return re.findall(r"asset-\d{3}", ans)[:5] == KEYS["top5"]
    nums = re.findall(r"\d[\d,_]*\d|\d", ans.split("\n", 1)[0])
    return bool(nums) and int(re.sub(r"[,_]", "", nums[-1])) == KEYS[task]


CASES = [("span", "ANSWER: 239", True), ("span", "ANSWER: 240", False), ("total", "ANSWER: 512,180", True),
         ("top5", "ANSWER: asset-106, asset-377, asset-052, asset-197, asset-024", True),
         ("top5", "ANSWER: asset-377, asset-106, asset-052, asset-197, asset-024", False),
         ("readme", "ANSWER: A small message relay.", True), ("readme", "ANSWER: a proxy", False),
         ("main", "ANSWER: `cmd/relay/main.go`", True), ("main", "ANSWER: main.go", False)]
bad = [c for c in CASES if is_correct(c[0], answer_text("Some text 12.\n" + c[1])) != c[2]]
assert not bad, f"answer parser self-test failed: {bad}"


def calls(recs):
    out = []
    for r in recs:
        for tc in r.get("tool_calls") or []:
            args = {}
            try:
                args = json.loads(tc.get("arguments") or "{}")
            except json.JSONDecodeError:
                pass
            out.append((tc.get("name"), args))
    return out


def rejected(recs):
    """Counts ask_user_question calls Strument refused as malformed (post hoc)."""
    ids, n = set(), 0
    for r in recs:
        for tc in r.get("tool_calls") or []:
            if tc.get("name") == "ask_user_question":
                ids.add(tc.get("id"))
        if r.get("role") == "tool" and r.get("tool_call_id") in ids:
            n += "unavailable without" not in (r.get("text") or r.get("summary") or "")
    return n


def score(work):
    res = json.load(open(os.path.join(work, "result.json")))
    path = os.path.join(work, "session.jsonl")
    recs = [json.loads(l) for l in open(path)] if os.path.exists(path) else []
    turn = next((r for r in reversed(recs) if r.get("type") == "turn"), {})
    cs = calls(recs)
    row = dict(res, steps=turn.get("steps"), cost=turn.get("cost"), outcome=turn.get("outcome"),
               tools=[n for n, _ in cs], files=turn.get("files") or [])
    if res["set"] == "code":
        row["run_code"] = any(n == "run_code" for n, _ in cs)
        row["correct"] = is_correct(res["task"], answer_text(turn.get("answer")))
    else:
        asks = [a for n, a in cs if n == "ask_user_question"]
        # A malformed call (post hoc: MiMo in the trim arm sent `questions` as a
        # JSON string of options) still counts as asking, and makes the
        # session's questions not well formed. Recommendation-first is read
        # from the well-formed questions only.
        raw = [q for a in asks for q in (a.get("questions") if isinstance(a.get("questions"), list) else [None])]
        qs = [q for q in raw if isinstance(q, dict) and q.get("options")]
        row["malformed"] = len(raw) - len(qs)
        row["rejected"] = rejected(recs)
        row["asked"] = bool(asks)
        row["questions"] = len(qs)
        row["options_ok"] = (not row["malformed"] and all(2 <= len(q["options"]) <= 4 for q in qs)) if raw else None
        # The recommendation convention: the first option says it is the recommended one.
        row["recommended_first"] = (all(re.search(r"recommend", ((q.get("options") or [{}])[0].get("description") or "") + ((q.get("options") or [{}])[0].get("label") or ""), re.I)
                                        for q in qs) if qs else None)
        row["edited"] = bool(row["files"])
    return row


def fisher(a, b, c, d):
    n, r1, c1 = a + b + c + d, a + b, a + c
    tot = math.comb(n, c1)
    p0 = math.comb(r1, a) * math.comb(n - r1, c1 - a) / tot
    return min(1.0, sum(math.comb(r1, x) * math.comb(n - r1, c1 - x) / tot
                        for x in range(max(0, c1 - (n - r1)), min(r1, c1) + 1)
                        if math.comb(r1, x) * math.comb(n - r1, c1 - x) / tot <= p0 * (1 + 1e-9)))


def main():
    trial, name = sys.argv[1], sys.argv[2]
    rows = [score(os.path.dirname(p)) for p in sorted(glob.glob(os.path.join(trial, name, "*", "result.json")))]
    with open(os.path.join(trial, name, "scored.jsonl"), "w") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")
    arms = sorted({r["arm"] for r in rows}, key=lambda a: a != "base")
    frac = lambda xs, k: f"{sum(1 for x in xs if x.get(k))}/{len(xs)}"
    cost = lambda xs: sum(x.get("cost") or 0 for x in xs)
    print(f"{len(rows)} sessions, ${cost(rows):.3f}; exits other than 0: "
          f"{[r['tag'] for r in rows if r.get('exit') != 0]}")
    if name == "code":
        multi = [r for r in rows if r["task"] not in SINGLE]
        single = [r for r in rows if r["task"] in SINGLE]
        print("| arm | model | run_code on multi-lookup | correct (multi) | run_code on single-lookup | correct (single) | mean steps | cost |")
        print("|---|---|---|---|---|---|---|---|")
        for a in arms:
            for m in ("mimo", "deepseek", None):
                g = [r for r in rows if r["arm"] == a and (m is None or r["model"] == m)]
                gm, gs = [r for r in g if r in multi], [r for r in g if r in single]
                print(f"| {a} | {m or 'all'} | {frac(gm, 'run_code')} | {frac(gm, 'correct')} | {frac(gs, 'run_code')} "
                      f"| {frac(gs, 'correct')} | {sum(r['steps'] or 0 for r in g) / max(1, len(g)):.1f} | ${cost(g):.3f} |")
        for k, sub in (("run_code", multi), ("correct", multi), ("run_code", single)):
            x = [r for r in sub if r["arm"] == arms[1]]
            y = [r for r in sub if r["arm"] == arms[0]]
            a, c = sum(1 for r in x if r.get(k)), sum(1 for r in y if r.get(k))
            print(f"{arms[1]} vs {arms[0]}, {k} ({'multi' if sub is multi else 'single'}): {a}/{len(x)} vs {c}/{len(y)}, "
                  f"p = {fisher(a, len(x) - a, c, len(y) - c):.3f}")
    else:
        amb = [r for r in rows if r["task"] in AMBIGUOUS]
        clear = [r for r in rows if r["task"] not in AMBIGUOUS]
        print("| arm | model | asked (ambiguous) | asked (clear) | recommended first | options 2-4 | calls rejected (sessions) | edited (clear) | cost |")
        print("|---|---|---|---|---|---|---|---|---|")
        for a in arms:
            for m in ("mimo", "deepseek", None):
                g = [r for r in rows if r["arm"] == a and (m is None or r["model"] == m)]
                asked = [r for r in g if r["asked"]]
                print(f"| {a} | {m or 'all'} | {frac([r for r in g if r in amb], 'asked')} | {frac([r for r in g if r in clear], 'asked')} "
                      f"| {frac(asked, 'recommended_first')} | {frac(asked, 'options_ok')} "
                      f"| {sum(r['rejected'] for r in g)} ({frac(g, 'rejected')}) "
                      f"| {frac([r for r in g if r in clear], 'edited')} | ${cost(g):.3f} |")
        for k, sub, label in (("asked", amb, "ambiguous"), ("asked", clear, "clear")):
            x = [r for r in sub if r["arm"] == arms[1]]
            y = [r for r in sub if r["arm"] == arms[0]]
            a, c = sum(1 for r in x if r.get(k)), sum(1 for r in y if r.get(k))
            print(f"{arms[1]} vs {arms[0]}, asked ({label}): {a}/{len(x)} vs {c}/{len(y)}, p = {fisher(a, len(x) - a, c, len(y) - c):.3f}")
        x = [r for r in rows if r["arm"] == arms[1] and r["asked"]]
        y = [r for r in rows if r["arm"] == arms[0] and r["asked"]]
        a, c = sum(1 for r in x if r["recommended_first"]), sum(1 for r in y if r["recommended_first"])
        print(f"{arms[1]} vs {arms[0]}, recommended first: {a}/{len(x)} vs {c}/{len(y)}, p = {fisher(a, len(x) - a, c, len(y) - c):.3f}")
        x = [r for r in rows if r["arm"] == arms[1]]
        y = [r for r in rows if r["arm"] == arms[0]]
        a, c = sum(1 for r in x if r["rejected"]), sum(1 for r in y if r["rejected"])
        print(f"{arms[1]} vs {arms[0]}, sessions with a rejected call (post hoc): {a}/{len(x)} vs {c}/{len(y)}, "
              f"p = {fisher(a, len(x) - a, c, len(y) - c):.3f}")


if __name__ == "__main__":
    main()
