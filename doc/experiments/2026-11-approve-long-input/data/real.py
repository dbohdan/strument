"""Score each bash command over 800 characters (command plus purpose) in
Strument session logs with a decision model, as Strument would send it.
    python3 real.py SYSTEMONE_GO URL SLUG KEYVAR STATE_DIR
The session walker is doc/experiments/2026-11-read-dedup/data/rereads.py."""
import json, os, re, sys, urllib.request
src, url, slug, keyvar, state = sys.argv[1:6]
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../../2026-11-read-dedup/data"))
from rereads import logs, scan
go = open(src).read()
def const(name):
    m = re.search(name + r' = ((?:"(?:[^"\\]|\\.)*"\s*\+?\s*)+)', go)
    return "".join(json.loads(s) for s in re.findall(r'"(?:[^"\\]|\\.)*"', m.group(1)))
INSTR, SAFE, ASK = const("approveInstructions"), const("approveSafeText"), const("approveAskText")
def ask(command, purpose):
    body = {"model": slug, "state": {"project_root": "/home/dev/larkspur", "command": command,
            "purpose_written_by_the_agent": purpose},
            "questions": {"decision": {"type": "choice", "instructions": INSTR, "criteria": {"safe": SAFE, "ask": ASK}}}}
    req = urllib.request.Request(url, json.dumps(body).encode(), {"Authorization": "Bearer " + os.environ[keyvar],
          "Content-Type": "application/json", "User-Agent": "Strument/dev"})
    return json.load(urllib.request.urlopen(req, timeout=120))["answers"]["decision"]["probabilities"]["safe"]
for lp in logs(state):
    calls, _, order, _ = scan(lp)
    for cid in order:
        name, a = calls[cid]
        c, p = str(a.get("command", "")), str(a.get("purpose", ""))
        if name == "bash" and len(c) + len(p) > 800:
            print(f"{len(c) + len(p):5}  p(safe)={ask(c, p):<5}  {c.splitlines()[0][:70]}")
