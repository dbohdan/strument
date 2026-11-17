#!/usr/bin/env python3
"""Run the pared-down trial's sessions.

    runner.py --old DIR --new DIR --outdir DIR [--scale 1.0] [--only ID,...] [--jobs 8]

Arms:
  S   Strument at a42af21, the binary of the Featherweight trial: before the
      paring down.
  N   Strument after it (the commit is in the write-up).

Each DIR holds that arm's strument; --new's also holds the strumentrec both
arms record through. Each session is one turn in a fresh git copy of the
task's fixture, through its own strumentrec, so every request and response is
on the wire. The provider is pinned through extra_params. Sessions run in a
seeded shuffled order. After a session the project's final state is scored by
tasks.outcome. A session with done.json is skipped, so rerunning resumes. The
key is read from OPENROUTER_API_KEY; strumentrec strips it from what it
records.
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

ap = argparse.ArgumentParser()
ap.add_argument("--old", required=True)
ap.add_argument("--new", required=True)
ap.add_argument("--outdir", required=True)
ap.add_argument("--scale", type=float, default=1.0, help="multiply each task's repetitions (a pilot uses less)")
ap.add_argument("--seed", type=int, default=20261114)
ap.add_argument("--jobs", type=int, default=8)
ap.add_argument("--only", default="")
ap.add_argument("--arms", default="S,N")
ap.add_argument("--models", default="mimo,deepseek")
ap.add_argument("--tasks", default=",".join(T.TASKS))
ap.add_argument("--timeout", type=int, default=1200)
ap.add_argument("--port-base", type=int, default=23000)
a = ap.parse_args()

MODELS = {"mimo": ("xiaomi/mimo-v2.6-flash", "xiaomi"), "deepseek": ("deepseek/deepseek-v4.1-flash", "wafer")}
BIN = {"S": a.old, "N": a.new}


def jobs():
    js = []
    for task in a.tasks.split(","):
        reps = max(1, round(T.TASKS[task]["reps"] * a.scale))
        js += [{"id": f"{arm}_{task}_{model}_{rep:02d}", "arm": arm, "task": task, "model": model}
               for arm in a.arms.split(",") for model in a.models.split(",") for rep in range(reps)]
    random.Random(a.seed).shuffle(js)
    for i, j in enumerate(js):
        j["order"] = i
    return js


def free_port(start):
    for p in range(start, start + 400):
        with socket.socket() as s:
            try:
                s.bind(("127.0.0.1", p))
                return p
            except OSError:
                continue
    raise RuntimeError("no free port")


def config(path, port, model):
    slug, prov = MODELS[model]
    with open(path, "w") as f:
        f.write(f'orr = provider("openrouter", base_url = "http://127.0.0.1:{port}/api/v1", '
                'api_key = env("OPENROUTER_API_KEY"))\n'
                f'default = "{model}"\nretry_timeout = 600\n'
                f'models = {{"{model}": model(orr, "{slug}", context = 131072, reasoning = "low", cache = True, '
                f'extra_params = {{"provider": {{"only": ["{prov}"], "allow_fallbacks": False}}}})}}\n')


def run(job):
    base = os.path.join(a.outdir, job["id"])
    if os.path.exists(os.path.join(base, "done.json")):
        return job["id"], "skipped"
    shutil.rmtree(base, ignore_errors=True)
    os.makedirs(base)
    proj = os.path.join(base, "proj")
    t = T.TASKS[job["task"]]
    t["build"](proj)
    base_commit = T.git(proj, "rev-parse", "HEAD").strip()
    state = os.path.join(base, "state")
    os.makedirs(state)
    port = free_port(a.port_base + (job["order"] % 1000) * 3)
    cfg = os.path.join(base, "config.star")
    config(cfg, port, job["model"])
    yes = "steps" if t.get("no_bash") else "bash,steps"
    cmd = [os.path.join(BIN[job["arm"]], "strument"), "--config", cfg, "chat",
           "--yes", yes, "--unattended", "-m", t["text"]]
    env = dict(os.environ, XDG_STATE_HOME=state, NO_COLOR="1")
    rec = subprocess.Popen([os.path.join(a.new, "strumentrec"), "-out", os.path.join(base, "wire.jsonl"),
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
    changed = [l for l in T.git(proj, "diff", "--name-only", base_commit).splitlines() if l]
    result = {
        "changed": changed,
        "untracked": [l for l in T.git(proj, "ls-files", "--others", "--exclude-standard").splitlines() if l],
        "commits": int(T.git(proj, "rev-list", "--count", f"{base_commit}..HEAD").strip() or 0),
        "dirty_after": [l for l in T.git(proj, "status", "--porcelain").splitlines() if l],
    }
    result.update(T.outcome(job["task"], proj, base_commit, changed))
    with open(os.path.join(base, "done.json"), "w") as f:
        json.dump(dict(job, exit=code, status=status, seconds=seconds, **result), f)
    return job["id"], f"{status} exit={code} {seconds:.0f}s"


def main():
    if not os.environ.get("OPENROUTER_API_KEY"):
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
