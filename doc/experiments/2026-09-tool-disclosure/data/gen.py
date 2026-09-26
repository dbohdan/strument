"""Record sessions on the Larkspur fixture, to use as replay prefixes.

Each session runs one model on one task in a fresh copy of the fixture,
through its own strumentrec proxy, which writes every request verbatim. Any
recorded request is a complete prefix: system prompt, tools, and history.

Environment:
  OPENROUTER_API_KEY   read by the config through env(), never an argument
  TD_DIR               scratch directory for fixtures, state and captures
  STRUMENT, STRUMENTREC  the two binaries

Usage: python3 gen.py [pilot]
"""

import concurrent.futures as cf
import json
import os
import random
import shutil
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
TD = os.environ["TD_DIR"]
STRUMENT = os.environ["STRUMENT"]
STRUMENTREC = os.environ["STRUMENTREC"]

MODELS = {
    "glm": ('"z-ai/glm-5.3-flash"', 'context = 200000, reasoning = "low"'),
    "mimo": ('"xiaomi/mimo-v2.6-flash"', 'context = 1050000, reasoning = "low"'),
    "qwen27": ('"qwen/qwen3.8-27b"', 'context = 262144, reasoning = "low"'),
    "ling": ('"inclusionai/ling-3.0-flash"', "context = 131072"),
    "luna": ('"openai/gpt-6-luna"', 'context = 272000, reasoning = "high"'),
}
TASKS = {
    "docs": "Read the design notes under docs/ and explain how a bed's crop family is "
            "chosen each season, including resting and pins, and what changed between "
            "version 1 and version 2.",
    "code": "The rotation tests fail. Find out why, fix the code, and run the tests to "
            "confirm they pass.",
}
REPS = 2


def config(port, model):
    slug, opts = MODELS[model]
    return (
        f'rec = provider("openrouter", base_url = "http://127.0.0.1:{port}/api/v1", '
        f'api_key = env("OPENROUTER_API_KEY"), proxy = "direct")\n'
        f'default = "{model}"\n'
        f"models = {{\"{model}\": model(rec, {slug}, {opts})}}\n"
    )


def one(job):
    (model, task, rep), port = job
    tag = f"{model}-{task}-{rep}"
    work = os.path.join(TD, "sessions", tag)
    if os.path.exists(os.path.join(work, "done.json")):
        return tag, "skipped"
    shutil.rmtree(work, ignore_errors=True)
    tree, state, cfg = (os.path.join(work, d) for d in ("tree", "state", "cfg"))
    os.makedirs(os.path.join(cfg, "strument"))
    subprocess.run([sys.executable, os.path.join(HERE, "fixture.py"), tree], check=True)
    with open(os.path.join(cfg, "strument", "config.star"), "w") as f:
        f.write(config(port, model))

    capture = os.path.join(work, "capture.jsonl")
    rec = subprocess.Popen([STRUMENTREC, "-out", capture, "-upstream", "https://openrouter.ai",
                            "-listen", f"127.0.0.1:{port}"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    time.sleep(0.5)
    env = dict(os.environ, XDG_CONFIG_HOME=cfg, XDG_STATE_HOME=state)
    row = {"model": model, "task": task, "rep": rep}
    t0 = time.time()
    try:
        p = subprocess.run([STRUMENT, "--no-git", "--yes", "steps,bash", "-M", model, "-m", TASKS[task]],
                           cwd=tree, env=env, stdin=subprocess.DEVNULL,
                           capture_output=True, text=True, timeout=900)
        row["exit"] = p.returncode
        with open(os.path.join(work, "stdout.txt"), "w") as f:
            f.write(p.stdout + p.stderr)
    except subprocess.TimeoutExpired:
        row["exit"] = "timeout"
    finally:
        rec.terminate()
        rec.wait()
    row["seconds"] = round(time.time() - t0, 1)
    row["requests"] = sum(1 for line in open(capture) if '"raw_request"' in line)
    with open(os.path.join(work, "done.json"), "w") as f:
        json.dump(row, f)
    return tag, row


def main():
    jobs = [(m, t, r) for m in MODELS for t in TASKS for r in range(REPS)]
    random.Random(20260926).shuffle(jobs)
    if sys.argv[1:] == ["pilot"]:
        seen, pilot = set(), []
        for j in jobs:
            if j[0] not in seen:
                seen.add(j[0])
                pilot.append(j)
        jobs = pilot
    numbered = [(j, 8500 + i) for i, j in enumerate(jobs)]
    with cf.ThreadPoolExecutor(max_workers=4) as ex:
        for tag, status in ex.map(one, numbered):
            print(tag, status, flush=True)


if __name__ == "__main__":
    main()
