"""Runner for the run_code arms trial. Real Strument sessions in script mode.

Resumable (a job with a result file is skipped), shuffled with a fixed seed,
four in flight. Each job gets a fresh fixture and its own state directory, so
its session record and blob store are its own. The key is read from
OPENROUTER_API_KEY by the config, never passed as an argument.

Environment: STRUMENT (the binary under test), TRIAL_DIR (where runs go),
XDG_CONFIG_HOME (a config defining the aliases in MODELS).
"""
import concurrent.futures as cf, json, os, random, shutil, subprocess, sys, time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

STRUMENT = os.environ.get("STRUMENT", "strument")
TRIAL = os.environ["TRIAL_DIR"]
RUNS = os.path.join(TRIAL, "runs")
ARMS = ["monty", "monty-open", "js"]
MODELS = ["mimo", "glm", "deepseek"]
MARKER = "Put the answer on its own line, beginning with ANSWER:"
TASKS = {
    "walk": "How many Markdown files under docs/ mention the phrase \"retry budget\"?",
    "total": "What is the sum of all the numbers in the .txt files under data/?",
    "top5": "Which five entries in data/sizes.csv have the largest byte counts? List them largest first.",
    "span": "How many days are there between the earliest and the latest date in logs/app.log?",
    "longest": "How long is the longest line in notes.md, in characters?",
    "cite": "On which line of notes.md does the XYZZY marker appear?",
}
REPS = 6
TIMEOUT = 600


def jobs():
    out = [(a, m, t, r) for a in ARMS for m in MODELS for t in TASKS for r in range(REPS)]
    random.Random(20260925).shuffle(out)
    return out


def one(job):
    arm, model, task, rep = job
    tag = f"{model}-{arm}-{task}-{rep}"
    done = os.path.join(RUNS, tag, "result.json")
    if os.path.exists(done):
        return tag, "skipped"
    work = os.path.join(RUNS, tag)
    shutil.rmtree(work, ignore_errors=True)
    tree, state = os.path.join(work, "tree"), os.path.join(work, "state")
    subprocess.run([sys.executable, os.path.join(HERE, "fixture.py"), tree], check=True, capture_output=True)
    env = dict(os.environ, XDG_STATE_HOME=state)
    cmd = [STRUMENT, "--no-git", "--yes", "steps", "--run-code-arm", arm, "-M", model,
           "-m", TASKS[task] + "\n\n" + MARKER]
    row = {"arm": arm, "model": model, "task": task, "rep": rep, "tag": tag}
    t0 = time.time()
    try:
        p = subprocess.run(cmd, cwd=tree, env=env, stdin=subprocess.DEVNULL,
                           capture_output=True, text=True, timeout=TIMEOUT)
        row["exit"] = p.returncode
        with open(os.path.join(work, "stdout.txt"), "w") as f:
            f.write(p.stdout)
        with open(os.path.join(work, "stderr.txt"), "w") as f:
            f.write(p.stderr)
    except subprocess.TimeoutExpired:
        row["exit"] = "timeout"
    row["seconds"] = round(time.time() - t0, 1)
    shutil.rmtree(tree, ignore_errors=True)
    with open(done, "w") as f:
        json.dump(row, f)
    return tag, row["exit"]


def main():
    os.makedirs(RUNS, exist_ok=True)
    todo = jobs()
    if sys.argv[1:] == ["pilot"]:
        seen, pilot = set(), []
        for j in todo:
            if (j[0], j[1]) not in seen and j[2] in ("top5", "span"):
                seen.add((j[0], j[1]))
                pilot.append(j)
        todo = pilot
    print(f"{len(todo)} jobs", flush=True)
    with cf.ThreadPoolExecutor(max_workers=4) as ex:
        futs = {ex.submit(one, j): j for j in todo}
        for f in cf.as_completed(futs):
            try:
                print(*f.result(), flush=True)
            except Exception as e:  # a job's failure must not end the batch
                print("ERROR", futs[f], repr(e), flush=True)


if __name__ == "__main__":
    main()
