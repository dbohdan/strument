"""Kimi K3 at its default effort (max) against high and low, on three
FrontierHarness tasks, in a shuffled order so no arm owns a stretch of time.
Resumable: a trial with a result.json is skipped. Key from OPENROUTER_API_KEY."""
import concurrent.futures as cf, json, os, random, subprocess, sys

TASKS = ["constraints-scheduling", "sqlite-db-truncate", "polyglot-c-py"]
ARMS = ["k3", "k3-high", "k3-low"]
REPS = 3
SEED = 20260924
OUT = "/tmp/fh/runs/effort"


def trial(job):
    arm, task, rep = job
    out = f"{OUT}/{arm}/{task}-{rep}"
    if os.path.exists(os.path.join(out, "result.json")):
        return f"skip {arm} {task}-{rep}"
    r = subprocess.run([sys.executable, "/tmp/fh/trial.py", task, arm, out], capture_output=True, text=True)
    return f"{arm} " + ((r.stdout.strip().splitlines() or [r.stderr[-300:]])[-1])


if __name__ == "__main__":
    jobs = [(a, t, r) for a in ARMS for t in TASKS for r in range(REPS)]
    random.Random(SEED).shuffle(jobs)
    os.makedirs(OUT, exist_ok=True)
    json.dump(jobs, open(f"{OUT}/order.json", "w"))
    with cf.ThreadPoolExecutor(max_workers=int(os.environ.get("FH_WORKERS", "2"))) as ex:
        for line in ex.map(trial, jobs):
            print(line, flush=True)
