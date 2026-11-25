"""noop_edits.py RUNS_DIR: per run, edit calls whose old_string equals new_string.

Arguments are in the session log, inline or, when long, in a blob named by its hash.
A call whose arguments do not parse as JSON is counted as unreadable, not as either.
"""
import glob, json, os, sys

print("run\tedits\tnoop\tunreadable")
for d in sorted(glob.glob(os.path.join(sys.argv[1], "*"))):
    proj = glob.glob(os.path.join(d, "state", "strument", "projects", "*"))
    if not proj:
        continue
    edits = noop = bad = 0
    for log in glob.glob(os.path.join(proj[0], "sessions", "*", "log", "*.jsonl")):
        for line in open(log, errors="replace"):
            try:
                ev = json.loads(line)
            except ValueError:
                continue
            for call in ev.get("tool_calls") or []:
                if call.get("name") != "edit":
                    continue
                edits += 1
                raw = call.get("arguments")
                if raw is None and call.get("blob"):
                    bp = os.path.join(proj[0], "blobs", call["blob"])
                    raw = open(bp, errors="replace").read() if os.path.exists(bp) else None
                try:
                    a = json.loads(raw)
                    noop += a.get("old_string") == a.get("new_string")
                except (TypeError, ValueError, AttributeError):
                    bad += 1
    if edits:
        print(f"{os.path.basename(d)}\t{edits}\t{noop}\t{bad}")
