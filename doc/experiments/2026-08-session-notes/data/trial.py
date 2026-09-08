"""Trial 2: do session notes help, and what do they cost when they go stale?

Session A works and is killed. The tree is then changed *behind* the notes —
A's rename is reverted — and session B is asked a question the notes answer
confidently and wrongly.

  benefit  Does B know the *reason* for a decision without re-asking? The
           reason lives only in the conversation; the code cannot supply it.
  harm     Does B assert the stale fact the notes carry, when the file now
           says otherwise? This is the failure the feature can actually cause,
           and the notes header is written to prevent it ("where they disagree
           with what you find in the files, the files are right").

Arms are the same binary with and without notes, so nothing but the notes
differs. Both questions are asked in the same session B, so each session
scores both metrics.
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
BIN = EXP / "bin/strument"
MODELS = {"mimo": "xiaomi/mimo-v2.5", "v4flash": "deepseek/deepseek-v4-flash-0731"}

CONFIG = """\
router = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}", context = 1050000)}}
default = "m"
"""

ORIGINAL = ('package poll\n\nconst defaultTimeout = 30\n\n'
            'func Tick() int { return defaultTimeout }\n')

SESSION_A = [
    "Rename defaultTimeout to pollInterval in poll/poll.go. Use 45, not 30, "
    "because the upstream load balancer idles connections out at 60 seconds.",
    "Add a Stop function to the poll package that returns nil.",
    'Add a Ping function to the poll package that returns "pong".',
]

# Asked in one session B. The first probes benefit, the second probes harm.
SESSION_B = [
    "Why is the poll interval set to the value it is? Answer from what you "
    "already know; do not read any files. One line beginning with WHY:",
    "What is the poll constant in poll/poll.go called right now, and what is "
    "its value? One line beginning with NAME:",
]

WHY = re.compile(r"WHY:.*", re.I)
NAME = re.compile(r"NAME:.*", re.I)
ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07")
REASON = re.compile(r"(load.?balanc|idle)", re.I)
STALE = re.compile(r"pollInterval", re.I)
TRUE = re.compile(r"defaultTimeout", re.I)


def session(binary, root, cfg_home, state_home, lines, timeout=600):
    try:
        p = subprocess.run([str(binary), "--no-git", "--yes"],
                           input="".join(l + "\n" for l in lines),
                           cwd=root, capture_output=True, text=True, timeout=timeout,
                           env=dict(os.environ, XDG_CONFIG_HOME=cfg_home,
                                    XDG_STATE_HOME=state_home))
        return ANSI.sub("", p.stdout + p.stderr)
    except subprocess.TimeoutExpired as e:
        return ANSI.sub("", (e.stdout or "") + (e.stderr or ""))


def run_one(job):
    arm, model_key, rep = job
    work = tempfile.mkdtemp(prefix=f"t2-{arm}-")
    try:
        root = pathlib.Path(work) / "proj"
        (root / "poll").mkdir(parents=True)
        (root / "go.mod").write_text("module demo\n\ngo 1.26\n")
        (root / "poll" / "poll.go").write_text(ORIGINAL)
        cfg = pathlib.Path(work) / "cfg" / "strument"
        cfg.mkdir(parents=True)
        (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))
        cfg_home, state_home = str(cfg.parent), str(pathlib.Path(work) / "state")

        t0 = time.time()
        session(BIN, root, cfg_home, state_home, SESSION_A)

        notes_path = list(pathlib.Path(state_home).glob("strument/projects/*/notes.md"))
        had_notes = bool(notes_path)
        notes_text = notes_path[0].read_text() if had_notes else ""
        if arm == "off" and had_notes:
            notes_path[0].unlink()   # the only difference between the arms

        # The tree moves behind the notes.
        (root / "poll" / "poll.go").write_text(ORIGINAL)

        out_b = session(BIN, root, cfg_home, state_home, SESSION_B + ["/exit"])
        why = (WHY.findall(out_b) or [""])[-1]
        name = (NAME.findall(out_b) or [""])[-1]
        return {
            "arm": arm, "model": model_key, "rep": rep,
            "elapsed": round(time.time() - t0, 1),
            "notes_written": had_notes,
            "notes": notes_text.strip()[:400],
            "answered_why": bool(why), "answered_name": bool(name),
            # Benefit: the reason survived without re-reading anything.
            "knew_reason": bool(REASON.search(why)),
            # Harm: asserted the note's stale name over the file's real one.
            "stale_assert": bool(STALE.search(name)) and not TRUE.search(name),
            "said_true": bool(TRUE.search(name)),
            "why": why.strip()[:200], "name": name.strip()[:200],
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    reps = int(sys.argv[1]) if len(sys.argv) > 1 else 4
    jobs = [(a, m, r) for a in ("on", "off") for m in MODELS for r in range(reps)]
    random.seed(20260817)
    random.shuffle(jobs)
    with open(EXP / "results.jsonl", "w") as fh, ThreadPoolExecutor(4) as pool:
        done = 0
        for fut in as_completed([pool.submit(run_one, j) for j in jobs]):
            fh.write(json.dumps(fut.result()) + "\n")
            fh.flush()
            done += 1
            print(f"  {done}/{len(jobs)}", flush=True)
