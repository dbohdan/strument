"""Runner for the read-outline trial. Real Strument sessions in script mode,
one binary per arm (built with -ldflags -X ...coder.readArm=<arm>), shuffled
with a fixed seed, four in flight, resumable. Each job gets a fresh fixture
(fixture.py) and its own state directory. The key is read from
OPENROUTER_API_KEY by the config, never passed as an argument.

Usage: run.py [pilot]
Environment: OUTLINE_BINS (directory holding strument-A..D), TRIAL_DIR (where
runs go), XDG_CONFIG_HOME (a config defining mimo and luna), OUTLINE_SRC (the
fixture's clone cache).
"""
import concurrent.futures as cf, json, os, random, shutil, subprocess, sys, time

HERE = os.path.dirname(os.path.abspath(__file__))
BINS = os.environ["OUTLINE_BINS"]
TRIAL = os.environ["TRIAL_DIR"]
ARMS = ["A", "B", "C", "D"]
MODELS = ["mimo", "luna"]
REPS = 5
PREFIX = "Answer from the code in this project, which vendors click and rich with local changes. "
SUFFIX = "\n\nPut the answer on its own line, beginning with ANSWER:"

TASKS = {
    # Past the read window: the target is beyond line 2,000 of its file.
    "envvar": "An option named `port` has no explicit envvar, and the context sets "
              "auto_envvar_prefix to APP. Which environment variable name does click read for it?",
    "metavar": "What does click's Argument.make_metavar append to the metavar of a deprecated argument? "
               "Give the exact string.",
    "flag": "A click boolean flag option is declared without a flag_value. What value does the "
            "command's function receive when the flag is given on the command line?",
    "pipe": "What exit status does a rich Console exit with when writing to its output hits a broken pipe?",
    # Structural, past the window: what a class defines, which a map answers.
    "exports": "List the names of every method of rich's Console class whose name begins with "
               "export_ or save_.",
    "argmethods": "List the names of every method defined directly in click's Argument class, "
                  "not the ones it inherits.",
    # Controls, inside the window.
    "dumb": "What size does rich's Console.size report for a dumb terminal? Answer as WIDTHxHEIGHT.",
    "abort": "What message does a click command running in standalone mode print to stderr when it is "
             "aborted? Give the exact text.",
}


def jobs():
    out = [(a, m, t, r) for a in ARMS for m in MODELS for t in TASKS for r in range(REPS)]
    random.Random(20260927).shuffle(out)
    return out


def one(job):
    arm, model, task, rep = job
    tag = f"{model}-{arm}-{task}-{rep}"
    work = os.path.join(TRIAL, tag)
    done = os.path.join(work, "result.json")
    if os.path.exists(done):
        return tag, "skipped"
    shutil.rmtree(work, ignore_errors=True)
    tree, state = os.path.join(work, "tree"), os.path.join(work, "state")
    os.makedirs(work)
    subprocess.run([sys.executable, os.path.join(HERE, "fixture.py"), tree], check=True, capture_output=True)
    env = dict(os.environ, XDG_STATE_HOME=state)
    cmd = [os.path.join(BINS, "strument-" + arm), "--no-git", "--yes", "steps", "-M", model,
           "-m", PREFIX + TASKS[task] + SUFFIX]
    row = {"arm": arm, "model": model, "task": task, "rep": rep, "tag": tag}
    t0 = time.time()
    try:
        p = subprocess.run(cmd, cwd=tree, env=env, stdin=subprocess.DEVNULL,
                           capture_output=True, text=True, timeout=600)
        row["exit"] = p.returncode
        open(os.path.join(work, "stdout.txt"), "w").write(p.stdout)
    except subprocess.TimeoutExpired:
        row["exit"] = "timeout"
    row["seconds"] = round(time.time() - t0, 1)
    logs = sorted(os.path.join(dp, f) for dp, _, fs in os.walk(state) for f in fs if f.endswith(".jsonl") and "/log" in dp)
    if logs:
        shutil.copy(logs[0], os.path.join(work, "session.jsonl"))
    shutil.rmtree(tree, ignore_errors=True)
    json.dump(row, open(done, "w"))
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
    os.makedirs(TRIAL, exist_ok=True)
    json.dump(todo, open(os.path.join(TRIAL, "order.json"), "w"))
    with cf.ThreadPoolExecutor(max_workers=4) as ex:
        for tag, status in ex.map(one, todo):
            print(tag, status, flush=True)


if __name__ == "__main__":
    main()
