#!/usr/bin/env python3
"""Post-hoc: question 3 on tally, scored leniently.

    lenient.py OUTDIR SCORES [MODEL ...]

Question 3 asks whether report() lists codes in the order they first appear.
The key is "no" (integer-like codes come first), but the behavior holds for
every other code, and many answers say "yes, with one exception" and state the
exception. Strictly, that is wrong. Here an answer counts as right on question
3 if it is right, or if its prose states the exception. Added after reading
the main run; it is a sensitivity check, not a preregistered measure.
"""
import glob, json, re, sys

EXC = re.compile(r"integer[- ]like|look(s)? like an? integer|numeric[- ]looking|numeric[- ]like|integer keys|array[- ]index|except", re.I)

root, scorefile, models = sys.argv[1], sys.argv[2], sys.argv[3:]
scores = {json.loads(l)["id"]: json.loads(l) for l in open(scorefile) if l.startswith("{")}
rows = []
for d in glob.glob(root + "/*/"):
    r = scores.get(d.rstrip("/").split("/")[-1])
    if not r or r["fixture"] != "tally" or r["task"] != "run":
        continue
    ans = ""
    for lg in glob.glob(d + "session-2*.jsonl"):
        for l in open(lg):
            x = json.loads(l)
            if x["type"] == "turn":
                ans = x.get("answer", "")  # a turn with no answer has none
    pq = r["per_question"]
    r["lenient"] = bool(pq) and pq[0] == pq[1] == pq[3] == "1" and (pq[2] == "1" or bool(EXC.search(ans)))
    rows.append(r)
for m in models:
    for arm in ("A", "C", "C-length", "C-direct", "P"):
        rs = [r for r in rows if r["model"] == m and r["arm"] == arm]
        if rs:
            print(f"{m:9} {arm:9} strict {sum(r['grade'] == 'correct' for r in rs):2}/{len(rs)}  lenient {sum(r['lenient'] for r in rs):2}/{len(rs)}")
