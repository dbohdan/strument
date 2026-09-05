#!/usr/bin/env python3
"""Regression tests for transcript-audit.py.

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


def totals(out):
    """Return the A1 total from the "TOTAL across all transcripts" block."""
    for line in out.splitlines():
        m = re.match(r"^  A1 blind edits\s+(\d+)$", line)
        if m:
            return int(m.group(1))
    return None


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
            transcript = os.path.join(FIXTURES, fixture)
            out = run_audit(fixture)
            # Every session block header must name the transcript that was
            # reported on.  The defect printed an edited source filename in
            # that slot, so the transcript's name was simply absent from the
            # line.
            header_lines = [
                line for line in out.splitlines()
                if re.search(r"session \d+  model=", line)
            ]
            self.assertTrue(
                header_lines,
                msg=f"no session block header in audit output for "
                    f"{fixture}:\n{out}",
            )
            for line in header_lines:
                self.assertIn(
                    transcript,
                    line,
                    msg=f"session header for {fixture} does not name the "
                        f"transcript file:\n{line}",
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
            self.assertEqual(
                totals(out),
                want,
                msg=f"A1 moved for {fixture}:\n{out}",
            )

    def test_a1_pinned_edit_is_not_blind(self):
        # The model edited a pinned file with no read at all; the pin means
        # the file was already in its context, so A1 must not count it.
        out = run_audit("pinned-edit.jsonl")
        self.assertEqual(totals(out), 0, msg=f"A1 moved for pinned-edit:\n{out}")

    def test_a1_unpinned_blind_edit_still_reported(self):
        # The counterpart to the pinning fix: without this, "never report
        # anything" would also pass test_a1_pinned_edit_is_not_blind.
        out = run_audit("blind-edit.jsonl")
        self.assertEqual(totals(out), 1, msg=f"A1 moved for blind-edit:\n{out}")


if __name__ == "__main__":
    unittest.main()