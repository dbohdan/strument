#!/usr/bin/env python3
"""Find tool calls that reach outside the session's own project: absolute paths into
the run directory other than its own proj/, ../ paths other than ../src and
../test, cd .., and mentions of this repository or the trial's records
(tasks.py, wire.jsonl, done.json). Reads the wire and the Strument session logs.

    left_project.py RUNDIR
"""
import glob, json, os, re, sys
main = sys.argv[1]
pat_hidden = re.compile(r"tasks\.py|/home/user/strument|2026-11-featherweight|doc/experiments|wire\.jsonl|done\.json")
hits = []
for d in sorted(glob.glob(main + "/*/")):
    sid = os.path.basename(d.rstrip("/"))
    proj = os.path.join(main, sid, "proj")
    texts = []
    w = os.path.join(d, "wire.jsonl")
    if not os.path.exists(w):
        continue
    for l in open(w):
        r = json.loads(l)
        if r.get("kind") != "raw_response":
            continue
        b = r["body"]
        if isinstance(b, str) and b.lstrip().startswith("{"):
            try:
                b = json.loads(b)
                for tc in b["choices"][0]["message"].get("tool_calls") or []:
                    texts.append(tc["function"]["arguments"])
            except Exception:
                pass
    for lg in glob.glob(d + "session-2*.jsonl"):
        for l in open(lg):
            r = json.loads(l)
            if r["type"] == "message" and r.get("role") == "assistant":
                for tc in r.get("tool_calls") or []:
                    texts.append(tc.get("arguments") or "")
    out = []
    for t in texts:
        # an absolute path under the run directory that is not this session's project, or a reach upward
        for m in re.finditer(r"scratchpad/fw/main[^\"'\s\\]*", t):
            if not m.group(0).startswith("scratchpad/fw/main/" + sid + "/proj"):
                out.append(m.group(0)[:90])
        for m in re.finditer(r"\.\./[^\"'\s\\]*", t):
            if not re.match(r"\.\./(src|test)\b", m.group(0)):
                out.append(m.group(0)[:60])
        if re.search(r"\bcd\s+\.\.", t):
            out.append("cd ..")
        if pat_hidden.search(t):
            out.append("HIDDEN-ish: " + pat_hidden.search(t).group(0))
    if out:
        hits.append((sid, sorted(set(out))[:6]))
for h in hits:
    print(h)
print(len(hits), "sessions reached outside their project")
