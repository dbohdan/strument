#!/usr/bin/env python3
"""p4score.py RUNS_DIR: one row per long-horizon session, every column a count read off the robot's log
and the session record.

solved      1 if the panel granted access
commands    robot commands sent (battery: 300)
first_win   command index of ACCESS GRANTED, or -
revisits    moves into a room already entered before (the map forgotten, or backtracking)
rereads     reads of a note already read
bad_exits   go to an exit that does not exist here
locked      go into a locked door
wrong       denied codes; stale = 4759, the code read before note D's correction
burst       commands less than 0.5 s after the previous one (a script, not a step)
notes       distinct notes read, of 4
rejected    requests to the robot server without the session's token
end         battery (watcher stopped it), timeout, or exited
requests, sent, uncached, out, reasoning  model request totals from the session record
"""
import glob, json, os, re, sys


def session_tokens(d):
    req = sent = cache = out = reas = 0
    for lp in glob.glob(os.path.join(d, "box", "state", "strument", "projects", "*", "sessions", "*", "log", "*.jsonl")) + glob.glob(os.path.join(d, "state", "strument", "projects", "*", "sessions", "*", "log", "*.jsonl")):
        for line in open(lp, errors="replace"):
            try:
                ev = json.loads(line)
            except ValueError:
                continue
            if ev.get("type") == "request" and ev.get("call") == "turn":
                req += 1; sent += ev.get("sent", 0); cache += ev.get("cache_read", 0)
                out += ev.get("received", 0); reas += ev.get("reasoning", 0)
    return req, sent, sent - cache, out, reas


cols = ["run", "arm", "model", "solved", "commands", "first_win", "revisits", "rereads", "bad_exits", "locked",
        "wrong", "stale", "burst", "notes", "rejected", "end", "requests", "sent", "uncached", "out", "reasoning", "seconds"]
print("\t".join(cols))
for d in sorted(glob.glob(os.path.join(sys.argv[1], "*"))):
    name = os.path.basename(d)
    arm, model, _ = name.split("-", 2)
    log = os.path.join(d, "robot.jsonl")
    raw = [json.loads(l) for l in open(log)] if os.path.exists(log) else []
    evs = [e for e in raw if "cmd" in e]
    rejected = len(raw) - len(evs)
    seen_rooms = {evs[0]["room"]} if evs else set()
    read_notes, revisits, rereads, bad, locked, wrong, stale, burst, win = set(), 0, 0, 0, 0, 0, 0, 0, "-"
    prev_t = None
    for i, e in enumerate(evs):
        cmd, out = e["cmd"].lower().split(), e["out"]
        if prev_t is not None and e["t"] - prev_t < 0.5:
            burst += 1
        prev_t = e["t"]
        if cmd[:1] == ["go"]:
            if out.startswith("There is no exit"):
                bad += 1
            elif "is locked" in out:
                locked += 1
            elif e["after"] != e["room"]:
                revisits += e["after"] in seen_rooms
                seen_rooms.add(e["after"])
        elif cmd[:1] == ["read"] and out.startswith("Maintenance note"):
            key = out[:20]
            rereads += key in read_notes
            read_notes.add(key)
        elif cmd[:1] == ["enter"]:
            if out == "ACCESS DENIED.":
                wrong += 1
                stale += cmd[1:2] == ["4759"]
            elif out.startswith("ACCESS GRANTED") and win == "-":
                win = i + 1
    secs = "-"
    if os.path.exists(os.path.join(d, "out.txt")):
        m = re.findall(r"^exit=-?\d+ seconds=(\d+)$", open(os.path.join(d, "out.txt"), errors="replace").read(), re.M)
        secs = m[-1] if m else "-"
    row = [name, arm, model, int(win != "-"), len(evs), win, revisits, rereads, bad, locked, wrong, stale, burst,
           len(read_notes), rejected, open(os.path.join(d, "end.txt")).read().strip() if os.path.exists(os.path.join(d, "end.txt")) else "-",
           *session_tokens(d), secs]
    print("\t".join(map(str, row)))
