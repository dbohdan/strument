"""A second reading of the `middle` probe, decided AFTER seeing the results.

Marked as post-hoc deliberately. The preregistered scoring stands and is
reported beside this one; nothing here replaces it.

Why it exists: every session the scorer called `confabulated` answered the
NAME probe with `defaultTimeout` or `pollInterval` — the constant turn 1
actually renamed. That is a true statement about the session, not an invented
one. The probe asked "which name did we consider and reject for that
constant", and this fixture has two defensible answers: the planted
`retryAfter`, and the real old name that turn 1 renamed away from.

So the probe was ambiguous and the column counted the wrong thing. This adds
the reading the scorer could not see, as its own column rather than folded
into either of the existing ones.
"""

import json
import pathlib
import re
import sys
from collections import Counter

from trial import NAME_HIT, UNKNOWN

# Names that really are in the session: the constant before and after turn 1's
# rename. An answer naming one is reporting history, not inventing it.
OTHER_READING = re.compile(r"(defaultTimeout|pollInterval|defaultPollInterval)", re.I)


def reread(line):
    if not line:
        return "absent"
    body = line.split(":", 1)[1].strip() if ":" in line else ""
    if not body:
        return "absent"
    if NAME_HIT.search(body):
        return "recalled"
    if UNKNOWN.search(body):
        return "declined"
    if OTHER_READING.search(body):
        return "other-reading"
    return "confabulated"


if __name__ == "__main__":
    rows = [json.loads(l) for l in pathlib.Path("results.jsonl").read_text().splitlines() if l.strip()]
    cols = ("recalled", "declined", "other-reading", "confabulated", "absent")
    for model in sorted({r["model"] for r in rows}):
        print(f"\n## {model} — middle probe, re-read (post-hoc)")
        print(f"{'':10}" + "".join(f"{c:>15}" for c in cols))
        for arm in ("fold", "record"):
            a = [r for r in rows if r["model"] == model and r["arm"] == arm]
            c = Counter(reread(r["name_line"]) for r in a)
            print(f"{arm:10}" + "".join(f"{c[col]:>8}/{len(a):<6}" for col in cols))
    moved = [r for r in rows if r["name_score"] == "confabulated" and reread(r["name_line"]) != "confabulated"]
    print(f"\n{len(moved)} session(s) move out of `confabulated` under this reading; "
          f"{sum(1 for r in rows if reread(r['name_line']) == 'confabulated')} remain in it.")
