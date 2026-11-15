#!/usr/bin/env python3
"""Write each task's file with each model, alone: the judge check's candidates.

    generate.py --outdir DIR [--samples 2] [--only TASK,...] [--jobs 8]

For every task, `samples` versions from MiMo and from Terra, each from one
call with the same prompt: the instruction, the task's files, and a request
for the target's full new contents in one fenced block. No harness, no tools —
this measures the writing, and the judge, not the consult tool.

One JSON file per call under DIR/gen/, named task__model__n.json. A call
whose file exists is skipped, so rerunning resumes. Calls run in a seeded
shuffled order, so a provider's drift over the run does not line up with a
model or a task.
"""

import argparse
import concurrent.futures as cf
import json
import os
import random
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import common as C  # noqa: E402
import tasks as T  # noqa: E402

GEN_MODELS = ["mimo", "terra"]
# Room for a writer reasoning at its default, which "low" never needed.
MAX_TOKENS = 32000


def prompt(task):
    t = T.TASKS[task]
    parts = [f"You are working on the project whose files are below. {t['instruction']} "
             "The project's house style is in STYLE.md.\n"]
    for rel, body in T.files_of(task).items():
        parts.append(f"File {rel}:\n{C.fenced(body)}\n")
    exists = t["target"] in T.PROJECTS[t["project"]]
    parts.append(f"Reply with the complete {'new ' if exists else ''}contents of {t['target']}"
                 f"{'' if exists else ' (a new file)'} in one fenced code block, and nothing else.")
    return "\n".join(parts)


def jobs(samples, only, models=GEN_MODELS):
    js = [(task, m, n) for task in T.TASKS for m in models for n in range(1, samples + 1)
          if not only or task in only]
    random.Random(20261115).shuffle(js)
    return js


def run(outdir, job):
    task, m, n = job
    path = os.path.join(outdir, "gen", f"{task}__{m}__{n}.json")
    if os.path.exists(path):
        return path, "skipped"
    rec = C.chat(m, prompt(task), max_tokens=MAX_TOKENS)
    block, how = C.extract_block(rec.get("text", "")) if rec["ok"] else (None, "error")
    rec.update(task=task, n=n, block=block, extracted=how)
    if block is not None:
        rec["facts"] = T.facts(task, block)
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(rec, f, indent=1)
    os.replace(tmp, path)
    return path, f"{how} ok={rec.get('facts', {}).get('ok')} ${rec.get('cost')}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--outdir", required=True)
    ap.add_argument("--samples", type=int, default=2)
    ap.add_argument("--only", default="")
    ap.add_argument("--jobs", type=int, default=8)
    ap.add_argument("--set", default="short", choices=["short", "long"], help="which task set")
    ap.add_argument("--models", default=",".join(GEN_MODELS))
    a = ap.parse_args()
    T.use(a.set)
    if not os.environ.get("OPENROUTER_API_KEY"):
        sys.exit("OPENROUTER_API_KEY is not set")
    os.makedirs(os.path.join(a.outdir, "gen"), exist_ok=True)
    js = jobs(a.samples, set(filter(None, a.only.split(","))), a.models.split(","))
    print(f"{len(js)} calls", flush=True)
    failed = 0
    with cf.ThreadPoolExecutor(a.jobs) as ex:
        futs = {ex.submit(run, a.outdir, j): j for j in js}
        for fut in cf.as_completed(futs):
            try:
                path, what = fut.result()
                print(os.path.basename(path), what, flush=True)
            except Exception as e:  # a worker that died is reported, not lost (§19)
                failed += 1
                print("FAILED", futs[fut], repr(e), flush=True)
    print(f"finished, {failed} workers failed", flush=True)


if __name__ == "__main__":
    main()
