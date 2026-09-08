"""Live trial: does containing read/ls refuse anything legitimate?

Not an A/B. The containment is landing either way — the question is whether it
gets in the way of ordinary work, and whether the pinned-file exemption keeps
the one case that depends on it working.

Two shapes:
  ordinary  — a normal editing turn in a tree with a symlinked directory and a
              vendored subtree, the shapes most likely to trip a naive check.
  pinned    — a /read-only spec outside the project root, which models were
              observed reading in the read-only trial. The exemption must hold.
"""

import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

EXP = pathlib.Path(__file__).parent
BIN = EXP / "bin/strument-S1"
MODELS = {
    "mimo": "xiaomi/mimo-v2.5",
    "luna": "openai/gpt-5.6-luna",
    "v4flash": "deepseek/deepseek-v4-flash-0731",
}
CONFIG = """\
router = provider(adapter = "openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}", cache = True)}}
default = "m"
reasoning_display = "full"
"""

SPEC = """\
# Widget API v3

GET /widgets/{id} returns exactly these fields:

- `widget_uid`   (string)
- `disp_name`    (string)
- `qty_on_hand`  (integer)
"""


def build(work, task):
    root = pathlib.Path(work) / "proj"
    (root / "internal" / "poll").mkdir(parents=True)
    (root / "vendor" / "dep").mkdir(parents=True)
    (root / "go.mod").write_text("module demo\n\ngo 1.26\n")
    (root / "internal" / "poll" / "poll.go").write_text(
        "package poll\n\nconst defaultTimeout = 30\n\nfunc Tick() int { return defaultTimeout }\n")
    (root / "internal" / "poll" / "watch.go").write_text(
        "package poll\n\nfunc Watch() int { return defaultTimeout * 2 }\n")
    (root / "vendor" / "dep" / "dep.go").write_text("package dep\n\nfunc Helper() {}\n")
    (root / "client.go").write_text("package demo\n\ntype Widget struct {\n}\n")
    (root / "README.md").write_text("# demo\n\nA package with a poll loop.\n")
    # A symlinked directory inside the project, pointing inside it: legitimate,
    # and the shape a naive symlink check would refuse.
    os.symlink(root / "internal" / "poll", root / "poll-link")

    ref = pathlib.Path(work) / "reference" / "api-spec.md"
    ref.parent.mkdir()
    ref.write_text(SPEC)
    return root, ref


TASKS = {
    "ordinary": "Rename defaultTimeout to pollInterval everywhere it is used, "
                "including in watch.go, and mention it in the README.",
    "pinned": "Fill in the Widget struct in client.go with the fields the API "
              "spec defines, with correct Go types and json tags.",
}


def run_one(job):
    model_key, task, rep = job
    work = tempfile.mkdtemp(prefix=f"ct-{task}-")
    try:
        root, ref = build(work, task)
        cfg = pathlib.Path(work) / "cfg" / "strument"
        cfg.mkdir(parents=True)
        (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))
        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = str(cfg.parent)
        env["XDG_STATE_HOME"] = str(pathlib.Path(work) / "state")

        script = (f"/read-only {ref}\n" if task == "pinned" else "") + TASKS[task] + "\n/exit\n"
        t0 = time.time()
        try:
            proc = subprocess.run([str(BIN), "--no-git"], input=script, cwd=root,
                                  env=env, capture_output=True, text=True, timeout=420)
            out, rc = proc.stdout + proc.stderr, proc.returncode
        except subprocess.TimeoutExpired:
            out, rc = "TIMEOUT", -9

        steps = re.search(r"(\d+) steps", out)
        return {
            "model": model_key, "task": task, "rep": rep, "returncode": rc,
            "elapsed": round(time.time() - t0, 1),
            "steps": int(steps.group(1)) if steps else 0,
            # Any containment or ignore refusal the model actually hit.
            "refusals": re.findall(
                r"Could not (?:read|list) [^\n]*?(?:outside the project|through a symlink"
                r"|absolute paths|\.git directory|ignored by the project)[^\n]*", out),
            "read_the_spec": "api-spec.md" in out and "Read " in out,
            "used_v3": all(f in (root / "client.go").read_text()
                           for f in ("widget_uid", "disp_name")) if task == "pinned" else None,
            "renamed": "pollInterval" in (root / "internal" / "poll" / "poll.go").read_text()
                       if task == "ordinary" else None,
            "stdout": out,
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    reps = int(sys.argv[1]) if len(sys.argv) > 1 else 3
    jobs = [(m, t, r) for m in MODELS for t in TASKS for r in range(reps)]
    out = EXP / "contain-trial.jsonl"
    with open(out, "w") as fh, ThreadPoolExecutor(6) as pool:
        done = 0
        for fut in as_completed([pool.submit(run_one, j) for j in jobs]):
            fh.write(json.dumps(fut.result()) + "\n")
            fh.flush()
            done += 1
            print(f"  {done}/{len(jobs)}", flush=True)
