"""Runner for the run_code ergonomics trial.

Waits on each process, records raw output for rescoring, and shuffles the job
list with a fixed seed so an arm is not confounded with the hour it ran.
"""
import argparse, json, os, pathlib, random, re, shutil, subprocess, sys, tempfile
from concurrent.futures import ThreadPoolExecutor

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fixture

BIN = "/tmp/strument"
ARMS = {
    "base": [],
    "sigs": ["--code-signatures"],
    "text": ["--code-read-text"],
    "both": ["--code-signatures", "--code-read-text"],
}
MODELS = ["mimo", "deepseek", "glm"]

def one(job, outdir, conf):
    arm, model, task, rep = job
    tag = f"{model}-{arm}-{task}-{rep}"
    work = tempfile.mkdtemp(prefix="ergo-")
    answers = fixture.build(work)
    jsonl = os.path.join(outdir, tag + ".jsonl")
    env = dict(os.environ, XDG_CONFIG_HOME=conf, XDG_STATE_HOME=os.path.join(work, ".state"))
    cmd = [BIN, "--no-git", "--yes", "all", "-M", model, "--jsonl", jsonl,
           *ARMS[arm], "-m", fixture.TASKS[task]]
    row = {"arm": arm, "model": model, "task": task, "rep": rep,
           "expected": answers[task], "tag": tag}
    try:
        p = subprocess.run(cmd, cwd=work, env=env, capture_output=True,
                           text=True, timeout=420)
        row["exit"] = p.returncode
        row["stderr_tail"] = p.stderr[-400:]
    except subprocess.TimeoutExpired:
        row["exit"] = -1
        row["status"] = "timeout"
        shutil.rmtree(work, ignore_errors=True)
        return row
    row.update(scan(jsonl))
    shutil.rmtree(work, ignore_errors=True)
    return row

def scan(path):
    """Everything measurable from one transcript. Raw text is kept so the run
    can be rescored without being repeated."""
    out = {"status": "no-transcript", "answer_text": "", "programs": 0,
           "steps": None, "sent": 0, "recv": 0, "cost": None, "tps": None,
           "used_read_text": 0, "positional": 0, "first_program": "",
           "programs_list": []}
    try:
        rows = [json.loads(l) for l in open(path) if l.strip()]
    except OSError:
        return out
    last_assistant = ""
    for d in rows:
        if d.get("type") == "message" and d.get("role") == "assistant":
            if d.get("text"):
                last_assistant = d["text"]
        for tc in d.get("tool_calls") or []:
            if tc.get("name") != "run_code":
                continue
            try:
                code = json.loads(tc["arguments"]).get("code", "")
            except Exception:
                code = ""
            out["programs"] += 1
            out["programs_list"].append(code)
            if out["programs"] == 1:
                out["first_program"] = code
            out["used_read_text"] += len(re.findall(r"\bread_text\s*\(", code))
            # A positional call to a bridged tool: first argument is not name=.
            out["positional"] += len(re.findall(
                r"\b(?:read|grep|glob|ls|symbol|read_text)\s*\(\s*(?![\s)]|\w+\s*=)", code))
        if d.get("type") == "turn":
            out["steps"] = d.get("steps")
            out["sent"] = d.get("sent", 0)
            out["recv"] = d.get("received", 0)
            out["cost"] = d.get("cost")
            out["tps"] = d.get("tokens_per_second")
            out["status"] = d.get("outcome", "?")
    out["answer_text"] = last_assistant
    return out

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("--conf", required=True)
    ap.add_argument("--reps", type=int, default=3)
    ap.add_argument("--models", default=",".join(MODELS))
    ap.add_argument("--arms", default=",".join(ARMS))
    ap.add_argument("--tasks", default=",".join(fixture.TASKS))
    ap.add_argument("--jobs", type=int, default=4)
    ap.add_argument("--seed", type=int, default=20260918)
    a = ap.parse_args()

    os.makedirs(a.out, exist_ok=True)
    jobs = [(arm, m, t, r)
            for arm in a.arms.split(",")
            for m in a.models.split(",")
            for t in a.tasks.split(",")
            for r in range(a.reps)]
    random.Random(a.seed).shuffle(jobs)
    print(f"{len(jobs)} runs, {a.jobs} at a time", flush=True)

    results = os.path.join(a.out, "results.jsonl")
    done = set()
    if os.path.exists(results):
        for l in open(results):
            try:
                done.add(json.loads(l)["tag"])
            except Exception:
                pass
    todo = [j for j in jobs if f"{j[1]}-{j[0]}-{j[2]}-{j[3]}" not in done]
    print(f"{len(done)} already done, {len(todo)} to run", flush=True)

    with open(results, "a") as fh, ThreadPoolExecutor(max_workers=a.jobs) as pool:
        for i, row in enumerate(pool.map(lambda j: one(j, a.out, a.conf), todo), 1):
            fh.write(json.dumps(row) + "\n")
            fh.flush()
            print(f"[{i}/{len(todo)}] {row['tag']:34} {row.get('status','?'):10} "
                  f"answer={row.get('answer_text','')[:24]!r}", flush=True)

if __name__ == "__main__":
    main()
