# Preregistration: the read tool's line-number separator

Written after the pilot (two runs, below) and before the main run.

## Question

The read tool prints each line as `%*d\t%s`: the number, a tab, the line. In a
tab-indented file the separator joins the indentation, so a line at three tabs
reads as a number followed by four. In a GPT-6 Luna run on a Go project, two
edits sent one tab more than the file had; in a deeply nested templ file, four
sent three or four fewer. Does a separator that is not whitespace,
`%*d→%s` (the form Claude Code's read tool uses), reduce indentation errors in
edits built from what was read?

## Arms

- **tab**: `dev` at 5be046f, unchanged.
- **arrow**: the same, with the one format string changed to `%*d→%s`.

Both arms carry the loose-match explanation added in 553f3a5, so a model in
either arm is told its offset after a loose match.

## Design

Two models: GPT-6 Luna (`openai/gpt-6-luna`) and MiMo-V2.6-Flash
(`xiaomi/mimo-v2.6-flash`), `reasoning = "low"`. One world: `deep.go`, a Go
function nested 2 to 7 tabs deep, and `page.html`, markup nested 1 to 13 tabs
deep. One prompt asking to read both files with the read tool, then make eight
named changes, each with its own edit call whose old_string holds the changed
line and the one above it. 5 repetitions per cell, 20 runs, order shuffled
with seed 20260929. `--yes steps --no-auto-commits`.

## Metrics (counts, from the `edit` records)

Per run: edit calls, and how each matched — exact, loose, or failed
(not_found or ambiguous). For each loose match, the signed tab offset (sent
minus file) from its recorded explanation. Primary: the share of edit calls
that matched exactly, per arm, per model. Counter-metric: the eight changes
landing (the diff touches 8 lines in each file set) — a separator that
confused models into failing edits would show there.

## Predictions

1. Luna: the arrow arm has a higher exact share than the tab arm.
2. The +1 offsets (one tab too many) appear in the tab arm and not in the arrow
   arm, for both models. They are the separator's signature.
3. Negative offsets on deep lines (too few tabs) appear in both arms: they are
   miscounting of long tab runs, which the separator does not cause.
4. The counter-metric does not get worse in the arrow arm.

## Decision rule

Switch the separator if prediction 1 holds, prediction 2 holds for at least
Luna, and the counter-metric does not regress. If only prediction 3 shows, the
separator is not the problem and the loose matcher plus its explanation is the
remedy.

## Pilot (Luna)

| arm | edits | exact | loose | failed | offsets |
|---|---|---|---|---|---|
| tab | 9 | 1 | 7 | 1 | +1 +1 +1 +1 −3 −3 +1 |
| arrow | 8 | 7 | 1 | 0 | −4 |
