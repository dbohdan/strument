# Consult: can MiMo tell Terra's writing from its own?

**Status:** characterization — layer 2 run. **Result: neither judge passed
either preregistered design.** Every lean points to Terra, none clears its
bar, and the likeliest reasons are a task set where both writers already do
well and a Terra that, at `reasoning = "low"`, did not reason at all.

The `consult` tool (`c59f806`) lets the model ask another model, configured in
`consult_models`, and get the answer back. Whether that is worth having for
the use it was built for — MiMo handing documentation work to a stronger
writer — is a question for a later trial (layer 4 below). That trial needs a
judge, and the plan was MiMo: the maintainer's impression is that GPT-6.1 Terra
writes documentation in a key close to GPT-6 Astra's and ahead of MiMo's, in a
way MiMo itself recognizes. This layer checks that premise before anything
rests on it.

The four layers, cheapest first:

1. Offline tests of the tool (in `internal/coder/consult_test.go`): done.
2. **This one.** Is MiMo a usable judge of Terra's writing against its own?
3. A live mechanics pass: does MiMo call `consult`, and send the files the
   question needs?
4. The trial: MiMo with and without a consultable Terra, on these tasks.

## The task set

Fifteen documentation tasks over four small fictional projects (`data/tasks.py`):
a Go CLI (`tally`), a Python log rotator (`rotor`), a TypeScript retry library
(`pinwheel`) and a Go library behind a model-facing search tool (`grepkit`).
Each task rewrites, or writes, one file — help text, error messages, a README
section, a config reference, a changelog entry, a JSDoc block, a deprecation
warning, an upgrade note, a tool description, a confirmation prompt, a commit
message, a doc comment — under a shared `STYLE.md`.

Six tasks carry a **plant**: the current text states something the code
contradicts (tally's precision default, rotor's `keep` default and its missing
`compress` option, pinwheel's attempt count and its `maxMs` cap). Plants were
verified by running the code — `go test` reading the flag defaults, Python
importing `DEFAULTS`, Node importing the TypeScript — not by matching its text,
and a sabotaged value in each was caught (experimenting.md §26, §17). In this
layer both writers are shown the code, so the plants test little here; they
exist for layer 4, where MiMo decides what a consult gets to see.

Each task has **fact checks**, counted rather than judged: patterns that must
and must not appear, flags that do not exist, format verbs a string must keep,
and whether a Go, Python or TypeScript file still parses. `tasks.py --check`
builds every fixture and runs each task's checks both ways — a reference
answer passes, the file as shipped fails — and the syntax checks were each
shown to fail on broken input.

## Layer 2 design

**Candidates** (`generate.py`): for each task, two versions from MiMo-V2.6-Flash
and two from GPT-6.1 Terra, each from one call with the same prompt — the
instruction, the task's files, and a request for the target's full contents in
one fenced block. Sixty candidates, already generated: all 60 produced a block
and all 60 pass their fact checks, so on this layer the checks do not separate
the writers and the judge's verdict is the measurement.

**Pairwise judge** (`judge.py`): per task, four pairs — Terra against MiMo for
sample 1 and sample 2 (**tm**), and two null pairs, MiMo 1 against MiMo 2
(**mm**) and Terra 1 against Terra 2 (**tt**). Each pair is shown in both orders
to each judge, blind, with the task and the project's files; the judge ends
with `Winner: A` or `Winner: B`. A pair is **consistent** when the same member
wins in both orders, a **split** when each order picks the version in one
position, and **missing** when a call failed or did not parse (§3).

**Single-candidate judge** (`defects.py`): each candidate is shown alone, with
the task and the files, and the judge lists its concrete defects. Judged twice
per candidate. The measure is the count. No judge ever sees two versions, so
this design cannot have a position bias, at the price of being a noisier scale.

**Judges:** MiMo-V2.6-Flash and DeepSeek-V4.1-Flash, so that a failure of MiMo
can be told from a failure of the method.

**Settings:** every model pinned to one provider with fallbacks off (MiMo to
Xiaomi, Terra to OpenAI, DeepSeek to Wafer). Writers at `reasoning = "low"`.
Terra on OpenAI's flex tier, which OpenRouter passes through — measured: the
response echoes `service_tier: "flex"` and the cost of the same request halves
exactly. **Judges with reasoning disabled**, because at "low" both judges spent
their whole budget deliberating over the judging prompts (6,000 tokens for
MiMo, 16,000 for DeepSeek) and wrote nothing parsable, and a reasoning-token
cap was ignored by both providers. Calls run in a seeded shuffled order.

## What the pilots found

Two pilots, on `tally-help` and `pin-readme` only, are kept out of the main
run's directory and its numbers.

- **Pilot 1**, judges at reasoning "low": MiMo picked the version shown first
  in **15 of 16** verdicts. The transcripts show the mechanism: in one order
  MiMo praised Terra's line about empty fields as coverage a user needs, in
  the other it called the same line surplus the task did not ask for. Two of
  DeepSeek's sixteen ran out of tokens with no verdict.
- **Pilot 2**, reasoning disabled: MiMo picked A in 11 of 16 and DeepSeek in
  12 of 16. On two tasks neither judge separated the writers, and defect
  counts were 0–1 for most candidates from either. DeepSeek sometimes loops
  ("Final: none." 84 times) to the token limit; the scorer counts what it
  can parse and reports the rest as missing.
- Equipment faults fixed along the way, each before any main-run call: a
  fixture flag added for plant verification that both writers documented as
  real (now read by a test file written only in the check's build directory);
  an OpenRouter 200 response carrying an error and no choices, first read as
  an empty answer; a mid-stream `finish_reason: "error"` and dropped
  connections, now retried; a TypeScript syntax check that could not fail; a
  fact check that rejected "two digits" for "2".

## Preregistered criteria

Fixed before the main run, on all fifteen tasks, 30 Terra-against-MiMo pairs
per judge.

**MiMo is a usable pairwise discriminator** if both hold:

1. Of its consistent verdicts on tm pairs, Terra wins at least 70%, with a
   two-sided exact binomial p below 0.05 against ½.
2. Its consistency rate on tm pairs exceeds its rate on the null pairs (mm and
   tt pooled) by at least 15 points. A judge whose verdicts are as stable on
   two samples of one writer as on two writers is reading something other
   than the difference in writers.

**MiMo is a usable single-candidate discriminator** if Terra's candidates have
fewer defects than MiMo's in the paired comparison with a sign-test p below
0.05, and MiMo gives the same count on both reps for at least 60% of
candidates.

The same criteria are reported for DeepSeek.

**What follows from each outcome:**

- MiMo passes either design: it judges layer 4, in that design.
- MiMo fails and DeepSeek passes: DeepSeek judges layer 4, and the premise
  that MiMo recognizes Terra's writing does not hold on these tasks.
- Both fail: on this task set a judge cannot see a Terra–MiMo difference.
  Either the difference is small here — the tasks may be too easy, since every
  candidate already passes its fact checks — or it is not what these judges
  measure. Layer 4 would then rest on its counted metrics (the plants,
  integration fidelity, cost), and a harder task set would come before it.

**Reported whatever the outcome:** each judge's first-position rate, its
consistency on null pairs, unparsed and missing calls, the two judges'
agreement on tm pairs both judged consistently, and cost.

## Results

Run on all fifteen tasks after the preregistration (`cf8581f`): 240 pairwise
and 240 single-candidate judge calls, none failed, every pairwise verdict
parsed. Judges cost $0.06; the whole layer, with candidates and both pilots,
about $0.35. Full tables in `data/scores.txt`; raw records in
`data/runs.tar.xz` (main run and pilots), candidates in
`data/candidates.jsonl`.

### Against the criteria

| judge | design | criterion | measured | passes |
|---|---|---|---|---|
| MiMo | pairwise | Terra ≥ 70% of consistent tm verdicts, p < 0.05 | 6 of 15 (40%), p = 0.61 | no |
| MiMo | pairwise | tm consistency ≥ null + 15 points | 50% against 40% (+10) | no |
| MiMo | single | Terra fewer defects, sign p < 0.05 | 7 Terra fewer, 1 MiMo fewer, 22 ties; p = 0.070 | no |
| MiMo | single | same count on both reps ≥ 60% | 49 of 60 (82%) | yes |
| DeepSeek | pairwise | Terra ≥ 70%, p < 0.05 | 11 of 15 (73%), p = 0.118 | no |
| DeepSeek | pairwise | tm consistency ≥ null + 15 points | 50% against 37% (+13) | no |
| DeepSeek | single | Terra fewer defects, sign p < 0.05 | 8 and 8, 13 ties; p = 1.0 | no |
| DeepSeek | single | same count on both reps ≥ 60% | 40 of 52 (77%), 9 unparsed | yes |

By the preregistered rule this is the **both-fail** outcome: on this task set
neither judge sees a Terra–MiMo difference it can stand behind.

### What else the numbers say

- **Position bias, reasoning off:** MiMo picked A in 91 of 120 parsed
  verdicts (76%), DeepSeek in 82 of 120 (68%). Half of all Terra-against-MiMo
  pairs were splits for both judges. Pairwise judging spends most of its
  calls measuring the order.
- **The judges do not agree.** Of the nine tm pairs both judged consistently,
  they picked the same winner in four.
- **The single-candidate judge is the better instrument, even here.** It
  cannot have a position bias, MiMo repeated its own count 82% of the time,
  and its defects are concrete and checkable. Read in full, they are mostly
  right: MiMo's own `grep-tool` description left out the required `pattern`
  parameter and `ignore_case`, and broke gofmt alignment on the line it was
  told not to touch; a pinwheel README said a delay follows every failed
  attempt, which the code skips after the last; a rotor config page said
  "any other value turns it off" where the code accepts exactly three. Its
  lean, 7 against 1, is the strongest signal in the layer and still misses
  p < 0.05.
- **Both writers are good at these tasks.** All 60 candidates pass every fact
  check, and MiMo's judge found a mean 0.27 defects in MiMo's candidates and
  0.05 in Terra's. With 22 of 30 pairs tied, there is little left for any
  judge to find.

### Why, and what it changes

- **Terra was not reasoning.** At `reasoning = "low"`, 25 of Terra's 30
  generations used zero reasoning tokens (the other five, 32–173). The
  maintainer's impression of Terra and Astra comes from their defaults. Pinning
  reasoning low is the right rule for a harness trial, where reasoning is a
  cost to control; here the writer's quality is the thing measured, and the
  pin may have removed most of the gap the layer was looking for.
- **The tasks are short.** A help text or one error message leaves little
  room for a better writer to be better, and the fact checks — the counted
  half — were all passed by both. Documentation where the maintainer felt the
  difference is longer: whole pages, user-facing explanation.
- **A fixture fault:** `rotor-readme` asks what rotor does, but `rotor/cli.py`
  only prints its settings, so both judges rightly flagged both writers for
  claiming it rotates logs. It biases neither writer and is noted rather than
  fixed after the run.
- **A check gap:** the Go syntax check is `gofmt -e`, which parses but does
  not compare formatting, so the alignment MiMo broke passed it.

### Next

Before layer 4, a second judge check that changes one thing at a time:
Terra at its default reasoning on this task set (does the gap appear when
Terra thinks?), then a harder set of longer documents. The single-candidate
defect judge is the instrument for both, with three samples per writer; the
pairwise judge is dropped. Whichever passes decides layer 4's judge.

### A Strument bug found on the way

One pilot call came back from MiMo's Xiaomi endpoint as 1,066 tokens of
reasoning, no answer, and `finish_reason: "error"`. Strument's stream loop
accepted that finish as complete, so a turn — or a consult — could take a
fragment as the answer. Fixed in `c5ec76f`: it is retried as a server error.
