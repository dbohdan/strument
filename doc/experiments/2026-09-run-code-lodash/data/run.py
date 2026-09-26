"""Run the Lodash trial: three arms, two models, five tasks, shuffled.

Each session gets a fresh fixture and state directory and keeps its session
log and blobs, which is where the programs are (arguments over 1 KiB are
stored as blobs).

Environment:
  OPENROUTER_API_KEY  read by the config through env(), never an argument
  LOD_DIR             scratch directory for sessions
  LOD_BINS            directory holding strument-A, strument-B, strument-C
  LOD_CONFIG          XDG_CONFIG_HOME with a config naming the models below

Usage: python3 run.py [pilot]
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
LOD = os.environ["LOD_DIR"]
BINS = os.environ["LOD_BINS"]
CONFIG = os.environ["LOD_CONFIG"]

ARMS = "ABC"
MODELS = ["mimo", "qwen27"]
SUFFIX = ("\n\nThe data is in data/. Put the answer after a line containing only ANSWER:, "
          "in exactly the format asked for.")
TASKS = {
    "count": "How many harvest records are there for each crop family, across all the monthly "
             "files? Answer with one `family: count` line per family.",
    "groupmax": "For each bed, what is the id of its heaviest harvest over the year? Answer with "
                "one `bed: id` line per bed.",
    "join": "Which plot holders in data/plots.json have no harvest recorded all year, either "
            "because they have no bed or because nothing was harvested from their bed? Answer "
            "with their names, one per line.",
    "distinct": "How many distinct varieties were harvested over the year? Answer with the "
                "number alone.",
    "control": "How many grams did harvest H-0042 weigh? Answer with the number alone.",
}
REPS = 6


def jobs():
    out = [(a, m, t, r) for a in ARMS for m in MODELS for t in TASKS for r in range(REPS)]
    random.Random(20260926).shuffle(out)
    return out


def one(job):
    arm, model, task, rep = job
    tag = f"{model}-{arm}-{task}-{rep}"
    work = os.path.join(LOD, "sessions", tag)
    if os.path.exists(os.path.join(work, "result.json")):
        return tag, "skipped"
    shutil.rmtree(work, ignore_errors=True)
    tree, state = os.path.join(work, "tree"), os.path.join(work, "state")
    os.makedirs(work)
    subprocess.run([sys.executable, os.path.join(HERE, "fixture.py"), tree], check=True)
    env = dict(os.environ, XDG_CONFIG_HOME=CONFIG, XDG_STATE_HOME=state)
    row = {"arm": arm, "model": model, "task": task, "rep": rep, "tag": tag}
    t0 = time.time()
    try:
        p = subprocess.run([os.path.join(BINS, "strument-" + arm), "--no-git", "--yes", "steps",
                            "-M", model, "-m", TASKS[task] + SUFFIX],
                           cwd=tree, env=env, stdin=subprocess.DEVNULL,
                           capture_output=True, text=True, timeout=600)
        row["exit"] = p.returncode
        with open(os.path.join(work, "stdout.txt"), "w") as f:
            f.write(p.stdout + p.stderr)
    except subprocess.TimeoutExpired:
        row["exit"] = "timeout"
    row["seconds"] = round(time.time() - t0, 1)
    shutil.rmtree(os.path.join(tree, "data"), ignore_errors=True)
    with open(os.path.join(work, "result.json"), "w") as f:
        json.dump(row, f)
    return tag, row["exit"]


def main():
    todo = jobs()
    if sys.argv[1:] == ["pilot"]:
        seen, pilot = set(), []
        for j in todo:
            if (j[0], j[1]) not in seen:
                seen.add((j[0], j[1]))
                pilot.append(j)
        todo = pilot
    os.makedirs(os.path.join(LOD, "sessions"), exist_ok=True)
    with open(os.path.join(LOD, "order.json"), "w") as f:
        json.dump(todo, f)
    with cf.ThreadPoolExecutor(max_workers=4) as ex:
        for tag, status in ex.map(one, todo):
            print(tag, status, flush=True)


if __name__ == "__main__":
    main()
