# The read tool's line-number separator

September 2026. Preregistered in [`preregistration.md`](preregistration.md),
committed before the main run's results were read (a066796).

**Result: the tab between the line number and the line made models send edits
one tab too deep. An arrow in its place (`12→line`) cut those errors from 26 to
4 for GPT-6 Luna and from 18 to 0 for MiMo, and raised exact matches from 23% to
69% and from 52% to 85%. Shipped.**

## Where it came from

A GPT-6 Luna session on a Go project had six edits matched loosely: the line
matcher placed them after the text sent did not occur in the file. Replaying
every edit of that session against files rebuilt from its reads named them.
Two sent one tab more than the file had, in Go code at three tabs; four sent
three or four fewer, in a templ file nested nine to thirteen tabs deep. The
read tool printed `%*d\t%s`, so a line at three tabs came back as a number and
four tabs in a row — the separator joined the indentation. That explains the
first kind and not the second.

## Design

Two arms, differing in one format string: **tab** (`%*d\t%s`, `dev` at
5be046f) and **arrow** (`%*d→%s`, [`data/arrow-arm.diff`](data/arrow-arm.diff)).
The arrow was chosen from a remembered precedent: Claude Code's read tool
printing `     1→line`. That memory is unverified and may be out of date — Claude
Code is closed source, and its read tool returned `1<TAB>line` in the session
that ran this trial (2026-09-28). The preregistration states it as fact; this
corrects it. The result does not depend on it. Both arms carry the loose-match explanation
from 553f3a5, so either arm's model is told its offset after a loose match.

GPT-6 Luna and MiMo-V2.6-Flash, `reasoning = "low"`, 5 runs per cell, 20 runs
in an order shuffled with seed 20260929. One world: `deep.go` (Go nested 2–7
tabs, [`data/deep.go.txt`](data/deep.go.txt)) and `page.html` (markup nested
1–13 tabs, [`data/page.html`](data/page.html)). One prompt: read both files
with the read tool, then make eight named changes, each with its own edit call
whose `old_string` holds the changed line and the one above it
([`data/run.py`](data/run.py)).

Scored from the `edit` records each run leaves: how every edit call matched,
and for each loose match the signed tab offset (sent minus file) from its
recorded explanation. Counts, not judgments.

## Results

| model | arm | edit calls | exact | loose | failed | exact share | +1 offsets | negative offsets |
|---|---|---|---|---|---|---|---|---|
| Luna | tab | 48 | 11 | 35 | 2 | 23% | 26 | 9 |
| Luna | arrow | 49 | 34 | 10 | 5 | 69% | 4 | 6 |
| MiMo | tab | 42 | 22 | 18 | 2 | 52% | 18 | 0 |
| MiMo | arrow | 40 | 34 | 6 | 0 | 85% | 0 | 6 |

Per-run exact shares, exact two-sided permutation test with the run as the
unit (252 relabellings):

- Luna: tab 0.00, 0.30, 0.22, 0.36, 0.22; arrow 0.70, 0.50, 0.77, 0.62, 0.88.
  Complete separation, p = 0.008.
- MiMo: tab 0.50, 0.56, 0.62, 0.50, 0.44; arrow 0.50, 0.75, 1.00, 1.00, 1.00.
  p = 0.04.
- +1 offsets per run: Luna 7, 5, 5, 4, 5 against 1, 1, 2, 0, 0; MiMo 4, 3, 3, 4, 4
  against 0, 0, 0, 0, 0. p = 0.008 for both.

All 20 runs made all eight changes (8 lines changed in the two files).

## Against the predictions

1. **Luna's exact share is higher with the arrow.** Held, and for MiMo too.
2. **+1 offsets are the separator's signature.** Held: 44 in the tab arm across
   both models, 4 in the arrow arm, all Luna's.
3. **Deep undercounts appear in both arms.** Held for Luna: −3 and −4 on the
   13-tab lines in both arms. It is a second error, of counting long tab runs,
   and the separator does not cause it.
4. **The counter-metric does not regress.** Every run completed. But Luna's
   failed edit calls rose from 2 to 5 (each then retried and landed), and MiMo
   in the arrow arm sent one tab *too few* six times, in two of five runs — an
   error it never made with the tab. A model may read the arrow as standing in
   for a tab of indentation, which is how some renderings of `cat -n` output
   look. It is a smaller error than the one removed (6 against 18) and was
   corrected by the loose matcher every time.

## Decision

Switch the separator to the arrow, which the decision rule calls for. Shipped
with this write-up. The loose matcher and its explanation stay: they absorbed
every remaining offset in both arms, and the deep undercount is theirs to
catch.

## Caveats

One world, built to put edits at known depths, and a prompt that asks for the
line above in every `old_string`, which makes indentation errors visible as
uniform offsets. Real edits often send a mid-line span, which matches exactly
whatever the indentation. Two models, both with reasoning pinned low. The
effect is large enough that the sample is not the worry; its size on ordinary
work is unmeasured.

Data: [`data/results.jsonl`](data/results.jsonl) (one row per run),
[`data/records/`](data/records/) (every run's session record, pilots
included), [`data/plan.json`](data/plan.json) (the shuffled order).
