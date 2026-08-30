#!/usr/bin/env python3
"""Does the `code` tool get used, and does it buy round trips?

Part 4 of doc/plans/code-mode.md. Adapted from the rig in
2026-08-skill-uptake-data/ (run.py is a shuffled, resumable runner) and the
scoring discipline of 2026-08-symbol-uptake-data/.

Arms (three binaries, same task; the 2026-08-31 follow-up to the 0/36 trial):
  BASE no-code  feature reverted (8a39b0d + the --no-history fix, tool withheld)
  AB   prompt   HEAD: prompt bullet + FilesNoFullFiles fix + new description
  DESC desc     HEAD with the {code_tools} bullet suppressed: description-only

Primary questions, pre-registered (doc/plans/code-mode.md Part 4):
  1. did the model call `code` at all — per arm, per model. The replace_all
     lesson (experimenting.md 18): a feature the model may decline is one that
     was OFFERED, not APPLIED. If uptake is 0, the trial says so and stops.
  2. model round trips per run (the `sent` count of the turn record — each is
     a full request that re-sends the context).
Counter-metrics, reported as prominently:
  - was the answer still correct (a cheaper wrong answer is a loss)
  - programs that errored; how often the model retried after an error
  - cost and steps per run.

The task requires exploring THIS repository — the measured fact behind the
fixture choice is that one-pinned-file editing tasks leave only 0.77 removable
round trips per run, while a real exploration task against this repository
left 4.0 (2026-08-symbol-uptake).
"""

import argparse
import concurrent.futures as cf
import json
import os
import pathlib
import random
import re
import shutil
import subprocess
import sys

HERE = pathlib.Path(__file__).parent
# After the 2026-08 move out of the repo (the data dir was straining project
# backup), HERE is no longer inside the checkout: name it explicitly.
REPO = pathlib.Path("/home/dbohdan/sync/projects/2026/strument")

MODELS = {
    "v4flash": "deepseek/deepseek-v4-flash-0731",
    "luna": "openai/gpt-5.6-luna",
    "qwen": "qwen/qwen3.8-27b",
    "hy3": "tencent/hy3",
    "mimo": "xiaomi/mimo-v2.5",
    "glm": "z-ai/glm-5.3-flash",
}

ARMS = {"BASE": "strument-armBASE", "AB": "strument-armAB", "DESC": "strument-armDESC"}

CONFIG = """\
router = provider(adapter = "openrouter", base_url = env("STRUMENT_BASE_URL", "https://openrouter.ai/api/v1"), api_key = env("OPENROUTER_API_KEY"), proxy = "socks5://localhost:1080")
models = {{"m": model(router, "{slug}", context = 260000)}}
default = "m"
sandbox = ""
reasoning_display = "full"
max_steps = 40
"""

# Neutral: names no tool. The model picks its own path. Requires several greps
# and reads across several files to answer from scratch -- the shape
# 2026-08-symbol-uptake used, widened so a single grep cannot settle it.
TASK = (
    "In this repository's Go code: the coder caps the size of a single tool "
    "result, caps the number of work steps per turn, and caps the chat history "
    "budget. Give the value of each of those three caps and the file the "
    "constant lives in. Put your whole answer on one line beginning with "
    "ANSWER:. Keep it brief."
)

# Pre-registered answer key, verified by hand against the tree at cdfc411
# BEFORE any run (doc/plans/code-mode.md Part 4: "with an answer key you
# verify by hand before running anything").
ANSWER_KEY = {
    "max_tool_output_bytes": {"value": "60000", "file": "toolobserve.go"},
    "max_steps": {"value": "25", "file": "coder.go"},
    "max_chat_history_tokens": {"value": "context/8", "file": "send.go"},
}


def setup_world(root, arm, model_slug):
    """One disposable world: a copy of the repo (source only), a config home."""
    root.mkdir(parents=True, exist_ok=True)
    proj = root / "proj"
    if not proj.exists():
        # The task is about this repository, so the model explores a copy of
        # it. .git is excluded: --no-git and the questions do not need it, and
        # copying it would triple the size. worlds/ and runs/ live under this
        # data dir, which is itself inside the repo — excluding them by name
        # stops a world from recursively containing the worlds of the run that
        # is creating it (the first pilot died on exactly that).
        shutil.copytree(
            REPO, proj,
            ignore=shutil.ignore_patterns(
                ".git", "reference", "attic",
                "worlds", "runs", "*.txt", "*.jsonl", "*.json",
                # The project's own .strument.star is inert here and would
                # otherwise sit in front of the model as a distractor.
                ".strument.star",
            ),
            dirs_exist_ok=True,
        )
    cfg = root / "cfg" / "strument"
    cfg.mkdir(parents=True, exist_ok=True)
    (cfg / "config.star").write_text(CONFIG.format(slug=model_slug))
    return proj, cfg.parent


def run_one(job, args):
    model_key, arm, rep = job
    tag = f"{model_key}-{arm}-{rep}"
    out_txt = HERE / "runs" / f"{tag}.txt"
    out_jsonl = HERE / "runs" / f"{tag}.jsonl"
    root = HERE / "worlds" / tag

    # The runner writes both before strument starts, and strument refuses to
    # open a JSONL path whose directory does not exist — the first pilot lost
    # runs to exactly that.
    (HERE / "runs").mkdir(exist_ok=True)

    if not out_txt.exists():
        if root.exists():
            shutil.rmtree(root)
        proj, cfg_home = setup_world(root, arm, MODELS[model_key])

        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = str(cfg_home)
        env["XDG_DATA_HOME"] = str(root / "data")
        env["XDG_STATE_HOME"] = str(root / "state")
        env["XDG_CACHE_HOME"] = str(root / "cache")
        # The key never goes into a file (CLAUDE.md). It reaches the process
        # through the environment, from the helper the user named.
        env["OPENROUTER_API_KEY"] = subprocess.run(
            ["openrouter-api-key"], capture_output=True, text=True, check=True,
        ).stdout.strip()

        cmd = [
            HERE / ARMS[arm], "chat",
            "--no-git", "--no-history", "--no-color",
            "--yes", "steps",
            "--jsonl", str(out_jsonl),
            "-m", TASK,
        ]
        try:
            p = subprocess.run(cmd, cwd=proj, env=env, capture_output=True,
                               text=True, timeout=args.timeout)
            text = p.stdout + p.stderr
        except subprocess.TimeoutExpired as e:
            # TimeoutExpired carries RAW BYTES even when subprocess.run was
            # given text=True (experimenting.md 19). Decode defensively; one
            # job's TypeError must never take the bookkeeping down.
            def dec(v):
                if v is None:
                    return ""
                return v.decode("utf-8", "replace") if isinstance(v, bytes) else v
            text = dec(e.stdout) + dec(e.stderr) + "\n[TIMEOUT]\n"
        out_txt.parent.mkdir(exist_ok=True)
        out_txt.write_text(text)
    else:
        text = out_txt.read_text()

    row = {"tag": tag, "model": model_key, "arm": arm, "rep": rep}
    row.update(measure(text, out_jsonl))
    return row


def measure(text, jsonl_path):
    """Counts, not judgments. The JSONL record is the primary source: the
    rendered transcript has been the site of eleven scorer bugs on this
    project (internal/coder/record.go)."""
    r = {}
    calls = []
    turns = []
    if jsonl_path.exists():
        for line in jsonl_path.read_text().splitlines():
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            if rec.get("type") == "message" and rec.get("role") == "assistant":
                for tc in rec.get("tool_calls") or []:
                    calls.append(tc.get("name", ""))
            elif rec.get("type") == "turn":
                turns.append(rec)

    r["code_calls"] = calls.count("code")
    r["read_calls"] = calls.count("read")
    r["grep_calls"] = calls.count("grep")
    r["glob_calls"] = calls.count("glob")
    r["ls_calls"] = calls.count("ls")
    r["symbol_calls"] = calls.count("symbol")
    r["tool_calls"] = len(calls)

    # Primary metric 2: model round trips -- every `sent` is one full request.
    r["round_trips"] = sum(t.get("sent", 0) for t in turns) if turns else count_sends(text)
    # Actually the `sent` field is tokens. Round trips = number of turn records
    # (one per send) is wrong too -- a turn record is per *turn*, not per send.
    # The request count is the number of assistant messages with tool calls
    # plus the final one, which the JSONL gives directly.
    r["round_trips"] = assistant_sends(jsonl_path, text)
    r["steps"] = turns[-1].get("steps", 0) if turns else 0
    r["cost"] = turns[-1].get("cost", 0.0) if turns else 0.0
    r["cost_known"] = bool(turns and turns[-1].get("cost_known"))

    # Counter-metric: programs that errored, and retries after an error.
    # Monty's errors reach the model as "The program failed: ..."; a retry is
    # another `code` call after one.
    r["code_errors"] = len(re.findall(r"The program failed", text))
    r["timeout"] = "[TIMEOUT]" in text
    r["empty"] = "Empty response received" in text
    r["loop_stopped"] = "was repeating itself" in text

    # Counter-metric: was the answer still correct. Scored from the ANSWER:
    # line the task demands (experimenting.md 2: a marker, not a position).
    answer = extract_answer(text)
    r["answer"] = answer
    r.update(score_answer(answer))
    return r


def count_sends(text):
    m = re.findall(r"Tokens: ", text)
    return len(m)


def assistant_sends(jsonl_path, text):
    """Model requests this run made: one per assistant message in the JSONL
    (each assistant message is one completion), falling back to the transcript
    when the log is missing."""
    n = 0
    if jsonl_path.exists():
        for line in jsonl_path.read_text().splitlines():
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            if rec.get("type") == "message" and rec.get("role") == "assistant":
                n += 1
        if n:
            return n
    return count_sends(text)


def extract_answer(text):
    # The rendered transcript puts the answer after the thinking blocks; the
    # renderer has two reasoning forms and both must be stripped
    # (experimenting.md 15), or a scorer eats the answer of any run whose last
    # aside was one-line.
    a = re.sub(r"‹thinking›\n.*?(?:‹/›|\Z)", "", text, flags=re.S)
    a = re.sub(r"‹thinking›[^\n]*\n?", "", a)
    for ln in a.splitlines():
        if ln.startswith("ANSWER:"):
            return ln[len("ANSWER:"):].strip()
    return ""


def score_answer(answer):
    """Correct = all three values named. The file is checked as a substring of
    the answer so 'toolobserve.go' and 'internal/coder/toolobserve.go' both
    pass; the values are matched with word boundaries so 60000 does not match
    160000."""
    out = {"correct": 0, "named_value": 0, "named_file": 0}
    if not answer:
        return out
    for key, spec in ANSWER_KEY.items():
        value_ok = re.search(r"(?<!\d)" + re.escape(spec["value"]) + r"(?!\d)", answer)
        file_ok = spec["file"] in answer
        if value_ok:
            out["named_value"] += 1
        if file_ok:
            out["named_file"] += 1
        if value_ok and file_ok:
            out["correct"] += 1
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--reps", type=int, default=1)
    ap.add_argument("--seed", type=int, default=20260830)
    ap.add_argument("--workers", type=int, default=3)
    ap.add_argument("--timeout", type=int, default=900)
    ap.add_argument("--models", default="")
    ap.add_argument("--arms", default="A,B,C")
    ap.add_argument("--out", default=str(HERE / "results.json"))
    args = ap.parse_args()

    models = args.models.split(",") if args.models else list(MODELS)
    arms = args.arms.split(",")
    jobs = [(m, a, rep) for m in models for rep in range(args.reps)
            for a in arms]
    random.Random(args.seed).shuffle(jobs)
    print(f"{len(jobs)} runs, seed {args.seed}, {args.workers} workers", flush=True)

    results = []
    done = 0
    with cf.ThreadPoolExecutor(max_workers=args.workers) as ex:
        futs = {ex.submit(run_one, j, args): j for j in jobs}
        for f in cf.as_completed(futs):
            m, a, rep = futs[f]
            done += 1
            try:
                row = f.result()
            except Exception as exc:  # noqa: BLE001
                # One job must never take the collection loop down
                # (experimenting.md 19). Every completed run stays on disk and
                # a re-run picks it up.
                print(f"[{done}/{len(jobs)}] {m}-{a}-{rep} FAILED: "
                      f"{type(exc).__name__}: {exc}", flush=True)
                results.append({"tag": f"{m}-{a}-{rep}", "model": m, "arm": a,
                                "rep": rep, "runner_error":
                                f"{type(exc).__name__}: {exc}", "cost": 0.0,
                                "code_calls": 0, "round_trips": 0,
                                "empty": True, "timeout": True, "correct": 0})
                continue
            results.append(row)
            print(f"[{done}/{len(jobs)}] {row['tag']:<16} code={row['code_calls']} "
                  f"rt={row['round_trips']} ok={row['correct']}/3 "
                  f"${row['cost']:.4f}", flush=True)
            json.dump(results, open(args.out, "w"), indent=1)

    json.dump(results, open(args.out, "w"), indent=1)
    print(f"\nwrote {args.out}")


if __name__ == "__main__":
    main()
