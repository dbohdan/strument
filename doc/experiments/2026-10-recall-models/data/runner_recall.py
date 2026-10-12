"""Recall trial (doc/experiments/2026-10-recall): the roll trial's runner, with
the recall tool on or off per arm.

Arms are <mode>:<budget>, mode one of summary, summary+recall, roll, roll+recall.
Sessions run --unattended, so no prompt waits on the runner: the screen-matched
answers the roll trial typed are gone, because a declined prompt still prints its
question and an answer typed at it would arrive as a user turn.

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
ap.add_argument("--model", default="inclusionai/ling-3.1-flash")
# A provider to pin the model to, with no fallback: a discounted price is one
# provider's, and a fallback would change both the price and the model's host.
ap.add_argument("--provider", default="")
# A self-hosted server: the adapter, the variable holding its key, the variable
# holding its URL (passed to the proxy by name, never by value), its context,
# and extra request fields as JSON.
ap.add_argument("--adapter", default="openrouter")
ap.add_argument("--key-env", default="OPENROUTER_API_KEY")
ap.add_argument("--upstream-env", default="")
ap.add_argument("--context", type=int, default=262144)
ap.add_argument("--extra", default="")
ap.add_argument("--turn-timeout", type=int, default=3600); ap.add_argument("--bindir", required=True); ap.add_argument("--max-turns", type=int, default=99)
a = ap.parse_args()
PIN = (f', extra_params = {{"provider": {{"order": ["{a.provider}"], "allow_fallbacks": False}}}}'
       if a.provider else "")
if a.extra:
    PIN = ", extra_params = " + a.extra.replace("true", "True").replace("false", "False")
BASE = "/api/v1" if a.adapter == "openrouter" else "/v1"

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

MODE_ARGS = {
    "summary": ["--compaction", "summary"],
    "summary+recall": ["--compaction", "summary", "--recall"],
    "roll": ["--compaction", "roll"],
    "roll+recall": ["--compaction", "roll", "--recall"],
    "summary+recall+note": ["--compaction", "summary", "--recall", "--recall-note"],
}
# Turns where the model is asked to work something out and say it: the longest of
# the five files and its line count. Old: turns 3 and 4; middle: turns 11 and 12.
DERIVED = [3, 4, 11, 12]
DERIVED_ASK = (" Also say which of the five files is the longest and how many lines it has; "
               "I will ask about it later.")

def turn_messages():
    msgs = []
    for t in range(16):
        f = FILES[t * 5:(t + 1) * 5]
        note = f"Note for later: {FACTS[t][0]}. " if t in FACTS else ""
        ask = DERIVED_ASK if t in DERIVED else ""
        msgs.append(f"{note}Read std/{f[0]}, std/{f[1]}, std/{f[2]}, std/{f[3]} and std/{f[4]}, "
                    "one file per step, whole. Then say in two or three sentences what they have in common."
                    f"{ask} Do not edit anything.")
    for q in QUESTIONS:
        msgs.append("Two questions about things I told you earlier in this session. Answer from what you "
                    "know of this conversation; do not read the files again or run commands. If you do not know, say "
                    "so. Put the answers on their own lines as ANSWER1: and ANSWER2:.\n"
                    f"1. {q[0][0]}\n2. {q[1][0]}")
    for pair in ((3, 11), (4, 12)):
        qs = [f"In the turn where you read std/{FILES[t * 5]} and four other files, which of the five did you "
              "say was the longest, and how many lines did it have?" for t in pair]
        msgs.append("Two questions about work you did earlier in this session. Answer from what you know of "
                    "this conversation; do not read the files again or run commands. If you do not know, say so. Put "
                    "each answer on its own line as ANSWER1: and ANSWER2:, giving the file and the line count.\n"
                    f"1. {qs[0]}\n2. {qs[1]}")
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
    jid, budget, port, mode = job["id"], job["budget"], job["port"], job["mode"]
    base = os.path.join(a.outdir, jid); shutil.rmtree(base, ignore_errors=True); os.makedirs(base)
    proj = os.path.join(base, "proj"); fixture(proj)
    cfg = os.path.join(base, "config.star")
    open(cfg, "w").write(
        f'orr = provider("{a.adapter}", base_url = "http://127.0.0.1:{port}{BASE}", api_key = env("{a.key_env}"))\n'
        'default = "mimo"\n'
        f'models = {{"mimo": model(orr, "{a.model}", context = {a.context}, reasoning = "low", cache = True{PIN})}}\n')
    penv = dict(os.environ, PROXY_UPSTREAM=a.upstream_env) if a.upstream_env else None
    proxy = subprocess.Popen([sys.executable, "-I", os.path.join(S, "orproxy", "proxy.py"), str(port),
                              os.path.join(base, "proxy.jsonl")], stdout=subprocess.DEVNULL, env=penv,
                             stderr=open(os.path.join(base, "proxy.err"), "w"))
    time.sleep(1)
    env = dict(os.environ, XDG_STATE_HOME=os.path.join(base, "state"), TERM="xterm",
               STRUMENT_TRIAL_HISTORY_BUDGET=str(budget))
    raw = open(os.path.join(base, "transcript.raw"), "wb")
    child = pexpect.spawn(os.path.join(a.bindir, "strument-recall"), ["--config", cfg, "chat", "--yes", "steps", "--unattended"] + MODE_ARGS[mode],
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
            # --unattended answers both of these itself; they are only counted.
            # Typing an answer here, as the roll trial's runner did, would now
            # arrive as a user turn.
            declined_seen[0] = max(declined_seen[0], clean.count(b"this session runs unattended"))
            answered[0] = max(answered[0], clean.count(b"The model was repeating itself"))
            if until is not None and until.search(clean):
                return "ok"
            # A refused request ends the turn without a usage line; waiting
            # for one cost the main batch's first run an hour of timeouts
            # after the key's credit ran out.
            # A free endpoint's 429s print as "rate_limit: HTTP 429" once
            # retries run out; the turn is over then, not merely slow.
            if re.search(rb"(request|rate_limit): HTTP \d{3}[^\n]*\n(?:(?!Retrying)[^\n]*\n)*[^\n]*> ?$", clean):
                return "http-error"
        return "timeout"
    turns = []
    loops = [0]
    answered = [0]
    declined = [0]
    declined_seen = [0]
    status = pump(re.compile(rb"(^|\n)[ \x08]*> ?$"), 30)
    for i, msg in enumerate(turn_messages()[:a.max_turns]):
        buf = b""; answered[0] = 0; declined_seen[0] = 0; t0 = time.time()
        child.send(msg.replace("\n", " ") + "\r")
        st = pump(re.compile(rb"Tokens: [^\n]*\n"), a.turn_timeout)
        if st == "ok":
            st = pump(re.compile(rb"(^|\n)[ \x08]*> ?$"), 120)
        declined[0] += declined_seen[0]; loops[0] += answered[0]
        turns.append({"turn": i, "status": st, "secs": round(time.time() - t0, 1)})
        if st != "ok":
            break
    try:
        child.send("/exit\r"); pump(None, 3); child.close(force=True)
    except Exception:
        pass
    raw.close(); proxy.terminate(); proxy.wait()
    rec = dict(job, loops=loops[0], declined=declined[0], turns=turns, done=len(turns) == len(turn_messages()) and all(t["status"] == "ok" for t in turns))
    with open(os.path.join(a.outdir, "runs.jsonl"), "a") as f:
        f.write(json.dumps(rec) + "\n")
    return rec

os.makedirs(a.outdir, exist_ok=True)
jobs = [{"mode": arm.split(":")[0], "budget": int(arm.split(":")[1])} for arm in a.arms.split(",") for _ in range(a.reps)]
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
