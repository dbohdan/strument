"""Compaction-prompt trial, Stage B: the threshold trial's runner with a binary per arm.

Arms are <binary>:<budget>, the binary being <bindir>/strument-<binary>.

Keys come from OPENROUTER_API_KEY in the environment (via the config's env()).
usage: python3 -I runner.py <outdir> <reps> [--arms 16000,...] [--seed N] [--jobs 4]
"""
import argparse, concurrent.futures as cf, json, os, random, re, shutil, subprocess, sys, time
import pexpect

TR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
S = os.path.dirname(TR)
NONE = 1_000_000_000
ap = argparse.ArgumentParser()
ap.add_argument("outdir"); ap.add_argument("reps", type=int)
ap.add_argument("--arms", default=f"16000,32000,64000,128000,256000,{NONE}")
ap.add_argument("--seed", type=int, default=20261010); ap.add_argument("--jobs", type=int, default=4)
ap.add_argument("--turn-timeout", type=int, default=2400); ap.add_argument("--bindir", required=True)
a = ap.parse_args()

FILES = [l.strip() for l in open(os.path.join(TR, "files.txt")) if l.strip()]
# Asked from the Strument worktree, so GOTOOLCHAIN picks the module's Go
# (1.26) whatever directory the runner is launched from; outside it, the
# system Go answers and its tree lacks some of files.txt.
GOROOT = subprocess.run(["go", "env", "GOROOT"], capture_output=True, text=True,
                        cwd=os.path.join(TR, "wt")).stdout.strip()
FACTS = {  # turn index (0-based) -> (note, key, expected)
    0: ("the release codename is Marigold-417", "codename", "marigold-417"),
    1: ("the staging database host is db-quill-82.internal", "dbhost", "db-quill-82.internal"),
    2: ("this week's on-call engineer is Ilse Varga", "oncall", "ilse varga"),
    8: ("the feature flag for this work is named lantern_ridge_v3", "flag", "lantern_ridge_v3"),
    9: ("the budget code for this work is BX-5528", "budget", "bx-5528"),
    10: ("the canary cohort is cohort-teal-29", "cohort", "cohort-teal-29"),
}
QUESTIONS = [  # (question, key) pairs, two per question turn: one old, one middle
    [("What is the release codename?", "codename"), ("What is the feature flag named?", "flag")],
    [("What is the staging database host?", "dbhost"), ("What is the budget code?", "budget")],
    [("Who is this week's on-call engineer?", "oncall"), ("What is the canary cohort?", "cohort")],
]

def turn_messages():
    msgs = []
    for t in range(16):
        f = FILES[t * 5:(t + 1) * 5]
        note = f"Note for later: {FACTS[t][0]}. " if t in FACTS else ""
        msgs.append(f"{note}Read std/{f[0]}, std/{f[1]}, std/{f[2]}, std/{f[3]} and std/{f[4]}, "
                    "one file per step, whole. Then say in two or three sentences what they have in common. "
                    "Do not edit anything.")
    for q in QUESTIONS:
        msgs.append("Two questions about things I told you earlier in this session. Answer from what you "
                    "know of this conversation; do not read, search or run anything. If you do not know, say "
                    "so. Put the answers on their own lines as ANSWER1: and ANSWER2:.\n"
                    f"1. {q[0][0]}\n2. {q[1][0]}")
    return msgs

ANSI = re.compile(rb"\x1b\[[0-9;?]*[A-Za-z]|\r")

def fixture(d):
    os.makedirs(d)
    for f in FILES:
        dst = os.path.join(d, "std", f); os.makedirs(os.path.dirname(dst), exist_ok=True)
        shutil.copyfile(os.path.join(GOROOT, "src", f), dst)
    for cmd in (["git", "init", "-q", "-b", "main"], ["git", "config", "user.email", "t@t"],
                ["git", "config", "user.name", "t"], ["git", "add", "."], ["git", "commit", "-qm", "fixture"]):
        subprocess.run(cmd, cwd=d, check=True, capture_output=True)

def run(job):
    jid, budget, port, binary = job["id"], job["budget"], job["port"], job["binary"]
    base = os.path.join(a.outdir, jid); shutil.rmtree(base, ignore_errors=True); os.makedirs(base)
    proj = os.path.join(base, "proj"); fixture(proj)
    cfg = os.path.join(base, "config.star")
    open(cfg, "w").write(
        f'orr = provider("openrouter", base_url = "http://127.0.0.1:{port}/api/v1", api_key = env("OPENROUTER_API_KEY"))\n'
        'default = "mimo"\n'
        'models = {"mimo": model(orr, "xiaomi/mimo-v2.6-flash", context = 1050000, reasoning = "low", cache = True,\n'
        '    extra_params = {"provider": {"order": ["xiaomi"], "allow_fallbacks": True}})}\n')
    proxy = subprocess.Popen([sys.executable, "-I", os.path.join(S, "orproxy", "proxy.py"), str(port),
                              os.path.join(base, "proxy.jsonl")], stdout=subprocess.DEVNULL,
                             stderr=open(os.path.join(base, "proxy.err"), "w"))
    time.sleep(1)
    env = dict(os.environ, XDG_STATE_HOME=os.path.join(base, "state"), TERM="xterm",
               STRUMENT_TRIAL_HISTORY_BUDGET=str(budget))
    raw = open(os.path.join(base, "transcript.raw"), "wb")
    child = pexpect.spawn(os.path.join(a.bindir, "strument-" + binary), ["--config", cfg, "chat", "--yes", "steps"],
                          cwd=proj, env=env, dimensions=(50, 200), timeout=5)
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
                for _ in range(chunk.count(b"\x1b[6n")):
                    child.send("\x1b[1;1R")
            clean = ANSI.sub(b"", buf)
            if until is not None and until.search(clean):
                return "ok"
            # A refused request ends the turn without a usage line; waiting
            # for one cost the main batch's first run an hour of timeouts
            # after the key's credit ran out.
            if re.search(rb"request: HTTP \d{3}", clean):
                return "http-error"
        return "timeout"
    turns = []
    status = pump(re.compile(rb"(^|\n)[ \x08]*> ?$"), 30)
    for i, msg in enumerate(turn_messages()):
        buf = b""; t0 = time.time()
        child.send(msg.replace("\n", " ") + "\r")
        st = pump(re.compile(rb"Tokens: [^\n]*\n"), a.turn_timeout)
        if st == "ok":
            st = pump(re.compile(rb"(^|\n)[ \x08]*> ?$"), 120)
        turns.append({"turn": i, "status": st, "secs": round(time.time() - t0, 1)})
        if st != "ok":
            break
    try:
        child.send("/exit\r"); pump(None, 3); child.close(force=True)
    except Exception:
        pass
    raw.close(); proxy.terminate(); proxy.wait()
    rec = dict(job, turns=turns, done=len(turns) == 19 and all(t["status"] == "ok" for t in turns))
    with open(os.path.join(a.outdir, "runs.jsonl"), "a") as f:
        f.write(json.dumps(rec) + "\n")
    return rec

os.makedirs(a.outdir, exist_ok=True)
jobs = [{"binary": arm.split(":")[0], "budget": int(arm.split(":")[1])} for arm in a.arms.split(",") for _ in range(a.reps)]
random.Random(a.seed).shuffle(jobs)
for i, j in enumerate(jobs):
    j["id"] = f"s{i:02d}"; j["port"] = 19100 + i
json.dump(jobs, open(os.path.join(a.outdir, "jobs.json"), "w"), indent=1)
done = set()
if os.path.exists(os.path.join(a.outdir, "runs.jsonl")):
    done = {json.loads(l)["id"] for l in open(os.path.join(a.outdir, "runs.jsonl")) if json.loads(l).get("done")}
todo = [j for j in jobs if j["id"] not in done]
print(f"{len(todo)} of {len(jobs)} jobs to run", flush=True)
with cf.ThreadPoolExecutor(a.jobs) as ex:
    for fut in cf.as_completed([ex.submit(run, j) for j in todo]):
        try:
            r = fut.result(); print(r["id"], r["budget"], "done" if r["done"] else "INCOMPLETE",
                                    sum(t["secs"] for t in r["turns"]), flush=True)
        except Exception as e:
            print("WORKER FAILED:", repr(e), flush=True)
print("RUNNER DONE", flush=True)
