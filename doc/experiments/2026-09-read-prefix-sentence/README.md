# Explaining the read tool's line prefix

September 2026. Preregistered in [`preregistration.md`](preregistration.md)
(083fbe7), before any run.

**Result: one sentence saying where the file's text starts did nothing
measurable, with the arrow or with the tab. The tab plus the sentence did as
badly as the tab alone. The arrow is what fixed the indentation errors, not the
lack of an explanation. Nothing ships.**

## Question

[2026-09-read-separator](../2026-09-read-separator/) replaced the tab between
line number and line with an arrow, and one-tab-too-deep edits all but
disappeared. Claude Code's Edit tool tells its model to strip the Read tool's
"line number + tab" prefix before matching (its tool description, as seen on
2026-09-28); Strument's read tool described no format at all. Two questions:
would saying where the file's text starts remove the errors the arrow left, and
would the same sentence have made the tab good enough on its own?

## Design

Three arms ([`data/`](data/) holds both diffs): **arrow** (`dev` at e9a4408),
**arrow + sentence**, and **tab + sentence**, the sentence ending the read
tool's description: "Each line comes back as its number, an arrow, and then the
line exactly as in the file, indentation included: everything after the arrow
is the file's text, and an edit's old_string copies it from there." (The tab
version says "one tab" and "that first tab".) Each binary was checked for its
sentence before the run.

The world, prompt and scorer are the separator trial's: eight named edits at
known tab depths in `deep.go` and `page.html`, each `old_string` holding the
changed line and the one above, scored from the `edit` records. GPT-6 Luna and
MiMo-V2.6-Flash at `reasoning = "low"`, 8 runs per cell, 48 runs in an order
shuffled with seed 20260930.

## Results

| model | arm | edit calls | exact | loose | failed | exact share | +1 offsets | negative offsets |
|---|---|---|---|---|---|---|---|---|
| Luna | arrow | 71 | 55 | 13 | 3 | 77% | 6 | 7 |
| Luna | arrow + sentence | 81 | 63 | 11 | 7 | 78% | 2 | 9 |
| Luna | tab + sentence | 69 | 16 | 51 | 2 | 23% | 32 | 19 |
| MiMo | arrow | 65 | 54 | 10 | 1 | 83% | 0 | 10 |
| MiMo | arrow + sentence | 69 | 60 | 5 | 4 | 87% | 0 | 5 |
| MiMo | tab + sentence | 73 | 40 | 27 | 6 | 55% | 25 | 2 |

Exact two-sided permutation tests on per-run exact shares, run as the unit:

- arrow → arrow + sentence: Luna +0.00 (p = 0.97), MiMo +0.05 (p = 0.48).
- arrow → tab + sentence: Luna −0.55 (p < 0.001), MiMo −0.27 (p = 0.002).

Every run made all eight changes.

## Against the predictions

1. **The sentence raises the arrow's exact share.** Null for both models. Not
   adopted.
2. **MiMo's one-tab-short errors under the arrow get rarer.** 10 fell to 5, per
   run 1.25 to 0.62, p = 0.29. The direction held; the evidence does not.
3. **Tab + sentence does worse than the arrow.** Held, by more than predicted:
   it did as badly as the plain tab in the separator trial (Luna 23% then and
   now; MiMo 55% against 52%). The +1 errors per run went from 5.2 to 3.9 for
   Luna and 3.6 to 3.1 for MiMo, across two trials and not significant. Being
   told where the text starts barely changes a model's reading of a tab it can
   see.
4. **Deep undercounts persist in every arm.** Held: −3 and −4 on the 13-tab
   lines for Luna in all three arms.

The arrow arm replicates the separator trial: 77% for Luna (69% there) and 83%
for MiMo (85% there).

## What it means

The separator trial showed the tab causing the error. This one shows why a
description does not fix it: the model does not misread an instruction about
the prefix, it misreads the tab itself. A visible, non-whitespace boundary
works where a stated rule does not. Claude Code pairs a tab separator with an
instruction to strip it; on these two models, at least, the instruction alone
would not have been enough.

Failed edit calls rose with the sentence (Luna 3 to 7, MiMo 1 to 4, p ≥ 0.43),
all retried successfully. Noise at this size, but it does not argue for adding
text.

## Decision

No change: the read tool keeps the arrow and no sentence about it. The loose
matcher and its explanation stay the remedy for the deep undercount, which no
arm touched.

Data: [`data/results.jsonl`](data/results.jsonl),
[`data/records/`](data/records/) (every run's session record),
[`data/plan.json`](data/plan.json), [`data/run.py`](data/run.py).
