#!/usr/bin/env python3
"""Score layer 3's sessions from their wire records.

    mech_score.py DIR

For each session: how many consult calls MiMo made, and for each, which files
it named against what the task needs — the target, STYLE.md, and the source
the task's facts come from (the task's files less those two). Then the final
target's fact checks, the cost and the time. Read from the requests on the
wire, so what is counted is what was sent, not what the model said it sent.
"""

import collections
import glob
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tasks as T  # noqa: E402

TERRA = "openai/gpt-6.1-terra"


def consults(wire):
    """The consult calls in a session, with their results, from the last MiMo
    request's history (each request carries the turn so far), and the number
    of requests made to Terra."""
    last, terra = None, 0
    for line in open(wire):
        r = json.loads(line)
        if r["kind"] != "raw_request":
            continue
        b = json.loads(r["body"])
        if b["model"] == TERRA:
            terra += 1
        else:
            last = b
    calls, results = [], {}
    for m in (last or {}).get("messages", []):
        for tc in m.get("tool_calls") or []:
            if tc["function"]["name"] == "consult":
                try:
                    args = json.loads(tc["function"]["arguments"] or "{}")
                except json.JSONDecodeError:
                    args = {"unparsed": tc["function"]["arguments"]}
                calls.append((tc["id"], args))
        if m.get("role") == "tool":
            c = m.get("content")
            results[m.get("tool_call_id")] = c if isinstance(c, str) else json.dumps(c)
    return [(args, results.get(i, "")) for i, args in calls], terra


def norm(p):
    return os.path.normpath(p.lstrip("./")) if p else p


def main(outdir):
    rows = []
    for done in sorted(glob.glob(os.path.join(outdir, "*", "done.json"))):
        d = json.load(open(done))
        base = os.path.dirname(done)
        t = T.TASKS[d["task"]]
        source = [f for f in t["files"] if f not in ("STYLE.md", t["target"])]
        calls, terra = consults(os.path.join(base, "wire.jsonl"))
        out = open(os.path.join(base, "stdout.txt"), errors="replace").read()
        cost = re.findall(r"\$([0-9.]+) run", out)
        sent = [{norm(f) for f in (a.get("files") or [])} for a, _ in calls]
        rows.append({
            "purpose": d["purpose"], "task": d["task"], "status": d["status"], "seconds": d["seconds"],
            "calls": len(calls), "terra_requests": terra,
            "target": any(t["target"] in s for s in sent),
            "style": any("STYLE.md" in s for s in sent),
            "source": any(all(f in s for f in source) for s in sent) if source else None,
            "question_chars": max((len(a.get("question") or "") for a, _ in calls), default=0),
            "refused": sum(1 for _, res in calls if re.search(r"(?i)refus|declin|not approved|error", res[:300])),
            "facts_ok": T.facts(d["task"], d["target_text"])["ok"] if d.get("target_text") is not None else None,
            "changed_target": t["target"] in d["changed"] or t["target"] in d["untracked"],
            "cost": float(cost[-1]) if cost else None,
        })
    w = print
    w("| purpose | sessions | consulted | calls | sent target | sent STYLE.md | sent the source | refused | "
      "facts ok | target written | median s | mean cost |")
    w("|---|---|---|---|---|---|---|---|---|---|---|---|")
    by = collections.defaultdict(list)
    for r in rows:
        by[r["purpose"]].append(r)
    for p, rs in sorted(by.items()):
        con = [r for r in rs if r["calls"]]
        secs = sorted(r["seconds"] for r in rs)
        costs = [r["cost"] for r in rs if r["cost"] is not None]
        w(f"| {p} | {len(rs)} | {len(con)} | {sum(r['calls'] for r in rs)} | "
          f"{sum(r['target'] for r in con)} of {len(con)} | {sum(r['style'] for r in con)} of {len(con)} | "
          f"{sum(1 for r in con if r['source'])} of {sum(1 for r in con if r['source'] is not None)} | "
          f"{sum(r['refused'] for r in rs)} | {sum(1 for r in rs if r['facts_ok'])} of {len(rs)} | "
          f"{sum(r['changed_target'] for r in rs)} of {len(rs)} | {secs[len(secs) // 2]:.0f} | "
          f"${sum(costs) / len(costs):.4f} |" if costs else "|")
    w("\n| session | calls | target | STYLE | source | question chars | facts ok | s |")
    w("|---|---|---|---|---|---|---|---|")
    for r in sorted(rows, key=lambda r: (r["purpose"], r["task"])):
        w(f"| {r['purpose']} {r['task']} | {r['calls']} | {r['target']} | {r['style']} | {r['source']} | "
          f"{r['question_chars']} | {r['facts_ok']} | {r['seconds']:.0f} |")


if __name__ == "__main__":
    main(sys.argv[1])
