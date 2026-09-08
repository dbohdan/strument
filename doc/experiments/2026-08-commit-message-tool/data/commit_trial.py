"""Trial: commit_message tool (C1) against the rewritten separate call (C0).

The baseline is the *new* prompt, not aider's. Comparing against the old
one-line-only prompt would credit the tool with an improvement a prompt edit
also produces, which is the confound the prompt-scope experiment taught us to
design out.

Arm order is randomized across the whole job list: running every C0 and then
every C1 confounds the arm with the time it ran, and providers drift.
"""

import json
import os
import pathlib
import random
import re
import shutil
import subprocess
import sys
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

EXP = pathlib.Path(__file__).parent
MODELS = {
    "mimo": "xiaomi/mimo-v2.5",
    "luna": "openai/gpt-5.6-luna",
    "v4flash": "deepseek/deepseek-v4-flash-0731",
}
ARMS = {"C0": EXP / "bin/strument-C0", "C1": EXP / "bin/strument-C1"}

CONFIG = """\
router = provider(adapter = "openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}", cache = True)}}
default = "m"
"""

CALC = """\
package demo

// Sum adds two integers.
func Sum(a, b int) int {
\treturn a + b
}

// Mean returns the average of xs. It returns 0 for an empty slice.
func Mean(xs []int) int {
\ttotal := 0
\tfor _, x := range xs {
\t\ttotal += x
\t}
\treturn total / len(xs)
}
"""

FILES = {
    "go.mod": "module demo\n\ngo 1.26\n",
    "calc.go": CALC,
    "calc_test.go": (
        'package demo\n\nimport "testing"\n\n'
        "func TestSum(t *testing.T) {\n\tif Sum(2, 2) != 4 {\n\t\tt.Fail()\n\t}\n}\n"
    ),
    "README.md": "# demo\n\nA package with Sum and Mean.\n",
}

TASKS = {
    # Mechanical and self-evident: the body should stay empty.
    "rename": "Rename Sum to Add everywhere, including the test and the README.",
    # Renaming an exported function breaks callers: wants ! and BREAKING CHANGE.
    "breaking": "Change Mean to return a float64 instead of an int, and update "
                "everything that uses it.",
    # The why is not in the diff: Mean divides by zero on an empty slice despite
    # what its comment claims. A body earns its place here.
    "why": "Mean has a bug. Find it, fix it, and add a test that would have "
           "caught it.",
}


def run_one(job):
    arm, model_key, task, rep = job
    work = tempfile.mkdtemp(prefix=f"ct-{arm}-{task}-")
    try:
        root = pathlib.Path(work) / "proj"
        root.mkdir(parents=True)
        for name, body in FILES.items():
            (root / name).write_text(body)
        for cmd in (["git", "init", "-q"], ["git", "config", "user.email", "a@b.c"],
                    ["git", "config", "user.name", "T"], ["git", "add", "-A"],
                    ["git", "commit", "-qm", "init"]):
            subprocess.run(cmd, cwd=root, check=True, capture_output=True)

        cfg = pathlib.Path(work) / "cfg" / "strument"
        cfg.mkdir(parents=True)
        (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))
        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = str(cfg.parent)
        env["XDG_STATE_HOME"] = str(pathlib.Path(work) / "state")

        t0 = time.time()
        try:
            proc = subprocess.run(
                [str(ARMS[arm])], input=TASKS[task] + "\n/exit\n",
                cwd=root, env=env, capture_output=True, text=True, timeout=420,
            )
            out, rc = proc.stdout + proc.stderr, proc.returncode
        except subprocess.TimeoutExpired:
            out, rc = "TIMEOUT", -9

        log = subprocess.run(["git", "log", "-1", "--format=%B"], cwd=root,
                             capture_output=True, text=True).stdout
        n_commits = subprocess.run(["git", "rev-list", "--count", "HEAD"], cwd=root,
                                   capture_output=True, text=True).stdout.strip()

        steps = re.search(r"(\d+) steps", out)
        cost = re.search(r"\$([0-9.]+) turn", out)
        sent = re.search(r"Tokens: ([0-9.]+)k? sent", out)
        # Strip the trailer Strument appends; what is left is the model's message.
        msg = re.sub(r"\n*Assisted-by:.*\n?", "", log).strip()
        subject, _, body = msg.partition("\n\n")

        return {
            "arm": arm, "model": model_key, "task": task, "rep": rep,
            "returncode": rc, "elapsed": round(time.time() - t0, 1),
            "steps": int(steps.group(1)) if steps else 0,
            "cost": float(cost.group(1)) if cost else 0.0,
            "sent_raw": sent.group(1) if sent else "",
            "commits": int(n_commits or 0),
            "tool_called": "Commit message set" in out or "Commit message replaced" in out,
            "replaced": "Commit message replaced" in out,
            "subject": subject.strip(),
            "body": body.strip(),
            "has_body": bool(body.strip()),
            "bang": "!:" in subject or "!" in subject.split(":")[0],
            "breaking_footer": "BREAKING CHANGE:" in msg,
            "conventional": bool(re.match(r"^[a-z]+(\([^)]+\))?!?: .", subject.strip())),
            "scoped": bool(re.match(r"^[a-z]+\([^)]+\)!?: ", subject.strip())),
            "subject_len": len(subject.strip()),
            "refused_edit": "Skipping edit" in out,
            "stdout": out,
            "commit_body": log,
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    reps = int(sys.argv[1]) if len(sys.argv) > 1 else 2
    jobs = [(a, m, t, r) for a in ARMS for m in MODELS for t in TASKS for r in range(reps)]
    random.seed(20260814)
    random.shuffle(jobs)  # arm must not be confounded with when it ran
    out = EXP / "commit-trial.jsonl"
    with open(out, "w") as fh, ThreadPoolExecutor(6) as pool:
        done = 0
        for fut in as_completed([pool.submit(run_one, j) for j in jobs]):
            fh.write(json.dumps(fut.result()) + "\n")
            fh.flush()
            done += 1
            print(f"  {done}/{len(jobs)}", flush=True)
