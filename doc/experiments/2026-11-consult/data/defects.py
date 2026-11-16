#!/usr/bin/env python3
"""Ask a judge to list one candidate's defects, alone: the position-free judge.

    defects.py --outdir DIR [--judges mimo,deepseek] [--reps 2] [--only TASK,...] [--jobs 8]

Pairwise judging puts one version first, and the pilot's MiMo picked the first
in 15 of 16 verdicts, praising a sentence in one order and calling it surplus
in the other. Here each candidate is shown by itself, with the task and the
project's files, and the judge lists every concrete defect, one per line. The
measure is the count, compared between writers afterwards; no judge sees two
versions at once.

One JSON file per call under DIR/defects/, task__model__n__judge__rep.json.

--order first asks for the list before anything else. On whole pages a judge
with reasoning disabled reasons in its answer instead, and asked for the list
last it could search until the token cap and write none.
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
import judge as J  # noqa: E402
import tasks as T  # noqa: E402

WRITERS = "mimo,terra"


def prompt(task, text, order="last"):
    t = T.TASKS[task]
    parts = [f"A new version of {t['target']} was written for this task in the project below.\n\n"
             f"The task: {t['instruction']} The project's house style is in STYLE.md.\n\n"
             "The project's files, as they were before the change:\n"]
    for rel, body in T.files_of(task).items():
        parts.append(f"File {rel}:\n{C.fenced(body)}\n")
    parts.append(f"The new version of {t['target']}:\n{C.fenced(text)}\n")
    ask = ("List every concrete defect in the new version: a statement the code contradicts, something "
           "the task asked for that is missing, a change the task did not ask for, or a breach of "
           "STYLE.md. Count only real defects a careful reviewer would ask to have fixed, not preferences. ")
    if order == "first":
        ask += ("Begin your answer with the line `Defects:` followed by one defect per line, each starting with "
                "`- ` and fitting on that line, or the single line `- none`. Then, if anything needs explaining, "
                "a line `Notes:` and your notes. Nothing before the list.")
    else:
        ask += ("After any reasoning, end with a section that starts with the line `Defects:` followed by one "
                "defect per line, each starting with `- `, or the single line `- none`.")
    parts.append(ask)
    return "\n".join(parts)


SECTION = re.compile(r"^[\W_]*defects[\W_]*:[*_ ]*(.*)$", re.I | re.M)


def parse_defects(text):
    """The defect lines after the last `Defects:` line, or None when there is no
    such section. `- none` is zero, not one. A list may start on the heading
    line itself (`Defects: - none`), which MiMo writes."""
    text = text or ""
    ms = list(SECTION.finditer(text))
    if not ms:
        return None
    inline = ms[-1].group(1).strip()
    lines = ([inline] if inline else []) + [l.strip() for l in text[ms[-1].end():].splitlines()]
    if inline and re.fullmatch(r"(?i)\W*none\W*", inline):
        return []
    items = [l[2:].strip() for l in lines if l.startswith(("- ", "* "))]
    if len(items) == 1 and re.fullmatch(r"(?i)\W*none\W*", items[0]):
        return []
    return items


def parse_first(text, finish_reason):
    """The list a list-first answer opens with: the first `Defects:` section,
    read up to the first non-blank line that is not an item. A list that runs to the end
    of an answer cut off by the token cap may be missing items, so it is None,
    as an answer with no section is."""
    text = text or ""
    m = SECTION.search(text)
    if not m:
        return None
    inline = m.group(1).strip()
    if inline and re.fullmatch(r"(?i)\W*none\W*", inline):
        return []
    items, ended = ([inline[2:].strip()] if inline.startswith(("- ", "* ")) else []), False
    for line in text[m.end():].splitlines():
        line = line.strip()
        if line.startswith(("- ", "* ")):
            items.append(line[2:].strip())
        elif line:  # a blank line between items does not end the list; `Notes:` does
            ended = True
            break
    if not ended and finish_reason == "length":
        return None
    if len(items) == 1 and re.fullmatch(r"(?i)\W*none\W*", items[0]):
        return []
    return items


def parse(rec):
    """A saved judgment's defect list, by the order its prompt asked for."""
    if not rec.get("ok"):
        return None
    if rec.get("order") == "first":
        return parse_first(rec.get("text"), rec.get("finish_reason"))
    return parse_defects(rec.get("text"))


def run(outdir, job, order="last", reasoning="off", max_tokens=24000):
    task, (model, n), judge, rep = job
    path = os.path.join(outdir, "defects", f"{task}__{model}__{n}__{judge}__{rep}.json")
    if os.path.exists(path):
        return path, "skipped"
    text = J.candidate(outdir, task, model, n)
    if text is None:
        return path, "missing candidate"
    rec = C.chat(judge, prompt(task, text, order), max_tokens=max_tokens,
                 reasoning=C.NO_REASONING if reasoning == "off" else {"effort": reasoning})
    rec.update(task=task, writer=model, n=n, judge=judge, rep=rep, order=order, judge_reasoning=reasoning,
               max_tokens=max_tokens)
    items = parse(rec)
    rec["defects"] = items
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(rec, f, indent=1)
    os.replace(tmp, path)
    return path, f"defects={None if items is None else len(items)} ${rec.get('cost')}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--outdir", required=True)
    ap.add_argument("--judges", default="mimo,deepseek")
    ap.add_argument("--reps", type=int, default=2)
    ap.add_argument("--samples", type=int, default=2, help="samples per writer to judge")
    ap.add_argument("--only", default="")
    ap.add_argument("--jobs", type=int, default=8)
    ap.add_argument("--set", default="short", choices=["short", "long"], help="which task set")
    ap.add_argument("--writers", default=WRITERS, help="the writers whose two samples are judged")
    ap.add_argument("--order", default="last", choices=["last", "first"],
                    help="where the answer puts its defect list: after the reasoning, or before anything")
    ap.add_argument("--reasoning", default="off", choices=["off", "low"], help="the judge's reasoning")
    ap.add_argument("--max-tokens", type=int, default=24000)
    a = ap.parse_args()
    T.use(a.set)
    if not os.environ.get("OPENROUTER_API_KEY"):
        sys.exit("OPENROUTER_API_KEY is not set")
    os.makedirs(os.path.join(a.outdir, "defects"), exist_ok=True)
    only = set(filter(None, a.only.split(",")))
    gen = [(w, n) for w in a.writers.split(",") for n in range(1, a.samples + 1)]
    js = [(task, g, j, rep) for task in T.TASKS for g in gen for j in a.judges.split(",")
          for rep in range(1, a.reps + 1) if not only or task in only]
    random.Random(20261117).shuffle(js)
    print(f"{len(js)} calls", flush=True)
    failed = 0
    with cf.ThreadPoolExecutor(a.jobs) as ex:
        futs = {ex.submit(run, a.outdir, j, a.order, a.reasoning, a.max_tokens): j for j in js}
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
