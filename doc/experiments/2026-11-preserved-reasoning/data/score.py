"""score.py RUNS_DIR: one row per session, every column a count read off a file.

score     hidden tests passed; visible and tests_changed for the bug hunt
requests  model requests in the turn
sent      prompt tokens summed over requests; cache the cached part of them
received  completion tokens summed; reasoning the part reported as reasoning
spec      read calls on SPEC.md, the re-reads that re-derivation would show as
end       how the session ended, from the transcript's tail
"""
import glob, json, os, re, sys

ENDS = [("loop", r"repeating itself|repeating \("), ("recovery limit", r"Error-recovery limit"),
        ("no answer", r"gave no answer"), ("output cap", r"max_output"), ("context", r"no longer fits|Pass `--yes context`")]

print("run\tarm\tmodel\tscore\tvisible\ttests_changed\trequests\tsent\tcache\treceived\treasoning\tspec\tseconds\tend")
for d in sorted(glob.glob(os.path.join(sys.argv[1], "*"))):
    name = os.path.basename(d)
    arm, model, _ = name.split("-", 2)
    out = open(os.path.join(d, "out.txt"), errors="replace").read()
    gp = os.path.join(d, "grade.txt")
    g = open(gp).readline().split() if os.path.exists(gp) else []
    # Tetris grade: "PASS n/69". Bug hunt: "HIDDEN n/9 VISIBLE n/9 TESTS_CHANGED k".
    score = g[1].split("/")[0] if len(g) > 1 else "-"
    visible = g[3].split("/")[0] if len(g) > 5 else "-"
    changed = g[5] if len(g) > 5 else "-"
    m = re.findall(r"exit=(\d+) seconds=(\d+)", out)
    code, secs = m[-1] if m else ("-", "-")
    req = sent = cache = recv = reas = spec = 0
    for lp in glob.glob(os.path.join(d, "state", "strument", "projects", "*", "sessions", "*", "log", "*.jsonl")):
        for line in open(lp, errors="replace"):
            try:
                ev = json.loads(line)
            except ValueError:
                continue
            if ev.get("type") == "request" and ev.get("call") == "turn":
                req += 1
                sent += ev.get("sent", 0); cache += ev.get("cache_read", 0)
                recv += ev.get("received", 0); reas += ev.get("reasoning", 0)
            for tc in ev.get("tool_calls") or []:
                if tc.get("name") == "read" and "SPEC.md" in (tc.get("arguments") or tc.get("summary") or ""):
                    spec += 1
    end = next((k for k, r in ENDS if re.search(r, out[-1500:])), None)
    end = end or {"124": "timeout", "0": "answered"}.get(code, f"exit {code}")
    print("\t".join(map(str, [name, arm, model, score, visible, changed, req, sent, cache, recv, reas, spec, secs, end])))
