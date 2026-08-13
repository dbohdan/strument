"""Runner for the A0-vs-A2 run. Arm order shuffled across the whole job list."""

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
import tasks2  # noqa: E402

EXP = pathlib.Path(__file__).parent
MODELS = {
    "mimo": "xiaomi/mimo-v2.5",
    "luna": "openai/gpt-5.6-luna",
    "v4flash": "deepseek/deepseek-v4-flash-0731",
}
ARMS = {v: EXP / f"bin/strument-{v}" for v in ("A0", "A2")}

CONFIG = """\
router = provider(adapter = "openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}")}}
default = "m"
reasoning_display = "full"
"""

READ = re.compile(r"^Read (\S+) ", re.M)
WROTE = re.compile(r"^(?:Applied edit to|Created|Overwrote) (\S+)", re.M)
TOKENS = re.compile(r"Tokens: ([\d.]+)k? sent", re.M)
COST = re.compile(r"Cost: \$([\d.]+) turn", re.M)
STEPS = re.compile(r"(\d+) steps?", re.M)


def run_one(job):
    arm, model_key, task_name, rep, seed = job
    rng = random.Random(seed)
    files, pinned, prompt, score, names, _ = tasks2.TASKS[task_name](rng)

    work = tempfile.mkdtemp(prefix=f"a2-{task_name}-")
    try:
        root = pathlib.Path(work) / "proj"
        root.mkdir()
        for rel, content in files.items():
            (root / rel).write_text(content)
        cfg = pathlib.Path(work) / "cfg" / "strument"
        cfg.mkdir(parents=True)
        (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))

        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = str(pathlib.Path(work) / "cfg")
        env["XDG_STATE_HOME"] = str(pathlib.Path(work) / "state")

        t0 = time.time()
        try:
            proc = subprocess.run(
                [str(ARMS[arm]), "--no-git", "-m", prompt, *pinned],
                cwd=root, env=env, capture_output=True, text=True, timeout=600,
            )
            out, rc = proc.stdout + proc.stderr, proc.returncode
        except subprocess.TimeoutExpired:
            out, rc = "TIMEOUT", -9
        elapsed = time.time() - t0

        final = {rel: (root / rel).read_text() if (root / rel).exists() else ""
                 for rel in files}

        pin = set(pinned)
        reads = [(m.start(), m.group(1)) for m in READ.finditer(out) if m.group(1) in pin]
        # Blind edit: a pinned file written with no read of it earlier in the
        # session. Normal under A0 (the content was supplied); under A2 it means
        # editing from memory, which is the hazard that design introduces.
        blind = 0
        for m in WROTE.finditer(out):
            path = m.group(1)
            if path in pin and not any(p < m.start() and f == path for p, f in reads):
                blind += 1

        try:
            passed = bool(score(final))
        except Exception:
            passed = False

        tok, cost, steps = TOKENS.search(out), COST.search(out), STEPS.search(out)
        return {
            "arm": arm, "model": model_key, "task": task_name, "rep": rep, "seed": seed,
            "passed": passed, "steps": int(steps.group(1)) if steps else None,
            "blind_edits": blind, "pinned_reads": len(reads), "n_pinned": len(pinned),
            "returncode": rc, "elapsed": round(elapsed, 1),
            "tokens_sent_k": float(tok.group(1)) if tok else None,
            "cost": float(cost.group(1)) if cost else None,
            "empty_response": "Empty response received" in out,
            "names": names, "final_files": final, "stdout": out,
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--reps", type=int, default=25)
    ap.add_argument("--seed", type=int, default=20260816)
    ap.add_argument("--workers", type=int, default=8)
    ap.add_argument("--out", default=str(EXP / "a2-results.jsonl"))
    ap.add_argument("--tasks", default=",".join(tasks2.TASKS))
    ap.add_argument("--arms", default=",".join(ARMS))
    a = ap.parse_args()

    # Refuse to run at all if the scorers cannot pass their own controls.
    fails = tasks2.check_controls()
    if fails:
        raise SystemExit(f"scorer controls failed ({len(fails)}): {fails[:3]}")
    print("scorer controls: all pass", flush=True)

    jobs = [
        (arm, mk, tn, rep, a.seed * 1000003 + i)
        for i, (arm, mk, tn, rep) in enumerate(
            (arm, mk, tn, rep)
            for arm in a.arms.split(",")
            for mk in MODELS
            for tn in a.tasks.split(",")
            for rep in range(a.reps)
        )
    ]
    random.Random(a.seed).shuffle(jobs)
    print(f"{len(jobs)} jobs, seed {a.seed}, {a.workers} workers", flush=True)

    done = 0
    with open(a.out, "w") as fh, ThreadPoolExecutor(a.workers) as pool:
        for fut in as_completed([pool.submit(run_one, j) for j in jobs]):
            fh.write(json.dumps(fut.result()) + "\n")
            fh.flush()
            done += 1
            if done % 25 == 0 or done == len(jobs):
                print(f"  {done}/{len(jobs)}", flush=True)


if __name__ == "__main__":
    main()
