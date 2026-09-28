# Preregistration: explaining the read tool's line prefix

Written before any run. Follows
[2026-09-read-separator](../2026-09-read-separator/), which replaced the tab
between line number and line with an arrow.

## Question

With the arrow, GPT-6 Luna still matched 69% of edits exactly and MiMo 85%;
MiMo gained a one-tab-short error it never made with the tab. Claude Code's own
Edit tool tells the model to strip the Read tool's "line number + tab" prefix,
and Strument's read tool says nothing about its format. Does one sentence in
the read tool's description, saying where the file's text starts, reduce the
remaining indentation errors? And would that sentence alone, with the old tab
separator, have done as well as the arrow?

## Arms

- **arrow**: `dev` at e9a4408, unchanged.
- **arrow + sentence**: the read description ends "Each line comes back as its
  number, an arrow, and then the line exactly as in the file, indentation
  included: everything after the arrow is the file's text, and an edit's
  old_string copies it from there."
- **tab + sentence**: the tab separator restored, and the same sentence saying
  "one tab" and "that first tab".

The binaries were checked for the sentence before the run: present in its arm,
absent from the others.

## Design

As in the separator trial: GPT-6 Luna and MiMo-V2.6-Flash at `reasoning =
"low"`, the same `deep.go`/`page.html` world, the same eight-edit prompt and
scorer. 8 runs per cell (the arrow arm leaves less room than the tab arm did),
48 runs in an order shuffled with seed 20260930.

## Metrics

From the `edit` records: the per-run exact share, and the signed tab offsets
of loose matches. Counter-metric: all eight changes landing.

## Predictions

1. Primary: arrow + sentence has a higher exact share than arrow, for both
   models. I expect a small effect and would not be surprised by a null at
   this size.
2. MiMo's −1 offsets under the arrow get rarer with the sentence.
3. Tab + sentence does worse than arrow: a sentence cannot undo a separator
   that looks like indentation. Its +1 offsets fall from the separator trial's
   tab arm, but do not vanish.
4. Deep undercounts (−3, −4 on the 13-tab lines) persist in every arm.

## Decision rule

Add the sentence to the arrow if prediction 1 holds for at least one model
without the other getting worse, and the counter-metric holds. If tab +
sentence matches or beats the arrow, the write-up says so, and the separator
choice is revisited rather than defended.
