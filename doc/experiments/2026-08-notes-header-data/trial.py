"""Trial 4: does telling the model not to answer state questions from the notes
stop it doing so, and what does the extra sentence cost?

Trial 2 (../2026-08-session-notes.md) shipped session notes on an 8/8 benefit
and a 3/8 harm: three sessions answered a question about the *current* state of
the code from a stale note without reading the file. The header's conflict rule
("where they disagree with what you find in the files, the files are right")
did not prevent it, because the failure is upstream of noticing a conflict —
the model never looked. That write-up left a candidate sentence untried:

    Do not answer questions about the current state of the code from these
    notes. Read the file. The notes say what was decided, not what is there now.

The arms are two binaries differing only in that sentence.

  harm      Does B assert the note's stale name when the file says otherwise?
            This is the metric the candidate exists to move.
  benefit   Does B still know the *reason* a decision was made? The candidate
            must not cost this; it is why notes exist at all.
  cost      Reads of a file B already has pinned, on questions no file can
            answer. "Read the file" is a cheap sentence to write and an
            expensive one to obey on every question.

Session B asks all three in one process, so every session scores every metric,
and the reads are attributed to the question that was on screen when they
happened.
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
BINS = {"base": EXP / "bin/strument-base", "check": EXP / "bin/strument-check"}
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

# One session B, three questions, then /notes so the run records what the model
# was actually given (the notes are regenerated per session by the weak model,
# so they are not the same text twice).
Q_WHY = ("Why is the poll interval set to the value it is? Answer from what you "
         "already know; do not read any files. One line beginning with WHY:")
Q_NAME = ("What is the poll constant in poll/poll.go called right now, and what "
          "is its value? One line beginning with NAME:")
Q_ALT = ("Was any other value for it considered and rejected in the earlier "
         "work? One line beginning with ALT:")
SESSION_B = [Q_WHY, Q_NAME, Q_ALT, "/notes", "/exit"]

# Region boundaries. Piped stdin does not echo the prompt, so the only marker
# of where one turn ends is the per-turn token line the harness prints. Three
# questions, three token lines; the /notes output follows the third.
TOKENS = re.compile(r"^Tokens: .*$", re.M)

ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07")
REASON = re.compile(r"(load.?balanc|idle)", re.I)
STALE = re.compile(r"pollInterval", re.I)
TRUE = re.compile(r"defaultTimeout", re.I)
# A read as the harness renders it: "Read poll/poll.go (5 lines)".
READ = re.compile(r"^Read \S.*\((?:\d+ lines?|lines \d+)", re.M)


def clean(text):
    """Strip what the terminal writes, not what the model wrote.

    The carriage return matters as much as the escape: clearWaiting emits
    "\\r\\x1b[K" before a tool line, and stripping only the escape leaves a
    "\\r" that breaks every ^-anchored pattern. A scorer broken this way turned
    a real effect into a clean null once already (../experimenting.md).
    """
    return ANSI.sub("", text).replace("\r", "")


def answer(text, marker):
    """The model's answer line for a marker.

    Anchored at the start of a line: the reasoning that precedes an answer
    discusses the question freely, and an unanchored match happily scores the
    model's deliberation instead of its answer.
    """
    found = re.findall(r"^" + marker + r":.*", text, re.I | re.M)
    return found[-1].strip() if found else ""


def regions(text):
    """Split B's output into one stretch per question, plus the /notes tail."""
    ends = [m.end() for m in TOKENS.finditer(text)]
    out, start = [], 0
    for end in ends[:3]:
        out.append(text[start:end])
        start = end
    while len(out) < 3:
        out.append("")
    return out, (text[ends[2]:] if len(ends) >= 3 else "")


def session(binary, root, cfg_home, state_home, lines, extra=(), timeout=600):
    argv = [str(binary), "--no-git", "--yes", *extra]
    try:
        p = subprocess.run(argv, input="".join(l + "\n" for l in lines),
                           cwd=root, capture_output=True, text=True, timeout=timeout,
                           env=dict(os.environ, XDG_CONFIG_HOME=cfg_home,
                                    XDG_STATE_HOME=state_home))
        return clean(p.stdout + p.stderr)
    except subprocess.TimeoutExpired as e:
        return clean((e.stdout or "") + (e.stderr or ""))


def run_one(job):
    arm, model_key, rep = job
    work = tempfile.mkdtemp(prefix=f"t4-{arm}-")
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
        session(BINS[arm], root, cfg_home, state_home, SESSION_A)

        # The tree moves behind the notes: A's rename is reverted, so the file
        # says defaultTimeout = 30 while the notes describe pollInterval = 45.
        (root / "poll" / "poll.go").write_text(ORIGINAL)

        out = session(BINS[arm], root, cfg_home, state_home, SESSION_B, extra=["--continue"])
        turns, notes = regions(out)
        why, name, alt = (answer(r, m) for r, m in zip(turns, ("WHY", "NAME", "ALT")))
        r_why, r_name, r_alt = (len(READ.findall(r)) for r in turns)
        return {
            "arm": arm, "model": model_key, "rep": rep,
            "elapsed": round(time.time() - t0, 1),
            # A row where the three questions did not become three turns is not
            # scoreable; recorded rather than dropped silently.
            "turns": len(TOKENS.findall(out)),
            # Did the notes reach the model at all? Nothing below means
            # anything if they did not.
            "notes_present": bool(REASON.search(notes)) or bool(STALE.search(notes)),
            "notes": notes.strip()[:400],
            "answered": {"why": bool(why), "name": bool(name), "alt": bool(alt)},
            # Harm, the metric the candidate targets.
            "stale_assert": bool(STALE.search(name)) and not TRUE.search(name),
            "said_true": bool(TRUE.search(name)),
            # Benefit, which the candidate must not cost.
            "knew_reason": bool(REASON.search(why)),
            # Cost. reads_why is against an explicit "do not read any files",
            # so it is the harsh test; reads_alt is the ordinary one — no file
            # can answer whether a value was considered and rejected.
            "reads": {"why": r_why, "name": r_name, "alt": r_alt},
            "why": why[:200], "name": name[:200], "alt": alt[:200],
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    reps = int(sys.argv[1]) if len(sys.argv) > 1 else 12
    jobs = [(a, m, r) for a in BINS for m in MODELS for r in range(reps)]
    # Shuffled, because running every base session and then every check session
    # confounds the arm with the hour it ran in. See ../experimenting.md.
    random.seed(20260820)
    random.shuffle(jobs)
    with open(EXP / "results.jsonl", "w") as fh, ThreadPoolExecutor(4) as pool:
        done = 0
        for fut in as_completed([pool.submit(run_one, j) for j in jobs]):
            fh.write(json.dumps(fut.result()) + "\n")
            fh.flush()
            done += 1
            print(f"  {done}/{len(jobs)}", flush=True)
