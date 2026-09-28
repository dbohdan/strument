# Preregistration: count read-only streaks in steps, not calls

Written after the pilot (two runs, below) and before the main run.

## Question

The tool-loop watcher (`internal/coder/toolloop.go`) nudges a model after 20
read-only tool calls since anything last changed, and ends the turn at 40. A
model that reads in parallel batches reaches 20 calls in a few steps. GPT-6
Luna, asked to get familiar with a repository, made 23 read calls in 4 steps
and was nudged; that was not a loop. Does counting *steps* that made read calls
(nudge at 20 such steps, stop at 40) remove those nudges without changing
anything for a model that calls one tool at a time?

## Arms

- **calls**: `dev` at 5be046f, unchanged.
- **steps**: the same, with the watcher counting a step once however many
  read calls it made (`beginStep`), and the note saying "steps".

## Design

Two models: GPT-6 Luna (`openai/gpt-6-luna`, parallel caller) and MiMo-V2.6-Flash
(`xiaomi/mimo-v2.6-flash`, the project default, mostly serial). Both
`reasoning = "low"`. Three worlds, each a fresh copy per run: catchup
(upstream HEAD), larkspur (this repository's `larkspur` branch), and Strument
itself at 5be046f. One prompt, the one from the Luna run: "Hi! Please get
yourself familiar with this repo." `--yes steps`, so the watcher is the only
limit. 3 repetitions per cell, 36 runs, order shuffled with seed 20260928.

## Metrics (counts)

Per run: nudged (0/1), turn outcome (a watcher stop shows as a non-Success
outcome), steps, tool calls, distinct files read, answer length in
characters, cost.

## Predictions

1. Luna: the calls arm is nudged in most runs; the steps arm in none.
2. MiMo (the counter-metric): nudge rates do not differ between arms, because
   a serial caller's calls and steps are nearly the same count.
3. No run in either arm is stopped by the watcher.
4. Open, not predicted: whether the nudge shortens Luna's exploration. The
   pilot suggests it may not: both arms answered after 4 steps.

## Decision rule

Adopt the steps count if prediction 1 holds and prediction 2 holds. If the
steps arm changes MiMo's nudge rate, it is not the harmless change it claims to
be, and the rule stays. Loop-catching is not tested live here: a real loop
cannot be provoked on demand. The offline scan of 106 recorded turns (see the
README) is the evidence for that side.

## Pilot

| arm | steps | calls | files read | nudged |
|---|---|---|---|---|
| calls | 4 | 23 | 8 | 1 |
| steps | 4 | 28 | 17 | 0 |
