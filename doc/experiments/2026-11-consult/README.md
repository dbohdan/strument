# Consult: can MiMo tell Terra's writing from its own?

**Status:** characterization — layer 2 and two follow-ups run. **Result:
MiMo's single-candidate defect judge separates Terra from MiMo, and the
blind pairwise judge does not.** In layer 2 the defect judge leaned Terra 7 to
1 (p = 0.07), short of its bar; the follow-up's replication, in a fresh run,
gave 11 to 0 (p = 0.001), and Terra at "low" drew no defects at all from it.
Terra at its default reasoning did *not* do better: 11 to 7 (p = 0.48), with
more defects than at "low". Telling MiMo who wrote each version changed
nothing measurable, and it never mentioned the authors.

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

## Follow-ups, preregistered before running

Two probes, both on this task set, fixed before either ran.

**Terra at its default reasoning.** Two more Terra samples per task with no
reasoning setting sent, so the provider's default applies (a two-task pilot,
kept out of the run: 148 and 213 reasoning tokens, against 0 at "low"). The
single-candidate defect judge then scores all ninety candidates — MiMo's, Terra
at "low", Terra at default — in one shuffled run, MiMo and DeepSeek judging,
two reps each. The layer-2 defect counts are not reused: one run, so no
writer's counts come from a different hour than another's, and Terra at "low"
gets a replication of its 7-to-1 lean.

- **The gap appears when Terra reasons** if, for the MiMo judge, Terra at
  default has fewer defects than MiMo in the paired comparison with a sign-test
  p below 0.05, and MiMo repeats its count on at least 60% of candidates.

**Label swap.** MiMo judges the thirty Terra-against-MiMo pairs (Terra at
"low") told who wrote each version — "written by GPT-6.1 Terra" or "written by
you, MiMo-V2.6-Flash" — truthfully in half the calls and swapped in the other
half, each in both orders: 120 calls, reasoning disabled as before. Under
swapped labels, the version credited to Terra is MiMo's own.

- **Deference** if, under swapped labels, MiMo's consistent verdicts favour the
  version *credited* to Terra in at least 70%, with p below 0.05.
- **Recognition** if, under swapped labels, they favour the version Terra
  *actually* wrote in at least 70%, with p below 0.05.
- Otherwise neither, and the per-call label effect — how often MiMo picks
  Terra's actual text with true labels against swapped ones (Fisher) — is
  reported as the size of whatever the label does.

## Follow-up results

Run after the amendment (`62e27e0`): 30 Terra generations at default
reasoning, then 360 defect-judge calls over all ninety candidates and 120
label-swap calls, none failed. About $0.44. Tables in
`data/follow-up-scores.txt`, records in `data/follow-up-runs.tar.xz`, the new
candidates in `data/candidates.jsonl`.

### Terra at its default reasoning

At default, Terra reasoned 44–504 tokens per task (median 113), against 0 in
25 of 30 at "low". All thirty candidates pass every fact check.

| judge | writer | mean defects | candidates with any | fewer than MiMo | tie | MiMo fewer | p |
|---|---|---|---|---|---|---|---|
| MiMo | MiMo | 0.35 | 11 of 30 | — | — | — | — |
| MiMo | Terra, low | 0.00 | 0 of 30 | 11 | 19 | 0 | 0.001 |
| MiMo | Terra, default | 0.17 | 8 of 30 | 11 | 12 | 7 | 0.48 |
| DeepSeek | MiMo | 0.45 | 14 of 30 | — | — | — | — |
| DeepSeek | Terra, low | 0.38 | 10 of 30 | 8 | 16 | 6 | 0.79 |
| DeepSeek | Terra, default | 0.42 | 9 of 30 | 11 | 11 | 8 | 0.65 |

**Against the criterion:** Terra at default does not have fewer defects than
MiMo by MiMo's judge (p = 0.48). The gap does not appear when Terra reasons;
if anything it narrows. The defects MiMo found in the reasoned candidates are
mostly real and small, and mostly in material Terra *added*: a config page
naming the implementation ("parsed with Python's `int()`"), a hedge ("This
multiplication can overflow."), an error message giving the remedy before the
event, "Print each number, then the total" without "by default". Reasoning
here bought elaboration, and elaboration is where defects live. MiMo repeated
its own count on 22 of 30 MiMo candidates, 27 of 27 Terra-low and 22 of 28
Terra-default.

The hypothesis this follow-up was built on — that "low" removed the gap by
stopping Terra from reasoning — is wrong. The gap is there at "low".

**Terra at "low", replicated.** Not a criterion of the amendment, which tested
only Terra at default; reported as preregistered. In a fresh run under the same
judge and settings, Terra at "low" had fewer defects than MiMo in 11 pairs and
more in none (layer 2: 7 and 1). Pooled over both runs, 18 to 1. DeepSeek's
defect judge sees no difference in either run.

### Label swap

| labels | calls parsed | picked Terra's actual text | picked the text labelled Terra | picked A | consistent pairs: Terra / MiMo / split |
|---|---|---|---|---|---|
| true | 60 | 25 | 25 | 41 | 5 / 10 / 15 |
| swapped | 60 | 33 | 27 | 45 | 8 / 5 / 17 |

**Against the criteria:** neither deference nor recognition. Under swapped
labels the version credited to Terra won 5 of 13 consistent pairs (38%,
p = 0.58), the version Terra actually wrote 8 of 13 (62%). Per call, MiMo
picked Terra's actual text 25 of 60 times with true labels and 33 of 60 with
swapped ones (Fisher p = 0.20). The direction, if anything, is toward the
version labelled "you": 18 of 28 consistent pairs across both conditions —
self-preference, not deference, and not significant. Position still decides
most of it: A in 86 of 120. In none of the 120 verdicts did MiMo mention
Terra, itself, or a stronger model.

So the label does not explain the maintainer's impression either. In this
setting MiMo reads past who wrote a version, and its pairwise verdicts are
mostly position.

## Decisions

- **The judge for layer 4 is MiMo's single-candidate defect judge.** This
  departs from layer 2's preregistered rule, which on its own numbers said
  both judges failed. It is made after seeing the follow-up and is labelled as
  such: the replication (11 to 0, p = 0.001; 18 to 1 pooled) is what moved it.
  The pairwise judge is dropped, for both judges. DeepSeek's defect judge does
  not separate the writers in either run and is not used.
- **Consult Terra at `reasoning = "low"`.** On this task set it was better by
  the only judge that separates the writers, and cheaper: $0.00061 a candidate
  against $0.00094 at default, on flex.
- **Still open:** whether any of this holds on longer documents, where the
  maintainer's impression was formed. Layer 4 should include some.

## Second follow-up: GPT-6.1 Sol at "low", preregistered before running

Asked for model choice beyond this trial: the Artificial Analysis index puts
GPT-6.1 Sol at "low" beside Terra at medium for performance and price. Two Sol
samples per task at `reasoning = "low"`, on flex (a two-task pilot, kept out:
both candidates pass their checks, 0 reasoning tokens, $0.0013 and $0.0019).
Then the defect judge scores all four writers — MiMo, Terra at "low", Terra at
default, Sol at "low" — in one fresh shuffled run, MiMo and DeepSeek, two reps.
Terra's default is the provider's; OpenAI documents medium as its default
effort, but this run does not verify that.

- **Sol writes better than MiMo** if, by MiMo's judge, Sol has fewer defects in
  the paired comparison with sign p below 0.05.
- **Sol against Terra** (each setting): reported, paired the same way. A
  ceiling limits it: Terra at "low" drew no defects last run, so this judge
  can show Sol worse than Terra but has little room to show it better. A tie
  here is not evidence that the two write alike.
- Terra at "low" against Terra at default, which the last run's data put at 8
  to 0 (p = 0.008, computed after the fact), is reported again as a
  replication.

### Results

Run after `4bebf90`: 30 Sol generations, 480 defect-judge calls over all
four writers, none failed, $0.62. Tables in `data/sol-scores.txt`, records in
`data/sol-runs.tar.xz`.

| MiMo's judge | mean defects | candidates with any | fewer than MiMo / tie / more | p |
|---|---|---|---|---|
| MiMo | 0.32 | 8 of 30 | — | — |
| Terra, low | 0.08 | 4 of 30 | 8 / 19 / 3 | 0.23 |
| Terra, default | 0.05 | 3 of 30 | 8 / 20 / 2 | 0.11 |
| Sol, low | 0.05 | 3 of 30 | 8 / 19 / 3 | 0.23 |

Sol against Terra, by MiMo's judge: 2 / 27 / 1 against Terra at "low", 3 / 24 / 3
against Terra at default. DeepSeek's judge, likewise: no pair of writers it
separates (all p > 0.25), though it too ranks all three OpenAI writers below
MiMo in defects (0.20–0.29 against 0.55).

**Against the criteria:** Sol does not have significantly fewer defects than
MiMo in this run (8 to 3, p = 0.23), and neither does either Terra. Sol and
Terra cannot be told apart: 27 of 30 pairs tie against Terra at "low". With
the ceiling stated beforehand, that is not evidence that they write alike —
only that on short tasks this judge cannot see past "both nearly flawless".
Sol costs about twice what Terra at "low" does ($0.00135 a candidate against
$0.00061, on flex) for no measured gain.

**Terra at "low" against default did not replicate:** 3 to 4 here, after 8 to
0 last run. Pooled, 11 to 4 (p = 0.12). The last run's finding that reasoning
made Terra worse is withdrawn as noise at this sample.

**Pooled over the three runs**, Terra at "low" had fewer defects than MiMo
26 times and more 4 times (sign p = 0.00006). That the OpenAI writers beat
MiMo on this task set is the one result that holds across runs; each run on
its own sits near or below the significance bar, so a single run of this size
is not enough to rank writers. The defects MiMo lists for the OpenAI writers
are real and small: a remedy placed before the event, "Numbers can span
multiple lines", "400 ms after the second" for a randomized delay.

### Decisions, revised

- **For model choice:** on short documentation tasks, Terra at "low", Terra
  at default and Sol at "low" are indistinguishable by the only judge that
  separates writers at all, and all three beat MiMo. Terra at "low" is the
  cheapest of the three.
- **For the trial:** one run of 30 pairs cannot rank writers here, so layer 4
  needs either more samples per task or longer documents, where defects have
  room to differ. Both, preferably.
