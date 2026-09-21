# Preregistration: where a compaction summary's input comes from

Written before the batch ran, and before any result was seen. The design is
fixed here so the write-up cannot quietly become the design.

## The change

Compaction folds `doneMessages`, and `doneMessages` already contains the
previous summary — `summarizeReal` prepends its output to the retained tail, so
summary *n+1* is built from summary *n*. An error introduced at fold 3 is
source material at fold 4.

`notes.go` names this pattern and rejects it for notes: *"regenerating purely
from the record is self-healing … folding the previous notes in is
self-reinforcing"*. Notes therefore regenerate from the durable record every
time. Compaction is the thing that argument was written against, and it was
never changed to match — because until Phase 2 there was no record to
regenerate from.

**`record`** makes compaction do what notes do: build the summary from the
session record, rendered as markdown and sampled to a bound, instead of from
the folded message list. The retained tail of recent messages is unchanged; the
only difference is what the summarizer reads.

## Arms

`fold` is `HEAD`. `record` is `HEAD` plus the flag. They differ in the
summarizer's input and in nothing else — same prompt, same budget, same
placement of the result, same tail.

## What the arms actually differ in

Two things, coupled, and saying so here rather than in the write-up.

The **source** is the intended change: the folded messages, which hold the
previous summary, against the record, which does not.

The **granularity** comes with it. `renderForSummary` lays out messages with
tool calls in full and tool results clipped to a budget. The record's markdown
has neither: it carries the prompt, the answer and the harness's one-line work
summaries. So `record` sees more of the session and less of each turn.

They are not separable without inventing a third rendering that exists nowhere
else, and the point of the change is that compaction reads *what notes read* —
one source, two prompts. A result therefore says something about that bundle,
the way the 2026-08 trial's did about its own. What it cannot say is which half
of the bundle carried the effect.

## What the prior says will happen

Two predictions that point in opposite directions, which is why both are
measured.

**Self-healing should help the oldest fact.** A reason stated once in turn 1
must survive every fold to be answerable at the end. Under `fold` it survives
only if each summary carries it forward; one summary that drops it has dropped
it permanently. Under `record` every fold re-reads turn 1 from disk, so a
single bad fold is recoverable.

**Sampling should hurt the middle.** `record`'s input is bounded the way notes'
is — head and tail, with the middle dropped when the record outgrows the bound.
`fold`'s summary is cumulative and has already distilled the middle. So a fact
planted mid-conversation is the one `record` can lose and `fold` can keep.

That second prediction is the counter-metric, and it is reported as prominently
as the first. A change that improves the oldest fact by destroying the middle
one is not an improvement.

## Fixture

The [2026-08 compaction fixture](../2026-08-compaction/README.md), extended.
That trial established the shape works: `context=16384` puts
`maxChatHistoryTokens` at its floor (2048 now, 1024 then) so ordinary tool work
forces compaction, while staying far above any real prompt so `checkTokens`
never fires.

Two facts are planted, each stated once, each unavailable from the files:

| fact | planted | probe | why it is the target |
| --- | --- | --- | --- |
| `reason` | turn 1 | why is the poll interval 45 seconds | the upstream load balancer idles connections at 60; `45` is readable from the code, the reason is only ever in the conversation |
| `middle` | turn 6 | which name was rejected, and why | `retryAfter` was rejected as ambiguous with the HTTP header; never written down |

Turns 2–5 and 7–11 bury them with ordinary file work. Turn 12 asks for both
back, with the files off-limits so the answer cannot be read.

That trial got 2–3 compactions per session. This one needs more, because the
claim is about *compounding* — so the burying turns are doubled, targeting 6+
folds. **A session that folded fewer than 4 times is excluded**, and the
exclusion count is reported: a fixture that stopped forcing compaction would
otherwise look like a null.

## Metrics

Counts, not judgments. Each probe answer is scored into exactly one column:

- **recalled** — names the fact.
- **declined** — says it does not know. Honest loss.
- **confabulated** — gives a *different* reason or name, asserted as fact.
- **absent** — no marked answer line, or the run failed.

`declined` and `confabulated` are separate columns because they are different
outcomes: a change that converts loss into invention is a regression even when
recall improves. The 2026-08 trial's own example is the model inventing *"to
balance between frequent updates and system load"* for a number nobody had
explained.

`absent` is separate from `declined` so a provider failure cannot be read as
the model declining to answer.

## Counter-metrics

- **`middle` recall**, above.
- **Compaction input tokens per session.** `record` re-reads the record every
  fold; `fold` reads a bounded message list. This is the cost of the change and
  it is expected to rise.
- **Session cost and wall-clock**, median per arm.

## Sample and models

12 sessions per arm per model, two models, **arm order randomized within each
model** — the 2026-08 prompt-scope trial found that shuffling the order moved a
baseline from 65% to 84% and took a comparison from p=0.0009 to p=0.15, which
was worth more than tripling the sample.

`xiaomi/mimo-v2.5` is the default per `CLAUDE.md`. The second model is chosen
for disagreement, not for capability, and was priced before the arms were
built — the August trial had one model take $3.93 of a $4.14 total because the
strata were not priced first. Read from OpenRouter on 2026-09-21, per million
tokens in/out:

| model | in | out |
| --- | --- | --- |
| `deepseek/deepseek-v4-flash-0731` | $0.040 | $0.160 |
| `z-ai/glm-5.3-flash` | $0.090 | $0.300 |
| `xiaomi/mimo-v2.5` | $0.140 | $0.280 |
| `deepseek/deepseek-v4.1-flash` | $0.150 | $0.600 |
| `anthropic/claude-haiku-4.5` | $1.000 | $5.000 |

`z-ai/glm-5.3-flash` is the second arm: a different vendor at a comparable
price. Haiku is 7× MiMo on input and 18× on output, which is the stratum that
ate the August budget, and it is not in this trial.

Budget is **$2**, against a key with $8.71 on it. If the pilot's per-session
cost projects past that, the second model is dropped rather than the sample
cut — the sample is what the comparison rests on.

`reasoning="low"` on every model, **and checked that it took** — a model
spending its budget thinking looks exactly like an API failure.

## Before the batch

The pre-run checklist, with what each check means here:

- **The mechanism fires.** Compaction count comes from the session record
  (`side_call` rows with `call == "chat summary"`), not inferred. A pilot
  confirms 4+ per session before anything is spent.
- **The arms differ.** The summarizer's input is dumped per fold in the pilot,
  and the two arms' dumps are compared. Identical inputs mean the flag did
  nothing.
- **The scorer discriminates.** It is tested against handwritten answers for
  all four columns, in both directions, before it scores anything real.
- **A check that can fail.** A sabotage that drops the planted reason from the
  fixture must move `recalled` to zero. If it does not, the probe is not
  measuring what it claims.
- **Raw output is kept**, so a scoring mistake is rescored rather than re-run.

## What would make this ship

`record` ships if `reason` recall improves without `middle` recall falling and
without confabulation rising, at a cost the counter-metric shows to be
tolerable. Any other combination is written up and not shipped — including a
clean null, which at this sample size is a statement about the sample and not
about the change.
