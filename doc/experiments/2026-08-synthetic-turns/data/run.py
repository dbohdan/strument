"""Runner for the synthetic-turn non-inferiority screen.

Job order is shuffled across the WHOLE list with a recorded seed. In the
previous experiment that mattered more than tripling the sample: running every
baseline then every treatment confounds the arm with the wall-clock window, and
providers drift across such a window.
"""

import argparse
import json
import os
import pathlib
import random
import re
import shutil
import subprocess
import sys
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import tasks  # noqa: E402

EXP = pathlib.Path(__file__).parent
MODELS = {
    "mimo": "xiaomi/mimo-v2.5",
    "luna": "openai/gpt-5.6-luna",
    "v4flash": "deepseek/deepseek-v4-flash-0731",
}
ARMS = {"A": EXP / "bin/strument-A", "B": EXP / "bin/strument-B"}

CONFIG = """\
router = provider(adapter = "openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}")}}
default = "m"
"""

# The harness's own outcome lines. "Read <path> (...)" and
# "Searched for <pat> ..." are how a look at a file is reported.
READ_RE = re.compile(r"^Read (\S+) ", re.M)
TOKENS_RE = re.compile(r"Tokens: ([\d.]+)k? sent", re.M)
COST_RE = re.compile(r"Cost: \$([\d.]+) turn", re.M)
STEPS_RE = re.compile(r"(\d+) steps?", re.M)


def run_one(job):
    arm, model_key, task_name, rep, seed = job
    rng = random.Random(seed)
    files, chat, prompt, score, names = tasks.TASKS[task_name](rng)

    work = tempfile.mkdtemp(prefix=f"exp-{task_name}-")
    try:
        root = pathlib.Path(work) / "proj"
        root.mkdir()
        for rel, content in files.items():
            (root / rel).write_text(content)

        cfgdir = pathlib.Path(work) / "cfg" / "strument"
        cfgdir.mkdir(parents=True)
        (cfgdir / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))

        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = str(pathlib.Path(work) / "cfg")
        env["XDG_STATE_HOME"] = str(pathlib.Path(work) / "state")

        t0 = time.time()
        try:
            proc = subprocess.run(
                [str(ARMS[arm]), "--no-git", "-m", prompt, *chat],
                cwd=root, env=env, capture_output=True, text=True, timeout=600,
            )
            out = proc.stdout + proc.stderr
            rc = proc.returncode
        except subprocess.TimeoutExpired:
            out, rc = "TIMEOUT", -9
        elapsed = time.time() - t0

        final = {}
        for rel in files:
            p = root / rel
            final[rel] = p.read_text() if p.exists() else ""

        # Counter-metric: reads of files ALREADY in the chat. If the file block
        # stops reading as a turn addressed to the model, it may re-fetch what
        # it already has. This is what the treatment is most likely to break.
        chat_set = set(chat)
        redundant = sum(1 for m in READ_RE.finditer(out) if m.group(1) in chat_set)

        tok = TOKENS_RE.search(out)
        cost = COST_RE.search(out)
        steps = STEPS_RE.search(out)

        try:
            passed = bool(score(final))
        except Exception:
            passed = False

        return {
            "arm": arm, "model": model_key, "task": task_name, "rep": rep,
            "seed": seed, "passed": passed, "redundant_reads": redundant,
            "returncode": rc, "elapsed": round(elapsed, 1),
            "tokens_sent_k": float(tok.group(1)) if tok else None,
            "cost": float(cost.group(1)) if cost else None,
            "steps": int(steps.group(1)) if steps else None,
            "empty_response": "Empty response received" in out,
            "names": names,
            "final_files": final,
            "stdout": out[-4000:],
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--reps", type=int, default=25)
    ap.add_argument("--seed", type=int, default=20260813)
    ap.add_argument("--workers", type=int, default=6)
    ap.add_argument("--out", default=str(EXP / "results.jsonl"))
    ap.add_argument("--tasks", default=",".join(tasks.TASKS))
    ap.add_argument("--models", default=",".join(MODELS))
    a = ap.parse_args()

    jobs = [
        (arm, mk, tn, rep, a.seed * 1000003 + i)
        for i, (arm, mk, tn, rep) in enumerate(
            (arm, mk, tn, rep)
            for arm in ARMS
            for mk in a.models.split(",")
            for tn in a.tasks.split(",")
            for rep in range(a.reps)
        )
    ]
    random.Random(a.seed).shuffle(jobs)
    print(f"{len(jobs)} jobs, seed {a.seed}, {a.workers} workers", flush=True)

    done = 0
    with open(a.out, "w") as fh, ThreadPoolExecutor(a.workers) as pool:
        futs = [pool.submit(run_one, j) for j in jobs]
        for fut in as_completed(futs):
            res = fut.result()
            fh.write(json.dumps(res) + "\n")
            fh.flush()
            done += 1
            if done % 25 == 0 or done == len(jobs):
                print(f"  {done}/{len(jobs)}", flush=True)


if __name__ == "__main__":
    main()
