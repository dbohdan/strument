#!/usr/bin/env python3
"""p3score.py RUNS_DIR: one row per two-turn session; every column a count read off a file.

rederive   turn-2 calls that re-establish the rule: read/grep/glob/recall/bash/run_code
           touching svc/config.py, deploy/, or config.load/layers, or a recall
guessed    1 if turn 2's first edit came before any rederive call, else 0
t2_edits   turn-2 edit/write calls, with the paths edited
t1_words   words in turn 1's final answer (the one-word instruction)
t1_visible words in all of turn 1's assistant text, narration between calls included
t1_rule    1 if that text states the rule (profile, layers, or the override path), else 0
prod, staging, dev  live request_timeout after the session (cfghunt grade)
t2_req, t2_sent, t2_uncached, t2_out, t2_reasoning  turn-2 request totals
t2_secs    turn 2's wall-clock, from the runner
"""
import glob, json, os, re, sys

RULE = re.compile(r"svc/config\.py|deploy/|config\.load|config\.layers|layers\(|\.env\b")
TURN2 = "Staging has the same problem"


def args_of(proj, tc):
    raw = tc.get("arguments")
    if raw is None and tc.get("blob"):
        p = os.path.join(proj, "blobs", tc["blob"])
        raw = open(p, errors="replace").read() if os.path.exists(p) else tc.get("summary", "")
    return raw or tc.get("summary", "") or ""


cols = ["run", "arm", "column", "model", "t1_status", "t2_status", "prod", "staging", "dev", "rederive", "guessed",
        "t2_edits", "t2_paths", "t1_words", "t1_visible", "t1_rule", "t2_req", "t2_sent", "t2_uncached", "t2_out", "t2_reasoning", "t2_secs", "cleared"]
print("\t".join(cols))
for d in sorted(glob.glob(os.path.join(sys.argv[1], "*"))):
    name = os.path.basename(d)
    arm, column, model, _ = name.split("-", 3)
    turns = json.load(open(os.path.join(d, "turns.json"))) if os.path.exists(os.path.join(d, "turns.json")) else []
    grade = open(os.path.join(d, "grade.txt")).read() if os.path.exists(os.path.join(d, "grade.txt")) else ""
    vals = dict(re.findall(r"'(prod|staging|dev)': (\d+|None)", grade))
    projs = glob.glob(os.path.join(d, "state", "strument", "projects", "*"))
    in2 = False; t1_answer = ""; t1_text = []; rederive = 0; first_edit_before = None; edits = []; paths = []
    req = sent = cache = out = reas = 0
    for lp in sorted(glob.glob(os.path.join(projs[0], "sessions", "*", "log", "*.jsonl"))) if projs else []:
        for line in open(lp, errors="replace"):
            try:
                ev = json.loads(line)
            except ValueError:
                continue
            if ev.get("type") == "message" and ev.get("role") == "user" and TURN2 in (ev.get("text") or ""):
                in2 = True
                continue
            if not in2:
                if ev.get("type") == "message" and ev.get("role") == "assistant" and ev.get("text"):
                    t1_answer = ev["text"]; t1_text.append(ev["text"])
                continue
            if ev.get("type") == "request" and ev.get("call") == "turn":
                req += 1; sent += ev.get("sent", 0); cache += ev.get("cache_read", 0)
                out += ev.get("received", 0); reas += ev.get("reasoning", 0)
            for tc in ev.get("tool_calls") or []:
                a = args_of(projs[0], tc)
                if tc.get("name") in ("edit", "write"):
                    edits.append(tc["name"])
                    m = re.search(r'"path"\s*:\s*"([^"]+)"', a)
                    paths.append(m.group(1) if m else "?")
                    if first_edit_before is None:
                        first_edit_before = rederive == 0
                elif tc.get("name") == "recall" or RULE.search(a):
                    rederive += 1
    t2 = turns[1] if len(turns) > 1 else {}
    row = [name, arm, column, model, turns[0]["status"] if turns else "-", t2.get("status", "-"),
           vals.get("prod", "-"), vals.get("staging", "-"), vals.get("dev", "-"), rederive,
           int(bool(first_edit_before)), len(edits), ",".join(paths) or "-", len(t1_answer.split()),
           len(" ".join(t1_text).split()),
           int(bool(re.search(r"overrides?/|eu-large|layer|preceden|wins", " ".join(t1_text), re.I))),
           req, sent, sent - cache, out, reas, t2.get("secs", "-"),
           len(turns[0]["cleared"]) if turns else "-"]
    print("\t".join(map(str, row)))
