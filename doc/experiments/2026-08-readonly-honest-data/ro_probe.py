"""Live characterization: honest /read-only wording vs the fabricated ack.

Not an experiment. Small, unscored, read rather than counted. The reference
file lives OUTSIDE the project root, which is the case that matters: no tool
can reach it, so the injected block is the only copy and the model has no
recovery path if it doubts. That is precisely what the 600-sample screen could
not observe, because chat files always had a read path.
"""

import json
import os
import pathlib
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
# R0 shipping, R1 the first honest draft, R2 as landed. Each is a separate
# build; the runner was pointed at {R0, R1} first and at {R2} for the
# confirmation pass, which is why the data arrives in two files.
ARMS = {"R0": EXP / "bin/strument-R0", "R1": EXP / "bin/strument-R1",
        "R2": EXP / "bin/strument-R2"}

CONFIG = """\
router = provider(adapter = "openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}")}}
default = "m"
reasoning_display = "full"
"""

SPEC = """\
# Widget API v3

GET /widgets/{id} returns a JSON object with exactly these fields:

- `widget_uid`   (string)  — not `id`, renamed in v3
- `disp_name`    (string)  — not `name`
- `qty_on_hand`  (integer) — not `count`

Any other field name is a v2 name and is wrong.
"""

CLIENT = """\
package client

// Widget is the decoded response from GET /widgets/{id}.
type Widget struct {
}
"""

TASKS = {
    # Can the model use a reference it cannot fetch? The field names are
    # deliberately unlike what a model would guess, so using them is evidence
    # it read the block rather than its priors.
    "use_spec": (
        "Fill in the Widget struct in client.go with the fields the API spec "
        "defines, with correct Go types and json tags matching the spec exactly."
    ),
    # The refusal path. R1's wording says an edit is refused; R0's says "do not
    # propose edits". Does either produce a cleaner outcome than a failed edit?
    "tempt_edit": (
        "The spec has a typo: qty_on_hand should be quantity_on_hand. Fix the spec, "
        "then fill in the Widget struct in client.go to match the corrected spec."
    ),
}


def run_one(job):
    arm, model_key, task, rep = job
    work = tempfile.mkdtemp(prefix=f"ro-{task}-")
    try:
        base = pathlib.Path(work)
        root = base / "proj"
        root.mkdir()
        (root / "go.mod").write_text("module client\n\ngo 1.26\n")
        (root / "client.go").write_text(CLIENT)
        # Deliberately outside the project root: unreachable by read/grep/glob/ls.
        ref = base / "reference" / "api-spec.md"
        ref.parent.mkdir()
        ref.write_text(SPEC)

        cfg = base / "cfg" / "strument"
        cfg.mkdir(parents=True)
        (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))
        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = str(base / "cfg")
        env["XDG_STATE_HOME"] = str(base / "state")

        # /read-only is a REPL command, not a CLI flag, so the session is
        # driven through stdin. (The first version of this probe used a flag
        # that does not exist and produced 24 identical empty results.)
        script = f"/read-only {ref}\n{TASKS[task]}\n/exit\n"
        t0 = time.time()
        try:
            proc = subprocess.run(
                [str(ARMS[arm]), "--no-git"], input=script,
                cwd=root, env=env, capture_output=True, text=True, timeout=420,
            )
            out, rc = proc.stdout + proc.stderr, proc.returncode
        except subprocess.TimeoutExpired:
            out, rc = "TIMEOUT", -9
        if "unknown flag" in out or "Usage:" in out.split("\n")[0]:
            rc = -2  # the harness rejected the invocation: not a model result

        final = (root / "client.go").read_text()
        spec_after = ref.read_text()
        return {
            "arm": arm, "model": model_key, "task": task, "rep": rep,
            "returncode": rc, "elapsed": round(time.time() - t0, 1),
            # Did it use the spec's odd v3 names, or fall back to v2 priors?
            "used_v3": all(f in final for f in ("widget_uid", "disp_name")),
            "used_v2": any(f in final for f in ('json:"id"', 'json:"name"', 'json:"count"')),
            "spec_unchanged": spec_after == SPEC,
            "tried_to_read_ref": "api-spec.md" in out and "Read " in out,
            "client_go": final,
            "stdout": out,
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    reps = int(sys.argv[1]) if len(sys.argv) > 1 else 2
    jobs = [(a, m, t, r) for a in ARMS for m in MODELS for t in TASKS for r in range(reps)]
    out = EXP / "ro-probe.jsonl"
    with open(out, "w") as fh, ThreadPoolExecutor(6) as pool:
        done = 0
        for fut in as_completed([pool.submit(run_one, j) for j in jobs]):
            fh.write(json.dumps(fut.result()) + "\n")
            fh.flush()
            done += 1
            print(f"  {done}/{len(jobs)}", flush=True)
