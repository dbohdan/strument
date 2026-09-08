"""Aggregate the code-result trial.

Counts, not judgements. Correctness and "answered" are separate columns (§3),
and the cost metrics are reported beside the effect rather than under it — a
result that wins on correctness and loses on tokens is a trade, not a win.
"""

import json
import pathlib
import statistics
import sys

HERE = pathlib.Path(__file__).resolve().parent
ARMS = ["last", "all", "main"]


def load() -> list[dict]:
    rows = []
    with (HERE / "results.jsonl").open() as fh:
        for line in fh:
            if line.strip():
                rows.append(json.loads(line))
    return rows


def med(xs: list[float]) -> float:
    return statistics.median(xs) if xs else 0.0


def table(rows: list[dict], key: str | None = None) -> None:
    groups = sorted({r[key] for r in rows}) if key else [None]
    for g in groups:
        sub = [r for r in rows if key is None or r[key] == g]
        if key:
            print(f"\n### {key} = {g}")
        print(f"{'arm':6s} {'n':>3s} {'answered':>9s} {'correct':>8s} "
              f"{'steps':>7s} {'sent':>9s} {'programs':>9s} {'prints':>7s}")
        for arm in ARMS:
            a = [r for r in sub if r["arm"] == arm]
            if not a:
                continue
            print(f"{arm:6s} {len(a):3d} {sum(r['answered'] for r in a):9d} "
                  f"{sum(r['correct'] for r in a):8d} "
                  f"{med([r['steps'] for r in a]):7.1f} "
                  f"{med([r['sent'] for r in a]):9.0f} "
                  f"{med([r['programs'] for r in a]):9.1f} "
                  f"{med([r['print_uses'] for r in a]):7.1f}")


def main() -> int:
    rows = load()
    bad = [r for r in rows if str(r.get("status", "")).startswith("runner-error")]
    timeouts = [r for r in rows if r.get("status") == "timeout"]
    print(f"{len(rows)} rows, {len(bad)} runner errors, {len(timeouts)} timeouts")
    if bad:
        # A batch that ended in runner errors is not a batch to read (§20).
        for r in bad[:5]:
            print("  ", r)
        return 1

    print("\n## overall")
    table(rows)
    table(rows, "task")
    table(rows, "model")

    print("\n## wrong or unanswered, in full")
    for r in rows:
        if not r["correct"]:
            print(f"  {r['model']:9s} {r['arm']:5s} {r['task']:10s} "
                  f"answered={r['answered']} answer={r['answer']!r} status={r['status']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
