#!/usr/bin/env python3
"""Regression tests for script/transcript-audit.py.

Two defects produced plausible-looking output that was wrong, and neither
raised:

  1. A2's median was reported as a (path, distance) tuple — e.g.
     "median distance ('calc.go', 0)" — because statistics.median() was
     called over a list of tuples instead of over the distances. Python
     orders tuples, so there was no exception.
  2. The per-file summary line named an edited source file instead of the
     transcript being reported on, because a loop variable `path` shadowed
     the transcript's filename.

Each test below fails if the corresponding defect comes back. The median
test asserts on the *type* of the value (a real number), not merely on a
value that happens to compare equal, so a tuple that compares equal to the
expected median would still fail it.
"""

import ast
import importlib.util
import io
import numbers
import os
import re
import unittest
from contextlib import redirect_stdout

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "transcript-audit.py")
FIXTURES = os.path.join(HERE, "testdata", "transcript-audit")


def load_audit():
    """Import transcript-audit.py; the hyphen makes plain `import` unusable."""
    spec = importlib.util.spec_from_file_location("transcript_audit", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


audit = load_audit()


def run_audit(fixture):
    """Run audit() on one fixture, returning what it printed."""
    path = os.path.join(FIXTURES, fixture)
    buf = io.StringIO()
    with redirect_stdout(buf):
        audit.audit([path])
    return buf.getvalue()


def per_file_totals(out):
    """Map path -> A1 total for every "PATH  total A1 = N" summary line."""
    totals = {}
    for line in out.splitlines():
        m = re.match(r"^(.*)  total A1 = (\d+)$", line)
        if m:
            totals[m.group(1)] = int(m.group(2))
    return totals


class TranscriptAuditRegressionTest(unittest.TestCase):
    def test_a2_median_is_numeric(self):
        out = run_audit("read-then-edit.jsonl")
        for line in out.splitlines():
            m = re.search(r"median distance (.*), max", line)
            if m:
                median = ast.literal_eval(m.group(1).strip())
                # The defect printed a (path, distance) tuple here; a tuple is
                # not a Real, so this fails if the defect comes back.
                self.assertIsInstance(median, numbers.Real)
                return
        self.fail(f"no A2 median line in audit output:\n{out}")

    def test_summary_names_the_transcript(self):
        for fixture in ("read-then-edit.jsonl",
                        "write-new-file.jsonl",
                        "blind-edit.jsonl"):
            out = run_audit(fixture)
            totals = per_file_totals(out)
            self.assertIn(
                os.path.join(FIXTURES, fixture),
                totals,
                msg=f"summary line does not name the transcript for {fixture}:\n{out}",
            )

    def test_a1_ground_truth(self):
        # Ground truth from real sessions; these must not move.
        expected = {
            "read-then-edit.jsonl": 0,
            "write-new-file.jsonl": 0,
            "blind-edit.jsonl": 1,
        }
        for fixture, want in expected.items():
            out = run_audit(fixture)
            totals = per_file_totals(out)
            self.assertEqual(
                totals[os.path.join(FIXTURES, fixture)],
                want,
                msg=f"A1 moved for {fixture}:\n{out}",
            )


if __name__ == "__main__":
    unittest.main()