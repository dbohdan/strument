"""Trial 6: how much conversation should the commit-message model see?

commitContext feeds the weak model `curMessages` — this turn and nothing else.
The reason for a change is usually settled a turn or two before the change
lands, so a model faithfully following the prompt's instruction to add a body
"only for something the diff cannot say" will correctly write nothing: it does
not know the reason either. Widening the context to a byte-bounded tail of
doneMessages is the candidate.

  narrow  curMessages only (shipped)
  wide    an 8000-byte tail of doneMessages, then curMessages

Three turns, each producing one commit, each scoring a different thing:

  T1  states the reason and does *unrelated* work. The reason is now in the
      session and not in any diff.
  T2  makes the change the reason was for. Its commit body should carry the
      reason. BENEFIT — and it is a count, not a judgment: the reason is a
      specific fact ("the load balancer idles at 60 seconds") that is nowhere
      in the tree, so a body either names it or does not.
  T3  a trivial addition with no reason stated anywhere, ever. COUNTER-METRICS:
      does a body appear at all (noise), and does it assert a cause (a reason
      invented to fill the wider context).

Cost is the session's own accounting: a wider context is a bigger side request
on every turn, whether or not it earns a body.
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
BINS = {a: EXP / f"bin/strument-{a}" for a in ("narrow", "wide")}
MODELS = {"mimo": "xiaomi/mimo-v2.5", "v4flash": "deepseek/deepseek-v4-flash-0731"}

CONFIG = """\
router = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}", context = 1050000)}}
default = "m"
"""

POLL = 'package poll\n\nconst defaultTimeout = 30\n\nfunc Tick() int { return defaultTimeout }\n'

SESSION = [
    # T1: the reason enters the session, attached to work that is not the change.
    "We will need to raise the poll interval soon. The upstream load balancer "
    "idles connections out at 60 seconds, so it has to stay under that, and 45 "
    "is the value we agreed on. For now, just add a Stop function to the poll "
    "package that returns nil.",
    # T2: the change the reason was for. Its commit body is the metric.
    "Now make that interval change in poll/poll.go.",
    # T3: no reason exists for this, anywhere.
    'Add a Ping function to the poll package that returns "pong".',
    "/exit",
]

ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07")
REASON = re.compile(r"(load.?balanc|idle|60.?second|60s)", re.I)
# A stated cause, for the turn where no cause exists.
CAUSE = re.compile(r"\b(because|so that|in order to|to avoid|to ensure|rationale|"
                   r"the reason|since the|which allows)\b", re.I)
# Git trailers are not a body. Every commit here ends with "Assisted-by: …",
# so scoring "did a body appear" on the raw text says yes every time.
TRAILER = re.compile(r"^[A-Za-z][A-Za-z-]*:\s")


def body_text(raw):
    lines = raw.splitlines()
    while lines and (not lines[-1].strip() or TRAILER.match(lines[-1].strip())):
        lines.pop()
    return "\n".join(lines).strip()


def clean(text):
    return ANSI.sub("", text).replace("\r", "")


def git(root, *args):
    return subprocess.run(["git", *args], cwd=root, capture_output=True, text=True).stdout


def run_one(job):
    arm, model_key, rep = job
    work = tempfile.mkdtemp(prefix=f"t6-{arm}-")
    try:
        root = pathlib.Path(work) / "proj"
        (root / "poll").mkdir(parents=True)
        (root / "go.mod").write_text("module demo\n\ngo 1.26\n")
        (root / "poll" / "poll.go").write_text(POLL)
        for args in (("init", "-q"), ("config", "user.email", "t@example.invalid"),
                     ("config", "user.name", "Trial"), ("add", "-A"),
                     ("-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial")):
            subprocess.run(["git", *args], cwd=root, capture_output=True, text=True)

        cfg = pathlib.Path(work) / "cfg" / "strument"
        cfg.mkdir(parents=True)
        (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))

        t0 = time.time()
        p = subprocess.run([str(BINS[arm]), "--yes"],
                           input="".join(l + "\n" for l in SESSION),
                           cwd=root, capture_output=True, text=True, timeout=900,
                           env=dict(os.environ, XDG_CONFIG_HOME=str(cfg.parent),
                                    XDG_STATE_HOME=str(pathlib.Path(work) / "state")))
        out = clean(p.stdout + p.stderr)

        # Commits oldest-first, excluding the fixture's own.
        log = git(root, "log", "--reverse", "--format=%H%x00%s%x00%b%x1e")
        # .strip() before splitting: the record separator leaves a newline on the
        # front of every record after the first, and a hash with a leading
        # newline makes `git show` return nothing at all — silently, so every
        # per-turn metric scores as "commit not found" rather than failing.
        commits = [c.strip().split("\x00") for c in log.split("\x1e") if c.strip()]
        commits = [c for c in commits if c[1].strip() != "initial"]
        subjects = [c[1].strip() for c in commits]
        bodies = [c[2].strip() for c in commits]

        # Commits are matched to turns by what they touched, not by position:
        # a turn that commits nothing would otherwise shift every later score.
        # T2 is the commit whose diff changes the constant to 45; T3 the one
        # that adds Ping. Located by diff content, so an extra or missing commit
        # cannot silently misalign the metrics.
        t2 = t3 = None
        merged = False
        for h, s_, b in commits:
            diff = git(root, "show", "--format=", h)
            if re.search(r"^\+.*= 45", diff, re.M):
                t2 = (s_.strip(), body_text(b))
                # Some sessions do T1's and T2's work in one turn despite being
                # told not to. There the reason is in curMessages already, so
                # the session tests nothing about widening — recorded, and
                # excluded from the benefit metric rather than left to dilute it.
                merged = bool(re.search(r"^\+.*func Stop", diff, re.M))
            if re.search(r"^\+.*func Ping", diff, re.M):
                t3 = (s_.strip(), body_text(b))

        cost = 0.0
        if m := re.findall(r"\$([\d.]+) session", out):
            cost = float(m[-1])
        return {
            "arm": arm, "model": model_key, "rep": rep,
            "elapsed": round(time.time() - t0, 1),
            "commits": len(commits), "subjects": subjects,
            # The weak model returning "" leaves the fallback subject; counted,
            # because a failed side call is not a short message.
            "empty_messages": sum(1 for s_ in subjects if "no commit message" in s_),
            "found_t2": t2 is not None, "found_t3": t3 is not None,
            "merged_turns": merged,
            # BENEFIT: the reason is nowhere in the diff or the tree.
            "t2_has_reason": bool(t2 and REASON.search(t2[1])),
            "t2_has_body": bool(t2 and t2[1]),
            "t2_body": (t2[1][:300] if t2 else ""),
            "t2_subject": (t2[0] if t2 else ""),
            # COUNTER-METRICS on the turn with no reason to give.
            "t3_has_body": bool(t3 and t3[1]),
            "t3_asserts_cause": bool(t3 and CAUSE.search(t3[1])),
            "t3_body": (t3[1][:300] if t3 else ""),
            "session_cost": cost,
            "bodies": [b[:160] for b in bodies],
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    reps = int(sys.argv[1]) if len(sys.argv) > 1 else 14
    jobs = [(a, m, r) for a in BINS for m in MODELS for r in range(reps)]
    random.seed(20260822)
    random.shuffle(jobs)
    with open(EXP / "results.jsonl", "w") as fh, ThreadPoolExecutor(4) as pool:
        done = 0
        for fut in as_completed([pool.submit(run_one, j) for j in jobs]):
            fh.write(json.dumps(fut.result()) + "\n")
            fh.flush()
            done += 1
            print(f"  {done}/{len(jobs)}", flush=True)
