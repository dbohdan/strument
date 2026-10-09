"""A/B runner: commit-tool description, with and without the whole-file sentence.

Reads OPENROUTER_API_KEY from the environment (via the config's env()), never argv.
usage: python3 -I runner.py <outdir> <reps> [--models m1,m2] [--seed N]
Jobs are shuffled with a fixed seed; run directories are neutral ids so the arm
never appears in the prompt's working-directory line.
"""
import concurrent.futures as cf, json, os, random, shutil, subprocess, sys, time, argparse

E = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ap = argparse.ArgumentParser()
ap.add_argument("outdir"); ap.add_argument("reps", type=int)
ap.add_argument("--models", default="mimo,glm,qwen"); ap.add_argument("--seed", type=int, default=20261009)
ap.add_argument("--jobs", type=int, default=4); ap.add_argument("--timeout", type=int, default=600)
a = ap.parse_args()
MSG = ('Rename notes.txt to NOTES.md with git mv and add a second line "more" to it. '
       'Commit that with the commit tool. Then change greet() in greet.py to return "hi" '
       'and commit that separately with the commit tool.')

def sh(cwd, *cmd, inp=None):
    subprocess.run(cmd, cwd=cwd, check=True, capture_output=True, input=inp)

def fixture(d):
    os.makedirs(d)
    sh(d, "git", "init", "-q", "-b", "main")
    for k, v in [("user.email", "t@t"), ("user.name", "t"), ("commit.gpgsign", "false")]:
        sh(d, "git", "config", k, v)
    open(f"{d}/greet.py", "w").write('def greet():\n    return "hello"\n')
    open(f"{d}/notes.txt", "w").write("notes\n")
    open(f"{d}/other.txt", "w").write("base\n")
    sh(d, "git", "add", "."); sh(d, "git", "commit", "-qm", "init")
    open(f"{d}/greet.py", "a").write("# user comment\n")       # the user's unstaged work
    open(f"{d}/other.txt", "w").write("user staged\n")          # the user's staged work
    sh(d, "git", "add", "other.txt")

def run(job):
    rid, model, arm = job["id"], job["model"], job["arm"]
    d = os.path.join(a.outdir, rid)
    shutil.rmtree(d, ignore_errors=True)  # a resumed job starts from a fresh fixture
    fixture(d)
    env = dict(os.environ, XDG_CONFIG_HOME=os.path.join(E, "livecfg"))
    t0 = time.time()
    try:
        p = subprocess.run([os.path.join(E, f"strument-{arm}"), "chat", "--no-history", "--no-color",
                            "--yes", "bash", "-M", model, "-m", MSG],
                           cwd=d, env=env, capture_output=True, timeout=a.timeout)
        code, out = p.returncode, p.stdout + p.stderr
    except subprocess.TimeoutExpired as e:
        code, out = 124, (e.stdout or b"") + (e.stderr or b"")
    open(d + ".out", "wb").write(out)
    rec = dict(job, exit=code, wall=round(time.time() - t0, 1))
    with open(os.path.join(a.outdir, "runs.jsonl"), "a") as f:
        f.write(json.dumps(rec) + "\n")
    return rec

os.makedirs(a.outdir, exist_ok=True)
jobs = [{"model": m, "arm": arm} for m in a.models.split(",") for arm in ("baseline", "treatment") for _ in range(a.reps)]
random.Random(a.seed).shuffle(jobs)
for i, j in enumerate(jobs):
    j["id"] = f"run-{i:03d}"
json.dump(jobs, open(os.path.join(a.outdir, "jobs.json"), "w"), indent=1)
done = set()
if os.path.exists(os.path.join(a.outdir, "runs.jsonl")):
    done = {json.loads(l)["id"] for l in open(os.path.join(a.outdir, "runs.jsonl"))}
todo = [j for j in jobs if j["id"] not in done]
print(f"{len(todo)} of {len(jobs)} jobs to run", flush=True)
with cf.ThreadPoolExecutor(a.jobs) as ex:
    for fut in cf.as_completed([ex.submit(run, j) for j in todo]):
        try:
            r = fut.result(); print(r["id"], r["model"], r["arm"], r["exit"], r["wall"], flush=True)
        except Exception as e:
            print("WORKER FAILED:", repr(e), flush=True)
print("RUNNER DONE", flush=True)
