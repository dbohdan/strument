"""Scores the read-outline trial from each session's record.

Counts, not judgments. An answer is the text after the last "ANSWER:" line,
compared against the planted value (fixture.py); a memory of upstream gives a
different one. Double underscores are kept: the envvar answer has one.

Usage: score.py TRIAL_DIR
"""
import glob, json, math, os, re, statistics as st, sys

LOOKUP = {"envvar", "metavar", "flag", "pipe"}  # one fact past line 2,000
STRUCT = {"exports", "argmethods"}               # what a class defines, past line 2,000
PAST = LOOKUP | STRUCT                           # the tasks beyond the default read window
CONTROL = {"dumb", "abort"}                      # inside the default read window

EXPORTS = {"export_text", "save_text", "export_html", "save_html", "export_svg", "save_svg",
           "export_markdown", "save_markdown"}
ARG_METHODS = {"human_readable_name", "make_metavar", "_parse_decls", "get_usage_pieces",
               "get_help_record", "get_error_hint", "add_to_parser", "describe"}
# Methods of Parameter and Option that Argument does not define: naming one is
# listing an inherited method as Argument's own.
NOT_ARG = {"get_default", "consume_value", "type_cast_value", "value_is_missing", "process_value",
           "resolve_envvar_value", "value_from_envvar", "handle_parse_result", "shell_complete",
           "to_info_dict", "get_help_extra", "prompt_for_value", "get_help_spec", "_check_name_is_usable",
           "_check_name_is_normalized", "flag_activation_value", "is_bool_flag", "_infer_flag_kind",
           "_pick_type", "_resolve_lazy_default", "_validate", "__repr__"}


def answer_text(msg, whole=False):
    """The answer after the last ANSWER: line; the rest of the message when
    whole, since a list can run over several lines."""
    if not msg:
        return None
    idx = [m.end() for m in re.finditer(r"(?m)^[ \t>*-]*\**ANSWER:\**", msg)]
    if not idx:
        return None
    text = msg[idx[-1]:].strip()
    if not whole:
        text = text.split("\n", 1)[0]
    return text.replace("**", "").strip().strip("`'\" .").strip()


def is_correct(task, ans):
    if ans is None:
        return False
    a = ans.strip("`'\" ")
    if task == "envvar":
        return re.search(r"\bAPP__PORT\b", a, re.I) is not None
    if task == "metavar":
        return a.strip("`'\"") == "~"
    if task == "flag":
        return re.fullmatch(r"""["'`]?on["'`]?(\s*\(.*\))?""", a, re.I) is not None
    if task == "pipe":
        nums = re.findall(r"\d+", a)
        return nums[:1] == ["3"]
    if task == "dumb":
        return re.fullmatch(r"72\s*[x×X]\s*21", a) is not None
    if task == "abort":
        return a.rstrip(".").lower() == "stopped by user"
    names = set(re.findall(r"[A-Za-z_][A-Za-z0-9_]*", a))
    if task == "exports":
        return {n for n in names if n.startswith(("export_", "save_"))} == EXPORTS
    if task == "argmethods":
        return ARG_METHODS <= names and not names & NOT_ARG
    raise ValueError(task)


CASES = [("envvar", "ANSWER: `APP__PORT`", True), ("envvar", "ANSWER: APP_PORT", False),
         ("metavar", "ANSWER: `~`", True), ("metavar", "ANSWER: !", False),
         ("flag", 'ANSWER: "on"', True), ("flag", "ANSWER: True", False),
         ("pipe", "ANSWER: 3", True), ("pipe", "ANSWER: 1", False),
         ("dumb", "ANSWER: 72x21", True), ("dumb", "ANSWER: 80x25", False),
         ("abort", "ANSWER: Stopped by user.", True), ("abort", "ANSWER: Aborted!", False),
         ("abort", "**ANSWER:** `Stopped by user.`", True),
         ("exports", "ANSWER: export_text, save_text, export_html, save_html, export_svg, save_svg, "
                     "export_markdown, save_markdown", True),
         ("exports", "ANSWER: export_text, save_text, export_html, save_html, export_svg, save_svg", False),
         ("argmethods", "ANSWER: __init__, human_readable_name, make_metavar, _parse_decls, get_usage_pieces, "
                        "get_help_record, get_error_hint, add_to_parser, describe", True),
         ("argmethods", "ANSWER: human_readable_name, make_metavar, _parse_decls, get_usage_pieces, "
                        "get_help_record, get_error_hint, add_to_parser", False),
         ("argmethods", "ANSWER: human_readable_name, make_metavar, _parse_decls, get_usage_pieces, "
                        "get_help_record, get_error_hint, add_to_parser, describe, process_value", False),
         ("exports", "ANSWER:\n- `export_text`\n- `save_text`\n- `export_html`\n- `save_html`\n- `export_svg`\n"
                     "- `save_svg`\n- `export_markdown`\n- `save_markdown`", True)]
bad = [c for c in CASES if is_correct(c[0], answer_text("Some text.\n" + c[1], c[0] in STRUCT)) != c[2]]
assert not bad, f"answer parser self-test failed: {bad}"


def score(work):
    res = json.load(open(os.path.join(work, "result.json")))
    path = os.path.join(work, "session.jsonl")
    recs = [json.loads(l) for l in open(path)] if os.path.exists(path) else []
    turn = next((r for r in reversed(recs) if r.get("type") == "turn"), {})
    reqs = [r for r in recs if r.get("type") == "request"]
    calls, read_ids = [], set()
    for r in recs:
        for tc in r.get("tool_calls") or []:
            try:
                args = json.loads(tc.get("arguments") or "{}")
            except json.JSONDecodeError:
                args = {}
            calls.append((tc.get("name"), args))
            if tc.get("name") == "read":
                read_ids.add(tc.get("id"))
    read_bytes = sum(r.get("bytes") or len(r.get("text") or "") for r in recs
                     if r.get("role") == "tool" and r.get("tool_call_id") in read_ids)
    reads = [a for n, a in calls if n == "read"]
    ans = answer_text(turn.get("answer"), res["task"] in STRUCT)
    return dict(res, correct=is_correct(res["task"], ans), answer=ans,
                sent=sum(r.get("sent") or 0 for r in reqs),
                uncached=sum((r.get("sent") or 0) - (r.get("cache_read") or 0) for r in reqs),
                cost=turn.get("cost"), steps=turn.get("steps"), read_bytes=read_bytes,
                reads=len(reads), outline_calls=sum(1 for a in reads if a.get("outline")),
                ranged=sum(1 for a in reads if a.get("limit")), tools=[n for n, _ in calls])


def fisher(a, b, c, d):
    n, r1, c1 = a + b + c + d, a + b, a + c
    tot = math.comb(n, c1)
    p0 = math.comb(r1, a) * math.comb(n - r1, c1 - a) / tot
    return min(1.0, sum(math.comb(r1, x) * math.comb(n - r1, c1 - x) / tot
                        for x in range(max(0, c1 - (n - r1)), min(r1, c1) + 1)
                        if math.comb(r1, x) * math.comb(n - r1, c1 - x) / tot <= p0 * (1 + 1e-9)))


def mwu(x, y):
    """Two-sided Mann-Whitney U, normal approximation with tie correction."""
    n1, n2 = len(x), len(y)
    if not n1 or not n2:
        return float("nan")
    pooled = sorted([(v, 0) for v in x] + [(v, 1) for v in y])
    ranks, i = [0.0] * len(pooled), 0
    ties = 0.0
    while i < len(pooled):
        j = i
        while j + 1 < len(pooled) and pooled[j + 1][0] == pooled[i][0]:
            j += 1
        for k in range(i, j + 1):
            ranks[k] = (i + j) / 2 + 1
        t = j - i + 1
        ties += t ** 3 - t
        i = j + 1
    r1 = sum(r for r, (_, g) in zip(ranks, pooled) if g == 0)
    u = r1 - n1 * (n1 + 1) / 2
    n = n1 + n2
    sd = math.sqrt(n1 * n2 / 12 * ((n + 1) - ties / (n * (n - 1))))
    if sd == 0:
        return 1.0
    z = (abs(u - n1 * n2 / 2) - 0.5) / sd
    return math.erfc(max(z, 0) / math.sqrt(2))


def main():
    trial = sys.argv[1]
    rows = [score(os.path.dirname(p)) for p in sorted(glob.glob(os.path.join(trial, "*", "result.json")))]
    with open(os.path.join(trial, "scored.jsonl"), "w") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")
    cost = sum(r.get("cost") or 0 for r in rows)
    print(f"{len(rows)} sessions, ${cost:.3f}; exits other than 0: {[r['tag'] for r in rows if r.get('exit') != 0]}")
    med = lambda xs: int(st.median(xs)) if xs else 0
    print("| arm | model | correct (past) | correct (control) | median sent, past | median read bytes, past | "
          "median steps, past | outline calls (sessions) | ranged reads | cost |")
    print("|---|---|---|---|---|---|---|---|---|---|")
    for a in "ABCD":
        for m in ("mimo", "luna", None):
            g = [r for r in rows if r["arm"] == a and (m is None or r["model"] == m)]
            p = [r for r in g if r["task"] in PAST]
            c = [r for r in g if r["task"] in CONTROL]
            reads = sum(r["reads"] for r in g)
            print(f"| {a} | {m or 'all'} | {sum(r['correct'] for r in p)}/{len(p)} | {sum(r['correct'] for r in c)}/{len(c)} "
                  f"| {med([r['sent'] for r in p])} | {med([r['read_bytes'] for r in p])} | {med([r['steps'] or 0 for r in p])} "
                  f"| {sum(r['outline_calls'] for r in g)} ({sum(1 for r in g if r['outline_calls'])}/{len(g)}) "
                  f"| {sum(r['ranged'] for r in g)}/{reads} | ${sum(r.get('cost') or 0 for r in g):.3f} |")
    base = [r for r in rows if r["arm"] == "A"]
    for a in "BCD":
        arm = [r for r in rows if r["arm"] == a]
        xs = [r["sent"] for r in arm if r["task"] in PAST]
        ys = [r["sent"] for r in base if r["task"] in PAST]
        ca, cb = sum(r["correct"] for r in arm), sum(r["correct"] for r in base)
        print(f"{a} vs A: median sent (past) {med(xs)} vs {med(ys)}, MWU p = {mwu(xs, ys):.4f}; "
              f"correct (all) {ca}/{len(arm)} vs {cb}/{len(base)}, Fisher p = {fisher(ca, len(arm) - ca, cb, len(base) - cb):.3f}")
        for m in ("mimo", "luna"):
            xm = [r["sent"] for r in arm if r["task"] in PAST and r["model"] == m]
            ym = [r["sent"] for r in base if r["task"] in PAST and r["model"] == m]
            print(f"    {m}: {med(xm)} vs {med(ym)}")


if __name__ == "__main__":
    main()
