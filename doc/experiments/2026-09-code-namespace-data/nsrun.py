"""Runner for the tools-namespace trial.

Four arms of how a run_code program reaches the tools — flat (shipped), both,
only, hint — against three models and three tasks, all at reasoning="low"
(experimenting.md §5).

The primary metric is a count of programs that reached for Python's own
filesystem: `import os`, `open(`, `import glob`, `pathlib`. That rate was
measured at 10.6% over 530 programs before any arm existed, so unlike the
code-result trial the hazard is known to fire.
"""

import argparse
import json
import pathlib
import random
import re
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed

HERE = pathlib.Path(__file__).resolve().parent
TRIAL = HERE.parent / "trial"
sys.path.insert(0, str(TRIAL))
import analyze  # noqa: E402
import run as R  # noqa: E402

PROJ = HERE / "proj"
OUT = HERE / "runs"
ARMS = ["flat", "both", "only", "hint"]

# The reach this trial is about. Matched on the program's source, so it counts
# the mistake whether or not the failure text is visible on screen.
WRONG_REACH = re.compile(
    r"\bimport\s+(os|glob|pathlib|subprocess|shutil|sys|io|tempfile)\b"
    r"|\bopen\s*\(|\bos\.\w+|\bpathlib\."
)


def jobs(reps: int) -> list[dict]:
    out = [
        {"arm": a, "model": m, "task": t, "rep": r}
        for a in ARMS
        for m in R.MODELS
        for t in R.TASKS
        for r in range(reps)
    ]
    random.Random(20260908).shuffle(out)
    return out


def job_id(j: dict) -> str:
    return f"{j['model']}-{j['arm']}-{j['task']}-{j['rep']}"


def run_one(j: dict, timeout: int) -> dict:
    OUT.mkdir(exist_ok=True)
    raw = OUT / f"{job_id(j)}.txt"
    prompt, want = R.TASKS[j["task"]]
    if raw.exists():
        text, status = raw.read_text(), "resumed"
    else:
        cmd = [
            str(HERE / "strument"), "chat", "--no-git", "--no-history",
            "--code-namespace", j["arm"], "--model", j["model"],
            "--yes", "all", "-m", prompt,
        ]
        try:
            p = subprocess.run(cmd, cwd=PROJ, capture_output=True, text=True, timeout=timeout)
            text, status = (p.stdout or "") + (p.stderr or ""), "ok"
        except subprocess.TimeoutExpired as e:
            def dec(v: bytes | str | None) -> str:
                if v is None:
                    return ""
                return v.decode("utf-8", "replace") if isinstance(v, bytes) else v
            text, status = dec(e.stdout) + dec(e.stderr) + "\n[TIMEOUT]\n", "timeout"
        raw.write_text(text)

    row = dict(j)
    row["status"] = status
    row.update(R.score(text, want))
    bs = analyze.blocks(text)
    row["blocks"] = len(bs)
    row["wrong_reach"] = sum(1 for src, _ in bs if WRONG_REACH.search(src))
    row["no_call"] = sum(1 for _, n in bs if n == 0)
    row["used_ns"] = sum(1 for src, _ in bs if "tools." in src)
    return row


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--reps", type=int, default=2)
    ap.add_argument("--timeout", type=int, default=300)
    ap.add_argument("--parallel", type=int, default=3)
    args = ap.parse_args()

    js = jobs(args.reps)
    rows: list[dict] = []
    with ThreadPoolExecutor(max_workers=args.parallel) as ex:
        futs = {ex.submit(run_one, j, args.timeout): j for j in js}
        for i, f in enumerate(as_completed(futs), 1):
            try:
                rows.append(f.result())
            except Exception as e:
                rows.append({**futs[f], "status": f"runner-error: {e!r}"})
            print(f"[{i}/{len(js)}] {job_id(futs[f])} {rows[-1].get('status')}", flush=True)

    with (HERE / "ns-results.jsonl").open("w") as fh:
        for r in rows:
            fh.write(json.dumps(r) + "\n")
    print(f"wrote ns-results.jsonl ({len(rows)} rows)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
