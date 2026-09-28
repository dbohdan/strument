# Counting read-only streaks in steps rather than calls

September 2026. Preregistered in [`preregistration.md`](preregistration.md),
committed before the main run's results were read (a066796).

**Result: counting steps removed every nudge in a read-only task (Luna 4/9 to
0/9, MiMo 2/9 to 0/9), but the nudge changed nothing measurable about how either
model explored, and the preregistered rule keeps the call count. Not shipped.**

## Where it came from

The tool-loop watcher (`internal/coder/toolloop.go`) sends a note after 20
read-only tool calls since anything last changed, and ends the turn at 40.
Asked to get familiar with a repository, GPT-6 Luna made 23 read calls in 4
steps — it reads in parallel batches — and was told it might be looping. It was
not. The question was whether that nudge cut its exploration short, and whether
counting steps, which a serial model's calls match anyway, would be the harmless
fix.

## Offline first

[`data/scan_loops.py`](data/scan_loops.py) replays the watcher's rule over the
106 turns recorded in this directory's transcripts. Those models averaged 1.8
calls a step, so calls and steps track each other: of the turns that crossed
20 calls, most also crossed 10 steps, and the one long streak (mimo-B-1, 27
steps and 55 calls) crosses 20 on either count. Luna's turn was the outlier: 23
calls in 4 steps.

## Design

Two arms: **calls** (`dev` at 5be046f) and **steps**
([`data/steps-arm.diff`](data/steps-arm.diff): a step's reads count once, and
the note says "steps"). GPT-6 Luna and MiMo-V2.6-Flash, `reasoning = "low"`.
Three worlds, fresh per run: catchup, this repository's `larkspur` branch, and
Strument itself. The prompt from the Luna session, "Hi! Please get yourself
familiar with this repo." `--yes steps`, so the watcher is the only limit. 3
runs per cell, 36 in an order shuffled with seed 20260928
([`data/run.py`](data/run.py)).

## Results

| model | arm | nudged | stopped | steps | calls | files read | answer chars |
|---|---|---|---|---|---|---|---|
| Luna | calls | 4/9 | 0 | 4.1 | 18.8 | 10.1 | 1400 |
| Luna | steps | 0/9 | 0 | 3.6 | 19.9 | 9.6 | 1366 |
| MiMo | calls | 2/9 | 0 | 6.4 | 15.9 | 7.9 | 2983 |
| MiMo | steps | 0/9 | 0 | 5.7 | 12.8 | 6.6 | 3130 |

Means per run. Exact permutation tests on steps, calls, files read and answer
length find no difference in either model (every p > 0.13). No run in either
arm reached 20 steps, so the steps arm's zero is its threshold not being met,
not a scorer that cannot see its note.

## Against the predictions

1. **Luna is nudged in most calls-arm runs, none in the steps arm.** Half held:
   4/9 is not most, and 0/9 is none.
2. **MiMo's nudge rate does not differ.** Failed. MiMo also reads in parallel
   on this prompt (over two calls a step), and was nudged in 2/9 calls-arm runs.
3. **No run is stopped.** Held.
4. **Open: does the nudge shorten exploration?** No sign of it. Nudged runs
   answered after as many steps, having read as many files, as runs that were
   not. The pilot already hinted at this — both arms answered after 4 steps —
   and it held at n = 36. The note arrives at about the point a model is
   finishing anyway.

## Decision

Keep counting calls. The preregistered rule said not to adopt the steps count
if it changed MiMo's nudge rate, and it did. That rule was written expecting
MiMo to be a serial control, which it was not here, so the failure says less
against the change than the rule's wording suggests. But the case for the change
was that the nudge does harm, and on this task it does none that could be
measured, while counting steps would make the watcher slower to see a loop
that reads in parallel. A false "you may be looping" costs a sentence of
context; a loop left running costs the turn.

## Caveats

One task, the one where the nudge was observed, and a read-only one. A task
that alternates reading and editing resets the streak on every edit and would
show nothing here. Loops were not provoked; the loop side rests on the offline
scan.

Data: [`data/results.jsonl`](data/results.jsonl), [`data/plan.json`](data/plan.json).
