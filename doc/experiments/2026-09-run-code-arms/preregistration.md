# Preregistration: Monty with open() against JavaScript, live

Written 2026-09-24, before any model call. Binary: `030ab98`, the three arms
behind `--run-code-arm`.

## Question

The text-only probe (`../2026-09-run-code-js`) found the first program's
reach for a filesystem gone when run_code is described as JavaScript (0/100
against 11/97), almost all of it MiMo's and most of it `open()`. Two answers
to the same programs, now run for real:

- **monty** — the shipped behaviour: `open` and `pathlib` reads are refused.
- **monty-open** — `open(path)` for reading, with or without `with`, and
  `Path(path).read_text()` work, answered through `read_text`.
- **js** — goja, with the same bridge, shapes, caps and hint placement.

Which one makes the first program fail least, without costing correct
answers?

## Method

Real Strument sessions in script mode (`-m`), one binary, the arm chosen by
the hidden flag. Each session gets its own copy of the probe's fixture (a
neutral Go project; `data/fixture.py` here reproduces it) and its own state
directory. `--no-git --yes steps`: the step limit is lifted, but `bash` is
not approved, so it is declined as in any unattended run. That is deliberate —
with bash approved, a model could compute in `python3 -c` and bypass the arm.

The six tasks of the probe, each followed by the same sentence in every arm:
"Put the answer on its own line, beginning with ANSWER:". Keys, computed from
the fixture:

| task | question | key |
| --- | --- | --- |
| walk | Markdown files under docs/ mentioning "retry budget" | 5 |
| total | sum of the numbers in data/*.txt | 512180 |
| top5 | five largest entries in data/sizes.csv, largest first | asset-106, asset-377, asset-052, asset-197, asset-024 |
| span | days between the earliest and latest date in logs/app.log | 239 |
| longest | length of the longest line in notes.md | 263 |
| cite | line of notes.md with the XYZZY marker | 212 |

Three models (MiMo-V2.6-Flash, GLM-5.3-flash, DeepSeek-v4.1-flash) at
reasoning effort low, 6 reps: 3 × 3 × 6 × 6 = 324 sessions, shuffled with
seed 20260925, four in flight.

## Primary metric

Among sessions that wrote a program, the share whose **first program
failed** — its tool result begins "The program failed". Two comparisons, each
against monty: monty-open and js (Fisher exact, two-sided).

## Counter-metric, weighted equally

**Correct answers**, per arm, over all sessions: the ANSWER line matches the
key (numbers as integers, ignoring separators; top5 as the five names in
order). An arm that fails less but answers worse does not win.

## Secondary

- Failure causes in first programs: host reach (the probe's classifier),
  other errors, time or call limits.
- **Silent wrong answers**: a wrong ANSWER in a session none of whose
  programs failed — the error the model never saw.
- Programs per session, steps, input tokens and cost per session.
- Per-model tables.
- Uptake: sessions that wrote a program.

## Decision rule

An arm is shipped if its first-program failure rate is significantly lower
than monty's (p < 0.05) and its correct-answer rate is not lower than
monty's by more than 5 points. If both qualify, the one with more correct
answers; on a tie in that too, monty-open, the smaller change. If neither
qualifies, monty stays and both arms are removed.

## What would make this result wrong

- The arms differ in more than the feature: checked by diffing the three
  arms' requests, captured through a stub, before the run.
- A scorer that misreads an answer: it self-tests on crafted ANSWER lines in
  both directions before reporting, and every wrong answer is read by hand.
- A runner that dies quietly: it is resumable, waits on its own pid, and a
  job's outcome records its exit status.
