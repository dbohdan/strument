"""Trial 1: does the rewritten summarizer preserve what a session needs?

Not an A/B on prompt wording alone — the arms differ in three ways at once
(agentless system message, tool messages fed in, clipped results), because they
ship together and the question is whether the bundle is better.

Design notes that cost time to learn elsewhere:
  * Arm order is randomized. Running all of one arm then all of the other
    confounds the arm with the hour it ran; providers drift across that window.
  * The metric is a count, not a judgment: a recall question whose answer is a
    literal string, graded by substring match on the final answer.
  * The counter-metric is cost. Feeding tool messages enlarges the summarizer's
    input, and recall rising while spend triples is a different trade.

Shape of a session: `context=16384` puts maxChatHistoryTokens at its 1024 floor
(min(max(16384/16,1024),8192)), so a few turns of real tool work overflow the
history budget and force compaction — while 16384 stays far above any actual
prompt, so checkTokens never fires and never blocks on a confirmation.

The recall target is planted in turn 1 as a decision with a *reason*, which is
exactly what the new prompt asks to keep and what the diff cannot recover.
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
ARMS = {"base": EXP / "bin/strument-base", "new": EXP / "bin/strument-new"}
MODELS = {"mimo": "xiaomi/mimo-v2.5", "v4flash": "deepseek/deepseek-v4-flash-0731"}

CONFIG = """\
router = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}", context = 16384)}}
default = "m"
"""

# The fixture has to be big enough that ordinary tool work overflows the
# 1024-token history budget. A first pass used four-line files and reached only
# 383 tokens of settled history after two turns, so nothing ever compacted and
# the trial measured nothing. Real files, real reads.
def _decls(pkg, n, seed):
    lines = []
    for i in range(n):
        lines.append(
            f"// Item{seed}{i} handles case {i} of the {pkg} path.\n"
            f"// It exists because the {seed} subsystem needs its own hook here.\n"
            f"func Item{seed}{i}(in string) string {{\n"
            f'\tif in == "" {{\n'
            f'\t\treturn "item{seed}{i}"\n'
            f"\t}}\n"
            f'\treturn in + "-{seed}{i}"\n'
            f"}}\n")
    return "\n".join(lines)


FILES = {
    "go.mod": "module demo\n\ngo 1.26\n",
    "poll/poll.go": (
        "package poll\n\nconst defaultTimeout = 30\n\n"
        "func Tick() int { return defaultTimeout }\n\n" + _decls("poll", 40, "a")),
    "poll/watch.go": (
        "package poll\n\nfunc Watch() int { return defaultTimeout * 2 }\n\n"
        + _decls("poll", 40, "b")),
    "store/store.go": (
        "package store\n\ntype Store struct{ items map[string]string }\n\n"
        "func (s *Store) Get(k string) string { return s.items[k] }\n\n"
        + _decls("store", 50, "c")),
    "store/cache.go": "package store\n\n" + _decls("store", 50, "d"),
    "api/api.go": "package api\n\n" + _decls("api", 50, "e"),
    "README.md": "# demo\n\nA small service.\n",
}


# Turn 1 plants the fact. Turns 2-4 bury it under enough tool work to overflow
# the 1024-token history budget. Turn 5 asks for it back.
TURNS = [
    "Rename defaultTimeout to pollInterval everywhere, including watch.go. "
    "Use the value 45, not 30. Record in README.md that we chose 45 seconds "
    "because the upstream load balancer idles connections out at 60.",
    "Add a Delete method to store.Store and use it nowhere yet.",
    "Add a Ping function to the api package that returns \"pong\".",
    "Grep the whole project for any remaining reference to the old constant "
    "name and report what you find.",
    # The marker is what makes the metric a count instead of a parse. The first
    # run split the output on "Tokens:" and took parts[-2], which silently
    # captured turn 4's answer in some sessions — so two of three recorded
    # "failures" were measuring the extractor, not the summarizer. Both arms get
    # the identical instruction, so it cannot favour either.
    "Why did we pick the poll interval value that we did? Answer from what you "
    "already know; do not read any files. Put your whole answer on one line "
    "beginning with the exact text ANSWER:",
]

# The answer line, wherever it appears in the session output.
ANSWER_LINE = re.compile(r"^ *ANSWER:.*$", re.M)

# The answer must name the reason, not just the number: 45 is in the code, the
# load balancer is only in the conversation.
RECALL = re.compile(r"(load.?balanc|idle)", re.I)
VALUE = re.compile(r"\b45\b")


def build(work):
    root = pathlib.Path(work) / "proj"
    for rel, body in FILES.items():
        p = root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(body)
    return root


def run_one(job):
    arm, model_key, rep = job
    work = tempfile.mkdtemp(prefix=f"t1-{arm}-")
    try:
        root = build(work)
        cfg = pathlib.Path(work) / "cfg" / "strument"
        cfg.mkdir(parents=True)
        (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))
        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = str(cfg.parent)
        env["XDG_STATE_HOME"] = str(pathlib.Path(work) / "state")

        script = "".join(t + "\n" for t in TURNS) + "/exit\n"
        t0 = time.time()
        try:
            proc = subprocess.run(
                [str(ARMS[arm]), "--no-git", "--yes"], input=script, cwd=root,
                env=env, capture_output=True, text=True, timeout=900)
            out, rc = proc.stdout + proc.stderr, proc.returncode
        except subprocess.TimeoutExpired as e:
            out = (e.stdout or "") + (e.stderr or "")
            rc = -9

        # Take the last marked line. A session that never emitted one is scored
        # as "no answer" rather than as a failure to recall — the two mean
        # different things and lumping them is how an aggregate misleads.
        marked = ANSWER_LINE.findall(out)
        answer = marked[-1] if marked else ""
        costs = [float(c) for c in re.findall(r"\$([0-9.]+) session", out)]
        return {
            "arm": arm, "model": model_key, "rep": rep, "returncode": rc,
            "elapsed": round(time.time() - t0, 1),
            "compacted": out.count("Summarizing chat history"),
            "answered": bool(marked),
            "recalled_reason": bool(RECALL.search(answer)),
            "recalled_value": bool(VALUE.search(answer)),
            "session_cost": costs[-1] if costs else None,
            "answer": answer.strip()[:400],
            "stdout": out,
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    reps = int(sys.argv[1]) if len(sys.argv) > 1 else 4
    jobs = [(a, m, r) for a in ARMS for m in MODELS for r in range(reps)]
    random.seed(20260815)
    random.shuffle(jobs)  # the arm must not be confounded with the hour it ran
    out_path = EXP / "results.jsonl"
    with open(out_path, "w") as fh, ThreadPoolExecutor(4) as pool:
        done = 0
        for fut in as_completed([pool.submit(run_one, j) for j in jobs]):
            fh.write(json.dumps(fut.result()) + "\n")
            fh.flush()
            done += 1
            print(f"  {done}/{len(jobs)}", flush=True)
