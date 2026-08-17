"""Randomized live A/B test for conservative compaction hysteresis.

The baseline disables slack with STRUMENT_COMPACTION_SLACK=0. The treatment uses
Strument's default bounded trigger slack while keeping the same compaction target.
The planted reason is conversation-only; the source contains the value but not the
load-balancer rationale. Run from the repository root after building `strument`:

    OPENROUTER_API_KEY=... python3 doc/experiments/2026-08-compaction-hysteresis-data/trial.py 6

Raw output is retained in each result record so the scorer can be improved without
spending on another run.
"""

import importlib.util
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

ROOT = pathlib.Path(__file__).resolve().parents[3]
EXP = pathlib.Path(__file__).parent
BINARY = ROOT / "strument"
BASE_TRIAL = EXP.parent / "2026-08-compaction-data" / "trial.py"
spec = importlib.util.spec_from_file_location("base_trial", BASE_TRIAL)
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)

ARMS = {"base": "0", "hysteresis": "128"}
MODELS = {"mimo": "xiaomi/mimo-v2.5", "v4flash": "deepseek/deepseek-v4-flash-0731"}
CONFIG = """\
router = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}", context = 16384)}}
default = "m"
"""
ANSWER_LINE = re.compile(r"^ *ANSWER:.*$", re.M)
REASON = re.compile(r"load.?balanc|idle", re.I)
VALUE = re.compile(r"\b45\b")
# These phrases describe reasons that were not planted in the conversation. Do not
# match "poll interval" and "load balancer" together: that is the target answer.
CONFABULATION = re.compile(
    r"frequent updates|server load|rate limiting|polling/processing overhead", re.I)
FATAL_OUTPUT = re.compile(
    r"Empty response received|authentication|provider error|"
    r"(?:HTTP|status|error|response)[^\n]{0,20}(?:401|403)",
    re.I,
)
CONTEXT_WARNING = re.compile(
    r"Your estimated chat context .* exceeds|Could not summarize chat history|"
    r"context deadline exceeded",
    re.I,
)


def run_one(job):
    arm, model_key, rep = job
    work = tempfile.mkdtemp(prefix=f"hyst-{arm}-")
    try:
        root = base.build(work)
        cfg = pathlib.Path(work) / "cfg" / "strument"
        cfg.mkdir(parents=True)
        (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[model_key]))
        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = str(cfg.parent)
        env["XDG_STATE_HOME"] = str(pathlib.Path(work) / "state")
        env["STRUMENT_COMPACTION_SLACK"] = ARMS[arm]

        turns = list(base.TURNS)
        # Repeat realistic work after the first recall probe so the same fact is
        # carried through several summary generations before the final probe.
        turns.extend([
            "Add a List method to store.Store and use it nowhere yet.",
            "Search the project for the poll interval and summarize the files involved.",
            "Do not read files. What was the reason for choosing the poll interval? "
            "Put the whole answer on one line beginning with the exact text ANSWER:",
        ])
        script = "".join(turn + "\n" for turn in turns) + "/exit\n"
        started = time.time()
        try:
            proc = subprocess.run(
                [str(BINARY), "--no-git", "--yes"], input=script, cwd=root,
                env=env, capture_output=True, text=True, timeout=1200)
            output, rc = proc.stdout + proc.stderr, proc.returncode
        except subprocess.TimeoutExpired as exc:
            stdout = exc.stdout if isinstance(exc.stdout, str) else (exc.stdout or b"").decode("utf-8", errors="replace")
            stderr = exc.stderr if isinstance(exc.stderr, str) else (exc.stderr or b"").decode("utf-8", errors="replace")
            output = stdout + stderr
            rc = -9

        answers = ANSWER_LINE.findall(output)
        answer = answers[-1] if answers else ""
        reports = re.findall(
            r"Chat history compacted: (\d+) tokens/(\d+) messages -> "
            r"(\d+) tokens/(\d+) messages; (\d+) summaries retained\.", output)
        costs = [float(value) for value in re.findall(r"\$([0-9.]+) session", output)]
        invalid_reasons = []
        warnings = []
        if rc != 0:
            invalid_reasons.append(f"returncode:{rc}")
        if not answers:
            invalid_reasons.append("no_answer")
        if FATAL_OUTPUT.search(output):
            invalid_reasons.append("provider_failure")
        if CONTEXT_WARNING.search(output):
            warnings.append("context_warning")
        return {
            "arm": arm, "model": model_key, "rep": rep, "returncode": rc,
            "elapsed": round(time.time() - started, 1),
            "compaction_attempts": output.count("Summarizing chat history"),
            "compactions": len(reports),
            "reports": [tuple(map(int, report)) for report in reports],
            "valid": not invalid_reasons,
            "invalid_reasons": invalid_reasons,
            "warnings": warnings,
            "answered": bool(answers),
            "recalled_reason": bool(REASON.search(answer)),
            "recalled_value": bool(VALUE.search(answer)),
            "confabulation": bool(CONFABULATION.search(answer)),
            "session_cost": costs[-1] if costs else None,
            "answer": answer.strip()[:400],
            "stdout": output,
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    reps = int(sys.argv[1]) if len(sys.argv) > 1 else 4
    jobs = [(arm, model, rep) for arm in ARMS for model in MODELS for rep in range(reps)]
    random.seed(20260817)
    random.shuffle(jobs)
    EXP.mkdir(parents=True, exist_ok=True)
    with (EXP / "results.jsonl").open("w") as results, ThreadPoolExecutor(4) as pool:
        futures = [pool.submit(run_one, job) for job in jobs]
        for done, future in enumerate(as_completed(futures), 1):
            results.write(json.dumps(future.result()) + "\n")
            results.flush()
            print(f"  {done}/{len(jobs)}", flush=True)
