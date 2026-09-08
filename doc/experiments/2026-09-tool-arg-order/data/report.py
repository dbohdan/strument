#!/usr/bin/env python3
"""Read the runs and report per model, per arm, per tool.

Two units, both reported, because they answer different questions and can
disagree:

  first call   one observation per run per tool — independent, and the one a
               user meets, since it is the first diff of the turn
  every call   every call in every run — more data, but a run that flailed
               through seven calls counts seven times

Counter-metrics are reported beside the effect, not under it: whether the run
ended in Success, and what it cost.
"""

import collections
import json
import pathlib
import sys

D = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(D))
from score import path_first  # noqa: E402

ARMS = ["A", "B"]
TOOLS = ["write", "edit"]


def load(run: pathlib.Path):
    """Return the scored calls, the turn outcome, the cost, and the misses.

    A miss is an edit whose search text did not match — the counter-metric
    that matters here. Reordering a schema is exactly the kind of change that
    could nudge a model into filling the fields differently and getting the
    span wrong, and an ordering win bought with more failed edits is a loss.
    """
    calls, outcome, cost, misses = [], None, 0.0, 0
    session = run / "session.jsonl"
    if not session.exists():
        return calls, "NO SESSION", cost, misses
    for line in session.read_text().splitlines():
        if not line.strip():
            continue
        try:
            rec = json.loads(line)
        except json.JSONDecodeError:
            continue
        if rec.get("type") == "turn":
            outcome = rec.get("outcome")
            cost += rec.get("cost") or 0.0
        if rec.get("role") == "tool" and "search text was not found" in (rec.get("text") or ""):
            misses += 1
        for tc in rec.get("tool_calls") or []:
            verdict = path_first(tc["name"], tc["arguments"])
            if verdict is not None:
                calls.append((tc["name"], verdict))
    return calls, outcome, cost, misses


def main() -> None:
    first = collections.defaultdict(lambda: [0, 0])  # (model, arm, tool) -> [yes, n]
    every = collections.defaultdict(lambda: [0, 0])
    runs = collections.defaultdict(int)
    bad = collections.Counter()
    cost = collections.defaultdict(float)
    missed = collections.defaultdict(int)  # edits whose search text did not match

    for run in sorted((D / "runs").iterdir()):
        if not run.is_dir():
            continue
        model, arm, _rep = run.name.rsplit("-", 2)
        calls, outcome, spent, misses = load(run)
        runs[(model, arm)] += 1
        cost[model] += spent
        missed[(model, arm)] += misses
        if outcome != "Success":
            bad[(model, arm, outcome)] += 1
        seen = set()
        for tool, verdict in calls:
            every[(model, arm, tool)][0] += verdict
            every[(model, arm, tool)][1] += 1
            if tool not in seen:
                seen.add(tool)
                first[(model, arm, tool)][0] += verdict
                first[(model, arm, tool)][1] += 1

    for tool in TOOLS:
        print(f"\n=== {tool}: calls naming `path` before the payload ===")
        print(f"{'model':<14} {'first call A':>13} {'first call B':>13}"
              f" {'every call A':>13} {'every call B':>13}")
        for model in sorted({m for m, _a in runs}):
            row = [f"{model:<14}"]
            for table in (first, every):
                for arm in ARMS:
                    yes, n = table[(model, arm, tool)]
                    row.append(f"{yes:>5}/{n:<3}{'':>5}" if n else f"{'—':>13}")
            print(" ".join(row))

    print("\n=== counter-metrics ===")
    print(f"{'model':<14} {'runs A':>7} {'runs B':>7} {'not Success':>12}"
          f" {'missed A':>9} {'missed B':>9} {'cost $':>9}")
    for model in sorted({m for m, _a in runs}):
        failed = sum(v for (m, _a, _o), v in bad.items() if m == model)
        print(f"{model:<14} {runs[(model, 'A')]:>7} {runs[(model, 'B')]:>7}"
              f" {failed:>12} {missed[(model, 'A')]:>9} {missed[(model, 'B')]:>9}"
              f" {cost[model]:>9.4f}")
    if bad:
        print("\nnon-Success outcomes:")
        for (model, arm, outcome), n in sorted(bad.items()):
            print(f"  {model}-{arm}: {outcome} ×{n}")
    print(f"\ntotal cost ${sum(cost.values()):.4f}")


if __name__ == "__main__":
    main()
