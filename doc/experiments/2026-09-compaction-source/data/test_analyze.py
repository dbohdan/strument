"""The analyser, checked against counts chosen in advance.

Synthetic input rather than the batch's own, and deliberately so: this has to
run before the results are read, and a check built from the data it is meant to
validate cannot fail in the way that matters. The counts here are planted, so
"the analyser reports what is there" is a claim with a right answer.

The exclusion lines are part of it. A fixture that stopped forcing compaction
would look exactly like a null, so the excluded sessions have to be counted and
attributed rather than quietly dropped.
"""

import json
import subprocess
import sys
import tempfile

ROWS = []


def sess(arm, reason, name, folds=10, rc=0):
    return {
        "arm": arm, "model": "synth", "rep": len(ROWS), "returncode": rc,
        "elapsed": 500.0, "folds": folds, "fold_input_tokens": 20000,
        "fold_failures": 0, "turns_recorded": 12, "session_cost": 0.02,
        "reasoning_chars": 9000, "reason_score": reason, "name_score": name,
        "reason_line": "", "name_line": "", "stdout": "",
    }


# The effect this trial predicts, and its counter-metric reversed, so a
# reversal cannot be read as agreement.
for _i in range(12):
    ROWS.append(sess("fold", "recalled" if _i < 3 else "declined", "recalled"))
for _i in range(12):
    ROWS.append(sess("record", "recalled" if _i < 9 else "confabulated", "declined"))
ROWS.append(sess("fold", "recalled", "recalled", folds=1))   # too few folds
ROWS.append(sess("record", "recalled", "recalled", rc=-9))   # crashed

WANT = [
    "26 sessions, 24 kept, 2 excluded",
    "1 × too few folds",
    "1 × exit -9",
    "     3/12",    # fold recalled the oldest fact 3 times
    "     9/12",    # record recalled it 9 times
    "p = 0.039",    # Fisher on that table
    "    12/12",    # the counter-metric reversal
]

if __name__ == "__main__":
    with tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False) as fh:
        for r in ROWS:
            fh.write(json.dumps(r) + "\n")
        path = fh.name

    out = subprocess.run([sys.executable, "analyze.py", path],
                         capture_output=True, text=True).stdout
    bad = [w for w in WANT if w not in out]
    for w in bad:
        print(f"FAIL  the analyser did not report {w!r}")
    if bad:
        print(out)
    print(f"{len(WANT) - len(bad)}/{len(WANT)} analyser checks pass")
    sys.exit(1 if bad else 0)
