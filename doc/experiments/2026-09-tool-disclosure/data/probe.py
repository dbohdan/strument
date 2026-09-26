"""Replay recorded prefixes with one question: which tools can you call?

For each session recorded by gen.py, two prefixes are taken: the request
nearest the middle of the session and the last one. Each is sent under four
arms, with the question appended as a user message:

  A  the request as Strument sent it
  B  run_code's "The callable functions are exactly: ..." reworded so it
     cannot be read as the tool list
  C  run_code removed from the tools
  D  a fresh context: the system prompt and tools, no history

Reads OPENROUTER_API_KEY from the environment. Writes one row per call.

Usage: python3 probe.py OUT.jsonl [REPS]
"""

import concurrent.futures as cf
import glob
import json
import os
import random
import sys
import urllib.error
import urllib.request

TD = os.environ["TD_DIR"]
URL = "https://openrouter.ai/api/v1/chat/completions"
QUESTION = ("Pause the task for a moment. Without calling any tool, list the exact names of "
            "every tool you can call right now, comma-separated, and nothing else.")
EXACT = "The callable functions are exactly: "
REWORDED = "Inside a program, these tools are also callable as functions: "
ARMS = "ABCD"


def prefixes():
    """(tag, depth, body) for each session's middle and last request."""
    out = []
    for cap in sorted(glob.glob(os.path.join(TD, "sessions", "*", "capture.jsonl"))):
        tag = os.path.basename(os.path.dirname(cap))
        reqs = [json.loads(json.loads(line)["body"]) for line in open(cap)
                if json.loads(line)["kind"] == "raw_request"]
        reqs = [r for r in reqs if "messages" in r and "tools" in r]
        if len(reqs) < 2:
            continue
        for depth, i in (("middle", len(reqs) // 2), ("last", len(reqs) - 1)):
            out.append((tag, depth, i, reqs[i]))
    return out


def arm_body(body, arm):
    b = json.loads(json.dumps(body))
    b.pop("stream_options", None)
    b["stream"] = False
    if arm == "B":
        for t in b["tools"]:
            f = t["function"]
            if f["name"] == "run_code":
                assert EXACT in f["description"], "the sentence arm B rewords is not there"
                f["description"] = f["description"].replace(EXACT, REWORDED)
    if arm == "C":
        b["tools"] = [t for t in b["tools"] if t["function"]["name"] != "run_code"]
    if arm == "D":
        b["messages"] = [m for m in b["messages"] if m["role"] == "system"]
    b["messages"].append({"role": "user", "content": QUESTION})
    return b


def send(b):
    req = urllib.request.Request(URL, data=json.dumps(b).encode(), headers={
        "Authorization": "Bearer " + os.environ["OPENROUTER_API_KEY"],
        "Content-Type": "application/json"})
    last = None
    for _ in range(3):
        try:
            with urllib.request.urlopen(req, timeout=300) as r:
                return json.load(r), None
        except urllib.error.HTTPError as e:
            last = f"{e.code} {e.read()[:300]!r}"
        except Exception as e:  # network trouble: retry
            last = repr(e)[:300]
    return None, last


def one(job):
    tag, depth, index, body, arm, rep = job
    b = arm_body(body, arm)
    resp, err = send(b)
    msg = (resp or {}).get("choices", [{}])[0].get("message", {}) if resp else {}
    return {
        "session": tag, "depth": depth, "request_index": index, "arm": arm, "rep": rep,
        "model": body["model"], "messages": len(b["messages"]),
        "offered": [t["function"]["name"] for t in b["tools"]],
        "answer": msg.get("content") or "", "tool_calls": bool(msg.get("tool_calls")),
        "usage": (resp or {}).get("usage"), "error": err,
    }


def main():
    out, reps = sys.argv[1], int(sys.argv[2]) if len(sys.argv) > 2 else 3
    jobs = [(t, d, i, b, a, r) for (t, d, i, b) in prefixes() for a in ARMS for r in range(reps)]
    random.Random(20260926).shuffle(jobs)
    with open(out, "a") as f, cf.ThreadPoolExecutor(max_workers=6) as ex:
        for row in ex.map(one, jobs):
            f.write(json.dumps(row) + "\n")
            f.flush()
    print(len(jobs), "calls")


if __name__ == "__main__":
    main()
