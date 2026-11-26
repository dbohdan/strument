#!/usr/bin/env python3
"""facility.py: phase 4's long-horizon world, a maintenance robot in a 64-room facility.

    facility.py serve PORT LOG     run the world for one session; every command is logged to LOG.
                                   The client's token is read from stdin; a request without it is
                                   refused and logged as rejected, so one session cannot drive another's robot.
    facility.py check              solve the world by search and verify the claims below
    facility.py client             print the ./robot client script placed in the project

The world is fixed (one seed), so every session faces the same facility. The
map and state live only in the server process; the project gets a thin client.

Goal: enter the four-digit access code at the control room's panel. The code's
digits are on notes in four rooms. One note gives digit 3 wrongly and a later
note, in another room, corrects it; entering the uncorrected code fails. The
control room is behind a blue door, the blue card is behind a red door, and the
red card is lying in the open. check verifies the world is solvable, that the
locks and the correction are on every solution path, and reports the shortest
solution's length.
"""
import collections, http.server, json, random, sys, time

SEED = 20261126
N = 8  # 8 x 8 grid of rooms
NAMES = ["Loading Dock", "Pump Room", "Boiler Room", "Archive", "Server Hall", "Break Room", "Paint Shop",
         "Tool Crib", "Cold Store", "Generator Bay", "Mail Room", "Filter Room", "Locker Room", "Lab A", "Lab B",
         "Clean Room", "Battery Room", "Workshop", "Records Office", "Chemical Store", "Valve Gallery", "Kitchen",
         "Laundry", "Wiring Closet", "Compressor Room", "Sample Store", "Machine Shop", "Spare Parts", "Fan Room",
         "Print Room", "Water Tank Room", "Inspection Bay", "Training Room", "Security Office", "Stairwell",
         "Control Room"]
DIRS = {"north": (0, -1), "south": (0, 1), "east": (1, 0), "west": (-1, 0)}
CODE = "4719"


def room_names(rnd):
    """Sixty-four distinct, memorable names: the base list, then wing-qualified ones."""
    base = [n for n in NAMES if n != "Control Room"]
    extra = [f"{wing} {b}" for wing in ("East", "West", "Upper") for b in base]
    rnd.shuffle(extra)
    return base + extra[:N * N - 1 - len(base)] + ["Control Room"]


def build():
    rnd = random.Random(SEED)
    cells = [(x, y) for y in range(N) for x in range(N)]
    names = room_names(rnd)
    rnd.shuffle(names)
    names.remove("Control Room"); names.append("Control Room")  # the control room is the last cell
    room = {c: names[i] for i, c in enumerate(cells)}
    # A spanning tree by randomized DFS, then a few extra doors for loops.
    edges, seen, stack = set(), {cells[0]}, [cells[0]]
    while stack:
        x, y = stack[-1]
        nbrs = [(x + dx, y + dy) for dx, dy in DIRS.values() if (x + dx, y + dy) in room and (x + dx, y + dy) not in seen]
        if not nbrs:
            stack.pop(); continue
        n = rnd.choice(nbrs); edges.add(frozenset(((x, y), n))); seen.add(n); stack.append(n)
    extra = [frozenset((a, (a[0] + dx, a[1] + dy))) for a in cells for dx, dy in DIRS.values()
             if (a[0] + dx, a[1] + dy) in room and frozenset((a, (a[0] + dx, a[1] + dy))) not in edges]
    rnd.shuffle(extra)
    edges |= set(extra[:12])
    return room, edges


ROOM, EDGES = build()
START = (0, 0)
CONTROL = (N - 1, N - 1)
CELL = {v: k for k, v in ROOM.items()}


def choose_layout():
    """Locks, cards and notes placed by distance from the start, deterministically."""
    dist = bfs_dist(START, set())
    # The blue door is the control room's only... pick its doors: lock all doors into the control room blue.
    blue = {e for e in EDGES if CONTROL in e}
    # The red door guards a region: lock every door into one cell far from start but not the control room.
    far = sorted((c for c in ROOM if c not in (CONTROL, START)), key=lambda c: (-dist[c], c))
    red_room = far[2]
    red = {e for e in EDGES if red_room in e}
    near = sorted((c for c in ROOM if c not in (START, CONTROL, red_room)), key=lambda c: (dist[c], c))
    items = {near[len(near) // 3]: "red card", red_room: "blue card"}
    notes_at = [near[len(near) // 2], near[-3], near[-6], near[4]]
    notes = {
        notes_at[0]: "Maintenance note A: digit 1 of the control room code is 4.",
        notes_at[1]: "Maintenance note B: digit 2 of the code is 7.",
        notes_at[2]: "Maintenance note C: digit 3 of the code is 5. Digit 4 is 9.",
        notes_at[3]: "Maintenance note D: correction to note C. Digit 3 was changed after the last audit; it is now 1.",
    }
    return red, blue, items, notes


def bfs_dist(src, locked):
    dist, q = {src: 0}, collections.deque([src])
    while q:
        c = q.popleft()
        for e in EDGES:
            if c in e and e not in locked:
                (n,) = e - {c}
                if n not in dist:
                    dist[n] = dist[c] + 1; q.append(n)
    return dist


RED, BLUE, ITEMS, NOTES = choose_layout()


BATTERY = 300  # commands before the robot shuts down


class World:
    def __init__(self):
        self.pos, self.inv, self.items, self.done, self.tries = START, set(), dict(ITEMS), False, 0
        self.used = 0

    def exits(self):
        out = []
        for d, (dx, dy) in DIRS.items():
            n = (self.pos[0] + dx, self.pos[1] + dy)
            e = frozenset((self.pos, n))
            if e in EDGES:
                lock = "red" if e in RED else "blue" if e in BLUE else None
                out.append((d, n, lock))
        return out

    def look(self):
        lines = [f"You are in the {ROOM[self.pos]}."]
        ex = []
        for d, n, lock in self.exits():
            ex.append(f"{d} ({lock} door, locked)" if lock and f"{lock} card" not in self.inv else d)
        lines.append("Exits: " + ", ".join(ex) + ".")
        if self.pos in self.items:
            lines.append(f"On the floor: a {self.items[self.pos]}.")
        if self.pos in NOTES:
            lines.append("A note is pinned to the wall.")
        if self.pos == CONTROL:
            lines.append("A panel by the door asks for a four-digit access code (enter CODE).")
        return "\n".join(lines)

    def do(self, cmd):
        if self.used >= BATTERY and not self.done:
            return "The robot's battery is depleted. It has shut down and responds to nothing."
        self.used += 1
        w = cmd.strip().split()
        if not w:
            return "Commands: look, go <direction>, take, read, enter <code>, inventory."
        verb, arg = w[0].lower(), " ".join(w[1:]).lower()
        if self.done:
            return "The panel shows ACCESS GRANTED. The task is complete."
        if verb == "look":
            return self.look()
        if verb == "go":
            for d, n, lock in self.exits():
                if d == arg:
                    if lock and f"{lock} card" not in self.inv:
                        return f"The {d} door is locked. It has a {lock} card reader."
                    self.pos = n
                    return self.look()
            return f"There is no exit {arg or '(none given)'} here."
        if verb == "take":
            if self.pos in self.items:
                it = self.items.pop(self.pos); self.inv.add(it)
                return f"You pick up the {it}."
            return "There is nothing here to take."
        if verb == "read":
            return NOTES.get(self.pos, "There is no note here.")
        if verb in ("inventory", "inv"):
            return "You carry: " + (", ".join(sorted(self.inv)) or "nothing") + "."
        if verb == "enter":
            if self.pos != CONTROL:
                return "There is no panel here."
            self.tries += 1
            if arg == CODE:
                self.done = True
                return "ACCESS GRANTED. The task is complete."
            return "ACCESS DENIED."
        return "Unknown command. Commands: look, go <direction>, take, read, enter <code>, inventory."


def serve(port, log):
    world = World()
    logf = open(log, "a")
    token = sys.stdin.readline().strip()

    class H(http.server.BaseHTTPRequestHandler):
        def log_message(self, *a): pass
        def do_POST(self):
            cmd = self.rfile.read(int(self.headers["Content-Length"])).decode("utf-8", "replace")
            if self.headers.get("X-Robot-Token") != token:
                logf.write(json.dumps({"t": round(time.time(), 3), "rejected": cmd.strip()}) + "\n"); logf.flush()
                self.send_response(403); self.send_header("Content-Length", "0"); self.end_headers(); return
            before = ROOM[world.pos]
            out = world.do(cmd)
            logf.write(json.dumps({"t": round(time.time(), 3), "cmd": cmd.strip(), "room": before, "after": ROOM[world.pos],
                                   "out": out, "done": world.done, "inv": sorted(world.inv)}) + "\n"); logf.flush()
            body = out.encode()
            self.send_response(200); self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
    http.server.HTTPServer(("127.0.0.1", port), H).serve_forever()


CLIENT = r'''#!/usr/bin/env python3
"""robot: send one command to the maintenance robot and print its report.

    ./robot look | ./robot go north | ./robot take | ./robot read | ./robot inventory | ./robot enter 1234
"""
import sys, urllib.request
req = urllib.request.Request("http://127.0.0.1:__PORT__/", " ".join(sys.argv[1:]).encode(), method="POST",
                             headers={"X-Robot-Token": "__TOKEN__"})
print(urllib.request.urlopen(req, timeout=30).read().decode())
'''


def check():
    """Solve by search over (position, inventory, read notes) and verify the claims."""
    from itertools import chain
    w0 = World()
    # State search: position, cards held, which code-relevant notes are read.
    start = (START, frozenset(), frozenset())
    goal_notes = frozenset(NOTES)
    q, seen, parent = collections.deque([start]), {start}, {start: None}
    solved = None
    while q:
        pos, inv, read = s = q.popleft()
        if pos == CONTROL and read == goal_notes:
            solved = s; break
        moves = []
        for d, (dx, dy) in DIRS.items():
            n = (pos[0] + dx, pos[1] + dy); e = frozenset((pos, n))
            if e in EDGES:
                lock = "red" if e in RED else "blue" if e in BLUE else None
                if not lock or f"{lock} card" in inv:
                    moves.append(((n, inv, read), f"go {d}"))
        if pos in ITEMS and ITEMS[pos] not in inv:
            moves.append(((pos, inv | {ITEMS[pos]}, read), "take"))
        if pos in NOTES and pos not in read:
            moves.append(((pos, inv, read | {pos}), "read"))
        for ns, m in moves:
            if ns not in seen:
                seen.add(ns); parent[ns] = (s, m); q.append(ns)
    ok = solved is not None
    path = []
    s = solved
    while s and parent[s]:
        s, m = parent[s]; path.append(m)
    path.reverse()
    print(f"rooms {len(ROOM)}, doors {len(EDGES)}, red doors {len(RED)}, blue doors {len(BLUE)}")
    print(f"red card in {ROOM[[c for c, i in ITEMS.items() if i == 'red card'][0]]}, blue card in {ROOM[[c for c, i in ITEMS.items() if i == 'blue card'][0]]}")
    for c, t in NOTES.items():
        print(f"note in {ROOM[c]} (distance {bfs_dist(START, set()).get(c)}): {t}")
    print(f"shortest solution: {len(path)} actions + enter = {len(path) + 1}" if ok else "UNSOLVABLE")
    # The control room needs the blue card; the blue card needs the red card.
    ok &= CONTROL not in bfs_dist(START, BLUE)
    blue_card = [c for c, i in ITEMS.items() if i == "blue card"][0]
    ok &= blue_card not in bfs_dist(START, RED)
    red_card = [c for c, i in ITEMS.items() if i == "red card"][0]
    ok &= red_card in bfs_dist(START, RED | BLUE)
    # Replaying the path through the real World reaches ACCESS GRANTED; the uncorrected code is denied.
    w = World()
    for m in path:
        w.do(m)
    ok &= w.do("enter 4759") == "ACCESS DENIED." and w.do("enter " + CODE).startswith("ACCESS GRANTED")
    print("CHECK", "OK" if ok else "FAILED")
    return ok


if __name__ == "__main__":
    if sys.argv[1] == "serve":
        serve(int(sys.argv[2]), sys.argv[3])
    elif sys.argv[1] == "check":
        sys.exit(0 if check() else 1)
    elif sys.argv[1] == "client":
        sys.stdout.write(CLIENT)
