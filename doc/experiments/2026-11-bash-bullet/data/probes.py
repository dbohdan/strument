#!/usr/bin/env python3
"""Split the run-task sessions that executed code by whether a probe could
have shown the parseInt trap: a probed line with two or more counts, or a
direct parseCounts/parseInt call. Reads the sessions from runs/ and the grades
from scores.txt.

    probes.py RUNSDIR SCORES
"""
import json,sys,glob,os,re,collections
rows={}
for l in open(sys.argv[2]):
    if l.startswith("{"): r=json.loads(l); rows[r["id"]]=r
LINE=re.compile(r"""['"]([^'"\s,]+(?:,[^'"\s,]*){2,})['"]""")
tab=collections.Counter(); ex=collections.defaultdict(list)
for d in sorted(glob.glob(os.path.join(sys.argv[1],"*-run-*.jsonl"))):
    r=rows[os.path.basename(d)[:-6]]
    if not r["executed"]: continue
    cmds=[]
    for lg in [d]:
        for l in open(lg):
            m=json.loads(l)
            if m["type"]=="message" and m.get("role")=="assistant":
                for tc in m.get("tool_calls") or []:
                    if tc["name"]=="bash": cmds.append(json.loads(tc.get("arguments") or "{}").get("command",""))
    probes=[c for c in cmds if re.search(r"\b(node|npm|npx)\b",c)]
    shown=any(LINE.search(c) for c in probes) or any(re.search(r"parseCounts\(|parseInt",c) for c in probes)
    q1=r["per_question"][:1]=="1"
    k=(r["arm"], "multi-count probe" if shown else "single-count probes only", "Q1 right" if q1 else "Q1 wrong")
    tab[k]+=1; ex[k].append(r["id"])
for k in sorted(tab): print(k, tab[k])
print(json.dumps({" / ".join(k):v[:4] for k,v in ex.items()}, indent=0))
