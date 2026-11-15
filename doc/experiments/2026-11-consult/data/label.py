#!/usr/bin/env python3
"""The label-swap probe: does MiMo's preference follow the text or the label?

    label.py --outdir DIR [--only TASK,...] [--jobs 8]

The blind judge check removed what the maintainer's sessions had: MiMo knew
which text came from the stronger model. Here MiMo judges the same Terra
against MiMo pairs (tm1, tm2) told who wrote each version — truthfully for
half the calls, swapped for the other half — in both orders. Four calls per
pair: order ab or ba, label true or false.

  recognition   MiMo's choice follows who actually wrote the text
  deference     MiMo's choice follows who it was told wrote the text

One JSON file per call under DIR/label/, task__pair__order__label.json.
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
import judge as J  # noqa: E402
import tasks as T  # noqa: E402

PAIRS = ("tm1", "tm2")
LABELS = ("true", "false")
NAMES = {"terra": "GPT-6.1 Terra (openai/gpt-6.1-terra)", "mimo": "you, MiMo-V2.6-Flash"}


def prompt(task, a, b, author_a, author_b):
    t = T.TASKS[task]
    parts = [f"Two versions of {t['target']} were written for this task in the project below.\n\n"
             f"The task: {t['instruction']} The project's house style is in STYLE.md.\n\n"
             "The project's files, as they were before the change:\n"]
    for rel, body in T.files_of(task).items():
        parts.append(f"File {rel}:\n{C.fenced(body)}\n")
    parts.append(f"Version A of {t['target']}, written by {NAMES[author_a]}:\n{C.fenced(a)}\n")
    parts.append(f"Version B of {t['target']}, written by {NAMES[author_b]}:\n{C.fenced(b)}\n")
    parts.append("Which version is better for the project's users and maintainers: accurate to the code, "
                 "following STYLE.md, and doing what the task asked, no more and no less? Think it through "
                 "briefly, then end with a line that is exactly `Winner: A` or `Winner: B`.")
    return "\n".join(parts)


def run(outdir, job):
    task, pair, order, label = job
    path = os.path.join(outdir, "label", f"{task}__{pair}__{order}__{label}.json")
    if os.path.exists(path):
        return path, "skipped"
    (wx, nx), (wy, ny) = J.PAIRS[pair]  # Terra's sample, then MiMo's
    x, y = J.candidate(outdir, task, wx, nx), J.candidate(outdir, task, wy, ny)
    if x is None or y is None:
        return path, "missing candidate"
    shown = [(x, wx), (y, wy)] if order == "ab" else [(y, wy), (x, wx)]
    authors = [w for _, w in shown]
    if label == "false":
        authors = authors[::-1]  # each version is credited to the other writer
    rec = C.chat("mimo", prompt(task, shown[0][0], shown[1][0], authors[0], authors[1]),
                 max_tokens=12000, reasoning=C.NO_REASONING)
    rec.update(task=task, pair=pair, order=order, label=label,
               written_a=shown[0][1], written_b=shown[1][1], labelled_a=authors[0], labelled_b=authors[1],
               verdict=J.parse_verdict(rec.get("text")) if rec["ok"] else None)
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(rec, f, indent=1)
    os.replace(tmp, path)
    return path, f"verdict={rec['verdict']} ${rec.get('cost')}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--outdir", required=True)
    ap.add_argument("--only", default="")
    ap.add_argument("--jobs", type=int, default=8)
    a = ap.parse_args()
    if not os.environ.get("OPENROUTER_API_KEY"):
        sys.exit("OPENROUTER_API_KEY is not set")
    os.makedirs(os.path.join(a.outdir, "label"), exist_ok=True)
    only = set(filter(None, a.only.split(",")))
    js = [(task, p, o, lab) for task in T.TASKS for p in PAIRS for o in J.ORDERS for lab in LABELS
          if not only or task in only]
    random.Random(20261118).shuffle(js)
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
