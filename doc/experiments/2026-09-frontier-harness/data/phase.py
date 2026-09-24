"""Pull the images a phase needs, then run its trials a few at a time.
Resumable: a trial with a result.json is skipped. Key from OPENROUTER_API_KEY."""
import concurrent.futures as cf, os, subprocess, sys, tomllib

FLOOR = ["log-summary-date-ranges", "constraints-scheduling", "openssl-selfsigned-cert",
         "git-leak-recovery", "modernize-scientific-stack", "multi-source-data-merger",
         "db-wal-recovery", "sqlite-db-truncate", "polyglot-c-py", "vulnerable-secret",
         "merge-diff-arc-agi-task", "build-cython-ext"]
DISC = ["dna-insert", "chess-best-move", "extract-elf", "gcode-to-text", "sanitize-git-repo", "code-from-image"]

def pull(task):
    dest = f"/tmp/fh/images/{task}"
    if os.path.exists(os.path.join(dest, ".image-config.json")):
        return
    image = tomllib.load(open(f"/tmp/harness/tb2/{task}/task.toml", "rb"))["environment"]["docker_image"]
    env = {k: v for k, v in os.environ.items() if k.lower() not in ("https_proxy", "http_proxy")}
    subprocess.run([sys.executable, "/tmp/fh/pull.py", image, dest], check=True, env=env,
                   stdout=subprocess.DEVNULL)
    print("pulled", task, flush=True)

def trial(job):
    task, model, rep = job
    out = f"/tmp/fh/runs/{model}{os.environ.get('RUNTAG', '')}/{task}-{rep}"
    if os.path.exists(os.path.join(out, "result.json")):
        return f"skip {task}-{rep}"
    r = subprocess.run([sys.executable, "/tmp/fh/trial.py", task, model, out], capture_output=True, text=True)
    return (r.stdout.strip().splitlines() or [r.stderr[-300:]])[-1]

if __name__ == "__main__":
    phase, model, reps = sys.argv[1], sys.argv[2], int(sys.argv[3])
    tasks = {"floor": FLOOR, "disc": DISC}[phase]
    for t in tasks:
        pull(t)
    jobs = [(t, model, r) for r in range(reps) for t in tasks]
    with cf.ThreadPoolExecutor(max_workers=int(os.environ.get("FH_WORKERS", "3"))) as ex:
        for line in ex.map(trial, jobs):
            print(line, flush=True)
