#!/usr/bin/env python3
"""Ask the judges to compare candidates blind, in both orders.

    judge.py --outdir DIR [--judges mimo,deepseek] [--only TASK,...] [--jobs 8]

Pairs, per task, from generate.py's candidates:
  tm1, tm2   Terra sample n against MiMo sample n: the comparison that matters
  mm         MiMo 1 against MiMo 2: a null pair, same writer
  tt         Terra 1 against Terra 2: a null pair, same writer

Each pair is judged in both orders by each judge. The judge sees the task, the
project's files as they were, and the two versions labelled A and B — never
which model wrote which. It ends with `Winner: A` or `Winner: B`.

One JSON file per call under DIR/judge/, named task__pair__order__judge.json.
Existing files are skipped; calls run in a seeded shuffled order.
"""

import argparse
import concurrent.futures as cf
import json
import os
import random
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import common as C  # noqa: E402
import tasks as T  # noqa: E402

PAIRS = {"tm1": (("terra", 1), ("mimo", 1)), "tm2": (("terra", 2), ("mimo", 2)),
         "mm": (("mimo", 1), ("mimo", 2)), "tt": (("terra", 1), ("terra", 2))}
ORDERS = ("ab", "ba")  # ab: the pair's first member is shown as A

WINNER = re.compile(r"^\W*winner\W*:\W*([AB])\W*$", re.I | re.M)


def parse_verdict(text):
    """The last `Winner: A|B` line, or None. Last, because a judge that weighs
    both may write "Winner: A" while thinking aloud and then decide B."""
    found = WINNER.findall(text or "")
    return found[-1].upper() if found else None


def candidate(outdir, task, model, n):
    path = os.path.join(outdir, "gen", f"{task}__{model}__{n}.json")
    if not os.path.exists(path):
        return None
    rec = json.load(open(path))
    return rec.get("block")


def prompt(task, a, b):
    t = T.TASKS[task]
    parts = [f"Two versions of {t['target']} were written for this task in the project below.\n\n"
             f"The task: {t['instruction']} The project's house style is in STYLE.md.\n\n"
             "The project's files, as they were before the change:\n"]
    for rel, body in T.files_of(task).items():
        parts.append(f"File {rel}:\n{C.fenced(body)}\n")
    parts.append(f"Version A of {t['target']}:\n{C.fenced(a)}\n")
    parts.append(f"Version B of {t['target']}:\n{C.fenced(b)}\n")
    parts.append("Which version is better for the project's users and maintainers: accurate to the code, "
                 "following STYLE.md, and doing what the task asked, no more and no less? Think it through "
                 "briefly, then end with a line that is exactly `Winner: A` or `Winner: B`.")
    return "\n".join(parts)


def jobs(judges, only):
    js = [(task, pair, order, j) for task in T.TASKS for pair in PAIRS for order in ORDERS for j in judges
          if not only or task in only]
    random.Random(20261116).shuffle(js)
    return js


def run(outdir, job):
    task, pair, order, judge = job
    path = os.path.join(outdir, "judge", f"{task}__{pair}__{order}__{judge}.json")
    if os.path.exists(path):
        return path, "skipped"
    first, second = PAIRS[pair]
    x, y = candidate(outdir, task, *first), candidate(outdir, task, *second)
    if x is None or y is None:
        return path, "missing candidate"  # not written: a later run fills it once the candidate exists
    a, b = (x, y) if order == "ab" else (y, x)
    rec = C.chat(judge, prompt(task, a, b), max_tokens=12000, reasoning=C.NO_REASONING)
    rec.update(task=task, pair=pair, order=order, judge=judge,
               shown_a=first if order == "ab" else second, shown_b=second if order == "ab" else first,
               verdict=parse_verdict(rec.get("text")) if rec["ok"] else None)
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(rec, f, indent=1)
    os.replace(tmp, path)
    return path, f"verdict={rec['verdict']} ${rec.get('cost')}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--outdir", required=True)
    ap.add_argument("--judges", default="mimo,deepseek")
    ap.add_argument("--only", default="")
    ap.add_argument("--jobs", type=int, default=8)
    a = ap.parse_args()
    if not os.environ.get("OPENROUTER_API_KEY"):
        sys.exit("OPENROUTER_API_KEY is not set")
    os.makedirs(os.path.join(a.outdir, "judge"), exist_ok=True)
    js = jobs(a.judges.split(","), set(filter(None, a.only.split(","))))
    print(f"{len(js)} calls", flush=True)
    failed = 0
    with cf.ThreadPoolExecutor(a.jobs) as ex:
        futs = {ex.submit(run, a.outdir, j): j for j in js}
        for fut in cf.as_completed(futs):
            try:
                path, what = fut.result()
                print(os.path.basename(path), what, flush=True)
            except Exception as e:
                failed += 1
                print("FAILED", futs[fut], repr(e), flush=True)
    print(f"finished, {failed} workers failed", flush=True)


if __name__ == "__main__":
    main()
