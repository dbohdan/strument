# Where a compaction summary's input comes from

**2026-09-21.** 48 live sessions, two models, arm order randomized. Design
fixed in [`preregistration.md`](preregistration.md) before the batch ran; data
and runner in [`data/`](data/).

**Result: a large effect on the primary metric, and a counter-metric that did
not work. Not shipped on this trial.**

## The question

Compaction folds `doneMessages`, and `doneMessages` already holds the previous
summary — `summarizeReal` prepends its output to the retained tail, so summary
*n+1* is built from summary *n*. An error at fold 3 is source material at fold
4.

`notes.go` names this and rejects it for notes: *"regenerating purely from the
record is self-healing … folding the previous notes in is self-reinforcing"*.
Notes regenerate from the durable record every time. Compaction is the thing
that argument was written against, and could not be changed to match until
Phase 2 gave it a record to read.

`record` makes compaction read the session record, sampled to the bound notes
use, instead of the messages being folded. Same prompt, same budget, same
retained tail.

## Result

Two facts planted once each and unavailable from the files: a **reason** in
turn 1 (45 seconds because the upstream load balancer idles at 60) and a
**middle** fact in turn 6 (`retryAfter` rejected as ambiguous with the HTTP
header). Turn 12 asks for both with files off-limits. 12 sessions per arm per
model; every session folded 10–18 times; none excluded.

### reason (turn 1) — the primary metric

| | recalled | declined | confabulated | absent |
| --- | --- | --- | --- | --- |
| mimo `fold` | **0/12** | 11/12 | 0/12 | 1/12 |
| mimo `record` | **10/12** | 2/12 | 0/12 | 0/12 |
| glm `fold` | **1/12** | 11/12 | 0/12 | 0/12 |
| glm `record` | **6/12** | 6/12 | 0/12 | 0/12 |

Two-sided Fisher: **p < 0.001** (mimo), **p = 0.069** (glm).

MiMo's `fold` arm recalled the reason in **zero of twelve** sessions. That is
the self-reinforcing fold doing exactly what `notes.go` predicted: across
fifteen folds the reason is dropped once and is then gone for good, because
nothing later re-reads the turn that carried it.

### middle (turn 6) — the counter-metric, as preregistered

| | recalled | declined | confabulated | absent |
| --- | --- | --- | --- | --- |
| mimo `fold` | 0/12 | 11/12 | **0/12** | 1/12 |
| mimo `record` | 3/12 | 5/12 | **4/12** | 0/12 |
| glm `fold` | 3/12 | 9/12 | 0/12 | 0/12 |
| glm `record` | 6/12 | 6/12 | 0/12 | 0/12 |

The predicted harm did **not** occur: `record`'s head-and-tail sampling did not
cost middle recall, which rose in both models. But confabulation rose 0 → 4 on
MiMo, and the preregistered rule says a change that converts loss into
invention is a regression even when recall improves. **By that rule this does
not ship.**

## The counter-metric counted the wrong thing

Reading the four "confabulations" — which the preregistration required, by a
rule written before any aggregate existed — shows none of them is an invention:

> `NAME: defaultTimeout was rejected because it misleadingly suggested a timeout value rather than a polling interval.`

> `NAME: pollInterval was the name we initially used but then rejected and renamed to defaultPollInterval.`

All four name `defaultTimeout` or `pollInterval`: the constant turn 1 actually
renamed. That is a true statement about the session. The probe asked *"which
name did we consider and reject for that constant"*, and this fixture has two
defensible answers — the planted `retryAfter`, and the real old name the
session renamed away from. The scorer had no column for the second, so it fell
into the one reserved for invention.

Re-read with that column added ([`data/rescore.py`](data/rescore.py),
**post-hoc**, decided after seeing the results):

| | recalled | declined | other-reading | confabulated | absent |
| --- | --- | --- | --- | --- | --- |
| mimo `fold` | 0/12 | 11/12 | 0/12 | **0/12** | 1/12 |
| mimo `record` | 3/12 | 5/12 | 4/12 | **0/12** | 0/12 |

No confabulation survives in either arm, under either model.

**This correction moves the result toward the hypothesis, which is where a
post-hoc reading deserves the least trust.** Both scorings are reported and
the preregistered one stands. What the trial can say is that the *predicted*
harm did not appear; what it cannot say is that confabulation did not rise,
because the column meant to detect that was measuring something else.

Note also that `fold`'s zero confabulations are not a virtue: that arm recalled
nothing and declined almost everything. An arm that knows nothing cannot invent
anything.

## Cost

| | folds | compaction input | session cost | wall clock |
| --- | --- | --- | --- | --- |
| mimo `fold` | 15.0 | 21,687 tok | $0.0243 | 683s |
| mimo `record` | 15.0 | 26,298 tok | $0.0228 | 649s |
| glm `fold` | 12.0 | 16,122 tok | $0.0349 | 220s |
| glm `record` | 10.5 | 14,662 tok | $0.0379 | 221s |

Medians. `record`'s compaction input rose 21% on MiMo and fell 9% on GLM;
session cost is flat either way, because compaction is a small share of a
session's spend. The cost objection to reading the record is not what stops
this.

## Limitations

**The arms differ in two coupled ways**, stated in the preregistration before
the run: the *source* is the intended change, but *granularity* rides along —
`renderForSummary` shows tool calls in full and clips results, while the
record's markdown carries prompts, answers and one-line work summaries. So
`record` sees more of the session and less of each turn. A result speaks to the
bundle; it cannot say which half carried the effect.

**GLM is suggestive, not significant** at p = 0.069 with n=12.

**Two compaction calls did not return ok** across 48 sessions, one in each arm.
Both sessions completed with 13–14 folds.

**One MiMo `fold` session answered `UNKNOWN` without the markers** and is scored
`absent` rather than `declined`. Its reasoning names the mechanism directly:
*"The summary mentions that pollInterval was changed … but doesn't provide
details about what value it was."*

## Decision

**Not shipped on this trial.** The preregistered rule required confabulation
not to rise, and the instrument that was supposed to establish that was
defective — so the check the rule depends on did not run. A large primary
effect does not substitute for a counter-metric that failed to measure.

What would settle it is a re-run with a disambiguated probe: name the
alternative explicitly, or ask for the name rejected *before* the rename, so
the two readings cannot both be correct. The fixture, runner, scorer and
analysis are in `data/` and the probe is one string.

`--compaction-source` stays hidden and defaults to `fold`.
