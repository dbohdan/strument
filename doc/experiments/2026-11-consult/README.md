# Consult: can MiMo tell Terra's writing from its own?

**Status:** characterization — layer 2 and two follow-ups run. **Result:
MiMo's single-candidate defect judge separates Terra from MiMo, and the
blind pairwise judge does not.** In layer 2 the defect judge leaned Terra 7 to
1 (p = 0.07), short of its bar; the follow-up's replication, in a fresh run,
gave 11 to 0 (p = 0.001), and Terra at "low" drew no defects at all from it.
Terra at its default reasoning did *not* do better: 11 to 7 (p = 0.48), with
more defects than at "low". Telling MiMo who wrote each version changed
nothing measurable, and it never mentioned the authors.

The `consult` tool (`9cd9b6f`) lets the model ask another model, configured in
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

Run on all fifteen tasks after the preregistration (`ae0e298`): 240 pairwise
and 240 single-candidate judge calls, none failed, every pairwise verdict
parsed. Judges cost $0.06; the whole layer, with candidates and both pilots,
about $0.35. Full tables in `data/scores.txt`; raw records in
[`experimental-data/strument/2026-11-consult/runs.tar.xz`](https://github.com/dbohdan/experimental-data/blob/425c636/strument/2026-11-consult/runs.tar.xz) (main run and pilots), candidates in
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
fragment as the answer. Fixed in `70c7a34`: it is retried as a server error.

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

Run after the amendment (`0a60c2c`): 30 Terra generations at default
reasoning, then 360 defect-judge calls over all ninety candidates and 120
label-swap calls, none failed. About $0.44. Tables in
`data/follow-up-scores.txt`, records in [`experimental-data/strument/2026-11-consult/follow-up-runs.tar.xz`](https://github.com/dbohdan/experimental-data/blob/425c636/strument/2026-11-consult/follow-up-runs.tar.xz), the new
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

Run after `f5a658d`: 30 Sol generations, 480 defect-judge calls over all
four writers, none failed, $0.62. Tables in `data/sol-scores.txt`, records in
[`experimental-data/strument/2026-11-consult/sol-runs.tar.xz`](https://github.com/dbohdan/experimental-data/blob/425c636/strument/2026-11-consult/sol-runs.tar.xz).

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

## Third follow-up: long documents, preregistered before running

Short tasks left no room: most pairs tied, and three runs were needed to rank
even MiMo against Terra. The maintainer's impression was formed on long
documents. Six tasks (`LONG_TASKS` in `data/tasks.py`), each a whole
user-facing page about one Strument feature, written from Strument's own source
at `6e3059b` and from nothing else: the consult tool, approving prompts
(`--yes`, `auto_approve`, `/yes`), moving a project (`strument project adopt`),
reading outside the project, `webfetch`, and skills. Neither writer nor judge
sees the existing docs. Each task's fact checks pass on Strument's existing
documentation and fail on an empty page; one check was dropped after the pilot,
because the fact it required (`run_code` cannot consult) is in a file the task
does not send, and all three pilot pages missed it.

**Writers:** MiMo, Terra at "low", Sol at "low", four samples each per task: 72
pages, 24 pairs per comparison. **Judge:** the single-candidate defect judge,
MiMo and DeepSeek, two reps, reasoning disabled. One shuffled run. A pilot on
one task, kept out: pages of 320–550 words, every judge call parsed and
finished well under its limit.

- **Sol against Terra (the question asked):** by MiMo's judge, whichever has
  fewer defects in the paired comparison with sign p below 0.05 is the better
  writer here; otherwise they are indistinguishable at this sample.
- **Each against MiMo:** the same test, each OpenAI writer against MiMo.
- **Secondary, fixed now:** defects per 100 words, because a longer page has
  more places for a defect; fact-check passes per writer; page length.
  Reported beside the primary, never instead of it.
- DeepSeek's judge reported the same way.

### Results

72 pages, 288 judgments, $1.79. The scores are in
[`data/long-scores.txt`](data/long-scores.txt) and the records in
[`experimental-data/strument/2026-11-consult/long-runs.tar.xz`](https://github.com/dbohdan/experimental-data/blob/425c636/strument/2026-11-consult/long-runs.tar.xz).

**Against the criteria, by MiMo's judge:**

| comparison | first fewer / tie / second fewer | p | missing pairs |
|---|---|---|---|
| Sol vs Terra | 7 / 8 / 3 | 0.34 | 6 |
| Sol vs MiMo | 11 / 4 / 2 | 0.022 | 7 |
| Terra vs MiMo | 15 / 1 / 4 | 0.019 | 4 |

Sol and Terra are indistinguishable at this sample. Each beats MiMo, on the
pairs that parsed. DeepSeek's judge separates none of the three (Sol vs Terra
8 / 3 / 4, p = 0.39; neither beats MiMo).

**The judge did not hold up on long pages.** 90 of 288 judgments wrote no
defect list. These are not loops. With reasoning disabled, MiMo reasons in its
visible text (as it did on short tasks), and on a 600-word page it keeps
searching ("Hmm. Let me reconsider…") until the 24,000-token cap, with about
100,000 characters and no verdict. The pilot, on one task with a single
sample, never came near the cap. The misses are not random:

| judge | MiMo's pages | Terra's | Sol's |
|---|---|---|---|
| MiMo | 15 of 48 | 11 of 48 | 18 of 48 |
| DeepSeek | 6 of 48 | 15 of 48 | 25 of 48 |

The likely mechanism is that a judge keeps looking longest at a page where it
finds nothing quickly, so the misses lean toward the cleaner pages, which would
bias every comparison toward MiMo. That mechanism is a guess. The bounds are
not:

- Credit every missing pair to the first writer, and all three of MiMo's
  judge's comparisons pass: Sol vs Terra p = 0.021, Sol vs MiMo p < 0.001,
  Terra vs MiMo p = 0.003.
- Credit them to the second writer, and none pass (p = 0.80, 0.82, 0.21).
- So the preregistered result stands on the data that parsed, and the missing
  data could move it either way.

**Secondary:**

| writer | median words | defects per 100 words (MiMo's judge) | (DeepSeek's) | fact checks passed |
|---|---|---|---|---|
| MiMo | 591 | 0.40 | 0.44 | 17 of 20 |
| Terra | 356 | 0.30 | 0.52 | 19 of 20 |
| Sol | 532 | 0.09 | 0.29 | 18 of 20 |

- Terra writes the shortest pages by far. Its win over MiMo by count is partly
  length: per 100 words the gap is smaller (0.30 vs 0.40), and DeepSeek ranks
  it last.
- Sol writes nearly as much as MiMo, with less than a quarter of its defect
  rate by MiMo's judge. That is the clearest signal in the run, and it is
  consistent with the maintainer's guess that Sol is the better writer. But it
  is a secondary measure, read from the pages that parsed. Sol's are the ones
  most often missing.

**Fact checks:** one task's checks were broken. The fact-check counts above
leave out `long-outside`, where all 12 pages failed the same two checks.

- The blanket ban on exclamation marks fires on a quoted `!`. A correct page
  about `secret_files` must show one, because a leading `!` is what the page
  tells the reader not to write.
- The grant's name `read-outside` is defined in `grants.go`, which the task
  does not send. It is the same mistake the pilot caught for `run_code`, and
  checking "every remaining pattern against what each task sends" missed it,
  because `outside.go` does contain the string `read-outside:`, as a group
  prefix.

The defect judgments on that task are unaffected. They don't use the fact
checks.

### What it changes

- **Sol vs Terra:** not settled. If anything leans, it leans Sol (fewer defects
  per word, more of the pairs). The censoring could account for that lean or
  hide a larger one.
- **The trial's judge needs to finish on long pages before it can carry the
  trial.** The fix to try is the order of the answer: the defect list first,
  each item one line, then the reasoning, if any. A judge that runs out of
  budget then leaves a list rather than nothing. A cap is no fix: a raised cap
  buys more deliberation, not a verdict. That is a protocol change, so it gets
  its own preregistration and a rerun on these 72 pages, not a patch to this
  run.
- **Consulting Terra at "low" stays the default** for now, being cheapest. The
  case for Sol is a secondary measure, read from censored data, against about
  2.7 times the cost per page ($0.0059 vs $0.0022).

## Fourth follow-up: the judge rerun, preregistered before running

The third follow-up's judge left 90 of 288 judgments without a verdict, more
often on the OpenAI writers' pages. This reruns the judging on the same 72
pages with a judge configured to finish.

**Two pages were cut off before judging.** In two answers (MiMo's
`long-skills` 1, Terra's `long-yes` 2) the writer never closed the outer
fence, and the extractor ended the page at the first bare fence of a code
sample inside it. The judges saw 28% and 56% of those pages, and said so ("the
page is cut off mid-sentence inside a fenced code block"). `extract_block` now
reads an answer that opens with a fence by the parity of the fences inside
it. Re-extracted with `generate.py --reextract`, those two pages are read
whole and no other page changes, long or short, in any run. The third
follow-up's records keep the blocks they were judged on. The rerun judges the
re-extracted copy.

**Two pilots, on two tasks with one sample per writer, kept out:**

- **List first** (`defects.py --order first`, reasoning still off): ask for the
  defect list before anything else. The pilot failed it. The deliberation
  moved into the list. MiMo listed 25 items on Sol's skills page, 13 of which
  end by calling the claim correct ("correct per `runSkill`"). DeepSeek listed
  66 on one page, ending in a run of "Real defect: … Consistent." A count of
  those lines does not measure defects.
- **Reasoning "low", list last, 64,000-token cap**
  (`--reasoning low --max-tokens 64000`): the original prompt, with the
  deliberation sent where reasoning goes. Five of six MiMo judgments finished
  on their own with a clean list. The sixth (Sol's webfetch page) was still
  running at 30 minutes. Reasoning tokens by page were MiMo's 1,005 and 8,266,
  Sol's 22,803, and Terra's 22,243 and 35,879. That is the third follow-up's
  guessed mechanism, measured: the judge works far longer on the OpenAI
  writers' pages. With reasoning visible, that showed up as a missing verdict.
  Here it shows up as time.

**Design:**

- **Pages:** the third follow-up's 72, as re-extracted.
- **Judges:** MiMo and DeepSeek, two reps each (288 calls), reasoning "low",
  the list-last prompt unchanged, `max_tokens` 64,000.
- **Order:** one shuffled run, resumed in chunks if it outlives a session's
  time limit. Resuming changes nothing about which calls are made.
- **Unparsed:** a judgment with no `Defects:` section, or one stopped at the
  cap, is unparsed, as before.

**Criteria:** the third follow-up's, unchanged.

- **Sol vs Terra by MiMo's judge:** a paired sign test at p below 0.05.
- **Each against MiMo:** the same test.
- **Secondary:** defects per 100 words, fact checks, page length.

This rerun's answer replaces the third follow-up's if no writer has more than
10% of its judgments unparsed by MiMo's judge. Otherwise it is reported with
the same bounds, and neither run settles the question.

**Also reported:**

- Reasoning tokens per judgment, by writer, as a measure of how hard each
  writer's pages were to judge.
- How well each judgment agrees with the third follow-up's count for the same
  page.
- DeepSeek's judge, reported the same way. Its reasoning at "low" may run
  long, as the handbook warns. If it does, that is reported, not worked around
  mid-run.

### Results

288 judgments, $4.31. 137 calls failed partway through when the key ran out
of funds (HTTP 402 and 403). They were deleted and made again, along with five
lost in flight at a session's time limit. Every judgment in the final set is a
completed call. The scores are in
[`data/long-rerun-scores.txt`](data/long-rerun-scores.txt) and the records in
[`experimental-data/strument/2026-11-consult/long-rerun-runs.tar.xz`](https://github.com/dbohdan/experimental-data/blob/425c636/strument/2026-11-consult/long-rerun-runs.tar.xz).

**The preregistered rule fails.** MiMo's judge left 11 of Sol's 48 judgments
unparsed (23%), all of them stopped at the 64,000-token cap. That is over the
10% bar, so this run is reported with bounds, as the rule says. The bounds
turn out to be tight. An unparsed judgment is usually one rep of two, and a
page's count is the mean of its parsed reps, so only 0–2 pairs per comparison
go missing (6–9 in the third follow-up):

| judge | comparison | first fewer / tie / second fewer | p | all missing to first | all to second |
|---|---|---|---|---|---|
| MiMo | Sol vs Terra | 7 / 8 / 8 | 1.00 | 1.00 | 0.80 |
| MiMo | Sol vs MiMo | 18 / 3 / 2 | < 0.001 | < 0.001 | 0.001 |
| MiMo | Terra vs MiMo | 18 / 2 / 4 | 0.004 | 0.004 | 0.004 |
| DeepSeek | Sol vs Terra | 14 / 2 / 6 | 0.12 | 0.05 | 0.29 |
| DeepSeek | Sol vs MiMo | 19 / 1 / 3 | 0.001 | < 0.001 | 0.003 |
| DeepSeek | Terra vs MiMo | 16 / 4 / 3 | 0.004 | 0.003 | 0.012 |

- **Sol vs Terra: indistinguishable,** by both judges and under both bounds.
  The question asked has its answer at this sample: by MiMo's judgment, Sol at
  "low" does not write better long pages than Terra at "low".
- **Each beats MiMo, by both judges, whichever way the missing pairs fall.**
  The third follow-up's result, which its missing data could have overturned,
  holds.

**Secondary:**

| writer | median words | defects per 100 words (MiMo's judge) | (DeepSeek's) | fact checks passed |
|---|---|---|---|---|
| MiMo | 591 | 0.49 | 0.63 | 18 of 20 |
| Terra | 361 | 0.45 | 0.67 | 19 of 20 |
| Sol | 532 | 0.26 | 0.34 | 18 of 20 |

- Sol has about half the defect rate of the other two by both judges. Terra's
  page-count win over MiMo is mostly length: per 100 words the two are level.
- The fact checks leave out `long-outside`, as in the third follow-up. MiMo's
  re-extracted skills page now passes.

**Also reported:**

- **How hard each writer's pages were to judge,** in median reasoning tokens
  per judgment. MiMo's judge spent 21,406 on MiMo's pages, 26,544 on Terra's
  and 40,195 on Sol's. DeepSeek's spent 28,707, 34,663 and 34,594. It is the
  same ordering as the missing verdicts in the third follow-up, and it is what
  censors Sol most.
- **Agreement with the third follow-up's counts,** per page (Spearman, the two
  re-extracted pages left out): 0.49 for MiMo's judge, 0.32 for DeepSeek's.
  With reasoning on, both find about twice as many defects (MiMo's mean 1.04 →
  2.07). The two judges agree with each other at 0.58.
- **Reps disagree often:** the same count on both reps for 3 of Sol's 14 pages
  by MiMo's judge. A single judgment of a long page is a noisy measure. The
  pairing and the sample are what carry the result.

### What it changes

- **Consult Terra at "low".** It writes as well as Sol by count, at about a
  third of the cost per page ($0.0022 vs $0.0059). Sol's lower defect rate per
  word is consistent across both judges and both runs, but it is a secondary
  measure. It is the place to look again if consult answers turn out wordy,
  not a reason to pay 2.7 times as much now.
- **The trial's judge:** MiMo with reasoning "low" and a 64,000-token cap, two
  reps or more, scored as a paired count with these bounds beside it.
  Reasoning off failed on long pages, and asking for the list first moved the
  deliberation into the list. A judge reads clean pages longest, so the cap
  censors the best writer first. Report the unparsed rate by writer every
  time.
- **Time is the cost, not money:** a judgment of a clean OpenAI page took up
  to 25 minutes at MiMo's speed. 288 judgments needed two and a half session
  time limits.

## Layer 3: the mechanics pass, preregistered before running

Does MiMo call `consult`, and does it send the files the question needs?
`data/mechanics.py` runs one unattended Strument turn per task in a fresh git
copy of the task's project, with Terra in `consult_models` and the `consult`
grant given up front. The user's message is the task's instruction.
`data/mech_score.py` reads every consult call from the recorded wire.

**Pilots, kept out** (21 sessions on the short tasks):

- **MiMo never consulted unprompted:** 0 of 19 sessions. Two were under the
  purpose from `doc/config.md` ("documentation and user-facing prose"), and
  17 under one that says why to hand the work over ("it writes them better
  than you do, so have it draft or review any you write"). Its reasoning and
  replies never mention the tool. All 17 under the second wording passed
  their fact checks without help.
- **Asked to ("Ask terra for a draft first."), it consulted in 4 of 4,**
  checked the draft against the code, and passed every fact check. It sent
  the source the facts come from in 2 of 2 tasks that have one, the target in
  3 of 4, and `STYLE.md` in 1 of 4.
- **Flex was rate-limited:** OpenAI's flex tier answered 429 to Terra for
  minutes at a time, and one session spent twelve minutes retrying. Terra
  runs on the standard tier from here.
- **A Strument bug found on the way (`8b0eaaf`):** a long pilot for layer 4
  made forty distinct reads of Strument's source to write a page, and the
  read-only streak detector ended its turn before it wrote anything, telling
  it that it had been repeating the same calls. The streak now only advises.
  An exact repeat kept up after its note still ends a turn.

**Design:**

- **Arms:**
  - `plain`: the documented purpose, with nothing asked.
  - `directed`: the same purpose, plus "Ask terra for a draft first." at the
    end of the user's message.
- **Sample:** 15 tasks × 2 reps × 2 arms = 60 sessions, shuffled.
- **Binary:** Strument at `8b0eaaf` or later.

**Reported:**

- Sessions that consulted, per arm.
- Per directed session, whether its consult carried:
  - the target;
  - `STYLE.md`;
  - every source file the task's facts come from (for the tasks that have
    one).
- Refused consults, fact checks passed, the target written, cost and time.

**Read as:**

- If `plain` consults in fewer than 3 of 30, the trial's treatment arm is
  `directed`. Whether MiMo reaches for the tool unprompted is a separate
  question, about the tool's description, for later.
- If `directed` sends `STYLE.md` in fewer than half its consults, that is
  reported as a finding about the tool's description, which says to include
  "the code or text it is about" but not the conventions it must follow. It
  is not fixed before the trial: the trial measures the tool as it is.

### Results

60 sessions, $0.10, Strument at `8b0eaaf`, Terra on the standard tier. Every
session finished and wrote its target. The scores are in
[`data/mechanics-scores.txt`](data/mechanics-scores.txt) and the records in
[`experimental-data/strument/2026-11-consult/mechanics-runs.tar.xz`](https://github.com/dbohdan/experimental-data/blob/425c636/strument/2026-11-consult/mechanics-runs.tar.xz).

| arm | consulted | sent no files | sent the target | sent `STYLE.md` | sent the source | facts ok | median s | mean cost |
|---|---|---|---|---|---|---|---|---|
| plain | 0 of 30 | — | — | — | — | 30 of 30 | 28 | $0.0009 |
| directed | 30 of 30 | 18 of 30 | 12 of 30 | 3 of 30 | 10 of 20 | 26 of 30 | 37 | $0.0023 |

- **MiMo does not consult unprompted:** 0 of 30, and 0 of 49 with the
  pilots. By the preregistered reading, the trial's treatment arm is
  `directed`.
- **Asked to, it always does, but mostly without files.** 18 of 30 consults
  named no file at all. MiMo pasted what it judged relevant into the question
  instead (600–2,400 characters). The tool's description says to send "the
  code or text it is about", and MiMo read that as "quote it". `STYLE.md` went
  with 3 of 30 consults, under the half the preregistration set, so this is
  reported as a finding about the tool's description. The source the facts
  come from went with 10 of the 20 consults on tasks that have one.
- **Consulting cost accuracy here:** 26 of 30 directed sessions passed their
  fact checks against 30 of 30 alone (Fisher p = 0.11, not significant at
  this sample). The four failures:
  - `grep-confirm`, no files: dropped the `%d` the code fills in.
  - `rotor-changelog`, twice, once with no files and once with the diff: an
    entry ("Fix off-by-one error in rotation pruning") that does not name the
    `keep` option it fixes.
  - `tally-help`, with the source: an invented `--flag`.

  In each, MiMo took Terra's draft largely as it came. On these short tasks
  MiMo alone passes every check, so a consult has only room to lose.
- **No consult was refused,** and none failed: Terra on the standard tier
  answered every one.

### What it changes for the trial

- **The treatment is the user asking:** MiMo alone, against MiMo told "Ask
  terra for a draft first." on the long tasks, where layer 2 found room for
  Terra to be better.
- **The trial measures the tool as it is,** file-less consults included.
  Whether the description should ask for files by name, and for the project's
  conventions, is the next change to the tool. Making it before the trial
  would measure a different tool.
- **The fact checks are the counter-metric,** reported beside the defect
  judge, because this layer's one signal is that a consult can make a page
  less accurate.

## Layer 3, second pass: the revised description, preregistered before running

The maintainer chose to fix the tool before the trial, so the trial measures
the tool as it will ship. `43bc200` rewrites the consult
tool's description:

- **Files by name:** name files rather than quoting them, because a file sent
  whole is exact and complete.
- **Which files:** the file the work is on, the code or text it has to agree
  with, and the project's conventions, such as a style guide.
- **The answer is a draft,** to check against the code before using it.

**Design:** the `directed` arm again, 15 tasks × 2 reps = 30 sessions, on the
new binary, scored by the same `mech_score.py`. The `plain` arm is not rerun.
Nothing in the change invites a consult the user did not ask for.

**The revision works if all three hold:**

- At most 8 of 30 consults name no file (was 18).
- At least 15 of 30 send `STYLE.md` (was 3).
- At least 28 of 30 pages pass their fact checks (was 26). This is the
  counter-metric: naming files should not cost accuracy.

If it fails, the trial waits for another revision rather than running on one
known not to work.

### Results

30 sessions, $0.07, Strument at `43bc200`. The scores are in
[`data/mechanics-desc-scores.txt`](data/mechanics-desc-scores.txt) and the
records in [`experimental-data/strument/2026-11-consult/mechanics-desc-runs.tar.xz`](https://github.com/dbohdan/experimental-data/blob/425c636/strument/2026-11-consult/mechanics-desc-runs.tar.xz).

| measure | before (`108d614`) | after | bar |
|---|---|---|---|
| consults naming no file | 18 of 30 | 3 of 30 | at most 8 |
| sent `STYLE.md` | 3 of 30 | 15 of 30 | at least 15 |
| sent the target | 12 of 30 | 26 of 30 | — |
| sent every source file the facts come from | 10 of 20 | 12 of 20 | — |
| fact checks passed | 26 of 30 | 29 of 30 | at least 28 |

**The revision passes all three, `STYLE.md` exactly at its bar.** The one
failure (`rotor-readme`, sent only `README.md`) dropped the word "rotate"
from the page. MiMo made 31 consults. One omitted the required `question`,
got the error back, and was made again correctly. That is a malformed call,
not a refusal.

What is still missing is the second source. In 8 of the 20 sessions on tasks
with one, the consult lacked a file the facts come from:

- the code behind a change, where MiMo sent the diff alone (`grep-commit`,
  `rotor-changelog`) or nothing;
- `main.go`, which holds the format verbs, for `tally-error`;
- `CHANGELOG.md`, which says what replaces the deprecated call, for
  `pin-deprecation`;
- the code, for `rotor-readme`.

The description asks for "what it has to agree with" and leaves the choice to
MiMo. Its choice was the file nearest the target.

## Layer 4: the trial, preregistered before running

Does asking MiMo to consult Terra produce better documentation pages than
MiMo alone?

**Sessions** (`data/trial.py`): one unattended Strument turn per session, at
`43bc200` or later (the loop fix and the revised description). Each session
gets a fresh copy of Strument's source at `6e3059b`, with three things
removed:

- `doc/`;
- every Markdown file;
- the project's `.strument.star`.

`STYLE.md` holds the long set's house style. The user asks for one of the six
long pages "from the source", so MiMo finds the files itself.

- **Arms:** both arms have Terra (standard tier, reasoning "low") in
  `consult_models` under the documented purpose, and the consult grant.
  - `alone`: the request as it is.
  - `consult`: the request plus "Ask terra for a draft first."
- **Sample:** 6 tasks × 6 reps × 2 arms = 72 sessions, shuffled; 36 pairs,
  each a task and a rep.
- **Pilot, kept out:** one `long-skills` session per arm.
  - Alone: 502 s, $0.01, 922 words.
  - Consult: 332 s, $0.03, 785 words. The consult sent `STYLE.md` and four
    source files.
  - Both passed their fact checks.

**Judging:** the long set's judge exactly as in the rerun. That is MiMo, the
list-last prompt with the task's source files, reasoning "low", a
64,000-token cap, and two reps. A missing page counts as a page with no
defect list, so it is unparsed; it is not dropped.

**Primary:** by MiMo's judge, the `consult` page against the `alone` page of
the same task and rep, with a paired sign test on the mean defect count.
`consult` is better if it has fewer defects with p below 0.05 **and** the
bound that credits every missing pair to `alone` still gives p below 0.05.
The same rule decides the other direction. Otherwise the arms are
indistinguishable at this sample.

**Counter-metric, reported beside the primary:** fact checks passed per arm
(all six tasks; the `!` check now bans only an exclamation mark after a word,
and every task passes on Strument's own docs at `6e3059b` and fails on an
empty page). Layer 3's one warning was that a consult can cost accuracy.

**Secondary:**

- defects per 100 words, and page length;
- unparsed judgments by arm, and median judge reasoning tokens by arm;
- per consult, the files sent: `STYLE.md`, and how many of the task's source
  files;
- cost and time per session.

**Budget:** about $1.50 for sessions and $1.30 for judging. Time: about an
hour of sessions, then 1.5–2.5 hours of judging.

### Results

**The arms are indistinguishable.** By MiMo's judge, the `consult` page had
fewer defects than the `alone` page in 12 pairs, tied in 6 and had more in 8
(p = 0.50). Ten pairs are missing. Credited all to `alone`, they give p = 0.36;
credited all to `consult`, p = 0.016. Neither direction meets the
preregistered rule. The scores are in
[`data/trial-scores.txt`](data/trial-scores.txt) and the records in
[`experimental-data/strument/2026-11-consult/trial-runs.tar.xz`](https://github.com/dbohdan/experimental-data/blob/425c636/strument/2026-11-consult/trial-runs.tar.xz).

| measure | `alone` | `consult` |
|---|---|---|
| pages written | 31 of 36 | 35 of 36 |
| fact checks passed | 31 of 31 | 35 of 35 |
| no page: timeout (1,800 s) | 2 | 1 |
| no page: context full | 3 | 0 |
| mean defects (parsed pages) | 3.48 (n = 29) | 3.14 (n = 33) |
| defects per 100 words | 0.37 | 0.42 |
| median words | 982 | 766 |
| unparsed judgments | 15 of 62 | 15 of 70 |
| judge reasoning tokens (median) | 46,313 | 40,211 |
| median session time | 615 s | 639 s |
| mean session cost | $0.023 | $0.041 |

**The counter-metric held.** Every page that was written passed its fact
checks, in both arms. The accuracy cost layer 3 warned about (4 failures in
30) did not appear here.

**Pages written leans to `consult`, not significantly.** `alone` lost five
pages and `consult` one (Fisher p = 0.20). All three context failures were
`alone` sessions that read until a request crossed MiMo's 131,072-token
window; see the open question below. Seven of the ten missing pairs are
missing on the `alone` side: five missing pages and two pages whose
judgments were both unparsed. That is why crediting the missing pairs to
`consult` reaches p = 0.016. That bound says what the misses *could* do, not
what they did, and the preregistered rule reads it only in the other
direction.

**Consults, from the wire:** 37 calls in 36 sessions. Every one named files:

- `STYLE.md` in 36 of 36 sessions;
- at least one source file in 35 of 36.

Layer 3 on the old description had 18 of 30 naming no file. The one
unanswered call omitted `model`, got the error back and was made again.

**Shorter pages, same density.** Consult pages ran about 22% shorter by
median word count, at about the same density of defects. Fewer words with
the same defect count per word would be fewer defects per page, which is the
direction of the 12–8 split, but too small to tell from noise at 26 pairs.

**Judge censoring was even this time.** 30 of 132 judgments hit the
64,000-token cap, 15 in each arm. The fourth follow-up's pattern, the
cleanest writer censored first, needed a gap in quality between the writers
to show, and these two arms showed none.

**Cost and time.**

| item | cost |
|---|---|
| the 72 kept sessions | $3.30 |
| the 17 superseded sessions | $0.59 |
| two discarded reruns (amendment 2) | $0.08 |
| judging | $1.66 |

The sessions ran more than twice the estimate: the pilot's two sessions were
short ones (`long-skills`), and the median session took ten minutes, not
five. Session time across all 89 sessions was 18.7 hours, run 8 at a time.

#### Amendments

1. **A cut stream ended 17 sessions; they were re-run on a fixed binary.**
   The first pass ran Strument at `43bc200`. In 17 of its 72 sessions, the
   provider cut MiMo's stream mid-response, with no `finish_reason` and no
   `[DONE]`:
   - 15 were cut mid-reasoning;
   - 2 were cut after only `: OPENROUTER PROCESSING` keep-alives.

   Strument took the cut stream for a finished response, reported "reasoning
   but no answer" or "an empty response", and ended the turn. The cuts came
   in bursts (16:28–16:30, 17:07, 17:20–17:24), which points to the
   provider. `a439a11` makes such a stream a retryable network error. Those
   17 sessions were moved to `superseded/` and run again on `a439a11`; the
   other 55 were kept. The fix changes nothing in a session whose stream was
   not cut, so this is the result a full run on `a439a11` would give, with
   the reruns at a later hour. In the kept sessions, the retry fired 8
   times.
2. **Two sessions were re-run by mistake, and their originals restored.**
   The selection moved every session whose turn failed without a page. That
   also caught two `alone` sessions that had ended on a full context,
   unrelated to the bug. Their reruns (one wrote a page, one hit the context
   limit again) were set aside before scoring, along with the page's one
   judgment, and the original failures are what is counted.
3. **One judgment was re-made after a network failure** (`IncompleteRead`
   from the provider).

#### An open question: context-full in an unattended turn

In each of the three context failures, Strument first cleared 40–54 old tool
results (about 36,000–48,000 tokens). The next request was still about
133,000 tokens, and freeing more needs the user's approval. Unattended, with
only `--yes consult,steps`, that approval was declined and the turn ended
with no page. Both arms had the same grants, so the comparison is fair. But
three turns in 36 doing an ordinary documentation task this way is worth a
look, both at how much clearing frees and at what an unattended turn should
do when it is not enough.

### What it changes

For MiMo writing a long page from source, asking it to consult Terra first
gives pages as good as it writes alone by this judge. They are shorter, cost
about 1.8 times as much and take the same time. The trial does not support
recommending the consult for this task, and it does not rule out an effect
smaller than 36 pairs can see.

What the experiment produced that does hold is in Strument:

- the loop stop that no longer ends research turns (`8b0eaaf`);
- the consult description that gets the style guide and the source sent
  (`43bc200`; 36 of 36 here);
- the retry on a cut stream (`a439a11`).

### Post hoc: does Terra's draft survive?

Not preregistered. This asks why the trial found nothing when Terra's own
pages had beaten MiMo's 18–4 in the long-document rerun:

- did MiMo pass the draft on unchanged, so that the consult itself doesn't help;
- or did it rewrite the draft, so that the help was lost?

[`data/survival.py`](data/survival.py) compares each consult page with the
answer Terra sent back, read from the wire. The output is in
[`data/survival-scores.txt`](data/survival-scores.txt).

| measure (35 consult pages) | median |
|---|---|
| page's 8-word sequences found in Terra's draft | 7% |
| page's lines found verbatim in the draft | 12% |
| same 8-word measure, `alone` pages of the same task (baseline) | 1% |
| page's 4-word sequences in the draft / baseline | 19% / 4% |
| words: page / draft | 777 / 611 |

**MiMo rewrote the draft.** Terra answered with a full page each time, and
MiMo wrote its own:

- 30 of 35 pages kept under 30% of the draft's 8-word sequences;
- 5 kept 40% or more (one, `long-yes` rep 4, kept 87%).

Overlap well above the baseline shows that MiMo read the draft, but it used
the draft as a reference, not as the text.

**The sequence explains why.** MiMo consulted at the median step 21 of its
turn, after reading the source itself. It never wrote the target before
consulting, and wrote it once or more after. "Ask terra for a draft first"
came out as research, then the draft, then MiMo's own page. By the time the
draft arrived, MiMo knew the source well enough to write without it, and it
did.

**But keeping more of the draft did not mean fewer defects.** The rank
correlation between draft kept and defects is +0.2 (n = 33, the wrong
direction for "Terra's text is better"). The five pages that kept 40% or
more averaged 3.1 defects, against 3.14 for the rest. At n = 5 this cannot
separate the two explanations. It does fail to support the simple one, that
more Terra in the page would have given a better page here. The drafts may
also be worse than the long-document rerun's Terra pages: there Terra got
the task's curated source files, and here it got what MiMo chose to send.
Judging the drafts on their own settles which.

### Post hoc: the drafts, judged as pages

Not preregistered. [`data/drafts.py`](data/drafts.py) extracts Terra's 36
drafts from the wire. The same judge reads them as pages, with the same
prompt, cap and reps. The scores are in
[`data/draft-scores.txt`](data/draft-scores.txt), and the judgments in
[`experimental-data/strument/2026-11-consult/draft-judgments.tar.xz`](https://github.com/dbohdan/experimental-data/blob/0a29715/strument/2026-11-consult/draft-judgments.tar.xz).

| writer | mean defects | defects per 100 words | median words | fact checks passed |
|---|---|---|---|---|
| `alone` (MiMo's page) | 3.48 | 0.37 | 982 | 31 of 31 |
| `draft` (Terra's answer) | 3.24 | 0.57 | 593 | 34 of 36 |
| `consult` (MiMo's page after the draft) | 3.14 | 0.42 | 766 | 35 of 35 |

| pair | first fewer / tie / second fewer | p | missing pairs |
|---|---|---|---|
| draft against consult | 13 / 2 / 14 | 1.0 | 7 |
| draft against alone | 14 / 3 / 9 | 0.41 | 10 |

**The drafts were no better than MiMo's pages.** They tie the pages MiMo
wrote from them, and they don't beat the pages MiMo wrote alone. They are the
shortest of the three and the densest in defects. Two failed a fact check
that MiMo's rewrite of the same draft passed:

- `long-adopt` rep 4 missed that nothing is deleted;
- `long-consult` rep 4 missed the three-quarters threshold.

**MiMo's rewrite lost nothing, and Terra was not short of source.** MiMo
sent a median of 3 source files besides `STYLE.md` (0 to 6), including a
median of all, and a mean of 93%, of the files the long-document rerun gave
Terra. In that rerun Terra beat MiMo 18–4. What changed is the MiMo it is
compared against. There, MiMo wrote in one pass from the curated files. Here,
it ran a tool-using turn and read a median of 6 files before consulting (3 to
10), and that MiMo writes pages as good as Terra's drafts from the same
source.

So the earlier gap measured MiMo's disadvantage writing in one pass, not an
advantage Terra keeps when MiMo can research. On this judge and these tasks,
a consult buys nothing a tool-using MiMo does not already have. It would
take a task MiMo cannot research its way through, or a judge that sees more
than defect counts, to show one.
