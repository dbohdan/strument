#!/usr/bin/env python3
"""Layer 4: MiMo writing a documentation page alone, and asked to consult Terra.

    trial.py --bin DIR --outdir DIR [--reps 6] [--only TASK,...] [--jobs 8]

Each session is one unattended Strument turn in a fresh copy of Strument's
own source at tasks.LONG_COMMIT, with doc/, every Markdown file and the
project's .strument.star removed, so there are no docs to copy and no project
config to change how the session runs; STYLE.md holds the long set's house
style. The user asks for one of the long set's pages, "from the source". Both
arms have Terra in consult_models under the documented purpose and the consult
grant given; the only difference is that in `consult` the message ends with
"Ask terra for a draft first." (layer 3: MiMo never consulted unprompted).

Each session's final page is saved as DIR/gen/task__arm__rep.json, the form
defects.py judges, so the long set's judge and scorer apply unchanged.
Sessions run in a seeded shuffled order; one with done.json is skipped, so
rerunning resumes. The key is read from OPENROUTER_API_KEY; strumentrec
strips it from what it records.
"""

import argparse
import concurrent.futures as cf
import glob
import json
import os
import random
import shutil
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import mechanics as M  # noqa: E402
import tasks as T  # noqa: E402

ARMS = ("alone", "consult")


def template(d):
    """The project every session copies: the source at LONG_COMMIT, no docs."""
    if os.path.exists(os.path.join(d, ".git")):
        return
    shutil.rmtree(d, ignore_errors=True)
    os.makedirs(d)
    root = M.git(HERE, "rev-parse", "--show-toplevel").strip()
    archive = subprocess.run(["git", "-C", root, "archive", T.LONG_COMMIT], capture_output=True, check=True).stdout
    subprocess.run(["tar", "-x", "-C", d], input=archive, check=True)
    shutil.rmtree(os.path.join(d, "doc"), ignore_errors=True)
    for dirpath, _, files in os.walk(d):
        for f in files:
            if f.endswith(".md") or (f == ".strument.star" and dirpath == d):
                os.remove(os.path.join(dirpath, f))
    with open(os.path.join(d, "STYLE.md"), "w") as f:
        f.write(T.LONG_STYLE)
    for args in (["init", "-q", "-b", "main"], ["config", "user.name", "Trial User"],
                 ["config", "user.email", "user@example.com"], ["add", "-A"], ["commit", "-q", "-m", "Initial commit"]):
        M.git(d, *args)


def message(task, arm):
    t = T.LONG_TASKS[task]
    msg = (f"Write {t['target']}, a user-facing documentation page about {t['subject']}, from the source. "
           "The project's house style is in STYLE.md.")
    return msg + (" " + M.ASK if arm == "consult" else "")


def run(a, job):
    base = os.path.join(a.outdir, "sessions", job["id"])
    if os.path.exists(os.path.join(base, "done.json")):
        return job["id"], "skipped"
    shutil.rmtree(base, ignore_errors=True)
    os.makedirs(base)
    proj = os.path.join(base, "proj")
    shutil.copytree(a.template, proj, symlinks=True)
    base_commit = M.git(proj, "rev-parse", "HEAD").strip()
    state = os.path.join(base, "state")
    os.makedirs(state)
    port = M.free_port(a.port_base + (job["order"] % 1000) * 3)
    cfg = os.path.join(base, "config.star")
    M.config(cfg, port, "plain")
    msg = message(job["task"], job["arm"])
    cmd = [os.path.join(a.bin, "strument"), "--config", cfg, "chat", "--yes", "consult,steps", "--unattended",
           "-m", msg]
    env = dict(os.environ, XDG_STATE_HOME=state, NO_COLOR="1")
    rec = subprocess.Popen([os.path.join(a.bin, "strumentrec"), "-out", os.path.join(base, "wire.jsonl"),
                            "-upstream", "https://openrouter.ai", "-listen", f"127.0.0.1:{port}"],
                           stdout=subprocess.DEVNULL, stderr=open(os.path.join(base, "rec.err"), "w"))
    time.sleep(0.5)
    start = time.time()
    status = "ok"
    try:
        p = subprocess.run(cmd, cwd=proj, env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=a.timeout)
        code, out, err = p.returncode, p.stdout, p.stderr
    except subprocess.TimeoutExpired as e:
        code, out, err, status = None, e.stdout or b"", e.stderr or b"", "timeout"
    finally:
        rec.terminate()
        rec.wait()
    seconds = round(time.time() - start, 1)
    with open(os.path.join(base, "stdout.txt"), "wb") as f:
        f.write(out)
    with open(os.path.join(base, "stderr.txt"), "wb") as f:
        f.write(err)
    for lg in glob.glob(os.path.join(state, "**", "*.jsonl"), recursive=True):
        if "/log/" in lg:
            shutil.copy(lg, os.path.join(base, "session-" + os.path.basename(lg)))
    target = os.path.join(proj, T.LONG_TASKS[job["task"]]["target"])
    page = open(target).read() if os.path.exists(target) else None
    changed = [l for l in M.git(proj, "diff", "--name-only", base_commit).splitlines() if l]
    untracked = [l for l in M.git(proj, "ls-files", "--others", "--exclude-standard").splitlines() if l]
    gen = {"model": job["arm"], "task": job["task"], "n": job["rep"], "ok": page is not None, "block": page,
           "extracted": "file", "facts": T.facts(job["task"], page) if page is not None else None}
    with open(os.path.join(a.outdir, "gen", f"{job['task']}__{job['arm']}__{job['rep']}.json"), "w") as f:
        json.dump(gen, f, indent=1)
    shutil.rmtree(proj)  # a whole source tree per session; the page and the diff are what is kept
    with open(os.path.join(base, "done.json"), "w") as f:
        json.dump(dict(job, exit=code, status=status, seconds=seconds, message=msg, changed=changed,
                       untracked=untracked, target_text=page), f)
    return job["id"], f"{status} exit={code} {seconds:.0f}s page={'yes' if page else 'no'}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bin", required=True, help="directory holding strument and strumentrec")
    ap.add_argument("--outdir", required=True)
    ap.add_argument("--reps", type=int, default=6)
    ap.add_argument("--only", default="", help="tasks")
    ap.add_argument("--jobs", type=int, default=8)
    ap.add_argument("--seed", type=int, default=20261118)
    ap.add_argument("--timeout", type=int, default=1800)
    ap.add_argument("--port-base", type=int, default=26000)
    a = ap.parse_args()
    if not os.environ.get("OPENROUTER_API_KEY"):
        sys.exit("OPENROUTER_API_KEY is not set")
    T.use("long")
    a.template = os.path.join(a.outdir, "template")
    os.makedirs(os.path.join(a.outdir, "gen"), exist_ok=True)
    template(a.template)
    only = set(filter(None, a.only.split(",")))
    js = [{"id": f"{task}__{arm}__{rep}", "task": task, "arm": arm, "rep": rep} for task in T.LONG_TASKS
          for arm in ARMS for rep in range(1, a.reps + 1) if not only or task in only]
    random.Random(a.seed).shuffle(js)
    for i, j in enumerate(js):
        j["order"] = i
    print(f"{len(js)} sessions", flush=True)
    with cf.ThreadPoolExecutor(a.jobs) as ex:
        futs = {ex.submit(run, a, j): j for j in js}
        for fut in cf.as_completed(futs):
            try:
                jid, what = fut.result()
                print(jid, what, flush=True)
            except Exception as e:
                print(futs[fut]["id"], "FAILED", repr(e), flush=True)
    print("finished", flush=True)


if __name__ == "__main__":
    main()
