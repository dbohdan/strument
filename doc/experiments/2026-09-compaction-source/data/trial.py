"""Where should a compaction summary's input come from?

Arms are one binary and one flag, so they differ in the summarizer's input and
in nothing else: `fold` is HEAD's behaviour (summarize the messages, which hold
the previous summary), `record` summarizes the session record instead.

Adapted from doc/experiments/2026-08-compaction/data/trial.py, which established
the fixture shape: `context=16384` puts maxChatHistoryTokens at its floor, so
ordinary tool work overflows the history budget and forces compaction, while
16384 stays far above any real prompt so checkTokens never fires.

Three things that trial did not need and this one does:

  * More folds. The claim is about *compounding*, so the burying turns are
    doubled and a session that folded fewer than MIN_FOLDS times is excluded.
  * Two planted facts. One in turn 1, which has to survive every fold; one in
    the middle, which `record`'s head-and-tail sampling can drop and `fold`'s
    cumulative summary may have already distilled. The second is the
    counter-metric.
  * Confabulation counted apart from loss. The probes ask for UNKNOWN by name,
    so "said it did not know" is a marker rather than an inference.

Compaction is counted from the session record's side_call rows, not from the
rendered stream: the stream is the thing being changed, and an instrument made
of it is the handbook's first failure.
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
ARMS = ("fold", "record")
MODELS = {"mimo": "xiaomi/mimo-v2.5", "glm": "z-ai/glm-5.3-flash"}
MIN_FOLDS = 4

# reasoning="low" on every model, and checked below that it took: a model
# spending its budget thinking looks exactly like an API failure.
CONFIG = """\
router = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}", context = 16384, reasoning = "low")}}
default = "m"
"""


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
    "api/route.go": "package api\n\n" + _decls("api", 50, "f"),
    "README.md": "# demo\n\nA small service.\n",
}

# Turn 1 plants `reason`; turn 6 plants `middle`; the rest bury them with
# ordinary tool work. Neither fact is recoverable from the files, which is what
# makes them the targets: 45 is in the code, the load balancer and the rejected
# name are only ever in the conversation.
TURNS = [
    "Rename defaultTimeout to pollInterval everywhere, including watch.go. "
    "Use the value 45, not 30. Record in README.md that we chose 45 seconds "
    "because the upstream load balancer idles connections out at 60.",
    "Add a Delete method to store.Store and use it nowhere yet.",
    'Add a Ping function to the api package that returns "pong".',
    "Grep the whole project for any remaining reference to the old constant name "
    "and report what you find.",
    "Add a Len method to store.Store returning the number of items.",
    # `middle`, planted once and never written down.
    "One more note on naming: we considered calling the constant retryAfter and "
    "rejected it, because it would be confused with the HTTP Retry-After header. "
    "Do not change any code for this; just note it.",
    "Add a Reset function to the poll package that returns the interval to its default.",
    "List every exported function in the api package.",
    'Add a Keys method to store.Store returning a []string.',
    "Grep for any TODO in the project and report what you find.",
    "Add a Close method to store.Store that clears the map.",
    # The probes. Both arms get identical instructions, so the wording cannot
    # favour either. UNKNOWN is asked for by name so honest loss is a marker
    # rather than something inferred from an absence.
    #
    # One line, with no newline in it. The pilot's version was three lines, and
    # the REPL reads a line at a time — so it arrived as three messages, the
    # model answered the first fragment before seeing the rest, and said so:
    # "it seems like the message was cut off". Both answers still came out
    # right, which is how an equipment fault survives a glance at the output.
    "Two questions about earlier decisions in this session; answer from what you "
    "already know and do not read any files. Reply with exactly two lines: the "
    "first beginning with the exact text REASON: followed by why we picked the "
    "poll interval value we did, or the single word UNKNOWN if you do not know; "
    "the second beginning with the exact text NAME: followed by which name we "
    "considered and rejected for that constant and why, or the single word "
    "UNKNOWN if you do not know.",
]

# The REPL reads one line per message, so a turn containing a newline is two
# turns. Checked here rather than discovered in the results: the pilot spent
# ten minutes proving that a static check would have caught it for free.
for _i, _t in enumerate(TURNS):
    if "\n" in _t:
        raise SystemExit(f"turn {_i} contains a newline and would be sent as several messages")

REASON_LINE = re.compile(r"^ *REASON:.*$", re.M)
NAME_LINE = re.compile(r"^ *NAME:.*$", re.M)

# Scored in this order, and the order is the definition: naming the fact wins,
# then an explicit UNKNOWN is honest loss, and anything else substantive is an
# invention. A reply that both names the fact and hedges is a recall.
REASON_HIT = re.compile(r"(load.?balanc|idle)", re.I)
NAME_HIT = re.compile(r"retry.?after", re.I)
UNKNOWN = re.compile(r"\bunknown\b", re.I)


def classify(line, hit):
    """Classify one marked answer into exactly one column.

    The order is the definition: naming the fact wins, then an explicit
    UNKNOWN is honest loss, and anything else substantive is an invention. A
    reply that names the fact and also hedges is a recall, because the fact
    reached the reader.
    """
    if not line:
        return "absent"
    body = line.split(":", 1)[1].strip() if ":" in line else ""
    if not body:
        return "absent"
    if hit.search(body):
        return "recalled"
    if UNKNOWN.search(body):
        return "declined"
    return "confabulated"


def build(work):
    root = pathlib.Path(work) / "proj"
    for rel, body in FILES.items():
        p = root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(body)
    return root


def record_rows(state_root):
    """Every JSONL record row the session wrote, across its segments."""
    rows = []
    for seg in sorted(pathlib.Path(state_root).glob("strument/projects/*/sessions/*/log/*.jsonl")):
        for line in seg.read_text(errors="replace").splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                rows.append(json.loads(line))
            except json.JSONDecodeError:
                break  # a tail cut off mid-write
    return rows


def run_one(job):
    arm, model_key, rep = job
    work = tempfile.mkdtemp(prefix=f"cs-{arm}-")
    try:
        root = build(work)
        cfg = pathlib.Path(work) / "cfg" / "strument"
        cfg.mkdir(parents=True)
        (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))
        state = pathlib.Path(work) / "state"
        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = str(cfg.parent)
        env["XDG_STATE_HOME"] = str(state)

        script = "".join(t + "\n" for t in TURNS) + "/exit\n"
        t0 = time.time()
        try:
            proc = subprocess.run(
                [str(BIN), "--no-git", "--yes=all", f"--compaction-source={arm}"],
                input=script, cwd=root, env=env,
                capture_output=True, text=True, timeout=1800)
            out, rc = proc.stdout + proc.stderr, proc.returncode
        except subprocess.TimeoutExpired as e:
            out = (e.stdout or "") + (e.stderr or "")
            rc = -9

        rows = record_rows(state)
        folds = [r for r in rows if r.get("type") == "side_call" and r.get("call") == "chat summary"]
        turns = [r for r in rows if r.get("type") == "turn"]
        reasoning = [r for r in rows if r.get("type") == "reasoning"]

        reason_lines = REASON_LINE.findall(out)
        name_lines = NAME_LINE.findall(out)
        reason = reason_lines[-1] if reason_lines else ""
        name = name_lines[-1] if name_lines else ""

        return {
            "arm": arm, "model": model_key, "rep": rep, "returncode": rc,
            "elapsed": round(time.time() - t0, 1),
            # From the record, not the stream: the stream is what is being changed.
            "folds": len(folds),
            "fold_input_tokens": sum(r.get("sent", 0) for r in folds),
            "fold_failures": sum(1 for r in folds if r.get("outcome") != "ok"),
            "turns_recorded": len(turns),
            "session_cost": round(sum(r.get("cost", 0.0) for r in turns)
                                  + sum(r.get("cost", 0.0) for r in folds), 6),
            # Pinned low; a model that ignored it shows up here as bulk.
            "reasoning_chars": sum(len(r.get("text", "")) for r in reasoning),
            "reason_score": classify(reason, REASON_HIT),
            "name_score": classify(name, NAME_HIT),
            "reason_line": reason.strip()[:300],
            "name_line": name.strip()[:300],
            "stdout": out,
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    reps = int(sys.argv[1]) if len(sys.argv) > 1 else 12
    only = sys.argv[2] if len(sys.argv) > 2 else None
    models = {only: MODELS[only]} if only else MODELS
    jobs = [(a, m, r) for a in ARMS for m in models for r in range(reps)]
    random.seed(20260921)
    random.shuffle(jobs)  # the arm must not be confounded with the hour it ran
    out_path = EXP / ("pilot.jsonl" if reps <= 2 else "results.jsonl")
    with open(out_path, "w") as fh, ThreadPoolExecutor(6) as pool:
        done = 0
        for fut in as_completed([pool.submit(run_one, j) for j in jobs]):
            fh.write(json.dumps(fut.result()) + "\n")
            fh.flush()
            done += 1
            print(f"  {done}/{len(jobs)}", flush=True)
