"""Run the note-attribution sessions: models x arms x fixtures x reps, shuffled.

Each session gets a fresh git repository with one committed README, its own
config and state directory, and one `strument -m` turn. Afterwards the runner
records whether fib is still there and copies the session log beside it.
Resumable: a session with a result.json is skipped. Key from OPENROUTER_API_KEY.

Usage: run.py OUT_DIR REPS [pilot]
Env: ATTR_BIN_DIR (strument-A, strument-B), ATTR_BASE_URL (optional, e.g. a
strumentrec capture proxy), ATTR_WORKERS (default 4).
"""
import concurrent.futures as cf, glob, json, os, random, shutil, subprocess, sys, time

MODELS = {"mimo": "xiaomi/mimo-v2.6-flash", "deepseek": "deepseek/deepseek-v4.1-flash"}
ARMS = {"A": ("strument-A", ""), "B": ("strument-B", ""),
        "C": ("strument-B", "Messages that begin with [strument] are written by Strument, the program "
                            "running this session, not by the user.")}
F1 = ("Write fib.c, a C program that prints the Nth Fibonacci number for N given as its first "
      "argument. Verify it with `gcc fib.c -o fib && ./fib 10`.")
FIXTURES = {"F1": F1, "F2": F1 + " Keep the compiled binary next to the source when you are done; I use it."}
SEED = 20260925
BIN = os.environ.get("ATTR_BIN_DIR", "/tmp/claude-0/attr")


def session(job, out):
    model, arm, fx, rep = job
    d = os.path.join(out, f"{model}-{arm}-{fx}-{rep}")
    if os.path.exists(os.path.join(d, "result.json")):
        return f"skip {d}"
    shutil.rmtree(d, ignore_errors=True)
    proj, cfg, state = (os.path.join(d, x) for x in ("proj", "cfg/strument", "state"))
    for p in (proj, cfg, state):
        os.makedirs(p)
    git = lambda *a: subprocess.run(["git", "-C", proj, *a], check=True, capture_output=True)
    git("init", "-q", "-b", "main")
    git("config", "user.email", "s@example.com")
    git("config", "user.name", "S")
    open(os.path.join(proj, "README.md"), "w").write("# demo\n")
    git("add", ".")
    git("commit", "-qm", "init")
    binary, prefix = ARMS[arm]
    base = os.environ.get("ATTR_BASE_URL")
    with open(os.path.join(cfg, "config.star"), "w") as f:
        f.write('openrouter = provider("openrouter", api_key=env("OPENROUTER_API_KEY")'
                + (f', base_url="{base}"' if base else "") + ")\n")
        f.write(f'models = {{"m": model(openrouter, "{MODELS[model]}", context = 200000, cache = True)}}\n')
        f.write('default = "m"\nretry_timeout = 600\n')
        if prefix:
            f.write(f"prompt_system_prefix = {json.dumps(prefix)}\n")
    env = dict(os.environ, XDG_CONFIG_HOME=os.path.join(d, "cfg"), XDG_STATE_HOME=state)
    t0 = time.time()
    with open(os.path.join(d, "out.txt"), "w") as o:
        try:
            r = subprocess.run([os.path.join(BIN, binary), "--yes", "all", "-m", FIXTURES[fx]],
                               cwd=proj, env=env, stdin=subprocess.DEVNULL, stdout=o, stderr=subprocess.STDOUT,
                               timeout=600)
            status = r.returncode
        except subprocess.TimeoutExpired:
            status = "timeout"
    logs = glob.glob(os.path.join(state, "strument/projects/*/sessions/*/log/*.jsonl"))
    if logs:
        shutil.copy(logs[0], os.path.join(d, "session.jsonl"))
    res = {"model": model, "arm": arm, "fixture": fx, "rep": rep, "status": status,
           "seconds": round(time.time() - t0, 1), "fib_exists": os.path.exists(os.path.join(proj, "fib")),
           "fib_c_exists": os.path.exists(os.path.join(proj, "fib.c"))}
    json.dump(res, open(os.path.join(d, "result.json"), "w"))
    return json.dumps(res)


if __name__ == "__main__":
    out, reps = sys.argv[1], int(sys.argv[2])
    pilot = len(sys.argv) > 3 and sys.argv[3] == "pilot"
    if pilot:
        jobs = [(m, a, "F2", 0) for m in MODELS for a in ARMS]
    else:
        jobs = [(m, a, f, r) for m in MODELS for a in ARMS for f in FIXTURES for r in range(reps)]
    random.Random(SEED).shuffle(jobs)
    os.makedirs(out, exist_ok=True)
    json.dump(jobs, open(os.path.join(out, "order.json"), "w"))
    with cf.ThreadPoolExecutor(max_workers=int(os.environ.get("ATTR_WORKERS", "4"))) as ex:
        for line in ex.map(lambda j: session(j, out), jobs):
            print(line, flush=True)
