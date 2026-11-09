#!/usr/bin/env python3
"""Run the concision-block trial's sessions.

    runner.py --bindir DIR --outdir DIR [--reps N] [--only ID,...] [--jobs 8]

Adapted from ../../2026-11-bash-bullet/data/runner.py: the same single-turn
`strument -m` session in a fresh copy of a fixture, through its own
strumentrec, with arms x fixtures x tasks x models x reps. BINDIR holds
strument-<ARM> for each arm (build_arms.py) and strumentrec. A session directory with done.json is skipped, so rerunning the
same command resumes. The key is read from OPENROUTER_API_KEY and never
written anywhere: the config names the variable, and strumentrec strips the
header from what it records.
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
import importlib.util  # noqa: E402


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


FIXTURES = {
    "tally": _load("fixture1", os.path.join(HERE, "..", "..", "2026-11-bash-bullet", "data", "fixture.py")),
    "pricetag": _load("fixture2", os.path.join(HERE, "fixture2.py")),
}

ap = argparse.ArgumentParser()
ap.add_argument("--bindir", required=True)
ap.add_argument("--outdir", required=True)
ap.add_argument("--reps", type=int, default=10)
ap.add_argument("--seed", type=int, default=20261110)
ap.add_argument("--jobs", type=int, default=8)
ap.add_argument("--only", default="", help="comma-separated session ids to run, from the full shuffled list")
ap.add_argument("--timeout", type=int, default=1200)
ap.add_argument("--port-base", type=int, default=20000)
ap.add_argument("--arms", default="A,C,C-length,C-direct,P")
ap.add_argument("--models", default="mimo,deepseek,haiku")
ap.add_argument("--pin", default="",
                help="model=provider,... : route each through that provider only, no fallbacks")
ap.add_argument("--stub", action="store_true", help="run `true` instead of strument, to exercise resume")
a = ap.parse_args()

ALL_MODELS = {
    "mimo": "xiaomi/mimo-v2.6-flash",
    "deepseek": "deepseek/deepseek-v4.1-flash",
    "haiku": "anthropic/claude-haiku-5.5",
    "sonnet": "anthropic/claude-sonnet-5.5",
}
MODELS = {m: ALL_MODELS[m] for m in a.models.split(",")}
PIN = dict(kv.split("=", 1) for kv in a.pin.split(",") if kv)
TASKS = ("run", "read")
ARMS = a.arms.split(",")


def task_text(fx, task):
    f = FIXTURES[fx]
    return f.RUN_TASK if task == "run" else f.READ_TASK


def jobs():
    js = [{"id": f"{arm}_{fx}_{task}_{model}_{rep:02d}", "arm": arm, "fixture": fx, "task": task, "model": model}
          for arm in ARMS for fx in FIXTURES for task in TASKS for model in MODELS for rep in range(a.reps)]
    random.Random(a.seed).shuffle(js)
    for i, j in enumerate(js):
        j["order"] = i
    return js


def free_port(start):
    for p in range(start, start + 200):
        with socket.socket() as s:
            try:
                s.bind(("127.0.0.1", p))
                return p
            except OSError:
                continue
    raise RuntimeError("no free port")


def run(job, slot):
    base = os.path.join(a.outdir, job["id"])
    if os.path.exists(os.path.join(base, "done.json")):
        return job["id"], "skipped"
    shutil.rmtree(base, ignore_errors=True)
    os.makedirs(base)
    proj = os.path.join(base, "proj")
    FIXTURES[job["fixture"]].build(proj)
    state = os.path.join(base, "state")
    os.makedirs(state)
    port = free_port(a.port_base + job["order"] * 3)
    cfg = os.path.join(base, "config.star")
    with open(cfg, "w") as f:
        f.write(
            f'orr = provider("openrouter", base_url = "http://127.0.0.1:{port}/api/v1", '
            'api_key = env("OPENROUTER_API_KEY"))\n'
            f'default = "{job["model"]}"\n'
            "retry_timeout = 600\n"
            f'models = {{"{job["model"]}": model(orr, "{MODELS[job["model"]]}", context = 131072, '
            'reasoning = "low", cache = True'
            + (f', extra_params = {{"provider": {{"only": ["{PIN[job["model"]]}"], "allow_fallbacks": False}}}}'
               if job["model"] in PIN else "")
            + ')}\n')
    start = time.time()
    rec = None
    if a.stub:
        cmd = ["true"]
    else:
        rec = subprocess.Popen([os.path.join(a.bindir, "strumentrec"), "-out", os.path.join(base, "wire.jsonl"),
                                "-upstream", "https://openrouter.ai", "-listen", f"127.0.0.1:{port}"],
                               stdout=subprocess.DEVNULL, stderr=open(os.path.join(base, "rec.err"), "w"))
        time.sleep(0.5)
        cmd = [os.path.join(a.bindir, "strument-" + job["arm"]), "--config", cfg, "chat",
               "--yes", "bash,steps", "--unattended", "-m", task_text(job["fixture"], job["task"])]
    env = dict(os.environ, XDG_STATE_HOME=state, NO_COLOR="1")
    status = "ok"
    try:
        p = subprocess.run(cmd, cwd=proj, env=env, stdin=subprocess.DEVNULL, capture_output=True,
                           timeout=a.timeout)
        code = p.returncode
        out, err = p.stdout, p.stderr
    except subprocess.TimeoutExpired as e:
        code, out, err, status = None, e.stdout or b"", e.stderr or b"", "timeout"
    finally:
        if rec:
            rec.terminate()
            rec.wait()
    with open(os.path.join(base, "stdout.txt"), "wb") as f:
        f.write(out)
    with open(os.path.join(base, "stderr.txt"), "wb") as f:
        f.write(err)
    logs = glob.glob(os.path.join(state, "**", "*.jsonl"), recursive=True)
    for lg in logs:
        shutil.copy(lg, os.path.join(base, "session-" + os.path.basename(lg)))
    with open(os.path.join(base, "done.json"), "w") as f:
        json.dump(dict(job, exit=code, status=status, seconds=round(time.time() - start, 1),
                       logs=[os.path.relpath(lg, base) for lg in logs]), f)
    return job["id"], f"{status} exit={code} {time.time() - start:.0f}s"


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
        futs = {}
        for i, j in enumerate(js):
            futs[ex.submit(run, j, i % a.jobs)] = j
        for fut in cf.as_completed(futs):
            try:
                jid, what = fut.result()
                print(jid, what, flush=True)
            except Exception as e:  # recorded, not swallowed: the session gets no done.json
                print(futs[fut]["id"], "FAILED", repr(e), flush=True)
    print("finished", flush=True)


if __name__ == "__main__":
    main()
