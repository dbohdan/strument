# Does the scope block suppress in-scope work?

A live A/B on Strument's own prompts, run 2026-08-10 against four models
through OpenRouter.
Total cost $4.14, of which $3.93 was one model.

The short version: the tested change works, the first design that measured it
was wrong, and the way it was wrong is more useful than the result.

## The question

`overeagerPrompt` (`internal/prompts/prompts.go`) is inherited from aider and
is the only block in the tool-format prompt with no positive counterpart:

```
Pay careful attention to the scope of the user's request.
Do what they ask, but no more.
Leave unrelated code untouched: no drive-by refactoring, reformatting, added
comments, or fixes to things the user didn't ask about.
```

One attend-to, one "no more", four named bans, and no statement of what good
scope discipline looks like.
The worry is that it cannot distinguish **in-scope work the user did not
enumerate** — the call sites a rename breaks, the test that covers the function
— from **out-of-scope drive-by work** like reformatting, and bans both by
implication.
Strument's founding regression case was a model asked to change a function and
its separately-stored test.

A second, smaller question: the tool taxonomy is introduced as three groups that
"differ in what they cost the user", and a cost frame over the whole tool
section may lean against looking even though the next clause says to look
freely.

## Design

Two arms per experiment, one binary each, prompts patched and compiled in.

- **E1** — `overeagerPrompt` with a positive reach clause added
  ("Carry the change through everywhere it reaches: the call sites it breaks,
  the tests that cover it, the docs that describe it.").
- **E2** — "differ in what they **cost** the user" becomes "differ in what they
  **need from** the user".

A fixture Go module (`money.Cents` truncating to whole cents, a test asserting
the truncated values, a caller, and a badly formatted file as drive-by bait).
Each run gets a pristine copy; edits are counted by diffing against it.
No `verify` is configured and `bash` stays gated, so the model cannot discover
the stale test by running anything — the prompt's disposition has to be the
deciding factor.

Metrics are counts, not judgements, which is the property that makes this worth
running rather than arguing about:

| | |
| --- | --- |
| M1 | `money_test.go` edited (the reach) |
| M2 | `README.md` edited (a second reach, named explicitly by the clause) |
| M3 | any file edited outside the three legitimate targets (**counter-metric**) |
| M4–M6 | observation tool calls per run; runs with none; whether the answer names both quirks |

M3 matters as much as M1: the whole risk of the change is that it buys reach at
the price of drive-by refactoring, which is what the block exists to prevent.

Predictions were pre-registered before any arm ran, and amended in place as the
design changed.

## What went wrong, in order

**The pilot's ceiling assumption came from n=1.**
A single smoke run updated the test file, so the pre-registration predicted the
baseline was near ceiling and the effect would be at most +1/12.
Baseline was 6/12.

**Naming a target in the prompt did not produce the behaviour.**
M2 predicted base 2–4/12 against e1 6–9/12.
Actual: 0/12 and 1/12, and at full scale 4/90 and 6/90 — null.
The clause literally says "the docs that describe it", the most
teaching-to-the-test phrasing available, and it moved nothing.
Whatever works in the clause is the test/call-site half or the framing; the doc
half is inert and was dropped before landing.

**Provider failures were read as model behaviour.**
Two Qwen strata returned `Empty response received from LLM` with 0 tokens
received on 28 runs.
Distributed unevenly across arms by luck, they looked like the treatment halving
task completion.
A separate 24 runs were genuinely the model emitting a tool call as inline text
markup (`<symbols kind="definition", name="Cents"`) rather than through the
function-calling API — real behaviour that must *not* be filtered out.
Lumping the two together was the error; only opening individual transcripts
separated them.

**And the one that matters: arm order was confounded with time.**
The runner generated jobs as `for arm, for model, for rep`, so every baseline
run preceded every treatment run.
This was noticed and named as a design weakness *before* the numbers came in,
and then not acted on — the results were interpreted anyway.

## The artifact, demonstrated

The confound is not theoretical.
Re-running one cell with shuffled order flipped its sign:

| Qwen3.6-27B, E2 | used tools at all |
| --- | --- |
| sequential | base 1/15 → e2 **12/15** |
| randomised | base 5/15 → e2 **1/14** |

Re-running E1 on the three provider-clean models with shuffled order moved the
baseline while leaving the treatment arm untouched:

| E1, three models | base | e1 | CMH p |
| --- | --- | --- | --- |
| sequential | 28/43 · 65% | 43/45 · 96% | **0.0009** |
| randomised | 38/45 · 84% | 43/45 · 96% | 0.151 |

The treatment arm is identical in both rows.
Everything that moved is the baseline, purely from *when* those runs happened.
The unrandomised design would have produced p=0.0009 and a confident
recommendation to ship.

## Result

Randomised, n=90 per arm, models deepseek-v4-flash / claude-haiku-4.5 /
mimo-v2.5 (the two Qwen strata dropped for provider instability):

| metric | base | e1 | |
| --- | --- | --- | --- |
| **M1 test file edited** | 76/90 · 84% | 87/90 · **97%** | Fisher p=0.0090, CMH p=0.0106 |
| M3 drive-by | 0/90 | 0/90 | clean |
| M2 README | 4/90 | 6/90 | null |

Per model: deepseek 29/30 → 29/30 (ceiling), haiku 22/30 → 30/30,
mimo 25/30 → 28/30.
Both non-ceiling models moved.

The effect is real at about +13 points with no measurable counter-metric cost
across 180 randomised runs.
E2 is null: the cost framing does not detectably change how much the model
looks, and the one cell that suggested otherwise was the artifact above.

**Scope of the claim:** one task, one fixture, one reach target, three models.

## What the transcripts show that the statistics do not

The mechanism is visible without any arm comparison.
Claude Haiku 4.5, baseline arm, changed the implementation and wrote in its own
summary:

> 2.999 rounds to **300** cents (not 299)

...and left `money_test.go` untouched.
It had computed the stale assertion and declined to go there.
That is not ignorance; it is scope discipline, and `overeagerPrompt` is the only
thing in the system asking for it.

Counting told us how often. Reading told us why.

## Implications for the next experiment

1. **Randomise arm order.** Not as hygiene — as the single highest-value step.
   Here it was worth more than tripling the sample size, and its absence
   manufactured a p=0.0009 result out of nothing.
2. **A result that confirms your hypothesis at implausible strength is data
   about your instrument, not your hypothesis.** The artifact was caught only
   because 1/15 → 12/15 was too good. A plausible 4/15 → 7/15 would have been
   believed and published.
3. **Separate infrastructure failure from model behaviour by opening
   transcripts,** not by log length or exit code. They look identical in
   aggregate and mean opposite things.
4. **Pick metrics that are counts, not judgements.** This removes the
   author-is-judge problem entirely — and it is worth knowing that it does not
   protect against anything else.
5. **Report the counter-metric with the same prominence as the primary.** M3 at
   0/90 in both arms is why the change is safe to land; M1 alone would not have
   justified it.
6. **Strata at ceiling or floor carry no information.** Stratify (CMH) rather
   than pool, and say which models were informative.
7. **Write predictions down before running, and amend in place.** Four of the
   predictions here were wrong. Recovering that from memory afterwards would
   have been impossible, and the record of them failing in order is the most
   useful artifact the exercise produced.
8. **Model choice dominates cost.** Claude Haiku 4.5 was $3.93 of the $4.14
   total — roughly 95% of the spend for one of four strata. A cheaper
   near-frontier model (MiMo-V2.5-Pro, GLM-5.2) buys several times the sample
   for the same money, and sample size is what this kind of question is
   starved of.
9. **Verify provider health per model before committing to a stratum.** Two of
   five models were unusable, and that was only discoverable live.
