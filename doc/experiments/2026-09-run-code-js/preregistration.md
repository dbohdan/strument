# Preregistration: does a JavaScript run_code cut the first program's reach for the host?

Written 2026-09-24, before any model call.

## Question

Models treat Monty as full Python and reach for os/open/glob in their first
program (2026-09-code-namespace: 46% of first programs). Would the same tool
described as JavaScript avoid that, or would models reach for Node instead
(require('fs'), process, fetch)?

## Method

A text-only probe. No JS engine is built: the first program's text is the
measurement. Strument's real request (captured from HEAD 5fc31e9 in the
fixture project) is sent to each model. The Monty arm is the capture
unchanged; the JS arm differs in exactly three passages (the system prompt's
run_code bullet, the run_code description, the code parameter's description),
translated to JavaScript with an options-object calling convention. read,
grep, glob, ls and symbol calls are answered with real results from
`strument tool`; any other tool call is answered "not available". The session
stops at the first run_code call (recorded), at a final answer (no program),
or after 8 steps.

Fixture: a neutral Go project with docs, data files, a CSV, a log and notes —
no package.json and no Python, so the repository does not favour either
language.

Six tasks (walk, total, top5, span, longest, cite), three models
(xiaomi/mimo-v2.6-flash, z-ai/glm-5.3-flash, deepseek/deepseek-v4.1-flash) at
reasoning effort low, five reps, two arms: 180 sessions, order shuffled with
seed 20260924, at most four in flight.

## Primary metric

Among sessions that wrote a program, the share whose FIRST program reaches for
the host environment:

- Python: import of os, glob, subprocess, pathlib, shutil or io; open(; os.;
  Path(; subprocess.
- JavaScript: require(; an import statement or import(); fs.; process.;
  fetch(; Deno.; Bun.; __dirname; std.; os.; XMLHttpRequest.

The classifier must pass a self-test on known positive and negative programs
in both languages before it scores anything.

## Decision rule

JS is worth a full trial (engine, bridge, live execution) only if its
first-program host-reach rate is at most half of Monty's AND the pooled
difference is significant (Fisher exact, two-sided, p < 0.05). Otherwise the
JS idea is dropped.

## Secondary, reported whatever the primary says

- Uptake: sessions that wrote any program, per arm (a language that is used
  less makes per-program rates conditional on selection).
- JS-specific hazards in the first program: Python-style keyword arguments
  (grep(pattern="x")), TypeScript syntax, .sort() with no comparator.
- Monty-specific hazards: with/match/del statements, eval/exec.
- Counter-metric: programs written for `cite`, which needs none.
- Per-model tables: providers disagree.

## Not measured

Recovery after a failure, execution correctness, and silent wrong answers from
running code. Those need an engine and belong to the full trial, if the rule
above calls for one.

## Amendment after the pilot, before the main run

The six-session pilot (one per model and arm) wrote programs in 2 of 6
sessions: the data files were small enough to read and rank by eye (a
40-row CSV), which is the handbook's fixture-cannot-contain-it. The data
files were enlarged about tenfold (400-row CSV, 600-line log, 150–250
numbers per file, 300-line notes) and reps raised from 5 to 8 (288
sessions), because the primary metric conditions on writing a program.
The pilot's sessions are set aside and not scored. Nothing else changed.
