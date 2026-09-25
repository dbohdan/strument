# Two tool-description trims

**Status: trial.** Preregistered in [`preregistration.md`](preregistration.md)
before the main run.

**Result: neither trim cost anything the preregistered rules measured.**
Dropping `run_code`'s opening paragraph left uptake at 28/36 against 27/36,
and every answer was correct in both arms. Cutting `ask_user_question`'s
description from 773 characters to 290 left asking where it should at 28/30
against 27/30, and asking where it shouldn't at 0/20 in both. One thing the
rules did not measure did move: the trimmed description dropped the sentence
that names the options and their parts, and MiMo then sent malformed calls in
3 of 25 sessions, against none on the untrimmed description.

**Decision: both trims ship, and the `ask_user_question` trim gets one
sentence back.** The sentence is "Each question offers 2 to 4 options, each a
short label and a description of its tradeoff." It was added after the trial,
so the shipped description is not exactly the one that was measured.

## Design

As preregistered: three binaries built from `618b21c`, two independent sets,
each in its own shuffled order (seed 20260926), four sessions at a time. Each
session had a fresh fixture and state directory, `--no-git` and `--yes steps`.

- **Trial A** (`code`): base against dedup, 8 tasks × 2 models × 3 reps = 96
  sessions.
- **Trial B** (`ask`): base against trim, 5 tasks × 2 models × 5 reps = 100
  sessions.

The total cost was $0.67: $0.12 for A and $0.55 for B.

## Reasoning ran at provider defaults

MiMo-V2.6-Flash and DeepSeek-v4.1-flash both ran at their providers' default
reasoning effort. That goes against the handbook's
[pin reasoning low](../../experimenting.md#pin-reasoning-low), which was
written while this trial was running and partly because of it:

- **Three sessions hit the ten-minute timeout.** All three were Trial B's
  "Add structured logging" task in the trim arm: `deepseek-trim-log-1`,
  `deepseek-trim-log-2` and `mimo-trim-log-4`.
- **The cause is reasoning length, not the arm.** In `mimo-trim-log-4`, one
  request reasoned for 21,231 tokens over 298 seconds. Base arm sessions on
  the same task came close too: `mimo-base-log-1` took 557 seconds and 27,656
  reasoning tokens.
- **Their partial logs are scored.** Both DeepSeek sessions had already asked
  before they timed out; the MiMo session had not. Counting all three as
  failures to ask would still leave the asking comparison where it is.

The comparisons are between arms under the same settings, so they hold. The
wall-clock numbers do not describe the models at a sensible effort.

## Trial A: `run_code` dedup

| arm | model | run_code on multi-lookup | correct (multi) | run_code on single-lookup | correct (single) | mean steps | cost |
|---|---|---|---|---|---|---|---|
| base | mimo | 12/18 | 18/18 | 0/6 | 6/6 | 2.5 | $0.025 |
| base | deepseek | 15/18 | 18/18 | 2/6 | 6/6 | 2.2 | $0.030 |
| base | all | 27/36 | 36/36 | 2/12 | 12/12 | 2.4 | $0.055 |
| dedup | mimo | 13/18 | 18/18 | 0/6 | 6/6 | 2.1 | $0.025 |
| dedup | deepseek | 15/18 | 18/18 | 0/6 | 6/6 | 2.1 | $0.035 |
| dedup | all | 28/36 | 36/36 | 0/12 | 12/12 | 2.1 | $0.060 |

- **Uptake: 28/36 against 27/36,** p = 1.0.
- **Correctness: 36/36 in both arms.**
- **Over-use: 0/12 against 2/12,** p = 0.48, and it was lower in the dedup
  arm. The two base-arm sessions were DeepSeek on "Which file holds the
  relay's main function?". Reading one: it ran two greps in one program, read
  the file, and answered correctly. That is a program where one grep would do,
  not a misuse.

So the system prompt's bullet does the work of telling the model when to use
the tool. The description's copy of it added nothing measurable.

## Trial B: `ask_user_question` trim

| arm | model | asked (ambiguous) | asked (clear) | recommended first | options 2-4 | calls rejected (sessions) | edited (clear) | cost |
|---|---|---|---|---|---|---|---|---|
| base | mimo | 13/15 | 0/10 | 7/13 | 13/13 | 0 (0/25) | 10/10 | $0.117 |
| base | deepseek | 14/15 | 0/10 | 7/14 | 14/14 | 0 (0/25) | 10/10 | $0.177 |
| base | all | 27/30 | 0/20 | 14/27 | 27/27 | 0 (0/50) | 20/20 | $0.295 |
| trim | mimo | 13/15 | 0/10 | 6/13 | 10/13 | 6 (3/25) | 10/10 | $0.101 |
| trim | deepseek | 15/15 | 0/10 | 5/15 | 15/15 | 0 (0/25) | 10/10 | $0.155 |
| trim | all | 28/30 | 0/20 | 11/28 | 25/28 | 6 (3/50) | 20/20 | $0.255 |

- **Asked on an ambiguous task:** 28/30 against 27/30, p = 1.0.
- **Asked on a clear task:** 0/20 in both arms. Every clear-task session went
  ahead and edited files.
- **Recommendation first:** 11/28 against 14/27, p = 0.42. Both descriptions
  instruct it, and neither gets it done much more than half the time. The
  base description's worked example ("— recommended, matches existing config
  style") did not make a significant difference.

All three preregistered rules pass, so the rule adopts the trim.

### What the rules missed: malformed calls

The scorer crashed on two trim-arm sessions. Reading them showed why:
`mimo-trim-name-0` and `mimo-trim-name-2` sent `questions` as a JSON-encoded
string. The string held a list of `{label, description}` objects, so the
options had been put where the questions go. Strument rejected the call. MiMo
then sent a real array that was still flat, and Strument rejected that too
("Question 1 must offer 2 to 4 options; it offered 0"). The third attempt was
nested correctly. A third session, `mimo-trim-port-0`, sent a question with
no `question` text.

- **Totals:** 6 calls rejected across 3/25 MiMo trim sessions, against 0
  calls in 0/25 MiMo base sessions. DeepSeek had no rejections in either arm.
  That is 3/50 against 0/50 sessions, p = 0.24, which is not significant.
- **Every one recovered** by the next call or the one after.
- **A likely mechanism:** the base description says "label is the fast scan,
  description carries the actual tradeoff", which names both levels of the
  structure in prose. The trim mentions an "option" once, in passing. The
  schema carries the structure either way, and the schema did not change. But
  MiMo appears to lean on the prose.

This is a post hoc count on a pattern found by a crash, so the trial does not
license a claim. It is enough to justify putting back the one sentence that
names the structure. That moves the shipped text toward base, not away from
it, and the shipped description is 398 characters, against 773 before.

## Transcripts read

- `mimo-trim-name-2`: took three attempts at the call (described above), then
  renamed the module and README to `courier` through the edit tools.
  Unattended, it could not run `mv`/`rm`, so it wrote `cmd/courier/main.go`
  and left `cmd/relay/` in place.
- `deepseek-base-main-0`: the single-lookup over-use described under Trial A.
- **The first option of every well-formed question in both arms** was listed
  side by side to check the recommendation-first scoring. Hits were what they
  appear to be ("courier — recommended", "stdlib log/slog (recommended)").
  Misses were options with no marker at all, not markers the regex failed to
  catch.

## Limitations

- **Sessions per cell are small.** A regression would have to be large to
  show at p < 0.1, as the preregistration said.
- **Two cheap models.** The malformed-call pattern appeared in one of them.
- **The preregistration's size is wrong.** It gives the base description as
  1,800 characters; it is 773. The arms are as described, and only the figure
  was off.
- **Script mode declines every question.** What was counted is whether the
  model asked, not whether the question was useful to a person.
- **The shipped `ask_user_question` description was not measured:** the trim
  plus one sentence.

## Files

| file | contents |
| --- | --- |
| [`preregistration.md`](preregistration.md) | the arms, metrics and rules, committed before the run |
| [`data/run.py`](data/run.py) | the runner |
| [`data/fixture.py`](data/fixture.py) | the fixture, from `2026-09-run-code-arms` |
| [`data/score.py`](data/score.py) | the scorer and Fisher tests; the malformed-call count is marked post hoc |
| [`data/scored-code.jsonl`](data/scored-code.jsonl), [`data/scored-ask.jsonl`](data/scored-ask.jsonl) | one row per session |
| [`data/order-code.json`](data/order-code.json), [`data/order-ask.json`](data/order-ask.json) | the shuffled run orders |
