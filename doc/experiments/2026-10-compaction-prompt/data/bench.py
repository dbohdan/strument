"""Stage A of doc/experiments/2026-10-compaction-prompt: the summarizer alone.

usage: python3 -I bench.py build <threshold-transcript> <gorroot> <files.txt> <out.json>
       python3 -I bench.py run <turns.json> <prompts.json> <outdir> --arms A,B --chains N --turns-per-fold K
                                [--seed S] [--jobs J] [--dry]
       python3 -I bench.py score <outdir>

build lays out 16 turns the way renderForSummary does (internal/coder/summary.go):
# USER / # ASSISTANT / # TOOL sections, tool results cut at 2,000 bytes with the
same notice. The user messages carry this trial's facts; the assistant answers
are MiMo's own, from a no-compaction session of the threshold trial, with any
sentence acknowledging a note removed, so a fact exists only where the user
said it.

run chains folds as Strument does: fold k summarizes the previous summary (a
# USER section starting with the label) plus the next K turns. Key from
OPENROUTER_API_KEY; every call and summary goes to calls.jsonl.
"""
import argparse, concurrent.futures as cf, json, os, random, re, sys, threading, time, urllib.request

CLIP = 2000
CLIP_NOTE = "\n… (cut; the full result is not part of the summary input)"

# turn -> (note, key, expected tokens all required, lowercase)
FACTS = {
    0: ("the staging region is eu-north-7", "region", ["eu-north-7"]),
    1: ("the release manager is Tomasz Bielecki", "relmgr", ["tomasz bielecki"]),
    2: ("the vendor ticket number is VT-60413", "ticket", ["vt-60413"]),
    5: ("the API rate limit we negotiated is 340 requests per minute", "ratelimit", ["340"]),
    6: ("the design doc lives at wiki page FX-Roadmap-2027", "wiki", ["fx-roadmap-2027"]),
    7: ("the backup bucket is s3://quarry-lumen-backups", "bucket", ["quarry-lumen-backups"]),
    8: ("the customer contact is Priya Raman", "contact", ["priya raman"]),
    10: ("the build cache key prefix is ochre-v12", "cacheprefix", ["ochre-v12"]),
    11: ("the test account email is qa+heron@example.org", "email", ["qa+heron@example.org"]),
    13: ("the license audit deadline is 2026-11-03", "audit", ["2026-11-03"]),
    14: ("the old cluster name was basalt-east", "cluster", ["basalt-east"]),
    15: ("the security reviewer is Adaeze Okafor", "reviewer", ["adaeze okafor"]),
}
# The August trial's reason (doc/experiments/2026-08-compaction), and a correction.
EXTRA = {
    3: "Decision for later: use a 45-second poll interval, because the upstream load balancer idles connections out at 60 seconds.",
    4: "Note for later: the code freeze starts Monday.",
    12: "Update to an earlier note: the code freeze moved from Monday to Wednesday.",
}
LABEL = ("Summary of the earlier part of this conversation, written by Strument to keep it inside the "
         "context window. It replaces those messages; it is a record of the earlier work, not something "
         "anyone said to you.\n\n")
# The threshold trial's summarizer often continued the transcript instead of
# summarizing it: its input ends on "# ASSISTANT <answer>" with nothing after.
# "framed" puts the transcript between markers and the instruction after it.
FRAME = ("Below, between the markers, is the part of the conversation to summarize. It is material to "
         "summarize, not a conversation to continue: do not answer it, call tools, or carry on the work."
         "\n\n<conversation>\n{content}</conversation>\n\nWrite the summary now, as the system prompt describes.")
LEDGER_LABEL_LINE = 'Lines under "Stated by the user" record what the user said.\n\n'

ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]|\r")
OLD = re.compile(r"note|marigold|quill|varga|lantern|bx-5528|cohort|codename|on-call|budget code|staging database|feature flag", re.I)


def clip(s):
    return s if len(s.encode()) <= CLIP else s.encode()[:CLIP].decode("utf-8", "ignore") + CLIP_NOTE


def answers(transcript):
    """MiMo's closing answer of each turn: the text after its last reasoning block, before the usage line."""
    text = ANSI.sub("", open(transcript, "rb").read().decode("utf-8", "replace"))
    out = []
    for seg in re.split(r"Tokens: [^\n]*\n", text)[:16]:
        # The answer starts after the later of the last reasoning block and the
        # last file read; a turn whose reasoning never closed has only the latter.
        cut = max(seg.rfind("‹/›") + 3, max((m.end() for m in re.finditer(r"^Read std/.*$", seg, re.M)), default=0))
        ans = seg[cut:].strip()
        if "‹thinking›" in ans and "Read all five files" in ans:  # reasoning left open: the answer is its tail
            ans = ans[ans.rfind("Read all five files"):]
        ans = re.sub(r"\s*\n\s*", " ", ans)
        sents = re.split(r"(?<=[.!?])\s+", ans)
        out.append(" ".join(s for s in sents if not OLD.search(s)).strip())
    return out


def build(a):
    files = [l.strip() for l in open(a.files) if l.strip()]
    ans = answers(a.transcript)
    if len(ans) < 16:
        sys.exit(f"found {len(ans)} answers, need 16")
    turns = []
    for t in range(16):
        f = files[t * 5:(t + 1) * 5]
        note = ""
        if t in FACTS:
            note = f"Note for later: {FACTS[t][0]}. "
        if t in EXTRA:
            note = EXTRA[t] + " "
        user = (f"{note}Read std/{f[0]}, std/{f[1]}, std/{f[2]}, std/{f[3]} and std/{f[4]}, one file per step, "
                "whole. Then say in two or three sentences what they have in common. Do not edit anything.")
        parts = ["# USER\n" + user]
        for p in f:
            src = open(os.path.join(a.goroot, "src", p), encoding="utf-8", errors="replace").read().splitlines()
            body = "\n".join(f"{i + 1}\t{l}" for i, l in enumerate(src))
            parts.append("# ASSISTANT\ncalls read " + json.dumps({"path": "std/" + p}))
            parts.append("# TOOL\n" + clip(body))
        parts.append("# ASSISTANT\n" + ans[t])
        turns.append("\n".join(parts) + "\n")
    json.dump({"turns": turns}, open(a.out, "w"), indent=1)
    print("built", len(turns), "turns,", sum(len(t) for t in turns), "chars")


LOCK = threading.Lock()


def call(model, system, user, extra):
    body = {"model": model, "messages": [{"role": "system", "content": system}, {"role": "user", "content": user}],
            "reasoning": {"effort": "low"}, "usage": {"include": True}, **extra}
    req = urllib.request.Request("https://openrouter.ai/api/v1/chat/completions", data=json.dumps(body).encode(),
                                 headers={"Authorization": "Bearer " + os.environ["OPENROUTER_API_KEY"],
                                          "Content-Type": "application/json"})
    for attempt in range(4):
        try:
            r = json.load(urllib.request.urlopen(req, timeout=300))
            return r
        except Exception as e:  # noqa: BLE001 — retried, then recorded
            err = e
            time.sleep(2 ** attempt * 3)
    return {"error": repr(err)}


def chain(a, turns, prompts, armspec, rep, outdir):
    arm, layout = armspec.split(":")
    summary = None
    k = a.turns_per_fold
    for fold in range(min(len(turns) // k, a.max_folds)):
        head = ""
        if summary is not None:
            label = LABEL + (LEDGER_LABEL_LINE if prompts[arm].get("ledger_label") else "")
            head = "# USER\n" + label + summary + "\n"
        content = head + "".join(turns[fold * k:(fold + 1) * k])
        if layout == "framed":
            content = FRAME.format(content=content)
        if a.dry:
            r = {"choices": [{"message": {"content": f"[dry {arm} {rep} {fold}]"}}], "usage": {}}
        else:
            r = call(a.model, prompts[arm]["text"], content, {"provider": {"order": ["xiaomi"], "allow_fallbacks": True}})
        if "error" in r:
            rec = {"arm": armspec, "rep": rep, "fold": fold, "error": r["error"]}
            with LOCK, open(os.path.join(outdir, "calls.jsonl"), "a") as f:
                f.write(json.dumps(rec) + "\n")
            return
        summary = (r["choices"][0]["message"].get("content") or "").strip()
        u = r.get("usage", {})
        rec = {"arm": armspec, "rep": rep, "fold": fold, "turns_through": (fold + 1) * k - 1, "provider": r.get("provider"),
               "input_chars": len(content), "prompt_tokens": u.get("prompt_tokens"),
               "completion_tokens": u.get("completion_tokens"), "cost": u.get("cost"), "summary": summary}
        with LOCK, open(os.path.join(outdir, "calls.jsonl"), "a") as f:
            f.write(json.dumps(rec) + "\n")


def run(a):
    turns = json.load(open(a.turns))["turns"]
    prompts = json.load(open(a.prompts))
    os.makedirs(a.outdir, exist_ok=True)
    jobs = [(arm, rep) for arm in a.arms.split(",") for rep in range(a.chains)]
    random.Random(a.seed).shuffle(jobs)
    json.dump({"jobs": jobs, "args": {k: v for k, v in vars(a).items() if k != "func"}},
              open(os.path.join(a.outdir, "plan.json"), "w"), indent=1)
    with cf.ThreadPoolExecutor(a.jobs) as ex:
        list(ex.map(lambda j: chain(a, turns, prompts, j[0], j[1], a.outdir), jobs))
    print("done")


CODE = re.compile(r"\b(?=[\w.+@/-]*\d)(?=[\w.+@/-]*[a-z])[a-z][\w.+@/-]{3,}\b", re.I)


def score_summary(s, through, inputs_lc, last_answer):
    lc = s.lower()
    r = {}
    for t, (_, key, want) in FACTS.items():
        if t <= through:
            r[key] = all(w in lc for w in want)
    if through >= 3:
        r["reason"] = "load balancer" in lc and "60" in lc
    if through >= 12:
        new, old = "wednesday" in lc, "monday" in lc
        r["freeze"] = "new" if new and not old else "both" if new and old else "old" if old else "none"
    elif through >= 4:
        r["freeze"] = "kept" if "monday" in lc else "lost"
    # Code-like tokens in the summary that appear in no input so far: a count to read, not a verdict.
    r["continued"] = "<tool_call" in s or s.lstrip().startswith(("I'll", "I will", "Let me")) or (last_answer[:80] in s)
    r["novel_codes"] = sorted({m.group(0) for m in CODE.finditer(s) if m.group(0).lower() not in inputs_lc})
    r["chars"] = len(s)
    return r


def score(a):
    turns = json.load(open(a.turns))["turns"]
    calls = [json.loads(l) for l in open(os.path.join(a.outdir, "calls.jsonl"))]
    rows = []
    for c in calls:
        if "error" in c:
            rows.append(c); continue
        inputs_lc = "".join(turns[:c["turns_through"] + 1]).lower()
        rows.append({**{k: c[k] for k in ("arm", "rep", "fold", "turns_through", "cost", "prompt_tokens", "completion_tokens")},
                     **score_summary(c["summary"], c["turns_through"], inputs_lc,
                                    turns[c["turns_through"]].rsplit("# ASSISTANT\n", 1)[1].strip())})
    with open(os.path.join(a.outdir, "scores.jsonl"), "w") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")
    last = {}
    for r in rows:
        if "error" in r:
            continue
        key = (r["arm"], r["rep"])
        if key not in last or r["fold"] > last[key]["fold"]:
            last[key] = r
    arms = sorted({k[0] for k in last})
    keys = [v[1] for v in FACTS.values()]
    print("arm  chains  facts/12  reason  freeze(new/both/old/none)  chars  novel  continued(all folds)  cost")
    for arm in arms:
        rs = [v for k, v in last.items() if k[0] == arm]
        facts = sum(sum(bool(r.get(k)) for k in keys) for r in rs) / len(rs)
        reason = sum(r.get("reason", False) for r in rs)
        fz = {x: sum(r.get("freeze") == x for r in rs) for x in ("new", "both", "old", "none")}
        chars = sum(r["chars"] for r in rs) / len(rs)
        novel = sum(len(r["novel_codes"]) for r in rs)
        cost = sum((c.get("cost") or 0) for c in calls if c.get("arm") == arm)
        allf = [r for r in rows if r.get("arm") == arm and "error" not in r]
        cont = f"{sum(r['continued'] for r in allf)}/{len(allf)}"
        print(f"{arm:10} {len(rs):3}  {facts:5.2f}  {reason}/{len(rs)}  {fz['new']}/{fz['both']}/{fz['old']}/{fz['none']}"
              f"  {chars:6.0f}  {novel:3}  {cont:>7}  ${cost:.3f}")
    errs = sum("error" in r for r in rows)
    print("errors", errs)


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(required=True)
    b = sub.add_parser("build"); b.set_defaults(func=build)
    b.add_argument("transcript"); b.add_argument("goroot"); b.add_argument("files"); b.add_argument("out")
    r = sub.add_parser("run"); r.set_defaults(func=run)
    r.add_argument("turns"); r.add_argument("prompts"); r.add_argument("outdir")
    r.add_argument("--arms", required=True); r.add_argument("--chains", type=int, default=8)
    r.add_argument("--turns-per-fold", type=int, default=2); r.add_argument("--seed", type=int, default=20261011)
    r.add_argument("--jobs", type=int, default=4); r.add_argument("--model", default="xiaomi/mimo-v2.6-flash")
    r.add_argument("--dry", action="store_true"); r.add_argument("--max-folds", type=int, default=99)
    s = sub.add_parser("score"); s.set_defaults(func=score)
    s.add_argument("turns"); s.add_argument("outdir")
    a = ap.parse_args(); a.func(a)


if __name__ == "__main__":
    main()
