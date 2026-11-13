#!/usr/bin/env python3
"""Run the Featherweight-vs-Strument trial's sessions.

    runner.py --bindir DIR --outdir DIR [--reps N] [--only ID,...] [--jobs 6]

Arms:
  S      Strument as it is (BINDIR/strument).
  S-fw   Strument with Featherweight's system prompt, through prompt_code,
         its tool names translated to Strument's.
  FW     Featherweight (featherweight-trial.tcl, the trial fork), run as
         `debug turn` inside script/strument-sandbox.sh (Landlock), since it
         has no sandbox of its own.

Each session is one turn in a fresh git copy of the task's fixture, through
its own strumentrec, so every request and response of every arm is on the
wire. Both harnesses pin the provider: Strument through extra_params,
Featherweight through the fork's provider_only. After the session the hidden
tests (fix tasks), the fixture's own tests, and the files changed since the
fixture's commit are recorded. A session with done.json is skipped, so
rerunning resumes. The key is read from OPENROUTER_API_KEY; strumentrec
strips it from what it records.
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
REPO = os.path.abspath(os.path.join(HERE, "..", "..", "..", ".."))
sys.path.insert(0, HERE)
import tasks as T  # noqa: E402

ap = argparse.ArgumentParser()
ap.add_argument("--bindir", required=True, help="holds strument and strumentrec")
ap.add_argument("--outdir", required=True)
ap.add_argument("--reps", type=int, default=20)
ap.add_argument("--seed", type=int, default=20261113)
ap.add_argument("--jobs", type=int, default=6)
ap.add_argument("--only", default="")
ap.add_argument("--arms", default="S,S-fw,FW")
ap.add_argument("--models", default="mimo,deepseek")
ap.add_argument("--tasks", default=",".join(T.TASKS))
ap.add_argument("--timeout", type=int, default=1200)
ap.add_argument("--port-base", type=int, default=21000)
ap.add_argument("--stub", action="store_true")
a = ap.parse_args()

MODELS = {"mimo": ("xiaomi/mimo-v2.6-flash", "xiaomi"), "deepseek": ("deepseek/deepseek-v4.1-flash", "wafer")}
FW_SCRIPT = os.path.join(HERE, "featherweight-trial.tcl")
SANDBOX = os.path.join(REPO, "script", "strument-sandbox.sh")

# Featherweight's prompt (featherweight.tcl at 915b3b3), with its tool names
# translated: read_file, write_file and edit_file are Strument's read, write
# and edit; its run_code runs Tcl with exec, whose counterpart here is bash.
FW_PROMPT_FOR_STRUMENT = """You are an expert software developer working with a user on their code.
The user will ask for changes to the code, or ask questions about it.
If a request is ambiguous, ask clarifying questions before making changes.

You have tools for reading and editing files, running shell commands, and learning about this process.
Prefer read, write, and edit for file operations.
Use bash for other operations in the user's environment.
"""


def jobs():
    js = [{"id": f"{arm}_{task}_{model}_{rep:02d}", "arm": arm, "task": task, "model": model}
          for arm in a.arms.split(",") for task in a.tasks.split(",")
          for model in a.models.split(",") for rep in range(a.reps)]
    random.Random(a.seed).shuffle(js)
    for i, j in enumerate(js):
        j["order"] = i
    return js


def free_port(start):
    for p in range(start, start + 300):
        with socket.socket() as s:
            try:
                s.bind(("127.0.0.1", p))
                return p
            except OSError:
                continue
    raise RuntimeError("no free port")


def git(proj, *args):
    return subprocess.run(["git", *args], cwd=proj, capture_output=True, text=True).stdout


def strument_config(path, port, model, arm):
    slug, prov = MODELS[model]
    with open(path, "w") as f:
        f.write(f'orr = provider("openrouter", base_url = "http://127.0.0.1:{port}/api/v1", '
                'api_key = env("OPENROUTER_API_KEY"))\n'
                f'default = "{model}"\nretry_timeout = 600\n'
                f'models = {{"{model}": model(orr, "{slug}", context = 131072, reasoning = "low", cache = True, '
                f'extra_params = {{"provider": {{"only": ["{prov}"], "allow_fallbacks": False}}}})}}\n')
        if arm == "S-fw":
            f.write('prompt_code = """' + FW_PROMPT_FOR_STRUMENT + '"""\n')


def run(job):
    base = os.path.join(a.outdir, job["id"])
    if os.path.exists(os.path.join(base, "done.json")):
        return job["id"], "skipped"
    shutil.rmtree(base, ignore_errors=True)
    os.makedirs(base)
    proj = os.path.join(base, "proj")
    T.build(job["task"], proj)
    base_commit = git(proj, "rev-parse", "HEAD").strip()
    state = os.path.join(base, "state")
    os.makedirs(state)
    port = free_port(a.port_base + job["order"] * 3)
    slug, prov = MODELS[job["model"]]
    text = T.TASKS[job["task"]]["text"]
    env = dict(os.environ, XDG_STATE_HOME=state, NO_COLOR="1")
    if a.stub:
        cmd = ["true"]
    elif job["arm"] == "FW":
        cmd = [SANDBOX, "-p", proj, "--", "tclsh", FW_SCRIPT, "debug", "turn", "--message", text,
               "--model", slug, "--endpoint", f"http://127.0.0.1:{port}/api/v1/chat/completions",
               "--provider", prov]
    else:
        cfg = os.path.join(base, "config.star")
        strument_config(cfg, port, job["model"], job["arm"])
        cmd = [os.path.join(a.bindir, "strument"), "--config", cfg, "chat",
               "--yes", "bash,steps", "--unattended", "-m", text]
    rec = None
    if not a.stub:
        rec = subprocess.Popen([os.path.join(a.bindir, "strumentrec"), "-out", os.path.join(base, "wire.jsonl"),
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
        if rec:
            rec.terminate()
            rec.wait()
    seconds = round(time.time() - start, 1)
    with open(os.path.join(base, "stdout.txt"), "wb") as f:
        f.write(out)
    with open(os.path.join(base, "stderr.txt"), "wb") as f:
        f.write(err)
    for lg in glob.glob(os.path.join(state, "**", "*.jsonl"), recursive=True):
        shutil.copy(lg, os.path.join(base, "session-" + os.path.basename(lg)))
    fwstate = os.path.join(proj, "featherweight")
    if os.path.isdir(fwstate):
        shutil.copytree(fwstate, os.path.join(base, "fw-state"))
    # The project's final state, however the harness left it.
    result = {}
    if T.TASKS[job["task"]]["kind"] == "fix":
        result["hidden"] = T.run_hidden(job["task"], proj)
    result["own_tests_pass"] = T.own_tests_pass(proj)
    result["changed"] = [l for l in git(proj, "diff", "--name-only", base_commit).splitlines() if l]
    result["untracked"] = [l for l in git(proj, "ls-files", "--others", "--exclude-standard").splitlines() if l]
    result["commits"] = int(git(proj, "rev-list", "--count", f"{base_commit}..HEAD").strip() or 0)
    with open(os.path.join(base, "done.json"), "w") as f:
        json.dump(dict(job, exit=code, status=status, seconds=seconds, **result), f)
    return job["id"], f"{status} exit={code} {seconds:.0f}s"


def main():
    if not a.stub and not os.environ.get("OPENROUTER_API_KEY"):
        sys.exit("OPENROUTER_API_KEY is not set")
    js = jobs()
    if a.only:
        want = set(a.only.split(","))
        js = [j for j in js if j["id"] in want]
    os.makedirs(a.outdir, exist_ok=True)
    print(f"{len(js)} sessions", flush=True)
    with cf.ThreadPoolExecutor(a.jobs) as ex:
        futs = {ex.submit(run, j): j for j in js}
        for fut in cf.as_completed(futs):
            try:
                jid, what = fut.result()
                print(jid, what, flush=True)
            except Exception as e:
                print(futs[fut]["id"], "FAILED", repr(e), flush=True)
    print("finished", flush=True)


if __name__ == "__main__":
    main()
