"""Runner for the code-result trial.

Three arms of what a run_code program hands back — last (shipped), all (every
bridged call's result), main (a main() the program defines) — against three
models and three tasks.

Handbook rules this implements: the job list is shuffled with a fixed seed so an
arm is not confounded with the hour it ran (CLAUDE.md); raw stdout is saved per
run so a broken scorer costs a rescore rather than a re-buy (§4); one job's
failure is recorded as a row rather than killing the collection loop (§19); and
a resumed job takes the same path as a fresh one, exercised on purpose before
the batch (§20).
"""

import argparse
import json
import pathlib
import random
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed

HERE = pathlib.Path(__file__).resolve().parent
STRUMENT = HERE / "strument"
PROJ = HERE / "proj"
OUT = HERE / "runs"

ARMS = ["last", "all", "main", "bare"]

# "bare" is arm last run against a binary built from HEAD with the warning
# sentence removed from the tool description — the wording that was in place
# when the field reports came in. It answers whether that sentence is what
# keeps models from writing the losing shape, which the other three arms
# cannot: they all carry it.
BINARY = {"bare": "strument-bare"}
STRUMENT_ARM = {"bare": "last"}
MODELS = ["mimo", "glm", "deepseek"]

ANSWER_RULE = ' Put your whole answer on one line beginning with "ANSWER:".'

TASKS = {
    # Programmable: the program can compute the answer without any result
    # reaching the model. This is the arm-all counter-metric's home — if
    # echoing every call hurts, it hurts here.
    "sum": (
        "Every module under pkg/ defines a RETRIES constant. Report the total of all of them."
        + ANSWER_RULE
        + ' Example: "ANSWER: 42".',
        "160",
    ),
    # Judgement: needs the bodies in front of the model, not a computed value.
    "ignores": (
        "Read every module under pkg/. Exactly one of them defines a function that ignores its "
        "argument and returns None. Name the file and the function."
        + ANSWER_RULE
        + ' Example: "ANSWER: pkg/foo.py bar".',
        "pkg/gamma.py handle",
    ),
    # Judgement over every file at once: eight docstrings, one odd.
    "docstring": (
        "Every module under pkg/ starts with a one-line docstring. Seven of them follow the same "
        "pattern and one does not. Name the file whose docstring breaks the pattern."
        + ANSWER_RULE
        + ' Example: "ANSWER: pkg/foo.py".',
        "pkg/zeta.py",
    ),
}


def jobs(reps: int) -> list[dict]:
    out = []
    for arm in ARMS:
        for model in MODELS:
            for task in TASKS:
                for rep in range(reps):
                    out.append({"arm": arm, "model": model, "task": task, "rep": rep})
    rng = random.Random(20260908)
    rng.shuffle(out)
    return out


def job_id(j: dict) -> str:
    return f"{j['model']}-{j['arm']}-{j['task']}-{j['rep']}"


def run_one(j: dict, timeout: int) -> dict:
    """Run one session, or reuse the saved output. Both paths end here."""
    OUT.mkdir(exist_ok=True)
    raw_path = OUT / f"{job_id(j)}.txt"
    prompt, want = TASKS[j["task"]]

    if raw_path.exists():
        text = raw_path.read_text()
        status = "resumed"
    else:
        cmd = [
            str(HERE / BINARY.get(j["arm"], "strument")),
            "chat", "--no-git", "--no-history",
            "--code-result", STRUMENT_ARM.get(j["arm"], j["arm"]),
            "--model", j["model"],
            "--yes", "all", "-m", prompt,
        ]
        try:
            p = subprocess.run(
                cmd, cwd=PROJ, capture_output=True, text=True, timeout=timeout
            )
            text = (p.stdout or "") + (p.stderr or "")
            status = "ok"
        except subprocess.TimeoutExpired as e:
            # TimeoutExpired carries bytes even under text=True: decoding
            # happens after communicate(), which on this path never returns.
            # §19 is the hour that cost.
            def dec(v: bytes | str | None) -> str:
                if v is None:
                    return ""
                return v.decode("utf-8", "replace") if isinstance(v, bytes) else v

            text = dec(e.stdout) + dec(e.stderr) + "\n[TIMEOUT]\n"
            status = "timeout"
        raw_path.write_text(text)

    row = dict(j)
    row["status"] = status
    row.update(score(text, want))
    return row


def strip_ansi(s: str) -> str:
    out = []
    i = 0
    while i < len(s):
        if s[i] == "\x1b":
            j = i + 1
            if j < len(s) and s[j] == "[":
                j += 1
                while j < len(s) and not s[j].isalpha():
                    j += 1
                i = j + 1
                continue
        out.append(s[i])
        i += 1
    return "".join(out)


def score(text: str, want: str) -> dict:
    """Counts, not judgements. ANSWER: is a marker we asked for (§2)."""
    clean = strip_ansi(text)
    # The line must *begin* with the marker, which is what the prompt asked for.
    # Merely containing it matches the model quoting the instruction back —
    # scored as the answer '"' before this was tightened.
    answers = [
        line.strip()[len("ANSWER:"):].strip().rstrip(".")
        for line in clean.splitlines()
        if line.strip().startswith("ANSWER:")
    ]
    # "no answer" and "wrong answer" are different columns (§3).
    answered = bool(answers)
    correct = answered and answers[-1].lower() == want.lower()

    programs = clean.count("‹run_code›")
    calls = 0
    for line in clean.splitlines():
        if line.startswith("Ran ") and " calling " in line:
            calls += 1
    printed = clean.count("print(")
    steps = 0
    for line in clean.splitlines():
        if "steps." in line and "Tokens:" in line:
            try:
                steps = int(line.split(" steps.")[0].split()[-1])
            except (ValueError, IndexError):
                pass
    sent = recv = 0
    for line in clean.splitlines():
        if line.startswith("Tokens:"):
            try:
                sent = int(float(line.split("Tokens:")[1].split()[0].replace("k", "e3")))
                recv_part = line.split("received")[0].split()
                recv = int(float(recv_part[-1].replace("k", "e3")))
            except (ValueError, IndexError):
                pass
    return {
        "answered": answered,
        "correct": correct,
        "answer": answers[-1] if answers else "",
        "programs": programs,
        "call_lines": calls,
        "print_uses": printed,
        "steps": steps,
        "sent": sent,
        "recv": recv,
        "bytes": len(clean),
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--reps", type=int, default=2)
    ap.add_argument("--timeout", type=int, default=300)
    ap.add_argument("--parallel", type=int, default=3)
    ap.add_argument("--smoke", action="store_true", help="one job per arm, then stop")
    args = ap.parse_args()

    js = jobs(args.reps)
    if args.smoke:
        seen: set[str] = set()
        picked = []
        for j in js:
            if j["arm"] not in seen:
                seen.add(j["arm"])
                picked.append(j)
        js = picked

    results_path = HERE / "results.jsonl"
    rows: list[dict] = []
    with ThreadPoolExecutor(max_workers=args.parallel) as ex:
        futs = {ex.submit(run_one, j, args.timeout): j for j in js}
        done = 0
        for f in as_completed(futs):
            j = futs[f]
            try:
                rows.append(f.result())
            except Exception as e:  # never let one job kill the loop (§19)
                rows.append({**j, "status": f"runner-error: {e!r}"})
            done += 1
            print(f"[{done}/{len(js)}] {job_id(j)} {rows[-1].get('status')}", flush=True)

    with results_path.open("w") as fh:
        for r in rows:
            fh.write(json.dumps(r) + "\n")
    bad = [r for r in rows if str(r.get("status", "")).startswith("runner-error")]
    print(f"wrote {results_path} ({len(rows)} rows, {len(bad)} runner errors)")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
