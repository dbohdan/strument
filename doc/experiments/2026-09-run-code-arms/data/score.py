"""Scores the run_code arms trial from each session's record.

Refuses to report unless its answer parser and the probe's classifier pass
their self-tests. Writes scored.jsonl (one row per session, programs and
results included) so the numbers can be recomputed without the raw runs.
"""
import glob, json, math, os, re, sys
from collections import defaultdict

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import classify

KEYS = {"walk": 5, "total": 512180, "span": 239, "longest": 263, "cite": 212,
        "top5": ["asset-106", "asset-377", "asset-052", "asset-197", "asset-024"]}
ARMS = ["monty", "monty-open", "js"]


def answer_text(msg):
    """Everything from the last ANSWER: marker on, markdown emphasis removed."""
    if not msg:
        return None
    text = msg.replace("**", "").replace("__", "")
    idx = [m.start() for m in re.finditer(r"(?m)^[ \t>*-]*ANSWER:", text)]
    return text[idx[-1]:] if idx else None


def is_correct(task, ans):
    if ans is None:
        return None
    if task == "top5":
        return re.findall(r"asset-\d{3}", ans)[:5] == KEYS["top5"]
    first_line = ans.split("\n", 1)[0]
    nums = re.findall(r"\d[\d,_]*\d|\d", first_line)
    if not nums:
        return False
    return int(re.sub(r"[,_]", "", nums[-1])) == KEYS[task]


ANSWER_CASES = [
    ("span", "ANSWER: 239", True), ("span", "**ANSWER:** 239 days", True),
    ("span", "ANSWER: 2026-01-04 to 2026-08-31: 239 days", True),
    ("span", "ANSWER: 240", False), ("span", "ANSWER: 239.5", False),
    ("total", "ANSWER: 512,180", True), ("total", "ANSWER: 512180", True), ("total", "ANSWER: 512181", False),
    ("walk", "ANSWER: 5 files", True), ("walk", "ANSWER: 4", False),
    ("cite", "ANSWER: line 212", True), ("cite", "ANSWER: 211", False),
    ("longest", "ANSWER: 263 characters", True),
    ("top5", "ANSWER: asset-106, asset-377, asset-052, asset-197, asset-024", True),
    ("top5", "ANSWER:\n1. asset-106 (99205)\n2. asset-377\n3. asset-052\n4. asset-197\n5. asset-024", True),
    ("top5", "ANSWER: asset-377, asset-106, asset-052, asset-197, asset-024", False),
    ("top5", "ANSWER: asset-106, asset-377, asset-052, asset-197", False),
]


def selftest():
    bad = []
    for task, text, want in ANSWER_CASES:
        got = is_correct(task, answer_text("Some reasoning, 12 and 99.\n" + text))
        if got != want:
            bad.append((task, text, want, got))
    if answer_text("no marker here") is not None:
        bad.append(("marker", "no marker", None, "found"))
    return bad + classify.selftest()


def fisher(a, b, c, d):
    n1, n2, k = a + b, c + d, a + c
    n = n1 + n2
    if n == 0 or k == 0 or k == n:
        return 1.0
    p = lambda x: math.comb(n1, x) * math.comb(n2, k - x) / math.comb(n, k)
    obs = p(a)
    return min(1.0, sum(p(x) for x in range(max(0, k - n2), min(k, n1) + 1) if p(x) <= obs * (1 + 1e-9)))


def load_session(run):
    """(records, blob resolver) for one run directory."""
    logs = glob.glob(os.path.join(run, "state", "strument", "projects", "*", "sessions", "*", "log", "*.jsonl"))
    blobs = glob.glob(os.path.join(run, "state", "strument", "projects", "*", "blobs"))
    recs = []
    for path in sorted(logs):
        recs += [json.loads(l) for l in open(path) if l.strip()]

    def resolve(blob):
        for d in blobs:
            p = os.path.join(d, blob)
            if os.path.exists(p):
                return open(p, encoding="utf-8", errors="replace").read()
        return None
    return recs, resolve


def scan(run, meta):
    recs, resolve = load_session(run)
    row = dict(meta)
    row.update({"programs": [], "results": [], "answer": None, "steps": None, "sent": None,
                "received": None, "cost": None, "records": len(recs)})
    pending = {}
    last_text = None
    for r in recs:
        if r.get("type") == "message" and r.get("role") == "assistant":
            if r.get("text"):
                last_text = r["text"]
            for tc in r.get("tool_calls") or []:
                if tc.get("name") != "run_code":
                    continue
                args = tc.get("arguments") or (resolve(tc["blob"]) if tc.get("blob") else None) or "{}"
                try:
                    code = json.loads(args).get("code", "")
                except json.JSONDecodeError:
                    code = ""
                pending[tc.get("id")] = len(row["programs"])
                row["programs"].append(code)
                row["results"].append(None)
        elif r.get("type") == "message" and r.get("role") == "tool" and r.get("tool_call_id") in pending:
            text = r.get("text")
            if text is None and r.get("blob"):
                text = resolve(r["blob"]) or r.get("summary") or ""
            row["results"][pending[r["tool_call_id"]]] = text or ""
        elif r.get("type") == "turn":
            row["steps"] = r.get("steps")
            row["sent"] = r.get("sent")
            row["received"] = r.get("received")
            row["cost"] = r.get("cost")
    row["final_text"] = last_text
    row["answer"] = answer_text(last_text)
    row["correct"] = is_correct(meta["task"], row["answer"])
    row["failed"] = [bool(t) and t.startswith("The program failed") for t in row["results"]]
    lang = "js" if meta["arm"] == "js" else "py"
    row["first_host"] = classify.classify(row["programs"][0], lang)["host"] if row["programs"] else None
    return row


def main():
    bad = selftest()
    if bad:
        sys.exit(f"self-test failed: {bad}")
    trial = os.environ.get("TRIAL_DIR")
    rows = []
    if trial and os.path.isdir(os.path.join(trial, "runs")):
        for res in sorted(glob.glob(os.path.join(trial, "runs", "*", "result.json"))):
            meta = json.load(open(res))
            rows.append(scan(os.path.dirname(res), meta))
        with open(os.path.join(HERE, "scored.jsonl"), "w") as f:
            for r in rows:
                f.write(json.dumps(r) + "\n")
    else:
        rows = [json.loads(l) for l in open(os.path.join(HERE, "scored.jsonl"))]
    report(rows)


def pct(k, n):
    return f"{k}/{n} ({k / n:.0%})" if n else "0/0"


def report(rows):
    cost = sum(r["cost"] or 0 for r in rows)
    print(f"{len(rows)} sessions, ${cost:.2f}; exits: "
          f"{dict((str(k), sum(1 for r in rows if r.get('exit') == k)) for k in {r.get('exit') for r in rows})}\n")
    by = defaultdict(list)
    for r in rows:
        by[r["arm"]].append(r)

    print("| arm | sessions | wrote a program | first program failed | correct | silent wrong | "
          "median steps | median input | mean cost |")
    print("|---|---|---|---|---|---|---|---|---|")
    stats = {}
    for arm in ARMS:
        rs = by[arm]
        prog = [r for r in rs if r["programs"]]
        ff = sum(1 for r in prog if r["failed"][0])
        corr = sum(1 for r in rs if r["correct"])
        silent = sum(1 for r in rs if r["correct"] is False and r["programs"] and not any(r["failed"]))
        steps = sorted(r["steps"] for r in rs if r["steps"] is not None)
        sent = sorted(r["sent"] for r in rs if r["sent"] is not None)
        med = lambda xs: xs[len(xs) // 2] if xs else None
        mc = sum(r["cost"] or 0 for r in rs) / len(rs) if rs else 0
        stats[arm] = (ff, len(prog), corr, len(rs))
        print(f"| {arm} | {len(rs)} | {len(prog)} | {pct(ff, len(prog))} | {pct(corr, len(rs))} | {silent} | "
              f"{med(steps)} | {med(sent)} | ${mc:.4f} |")

    print()
    a_ff, a_n, a_c, a_all = stats["monty"]
    for arm in ("monty-open", "js"):
        ff, n, c, alln = stats[arm]
        p1 = fisher(ff, n - ff, a_ff, a_n - a_ff)
        p2 = fisher(c, alln - c, a_c, a_all - a_c)
        drop = (a_c / a_all - c / alln) * 100 if alln and a_all else 0
        qualifies = p1 < 0.05 and ff / max(n, 1) < a_ff / max(a_n, 1) and drop <= 5
        print(f"- {arm} vs monty: first-program failure p = {p1:.4f}; correct p = {p2:.4f} "
              f"(correct-rate change {-drop:+.1f} points); qualifies: {qualifies}")

    print("\nPer model — first program failed / wrote a program; correct / sessions:\n")
    print("| model | " + " | ".join(ARMS) + " |")
    print("|---|" + "---|" * len(ARMS))
    for model in sorted({r["model"] for r in rows}):
        cells = []
        for arm in ARMS:
            rs = [r for r in by[arm] if r["model"] == model]
            prog = [r for r in rs if r["programs"]]
            cells.append(f"{sum(1 for r in prog if r['failed'][0])}/{len(prog)}; "
                         f"{sum(1 for r in rs if r['correct'])}/{len(rs)}")
        print(f"| {model} | " + " | ".join(cells) + " |")

    print("\nPer task — correct / sessions:\n")
    print("| task | " + " | ".join(ARMS) + " |")
    print("|---|" + "---|" * len(ARMS))
    for task in KEYS:
        cells = []
        for arm in ARMS:
            rs = [r for r in by[arm] if r["task"] == task]
            cells.append(f"{sum(1 for r in rs if r['correct'])}/{len(rs)}")
        print(f"| {task} | " + " | ".join(cells) + " |")

    print("\nFirst-program host reach / wrong answer with no marker:\n")
    for arm in ARMS:
        prog = [r for r in by[arm] if r["programs"]]
        noans = sum(1 for r in by[arm] if r["answer"] is None)
        print(f"- {arm}: host reach {sum(1 for r in prog if r['first_host'])}/{len(prog)}; no ANSWER line {noans}")


if __name__ == "__main__":
    main()
