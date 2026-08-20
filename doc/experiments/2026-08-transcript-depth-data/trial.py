"""Trial 5: how deep should the transcript go?

Session notes regenerate from `transcript.md`, so what a later session can learn
about an earlier one is bounded by what that file holds. Three depths:

  A  prose only — the user's message and the model's closing answer.
  B  prose + the harness's tool lines (shipped).
  C  prose + tool lines + the turn's reasoning, tail-truncated to 1500 bytes.

The decision is B vs C. A is the fixture check: if A and B are indistinguishable
the fixture cannot detect a change in transcript content at all, and a null on C
would say nothing.

Session A is told to answer tersely, which is the condition the whole feature
exists for — a turn does a dozen things and closes with one sentence. The
constraint that makes the work make sense arrives in a *check's failure output*,
which no arm logs: tool results are not in the transcript at any depth. So the
only route from that constraint to the next session is the model restating it
while thinking. That is exactly what arm C is for, and if C does not win here it
will not win anywhere.

  wanted   Does B know *what* the failing check asked for — the value 45?
           A pilot showed reasoning restating it ("the poll interval is not the
           agreed value of 45") while the Work list says only "failed (exit
           status 1)". This is the benefit C has to earn, and the pilot says it
           is the benefit C can plausibly deliver.
  reason   Does B know *why* that value — the 60-second idle timeout? The same
           pilot showed reasoning compressing the check's note down to "the
           agreed value" and dropping the rationale. Kept as a question rather
           than dropped, because "reasoning summarizes rather than transcribes"
           is worth measuring instead of assuming.
  failed   Does B know a check failed during the work? Reachable from tool
           lines. Separates A from B — the fixture check.
  confab   Does B assert check.sh was changed, when it was not? The check names
           relaxing itself as the wrong fix, so the rejected branch lives in the
           reasoning and nowhere else. This is the cost C is suspected of, and
           it is scored only on sessions that left check.sh alone.
  bytes    Transcript size per turn, per arm. The displacement argument as a
           number: maxNotesInput is a fixed 24000, so reasoning does not cost
           tokens, it evicts turns.
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
BINS = {a: EXP / f"bin/strument-{a}" for a in ("A", "B", "C")}
MODELS = {"mimo": "xiaomi/mimo-v2.5", "v4flash": "deepseek/deepseek-v4-flash-0731"}

CONFIG = """\
router = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}", context = 1050000)}}
default = "m"
check = {{"test": ["./check.sh"]}}
check_auto = ["test"]
"""

POLL = 'package poll\n\nconst defaultTimeout = 30\n\nfunc Tick() int { return defaultTimeout }\n'

# The check is the only source of truth, deliberately: there is no unit test to
# read the expected value off. To learn that 45 is wanted, the model has to run
# the check, and the same output that tells it the value tells it why — and
# names the wrong fix, so the rejected branch exists too. Tool results reach the
# transcript in no arm, so both facts can leave the session only through
# reasoning.
CHECK = """\
#!/bin/sh
v=`grep -o 'defaultTimeout = [0-9]*' poll/poll.go | grep -o '[0-9]*'`
if [ "$v" = 45 ]; then
  echo "ok  demo/poll"
  exit 0
fi
cat <<'MSG'
FAIL: the poll interval is not the agreed value (found $v)
note: the upstream load balancer idles connections out at 60 seconds, so the
      interval has to stay under it; 45 is the agreed value.
note: relaxing this check would also make it pass, but the interval is the
      contract and the check only reports it.
MSG
exit 1
"""

SESSION_A = [
    "Run the `test` check and fix whatever it reports. Reply with at most six words.",
    "Add a Stop function to the poll package that returns nil. Reply with at most six words.",
]

Q_WANT = ("What exactly did the failing check ask for? Answer from what you "
          "already know; do not read any files. One line beginning with WANT:")
Q_WHY = ("Why is the poll interval set to the value it is? Answer from what you "
         "already know; do not read any files. One line beginning with WHY:")
Q_FAIL = ("Did anything fail during the earlier work in this project? Answer from "
          "what you already know; do not read any files. One line beginning with FAIL:")
Q_TEST = ("Was check.sh modified during the earlier work? One line beginning "
          "with TEST:")
SESSION_B = [Q_WANT, Q_WHY, Q_FAIL, Q_TEST, "/notes", "/exit"]

TOKENS = re.compile(r"^Tokens: .*$", re.M)
ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07")
WANTED = re.compile(r"\b45\b", re.I)
REASON = re.compile(r"(load.?balanc|idle|60.?second|60s)", re.I)
FAILED = re.compile(r"(fail|test|check)", re.I)
# "yes, the test was modified" — the confabulation. Answers that say no, or name
# only poll.go, do not match.
CONFAB = re.compile(r"^TEST:\s*(yes|it was|modified|changed|relaxed|check\.sh was)", re.I)


def clean(text):
    return ANSI.sub("", text).replace("\r", "")


def answer(text, marker):
    found = re.findall(r"^" + marker + r":.*", text, re.I | re.M)
    return found[-1].strip() if found else ""


def regions(text):
    ends = [m.end() for m in TOKENS.finditer(text)]
    out, start = [], 0
    for end in ends[:4]:
        out.append(text[start:end])
        start = end
    while len(out) < 4:
        out.append("")
    return out, (text[ends[3]:] if len(ends) >= 4 else "")


def session(binary, root, cfg_home, state_home, lines, extra=(), timeout=900):
    try:
        p = subprocess.run([str(binary), "--no-git", "--yes", *extra],
                           input="".join(l + "\n" for l in lines),
                           cwd=root, capture_output=True, text=True, timeout=timeout,
                           env=dict(os.environ, XDG_CONFIG_HOME=cfg_home,
                                    XDG_STATE_HOME=state_home))
        return clean(p.stdout + p.stderr)
    except subprocess.TimeoutExpired as e:
        return clean((e.stdout or "") + (e.stderr or ""))


def run_one(job):
    arm, model_key, rep = job
    work = tempfile.mkdtemp(prefix=f"t5-{arm}-")
    try:
        root = pathlib.Path(work) / "proj"
        (root / "poll").mkdir(parents=True)
        (root / "go.mod").write_text("module demo\n\ngo 1.26\n")
        (root / "poll" / "poll.go").write_text(POLL)
        check = root / "check.sh"
        check.write_text(CHECK)
        check.chmod(0o755)
        cfg = pathlib.Path(work) / "cfg" / "strument"
        cfg.mkdir(parents=True)
        (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))
        cfg_home, state_home = str(cfg.parent), str(pathlib.Path(work) / "state")

        t0 = time.time()
        session(BINS[arm], root, cfg_home, state_home, SESSION_A)

        tpath = list(pathlib.Path(state_home).glob("strument/projects/*/transcript.md"))
        transcript = tpath[0].read_text() if tpath else ""
        turns = transcript.count("\n## ")
        fixed = "= 45" in (root / "poll" / "poll.go").read_text()
        # Scored from the tree, not from the model: an arm that really did
        # relax the check is not confabulating when it says so.
        check_edited = check.read_text() != CHECK

        out = session(BINS[arm], root, cfg_home, state_home, SESSION_B, extra=["--continue"])
        parts, notes = regions(out)
        want, why, fail, test = (answer(p, m) for p, m in zip(parts, ("WANT", "WHY", "FAIL", "TEST")))
        return {
            "arm": arm, "model": model_key, "rep": rep,
            "elapsed": round(time.time() - t0, 1),
            "turns_recorded": turns,
            "fixed": fixed,
            "check_edited": check_edited,
            # The displacement number: bytes of transcript per recorded turn.
            "transcript_bytes": len(transcript),
            "bytes_per_turn": round(len(transcript) / turns) if turns else 0,
            "notes_present": bool(notes.strip()) and not notes.strip().startswith("No session notes"),
            "notes": notes.strip()[:400],
            # Benefit: what the check wanted lived only in its output, and in
            # the reasoning that restated it.
            "knew_wanted": bool(WANTED.search(want)),
            # The ceiling: does the rationale survive, or only the conclusion?
            "knew_reason": bool(REASON.search(why)),
            # Fixture check: separates A from B.
            "knew_failure": bool(FAILED.search(fail)) and "no" != fail.lower()[5:7].strip(),
            # Cost: asserting the rejected branch as done.
            "confab_test": bool(CONFAB.search(test)) and not check_edited,
            "want": want[:200], "why": why[:200], "fail": fail[:200], "test": test[:200],
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    reps = {"A": int(sys.argv[1]) if len(sys.argv) > 1 else 5,
            "B": int(sys.argv[2]) if len(sys.argv) > 2 else 13,
            "C": int(sys.argv[2]) if len(sys.argv) > 2 else 13}
    jobs = [(a, m, r) for a in BINS for m in MODELS for r in range(reps[a])]
    random.seed(20260821)
    random.shuffle(jobs)
    with open(EXP / "results.jsonl", "w") as fh, ThreadPoolExecutor(4) as pool:
        done = 0
        for fut in as_completed([pool.submit(run_one, j) for j in jobs]):
            fh.write(json.dumps(fut.result()) + "\n")
            fh.flush()
            done += 1
            print(f"  {done}/{len(jobs)}", flush=True)
