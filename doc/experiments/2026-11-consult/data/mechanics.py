#!/usr/bin/env python3
"""Layer 3: does MiMo call consult, and send the files the question needs?

    mechanics.py --bin DIR --outdir DIR [--reps 2] [--only TASK,...] [--jobs 8]

One session per task and rep: Strument from DIR, MiMo asking, with Terra in
consult_models under one of two purposes (PURPOSES) and the consult grant
given up front. The user's message is
the task's instruction and nothing more, so whether to consult, and with
which files, is MiMo's decision. Each session is one turn in a fresh git copy
of the task's project, through its own strumentrec, so every request and
response is on the wire. Sessions run in a seeded shuffled order; one with
done.json is skipped, so rerunning resumes. The key is read from
OPENROUTER_API_KEY; strumentrec strips it from what it records.
"""

import argparse
import concurrent.futures as cf
import glob
import json
import os
import random
import shutil
import socket
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tasks as T  # noqa: E402

# Two wordings of the purpose. "plain" is the example in doc/config.md: it
# says what Terra is for. "better" also says why to hand the work over, as a
# user who configured it for that reason would. In the pilot, MiMo given the
# plain wording did both tasks itself.
PURPOSES = {
    "plain": "documentation and user-facing prose",
    # The plain wording, with the user asking for the consult: the tool's own
    # description allows "when the user asks you to". MiMo consulted in none of
    # 19 pilot sessions under the other two, so this is the arm that measures
    # which files it sends.
    "directed": "documentation and user-facing prose",
    "better": "documentation and user-facing prose: it writes them better than you do, so have it draft or "
              "review any you write",
}

ASK = "Ask terra for a draft first."


def git(d, *args):
    return subprocess.run(["git", *args], cwd=d, capture_output=True, text=True, check=True).stdout


def build_repo(project, d):
    T.build(project, d)
    git(d, "init", "-q", "-b", "main")
    git(d, "config", "user.name", "Trial User")
    git(d, "config", "user.email", "user@example.com")
    git(d, "add", "-A")
    git(d, "commit", "-q", "-m", "Initial commit")


def config(path, port, purpose):
    with open(path, "w") as f:
        f.write(f'''orr = provider("openrouter", base_url = "http://127.0.0.1:{port}/api/v1", api_key = env("OPENROUTER_API_KEY"))
default = "mimo"
retry_timeout = 600
models = {{
    "mimo": model(orr, "xiaomi/mimo-v2.6-flash", context = 131072, reasoning = "low", cache = True,
                  extra_params = {{"provider": {{"only": ["xiaomi"], "allow_fallbacks": False}}}}),
    "terra": model(orr, "openai/gpt-6.1-terra", context = 272000, reasoning = "low",
                   extra_params = {{"service_tier": "flex", "provider": {{"only": ["openai"], "allow_fallbacks": False}}}}),
}}
consult_models = {{"terra": "{PURPOSES[purpose]}"}}
''')


def free_port(start):
    for p in range(start, start + 400):
        with socket.socket() as s:
            try:
                s.bind(("127.0.0.1", p))
                return p
            except OSError:
                continue
    raise RuntimeError("no free port")


def run(a, job):
    base = os.path.join(a.outdir, job["id"])
    if os.path.exists(os.path.join(base, "done.json")):
        return job["id"], "skipped"
    shutil.rmtree(base, ignore_errors=True)
    os.makedirs(base)
    t = T.TASKS[job["task"]]
    proj = os.path.join(base, "proj")
    build_repo(t["project"], proj)
    base_commit = git(proj, "rev-parse", "HEAD").strip()
    state = os.path.join(base, "state")
    os.makedirs(state)
    port = free_port(a.port_base + (job["order"] % 1000) * 3)
    cfg = os.path.join(base, "config.star")
    config(cfg, port, job["purpose"])
    cmd = [os.path.join(a.bin, "strument"), "--config", cfg, "chat", "--yes", "consult,steps", "--unattended",
           "-m", t["instruction"] + (" " + ASK if job["purpose"] == "directed" else "")]
    env = dict(os.environ, XDG_STATE_HOME=state, NO_COLOR="1")
    rec = subprocess.Popen([os.path.join(a.bin, "strumentrec"), "-out", os.path.join(base, "wire.jsonl"),
                            "-upstream", "https://openrouter.ai", "-listen", f"127.0.0.1:{port}"],
                           stdout=subprocess.DEVNULL, stderr=open(os.path.join(base, "rec.err"), "w"))
    time.sleep(0.5)
    start = time.time()
    status = "ok"
    try:
        p = subprocess.run(cmd, cwd=proj, env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=a.timeout)
        code, out, err = p.returncode, p.stdout, p.stderr
    except subprocess.TimeoutExpired as e:
        code, out, err, status = None, e.stdout or b"", e.stderr or b"", "timeout"
    finally:
        rec.terminate()
        rec.wait()
    seconds = round(time.time() - start, 1)
    with open(os.path.join(base, "stdout.txt"), "wb") as f:
        f.write(out)
    with open(os.path.join(base, "stderr.txt"), "wb") as f:
        f.write(err)
    for lg in glob.glob(os.path.join(state, "**", "*.jsonl"), recursive=True):
        if "/log/" in lg:
            shutil.copy(lg, os.path.join(base, "session-" + os.path.basename(lg)))
    target = os.path.join(proj, t["target"])
    final = open(target).read() if os.path.exists(target) else None
    changed = [l for l in git(proj, "diff", "--name-only", base_commit).splitlines() if l]
    untracked = [l for l in git(proj, "ls-files", "--others", "--exclude-standard").splitlines() if l]
    with open(os.path.join(base, "done.json"), "w") as f:
        json.dump(dict(job, exit=code, status=status, seconds=seconds, changed=changed, untracked=untracked,
                       target_text=final), f)
    return job["id"], f"{status} exit={code} {seconds:.0f}s"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bin", required=True, help="directory holding strument and strumentrec")
    ap.add_argument("--outdir", required=True)
    ap.add_argument("--reps", type=int, default=2)
    ap.add_argument("--only", default="", help="tasks")
    ap.add_argument("--purposes", default=",".join(PURPOSES))
    ap.add_argument("--jobs", type=int, default=8)
    ap.add_argument("--seed", type=int, default=20261116)
    ap.add_argument("--timeout", type=int, default=1800)
    ap.add_argument("--port-base", type=int, default=24000)
    a = ap.parse_args()
    if not os.environ.get("OPENROUTER_API_KEY"):
        sys.exit("OPENROUTER_API_KEY is not set")
    only = set(filter(None, a.only.split(",")))
    js = [{"id": f"{p}_{task}_{rep:02d}", "task": task, "purpose": p} for task in T.TASKS
          for p in a.purposes.split(",") for rep in range(1, a.reps + 1) if not only or task in only]
    random.Random(a.seed).shuffle(js)
    for i, j in enumerate(js):
        j["order"] = i
    os.makedirs(a.outdir, exist_ok=True)
    print(f"{len(js)} sessions", flush=True)
    with cf.ThreadPoolExecutor(a.jobs) as ex:
        futs = {ex.submit(run, a, j): j for j in js}
        for fut in cf.as_completed(futs):
            try:
                jid, what = fut.result()
                print(jid, what, flush=True)
            except Exception as e:
                print(futs[fut]["id"], "FAILED", repr(e), flush=True)
    print("finished", flush=True)


if __name__ == "__main__":
    main()
