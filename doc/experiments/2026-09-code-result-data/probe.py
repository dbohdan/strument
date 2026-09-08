"""Hazard probe: can the fixture contain the phenomenon at all?

The main trial found one lossy program in 334, which is either "models do not
write that shape any more" or "this fixture never asks them to" (experimenting.md
§18, a fixture that cannot contain the phenomenon). The field transcripts that
started this were *verification* — did my edit land, do these files exist — where
the model wants a yes/no per file rather than the contents. The trial's tasks all
want the contents, which leads straight to print().

So this probe asks for verification, in normal mode (direct tools offered, the
setting the reports came from), against the shipped description and the
pre-warning one. If the losing shape does not appear here either, the trial's
null is about models rather than about the fixture.
"""

import json
import pathlib
import random
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import analyze  # noqa: E402
import run as R  # noqa: E402

HERE = pathlib.Path(__file__).resolve().parent
PROJ = HERE / "proj-normal"
OUT = HERE / "probe-runs"

TASKS = {
    # "Did it land" — the shape of the two field reports.
    "verify": (
        "I have just edited pkg/alpha.py, pkg/gamma.py and pkg/zeta.py. Check each of those "
        "three files and tell me which of them still contain the token RETRIES."
        ' Put your whole answer on one line beginning with "ANSWER:", listing the file names.',
        None,
    ),
    # "Do these exist" — the other field shape, with two names that do not.
    "exists": (
        "Check whether these files exist and are readable: pkg/alpha.py, pkg/omega.py, "
        "pkg/gamma.py, pkg/sigma.py. Say which ones you could read."
        ' Put your whole answer on one line beginning with "ANSWER:".',
        None,
    ),
}


def jobs(reps: int) -> list[dict]:
    out = [
        {"arm": arm, "model": m, "task": t, "rep": r}
        for arm in ("last", "bare")
        for m in R.MODELS
        for t in TASKS
        for r in range(reps)
    ]
    random.Random(20260908).shuffle(out)
    return out


def run_one(j: dict, timeout: int) -> dict:
    OUT.mkdir(exist_ok=True)
    jid = f"{j['model']}-{j['arm']}-{j['task']}-{j['rep']}"
    raw = OUT / f"{jid}.txt"
    if raw.exists():
        text, status = raw.read_text(), "resumed"
    else:
        binary = HERE / ("strument-bare" if j["arm"] == "bare" else "strument")
        cmd = [
            str(binary), "chat", "--no-git", "--no-history",
            "--code-result", "last", "--model", j["model"],
            "--yes", "all", "-m", TASKS[j["task"]][0],
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

    bs = analyze.blocks(text)
    return {
        **j,
        "status": status,
        "programs": len(bs),
        "multi_call": sum(1 for _, n in bs if n >= 2),
        "lossy": sum(1 for src, n in bs if analyze.lossy(src, n)),
        # Did it reach for a program at all, or just call the tools directly?
        "used_run_code": len(bs) > 0,
    }


def main() -> int:
    analyze.selfcheck()
    js = jobs(3)
    rows: list[dict] = []
    with ThreadPoolExecutor(max_workers=3) as ex:
        futs = {ex.submit(run_one, j, 300): j for j in js}
        for i, f in enumerate(as_completed(futs), 1):
            try:
                rows.append(f.result())
            except Exception as e:
                rows.append({**futs[f], "status": f"runner-error: {e!r}"})
            print(f"[{i}/{len(js)}] {rows[-1].get('status')}", flush=True)
    with (HERE / "probe-results.jsonl").open("w") as fh:
        for r in rows:
            fh.write(json.dumps(r) + "\n")

    print(f"\n{'arm':5s} {'runs':>5s} {'used run_code':>14s} {'programs':>9s} "
          f"{'multi-call':>11s} {'lossy':>6s}")
    for arm in ("last", "bare"):
        a = [r for r in rows if r["arm"] == arm and "programs" in r]
        print(f"{arm:5s} {len(a):5d} {sum(r['used_run_code'] for r in a):14d} "
              f"{sum(r['programs'] for r in a):9d} {sum(r['multi_call'] for r in a):11d} "
              f"{sum(r['lossy'] for r in a):6d}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
