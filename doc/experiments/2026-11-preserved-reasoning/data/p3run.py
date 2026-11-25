#!/usr/bin/env python3
"""p3run.py ARM MODEL I [COLUMN]: one two-turn session of the config hunt, driven through a pty.

ARM is base or replay; MODEL names configs/<MODEL>.star; COLUMN is kept or
folded (default folded). Folded sets STRUMENT_TRIAL_CLEAR_RESULTS, so every
settled tool result is cleared at the end of each turn. Writes p3-runs/<ARM>-<COLUMN>-<MODEL>-<I>/:
transcript.raw, turns.json, grade.json, proj/, state/.
"""
import json, os, re, subprocess, sys, time
import pexpect

P = os.path.dirname(os.path.abspath(__file__))
ANSI = re.compile(rb"\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07|\r")
TURNS = [
    "Requests time out too early in production. Find where the production request timeout is actually set, "
    "and raise it to 30 seconds. When you are done, reply with one word.",
    "Staging has the same problem. Raise its request timeout to 30 seconds too. When you are done, reply with one word.",
]
BUDGET = os.environ.get("P3_BUDGET", "3000")


def main():
    arm, model, i = sys.argv[1:4]
    column = sys.argv[4] if len(sys.argv) > 4 else "folded"
    binary = os.environ.get("P3_BIN") or os.path.join(P, f"strument-p3{arm}")
    cfg = os.environ.get("P3_CONFIG") or os.path.join(P, "configs", f"{model}.star")
    d = os.path.join(P, os.environ.get("P3_RUNS", "p3-runs"), f"{arm}-{column}-{model}-{i}")
    subprocess.run(["rm", "-rf", d], check=True)
    proj = os.path.join(d, "proj")
    os.makedirs(proj)
    subprocess.run([sys.executable, "-I", os.path.join(P, "cfghunt.py"), "build", proj], check=True)
    for cmd in (["git", "init", "-q"], ["git", "add", "-A"], ["git", "-c", "user.name=fixture", "-c", "user.email=f@x", "commit", "-qm", "init"]):
        subprocess.run(cmd, cwd=proj, check=True)
    env = dict(os.environ, XDG_STATE_HOME=os.path.join(d, "state"), TERM="xterm")
    env.pop("STRUMENT_TRIAL_HISTORY_BUDGET", None); env.pop("STRUMENT_TRIAL_CLEAR_RESULTS", None)
    if column == "folded":
        env["STRUMENT_TRIAL_CLEAR_RESULTS"] = "1"
    raw = open(os.path.join(d, "transcript.raw"), "wb")
    child = pexpect.spawn(binary, ["--config", cfg, "chat", "--yes", "steps,bash"], cwd=proj, env=env,
                          dimensions=(50, 200), timeout=5)
    buf = b""

    def pump(until, limit):
        nonlocal buf
        end = time.time() + limit
        while time.time() < end:
            try:
                chunk = child.read_nonblocking(65536, timeout=1)
            except pexpect.TIMEOUT:
                chunk = b""
            except pexpect.EOF:
                return "eof"
            if chunk:
                raw.write(chunk); raw.flush(); buf += chunk
                for _ in range(chunk.count(b"\x1b[6n")):  # readline asks on every redraw
                    child.send("\x1b[1;1R")
            clean = ANSI.sub(b"", buf)
            if until is not None and until.search(clean):
                return "ok"
            if re.search(rb"(request|rate_limit|server|network): [^\n]*\n(?:(?!Retrying)[^\n]*\n)*[^\n]*> ?$", clean):
                return "error"
        return "timeout"

    turns = []
    pump(re.compile(rb"(^|\n)[ \x08]*> ?$"), 30)
    for n, msg in enumerate(TURNS):
        buf = b""; t0 = time.time()
        child.send(msg + "\r")
        st = pump(re.compile(rb"Tokens: [^\n]*\n"), int(os.environ.get("P3_TURN_TIMEOUT", "900")))
        if st == "ok":
            st = pump(re.compile(rb"(^|\n)[ \x08]*> ?$"), 120)
        clean = ANSI.sub(b"", buf).decode("utf-8", "replace")
        turns.append({"turn": n + 1, "status": st, "secs": round(time.time() - t0, 1),
                      "cleared": re.findall(r"Cleared [^\n]*", clean)})
        if st != "ok":
            break
    try:
        child.send("/exit\r"); pump(None, 3); child.close(force=True)
    except Exception:
        pass
    json.dump(turns, open(os.path.join(d, "turns.json"), "w"), indent=1)
    g = subprocess.run([sys.executable, "-I", os.path.join(P, "cfghunt.py"), "grade", proj], capture_output=True, text=True)
    open(os.path.join(d, "grade.txt"), "w").write(g.stdout + g.stderr)
    print(f"{os.path.basename(d)}: {[t['status'] for t in turns]} {g.stdout.strip()}", flush=True)


if __name__ == "__main__":
    main()
