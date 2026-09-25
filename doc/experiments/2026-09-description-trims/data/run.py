"""Runner for the description-trims trials. Real Strument sessions in script
mode, one binary per arm, shuffled with a fixed seed, four in flight,
resumable. Each job gets a fresh fixture (fixture.py, from the run_code arms
trial) and its own state directory. The key is read from OPENROUTER_API_KEY
by the config, never passed as an argument.

Usage: run.py code|ask [pilot]
Environment: TRIM_BINS (directory holding strument-base, -dedup, -trim),
TRIAL_DIR (where runs go), XDG_CONFIG_HOME (a config defining mimo and
deepseek).
"""
import concurrent.futures as cf, json, os, random, shutil, subprocess, sys, time

HERE = os.path.dirname(os.path.abspath(__file__))
BINS = os.environ["TRIM_BINS"]
TRIAL = os.environ["TRIAL_DIR"]
MODELS = ["mimo", "deepseek"]
MARKER = "Put the answer on its own line, beginning with ANSWER:"

SETS = {
    # run_code: the tool description's copy of the system prompt's advice.
    "code": {
        "arms": ["base", "dedup"],
        "reps": 3,
        "tasks": {
            "walk": "How many Markdown files under docs/ mention the phrase \"retry budget\"?",
            "total": "What is the sum of all the numbers in the .txt files under data/?",
            "top5": "Which five entries in data/sizes.csv have the largest byte counts? List them largest first.",
            "span": "How many days are there between the earliest and the latest date in logs/app.log?",
            "longest": "How long is the longest line in notes.md, in characters?",
            "cite": "On which line of notes.md does the XYZZY marker appear?",
            # One lookup each: run_code is not needed, so reaching for it is over-use.
            "readme": "What does README.md say this project is?",
            "main": "Which file holds the relay's main function?",
        },
        "suffix": "\n\n" + MARKER,
    },
    # ask_user_question: the whole description, trimmed.
    "ask": {
        "arms": ["base", "trim"],
        "reps": 5,
        "tasks": {
            # A decision reading the project cannot settle.
            "name": "Rename the relay to something better.",
            "port": "Make the port the relay listens on configurable.",
            "log": "Add structured logging to the relay.",
            # Nothing to decide.
            "version": "Add a --version flag to cmd/relay that prints 0.1.0.",
            "usage": "Add a one-sentence Usage section to README.md saying to run `go run ./cmd/relay`.",
        },
        "suffix": "",
    },
}


def jobs(name):
    s = SETS[name]
    out = [(a, m, t, r) for a in s["arms"] for m in MODELS for t in s["tasks"] for r in range(s["reps"])]
    random.Random(20260926).shuffle(out)
    return out


def one(name, job):
    arm, model, task, rep = job
    tag = f"{model}-{arm}-{task}-{rep}"
    work = os.path.join(TRIAL, name, tag)
    done = os.path.join(work, "result.json")
    if os.path.exists(done):
        return tag, "skipped"
    shutil.rmtree(work, ignore_errors=True)
    tree, state = os.path.join(work, "tree"), os.path.join(work, "state")
    os.makedirs(work)
    subprocess.run([sys.executable, os.path.join(HERE, "fixture.py"), tree], check=True, capture_output=True)
    env = dict(os.environ, XDG_STATE_HOME=state)
    s = SETS[name]
    cmd = [os.path.join(BINS, "strument-" + arm), "--no-git", "--yes", "steps", "-M", model,
           "-m", s["tasks"][task] + s["suffix"]]
    row = {"set": name, "arm": arm, "model": model, "task": task, "rep": rep, "tag": tag}
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
    name = sys.argv[1]
    todo = jobs(name)
    if sys.argv[2:] == ["pilot"]:
        seen, pilot = set(), []
        for j in todo:
            if (j[0], j[1]) not in seen:
                seen.add((j[0], j[1]))
                pilot.append(j)
        todo = pilot
    os.makedirs(os.path.join(TRIAL, name), exist_ok=True)
    json.dump(todo, open(os.path.join(TRIAL, name, "order.json"), "w"))
    with cf.ThreadPoolExecutor(max_workers=4) as ex:
        for tag, status in ex.map(lambda j: one(name, j), todo):
            print(tag, status, flush=True)


if __name__ == "__main__":
    main()
