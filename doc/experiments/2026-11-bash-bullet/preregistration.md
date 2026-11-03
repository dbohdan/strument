# Preregistration: the bash bullet, after the pilot

Written and committed on 2026-11-03, after the pilot and before the main run.
The design is in [`README.md`](README.md). This file records what the pilot
changed and fixes the rule as it now stands. Where the two disagree, this file
wins.

## The pilot

Eight sessions, one per cell, from `564be22` plus [`data/trial.diff`](data/trial.diff)
for arm B. They cost $0.02 in all.

**The first run task could not contain the effect.** It asked what `report()`
returns for five given lines. Both arm A run sessions answered by running the
code with `node -e`, and both were right. So were both in arm B. A question that
names an input reads as a request to run it, whatever the bash bullet says. The
comparison this trial follows from asked "does the state proxy track nested
objects and arrays?", a question about a mechanism, where running is a choice.
That is the situation the trial has to contain (handbook §18, "a fixture that
cannot contain the phenomenon").

**The run task is now four yes/no questions about behavior** (`fixture.RUN_TASK`):
1. Does `tally()` add every count on a line into that code's total? The answer
   is no, because of `map(parseInt)` and the index as radix.
2. Does `report()` give the totals in ascending numeric order? No, because of
   the default sort on numbers.
3. Does `report()` list the codes in the order they first appear in the input?
   No, because integer-like keys come first.
4. Does a code that appears on two lines get one combined total? Yes.

The key `no,no,no,yes` is computed under Node by `fixture.py --check`, which
also shows that neutralizing each trap flips its answer. The format line gives
no example answer. The first draft's example, `yes,no,no,yes`, sat one answer
from the key.

**A second pilot of the four run cells** under the new task gave:

| session | executed | answer | correct |
| --- | --- | --- | --- |
| A, MiMo | yes | no,no,no,yes | yes |
| A, DeepSeek | no (three `run_code` reads) | yes,no,no,yes | no |
| B, MiMo | yes | yes,no,no,yes | no |
| B, DeepSeek | yes | no,no,no,yes | yes |

- **Execution varies**, so the mechanism can show up.
- **Correctness is off the ceiling and off the floor.**
- **B MiMo shows the second failure the review named.** It ran code, but its
  probes checked ordering and never a line's counts, so question 1 went
  unchecked. "Executed" and "correct" can come apart, which is why both are
  ruled on.
- **The read task is unchanged.** All four pilot sessions answered it
  correctly, and none of them executed anything.

**Checks:**
- Reasoning `effort: low` is on the wire for both models. Their reasoning
  tokens ran 8 to 465 per request.
- No key appears in any capture.
- The scorer's self-test (`score.py --self-test`) passes on hand-made correct,
  wrong, unparseable and missing answers, and on the `executed` and
  project-reach patterns, both positive and negative.
- The resume path skips finished sessions and reruns one whose `done.json`
  was removed. This was exercised with `--stub`.
- The two arm binaries were built from one tree. They differ by the diff
  above, which touches only the bash bullet.

**Pilot sessions are not counted.**

## What changed for the main run

- **The run task**, as above.
- **Reps: 20 per cell, not 10**, so 2 arms × 2 tasks × 2 models × 20 = 160
  sessions. Sessions cost about $0.003, not the $0.03 the design guessed, so
  the larger sample costs about $0.50. A sample twice as large was chosen
  before any main-run result was seen.
- **Thresholds that were counts of 20 are now counts of 40.** Seed 20261103
  and four sessions at a time are unchanged.
- **Per-question correctness is reported**, descriptively, beside the
  all-four score.

## Rule

All p-values are Fisher two-sided, pooled over both models, n = 40 per arm per
task.

1. **It fires.** On the run task, executed(B) > executed(A) at p < 0.05. If
   not, B is reported as a placebo and nothing else is ruled on.
2. **It helps.** On the run task, correct(B) > correct(A) at p < 0.05, and
   neither model's correct count is lower in B.
3. **It does not cost where it cannot help.** All three must hold:
   - on the read task, executed(B) ≤ executed(A) + 8;
   - B's median read-task cost is at most 1.25× A's, for each model;
   - correct(B) on the read task is at most 2 below correct(A).

B ships if 1, 2 and 3 all hold. If 1 holds and 2 does not, B does not ship.
The write-up then reads the transcripts and says which cause it was:
- A is already at ceiling;
- reading was enough;
- the probes missed the planted behavior, as the B MiMo pilot session's did.

The last of these would be a case for the review's edge-case sentence, as a
separate arm.

## Prediction

Revised from the design's after the pilot. I made the revision before any
main-run session.
- **executed (run task):** A about 50%, B about 80%.
- **correct (run task):** both arms between 30% and 60%, and B above A by
  less than executed moves. Question 1 is the one most often missed, in both
  arms. Rule 2 failing is still the outcome I expect most.
- **read task:** executed under 10% in both arms.
