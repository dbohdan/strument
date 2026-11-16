#!/usr/bin/env python3
"""Layer 4's session measures, beside score.py's defect tables.

    trial_score.py DIR

Per arm: pages written, fact checks, how the sessions ended, cost and time;
and in the consult arm, what MiMo sent Terra, read from the wire. Sessions
moved to DIR/superseded (the ones a cut stream ended, re-run on the fixed
binary) are counted apart, so the amendment can say what they were.
"""

import collections
import glob
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import mech_score as MS  # noqa: E402
import tasks as T  # noqa: E402


def sessions(d):
    rows = []
    for done in sorted(glob.glob(os.path.join(d, "*", "done.json"))):
        s = json.load(open(done))
        base = os.path.dirname(done)
        t = T.LONG_TASKS[s["task"]]
        out = open(os.path.join(base, "stdout.txt"), errors="replace").read()
        err = open(os.path.join(base, "stderr.txt"), errors="replace").read()
        cost = re.findall(r"\$([0-9.]+) run", out)
        calls, terra = MS.consults(os.path.join(base, "wire.jsonl"))
        sent = [{MS.norm(f) for f in (a.get("files") or [])} for a, _ in calls]
        source = [f for f in t["files"] if f not in ("STYLE.md", t["target"])]
        page = s.get("target_text")
        if s["status"] == "timeout":
            end = "timeout"
        elif page is not None:
            end = "page"
        elif "context limit" in err:
            end = "context full"
        elif "reasoning but no answer" in err or "reasoning but no answer" in out:
            end = "cut stream"
        else:
            end = "other"
        rows.append({
            "arm": s["arm"], "task": s["task"], "end": end, "seconds": s["seconds"],
            "facts_ok": T.facts(s["task"], page)["ok"] if page is not None else None,
            "words": len(page.split()) if page else None,
            "cost": float(cost[-1]) if cost else None,
            "calls": len(calls), "terra": terra, "no_files": MS.no_files(calls),
            "target": any(t["target"] in x for x in sent),
            "style": any("STYLE.md" in x for x in sent),
            "source": any(any(f in x for f in source) for x in sent),
            "refused": sum(1 for _, res in calls
                           if not re.search(r"^\W*\w+ \([^)]*\) answered", MS.answer_text(res))),
            "cut_retries": err.count("the stream ended before the response finished"),
        })
    return rows


def table(rows, title):
    w = print
    w(f"\n## {title}\n")
    w("| arm | sessions | pages | facts ok | ended | median s | mean cost | consulted | consult calls | "
      "sent no files | sent target | sent STYLE.md | sent some source | refused | cut-stream retries |")
    w("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
    by = collections.defaultdict(list)
    for r in rows:
        by[r["arm"]].append(r)
    for arm, rs in sorted(by.items()):
        con = [r for r in rs if r["calls"]]
        secs = sorted(r["seconds"] for r in rs)
        costs = [r["cost"] for r in rs if r["cost"] is not None]
        ends = ", ".join(f"{k} {v}" for k, v in collections.Counter(r["end"] for r in rs).most_common())
        pages = [r for r in rs if r["facts_ok"] is not None]
        w(f"| {arm} | {len(rs)} | {len(pages)} | {sum(1 for r in pages if r['facts_ok'])} of {len(pages)} | {ends} | "
          f"{secs[len(secs) // 2]:.0f} | " + (f"${sum(costs) / len(costs):.4f}" if costs else "—") + " | "
          f"{len(con)} | {sum(r['calls'] for r in rs)} | {sum(r['no_files'] for r in rs)} | "
          f"{sum(r['target'] for r in con)} of {len(con)} | {sum(r['style'] for r in con)} of {len(con)} | "
          f"{sum(r['source'] for r in con)} of {len(con)} | {sum(r['refused'] for r in rs)} | "
          f"{sum(r['cut_retries'] for r in rs)} |")
    w("\n| task | arm | pages | facts ok |")
    w("|---|---|---|---|")
    for task in T.LONG_TASKS:
        for arm in sorted(by):
            rs = [r for r in by[arm] if r["task"] == task]
            if not rs:
                continue
            ok = [r for r in rs if r["facts_ok"] is not None]
            w(f"| {task} | {arm} | {len(ok)} of {len(rs)} | {sum(1 for r in ok if r['facts_ok'])} |")


def main(d):
    T.use("long")
    table(sessions(os.path.join(d, "sessions")), "Sessions")
    sup = sessions(os.path.join(d, "superseded"))
    if sup:
        table(sup, "Superseded sessions (cut streams on the first binary)")


if __name__ == "__main__":
    main(sys.argv[1])
