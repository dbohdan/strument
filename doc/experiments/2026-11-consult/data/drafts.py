#!/usr/bin/env python3
"""Layer 4, post hoc: Terra's drafts as pages the defect judge can read.

    drafts.py DIR

For each consult session, Terra's last answer is read from the wire, its
attribution line dropped, and the page block extracted the way the long set
extracts any answer. Each is saved as DIR/gen/task__draft__rep.json, so
`defects.py --writers draft` judges the drafts exactly as it judged the pages.
"""

import glob
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import common as C  # noqa: E402
import mech_score as MS  # noqa: E402
import tasks as T  # noqa: E402

ANSWERED = r"^\W*\w+ \([^)]*\) answered"


def main(outdir):
    T.use("long")
    for done in sorted(glob.glob(os.path.join(outdir, "sessions", "*__consult__*", "done.json"))):
        s = json.load(open(done))
        calls, _ = MS.consults(os.path.join(os.path.dirname(done), "wire.jsonl"))
        answers = [MS.answer_text(r) for _, r in calls if re.search(ANSWERED, MS.answer_text(r))]
        if not answers:
            print(s["id"], "no answer")
            continue
        body = answers[-1].split("\n", 1)[1].lstrip("\n")
        block, how = C.extract_block(body)
        rec = {"model": "draft", "task": s["task"], "n": s["rep"], "ok": block is not None, "block": block,
               "extracted": how, "facts": T.facts(s["task"], block) if block else None}
        with open(os.path.join(outdir, "gen", f"{s['task']}__draft__{s['rep']}.json"), "w") as f:
            json.dump(rec, f, indent=1)
        print(s["id"], how, "facts ok" if rec["facts"] and rec["facts"]["ok"] else "facts FAILED")


if __name__ == "__main__":
    main(sys.argv[1])
