# Outlines in the read tool

**Status: trial.** Preregistered in [`preregistration.md`](preregistration.md)
(`884d68c`) before the main run.

**Result: none of the three changes reduced what a session read, and none cost
a correct answer.**

- **B** outlined a file when a read was cut short, and never once fired.
- **C** added an `outline` parameter with a prompt line. It was called in 2 of
  80 sessions.
- **D** also required `limit`. It was called in 6 of 80, all by Luna.
- **Input tokens on the tasks past the read window** were flat or higher
  against the baseline in every arm: p = 0.93, 0.28 and 0.29.

**Decision: no `read` change ships.** The arms are removed from the code, as
the rule says. The outline fix they rested on (`0235e81`) stays: the
`webfetch` outline of a source file now lists methods, not only file-scope
definitions, and gives each definition's signature.

The prediction, written down before the run, was a null.

## Design

As preregistered:

- **Arms:** four binaries from `cae2b9e`, differing only in the link-time
  string `coder.readArm`.
- **Fixture:** click (`06b2a67`) and rich (`9d8f9a3`) vendored side by side,
  with one planted change per task so that a memory of upstream gives the
  wrong answer.
- **Tasks:** eight.
  - Four lookups and two structural questions, each answered past line 2,000
    of its file.
  - Two controls inside the window.
- **Models:** MiMo-V2.6-Flash at `low`, and GPT-6 Luna at `high`. Luna's
  `low` reasons far less than MiMo's does.
- **Size and order:** five reps each, 320 sessions, shuffled with seed
  20260927.
- **Cost:** $0.50 in total. No session failed or timed out.

| arm | change |
| --- | --- |
| A | none |
| B | a read with no `limit` that the 2,000-line window cut short ends with an outline of the rest |
| C | B, plus `read`'s `outline` parameter, named in the description and in a system-prompt bullet |
| D | C, plus `limit` required, as maki requires it |

## Results

| arm | model | correct, past-window tasks | correct, controls | median input tokens, past-window tasks | outline calls (sessions) | reads that named a limit |
|---|---|---|---|---|---|---|
| A | MiMo | 25/30 | 10/10 | 43,870 | 0 (0/40) | 63/66 |
| A | Luna | 27/30 | 10/10 | 20,784 | 0 (0/40) | 26/26 |
| B | MiMo | 25/30 | 10/10 | 35,558 | 0 (0/40) | 73/76 |
| B | Luna | 26/30 | 10/10 | 23,043 | 0 (0/40) | 30/30 |
| C | MiMo | 25/30 | 10/10 | 52,563 | 0 (0/40) | 73/76 |
| C | Luna | 25/30 | 10/10 | 20,317 | 2 (2/40) | 40/40 |
| D | MiMo | 25/30 | 10/10 | 35,234 | 0 (0/40) | 75/75 |
| D | Luna | 25/30 | 10/10 | 25,468 | 6 (6/40) | 36/38 |

Pooled over models, against A:

| arm | median input tokens, past-window tasks | Mann-Whitney p | correct, all tasks | Fisher p |
|---|---|---|---|---|
| A | 27,664 | — | 72/80 | — |
| B | 28,371 | 0.93 | 71/80 | 1.0 |
| C | 30,036 | 0.28 | 70/80 | 0.80 |
| D | 30,576 | 0.29 | 70/80 | 0.80 |

No arm meets the rule. Per model, MiMo's medians move both ways across arms,
and Luna's are flat or higher. There is no reversal hidden by pooling.

### One task's key was wrong

**`flag` is invalid, and the plant is what broke it.** The fixture changed
`Option.flag_activation_value` to return `"on"` instead of `True`. But a
boolean flag's type is `BOOL`, which converts `"on"` back to `True`, so the
command's function still receives `True`. Luna's answer in one session said
exactly that (`luna-C-flag-1`): "True (the flag’s "on" activation value is converted to a boolean)". The key
said `"on"`, and 37 of 40 sessions "failed". Every other task was 20/20 for
both models in every arm.

- **What it did to the counts.** All of the correctness misses in the tables
  above are `flag`. Without it, correctness is 70/70 in every arm.
- **What it did to the tokens.** The contradiction between the code and the
  question made `flag` the costliest task by far. MiMo spent 200k–370k tokens
  on it, going back and forth between `consume_value`, `_pick_type` and
  `BOOL`, in the five most expensive sessions of the run.
- **Post hoc, without `flag`:** the median input tokens on the other five
  past-window tasks are 24,944 (B), 28,451 (C) and 30,488 (D), against
  25,350 (A). The p-values are 0.86, 0.37 and 0.23. Per model, the one p
  under 0.1 is Luna in D (21,702 against 19,201, p = 0.068), in the direction
  of *more* tokens.

The preregistered result stands either way. The lesson belongs in the
handbook: **a plant has to change the behavior the question asks about, not
just a line on its path.** This one was checked only by the fixture's count
assertion, which confirms the text changed. What would have caught it is
running the planted code once and looking at the value that comes out.

## Why nothing moved

- **B had nothing to act on.** Its outline attaches only to a read the
  2,000-line default window cut short. In 320 sessions there were 11 reads
  without a `limit`, and none of them was cut short. In every case the
  models searched first and then read the lines around the hit. The pilot's
  version of B, which also outlined ranged reads, is the one that would have
  fired, and it cost about 11 KB per read.
- **The models don't need a map to find a function.** Grep with context
  gives line numbers, and a ranged read around the hit is cheap. These tasks
  named identifiers, which is how a user usually asks. A question phrased
  purely by behavior might use the map more, but that was not tested.
- **The structural tasks were answered by grep too.** Both models listed a
  class's methods with a `def` search restricted to the file. That is
  one call, just like the outline.
- **Where the outline was used, it was used well.** Luna answered `exports`
  with one grep and one outline call. It did so in all five D sessions of
  that task and one C session. Its median there was 16.9k tokens against
  18.9k in A. That is 5 sessions against 5, not a result.
- **Requiring `limit` changed nothing.** Reads were already ranged: 89 of 92
  in A named a `limit`. maki's rule matches what these models do unasked.

## What this licenses

- **It licenses not shipping these three.** On these tasks, the map did not
  earn its schema space or its prompt line.
- **It does not license "outlines are useless".**
  - Two navigation-competent models answered identifier-shaped questions in
    two libraries.
  - A weaker model, a task phrased by behavior, or a file without greppable
    names could come out differently.
  - maki's reported saving is on its own usage, with a different `read` that
    has no default window at all.
- **It does not change the outline fix,** which corrects what `webfetch`
  shows regardless of whether a model asks for it.

## Transcripts read

- `luna-A-flag-0`: grep for `flag_value`, a 175-line ranged read at 3,134,
  then a grep for `flag_activation_value|consume_value`. It answered `"on"`,
  which matches the key but not click's behavior. This is the session that
  found the plant's flaw by contrast with the others.
- `luna-D-exports-0`: one grep for `class Console`, then
  `read {outline: true}` on `rich/console.py`, then the eight names. That is
  the pattern the arm was built for, in its best case.
- `mimo-C-flag-3`: the run's costliest session (374,591 tokens, 18 steps).
  It is a chain of greps and reads through `Option`'s flag handling, trying
  to reconcile `"on"` with the type conversion.

## Files

| file | contents |
| --- | --- |
| [`preregistration.md`](preregistration.md) | the arms, metrics and rule, committed before the run |
| [`data/fixture.py`](data/fixture.py) | the fixture builder, with its plants |
| [`data/run.py`](data/run.py) | the runner |
| [`data/score.py`](data/score.py) | the scorer, its answer-parser self-test, and the tests |
| [`data/scored.jsonl`](data/scored.jsonl) | one row per session |
| [`data/order.json`](data/order.json) | the shuffled run order |
