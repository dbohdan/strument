#!/usr/bin/env python3
"""M9: how often does the line matcher place an edit the model could not?

Arm A only — today's binary. The question is not comparative: it is whether the
fuzzy whitespace tier that internal/editblock/replace.go maintains still
rescues enough edits to justify arm C of
doc/experiments/2026-09-anchored-edit-preregistration.md, whose token argument
phase 0 removed.

Every fixture asks for an edit *inside* a nested block, because an edit to an
unindented top-level line cannot produce a whitespace rescue: a fixture that
cannot contain the phenomenon measures nothing.

Reads edits_exact / edits_fuzzy from the turn record the binary writes with
--jsonl, so the count comes from the harness rather than from parsing prose.
"""

import argparse
import concurrent.futures
import json
import os
import random
import shutil
import subprocess
import sys
import threading
import time

MODELS = [
    "deepseek/deepseek-v4-flash-0731",
    "openai/gpt-5.6-luna",
    "qwen/qwen3.8-27b",
    "tencent/hy3",
    "xiaomi/mimo-v2.5",
    "z-ai/glm-5.3-flash",
]

CONFIG = """\
router = provider(adapter = "openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}", context = 200000)}}
default = "m"
{extra}"""


def one_run(binary, fixtures, out_root, model, fixture, rep, arm="A"):
    name = f"{arm}--{model.replace('/', '_')}--{fixture}--{rep}"
    run_dir = os.path.join(out_root, name)
    if os.path.exists(os.path.join(run_dir, "result.json")):
        return json.load(open(os.path.join(run_dir, "result.json")))  # resume
    shutil.rmtree(run_dir, ignore_errors=True)
    proj = os.path.join(run_dir, "proj")
    os.makedirs(os.path.join(run_dir, "cfg", "strument"), exist_ok=True)
    shutil.copytree(os.path.join(fixtures, fixture), proj)
    task = open(os.path.join(proj, "TASK")).read().strip()
    os.remove(os.path.join(proj, "TASK"))
    before = {f: open(os.path.join(proj, f)).read() for f in os.listdir(proj)}

    with open(os.path.join(run_dir, "cfg", "strument", "config.star"), "w") as f:
        f.write(CONFIG.format(slug=model,
                extra="anchored_edits = True\n" if arm == "D" else ""))

    jsonl = os.path.join(run_dir, "log.jsonl")
    env = dict(os.environ)
    env.update({
        "XDG_CONFIG_HOME": os.path.join(run_dir, "cfg"),
        "XDG_DATA_HOME": os.path.join(run_dir, "data"),
        "XDG_STATE_HOME": os.path.join(run_dir, "state"),
        "XDG_CACHE_HOME": os.path.join(run_dir, "cache"),
    })
    t0 = time.time()
    p = subprocess.run(
        [binary, "chat", "--no-git", "--no-history", "--no-color",
         "--yes", "steps", "--jsonl", jsonl, "-m", task],
        cwd=proj, env=env, capture_output=True, timeout=300)
    elapsed = time.time() - t0

    rec = {"arm": arm, "model": model, "fixture": fixture, "rep": rep,
           "exit": p.returncode, "elapsed": round(elapsed, 1),
           "edits_exact": 0, "edits_fuzzy": 0, "steps": 0,
           "sent": 0, "received": 0, "cost": 0.0, "outcome": "",
           "edit_calls": 0, "edit_failures": 0, "changed": []}

    for line in open(jsonl, errors="ignore") if os.path.exists(jsonl) else []:
        try:
            d = json.loads(line)
        except json.JSONDecodeError:
            continue
        if d.get("type") == "turn":
            for k in ("edits_exact", "edits_fuzzy", "steps", "sent",
                      "received", "cost", "outcome"):
                rec[k] = d.get(k, rec[k])
        if d.get("type") == "message":
            for tc in d.get("tool_calls") or []:
                if tc.get("name") == "edit":
                    rec["edit_calls"] += 1
            if d.get("role") == "tool":
                t = d.get("text", "")
                if "was not found" in t or "appears" in t and "times" in t:
                    rec["edit_failures"] += 1

    for f, old in before.items():
        now = open(os.path.join(proj, f)).read()
        if now != old:
            rec["changed"].append(f)

    with open(os.path.join(run_dir, "stdout.txt"), "wb") as f:
        f.write(p.stdout + b"\n--- stderr ---\n" + p.stderr)
    json.dump(rec, open(os.path.join(run_dir, "result.json"), "w"), indent=1)
    return rec


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", required=True)
    ap.add_argument("--fixtures", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--reps", type=int, default=2)
    ap.add_argument("--models", default=",".join(MODELS))
    ap.add_argument("--seed", type=int, default=20260903)
    ap.add_argument("--limit", type=int, default=0, help="stop after N runs")
    ap.add_argument("--jobs", type=int, default=4, help="concurrent runs")
    ap.add_argument("--arms", default="A", help="comma-separated: A (today), D (anchored)")
    args = ap.parse_args()

    if not os.environ.get("OPENROUTER_API_KEY"):
        sys.exit("OPENROUTER_API_KEY is not set")

    fixtures = sorted(os.listdir(args.fixtures))
    models = args.models.split(",")
    arms = args.arms.split(",")
    jobs = [(a, m, f, r) for a in arms for m in models
            for f in fixtures for r in range(args.reps)]
    # Shuffled: an unrandomized order confounds the run with the wall-clock
    # window it ran in, and providers drift across such a window.
    random.Random(args.seed).shuffle(jobs)
    if args.limit:
        jobs = jobs[: args.limit]
    os.makedirs(args.out, exist_ok=True)
    print(f"{len(jobs)} runs, seed {args.seed}", flush=True)

    # Capped concurrency. Serial runs made an earlier trial take four hours for
    # work that fits in twenty minutes; unbounded concurrency would instead
    # rate-limit the provider and confound the arm with the retry it triggers.
    results, spend, lock = [], 0.0, threading.Lock()

    def work(job):
        a, m, f, r = job
        try:
            return one_run(args.binary, args.fixtures, args.out, m, f, r, a)
        except subprocess.TimeoutExpired:
            return {"arm": a, "model": m, "fixture": f, "rep": r, "exit": "timeout",
                    "edits_exact": 0, "edits_fuzzy": 0, "cost": 0.0,
                    "edit_calls": 0, "edit_failures": 0, "changed": []}
        except Exception as e:  # a crashed run must not take the sweep with it
            return {"arm": a, "model": m, "fixture": f, "rep": r, "exit": f"error: {e}",
                    "edits_exact": 0, "edits_fuzzy": 0, "cost": 0.0,
                    "edit_calls": 0, "edit_failures": 0, "changed": []}

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as ex:
        futures = {ex.submit(work, j): j for j in jobs}
        for i, fut in enumerate(concurrent.futures.as_completed(futures), 1):
            rec = fut.result()
            with lock:
                results.append(rec)
                spend += rec.get("cost") or 0.0
                print(f"[{i}/{len(jobs)}] {rec.get('arm','A')} {rec['model']:32s} {rec['fixture']:9s} "
                      f"r{rec['rep']} exact={rec.get('edits_exact')} "
                      f"fuzzy={rec.get('edits_fuzzy')} calls={rec.get('edit_calls')} "
                      f"fail={rec.get('edit_failures')} ${rec.get('cost') or 0:.5f}  "
                      f"running=${spend:.4f}", flush=True)
                with open(os.path.join(args.out, "results.jsonl"), "w") as fh:
                    for x in results:
                        fh.write(json.dumps(x, sort_keys=True) + "\n")
    print(f"\ntotal reported spend: ${spend:.4f}")


if __name__ == "__main__":
    main()
