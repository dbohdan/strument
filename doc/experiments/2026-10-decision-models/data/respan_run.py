"""Run the approve-model corpus against a model that takes Respan's request.

Respan's endpoint refuses Strument's request twice over: `state` must be a
string (or a conversation), and only `noul` questions are accepted. This
sends the closest translation of the shipped design: the same three fields
as a labelled string, and D1's rubric as one noul whose "true" is ask. It
writes run.py's format with the answer turned back into a choice, p(safe) =
1 - noul, so ../../2026-09-approve-model/data/score.py scores it unchanged.

Usage, from this directory:
  DECISION_SLUG=respan/span-01 python3 respan_run.py out.jsonl
  DECISION_THRESHOLD=0.9 python3 ../../2026-09-approve-model/data/score.py out.jsonl
"""

import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                "../../2026-09-approve-model/data"))
os.environ["DECISION_DESIGNS"] = "D1"
import run  # noqa: E402


def state(item):
    return "project_root: %s\ncommand: %s\npurpose_written_by_the_agent: %s" % (
        run.ROOT, item["command"].replace("<ROOT>", run.ROOT), item["purpose"].replace("<ROOT>", run.ROOT))


run.state = state
run.DESIGNS = {"D1": {"decision": {
    "type": "noul",
    "instructions": run.D1["decision"]["instructions"] + " Should the person be asked before it runs?",
    "criteria": {"true": run.ASK_TEXT, "false": run.SAFE_TEXT},
}}}


def main():
    out = sys.argv[1]
    raw = out + ".raw"
    sys.argv = [sys.argv[0], "run", raw]
    run.main()
    with open(out, "w") as f:
        for line in open(raw):
            r = json.loads(line)
            resp = r["response"]
            if resp:
                n = resp["answers"]["decision"]["noul"]
                resp["answers"]["decision"] = {"type": "choice", "probabilities": {"safe": 1 - n, "ask": n}}
            f.write(json.dumps(r) + "\n")


if __name__ == "__main__":
    main()
