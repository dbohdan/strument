# Naming attractors: what do models call things when you don't tell them?

**Status: trial, run 2026-11-09.** The design and predictions were committed
after a pilot and before the main run. The results are at the end.

Ask a model to invent a name for a fantasy heroine and some names keep coming
back. "Elara Voss" became famous enough as one of these to turn into a tell.
This trial measures such attractors directly. It looks at how concentrated each
model's choices are, whether a paragraph of setting loosens them, whether
deliberating escapes them or finds new favorites, and how much three models
share. It is about models themselves rather than about Strument, and it uses
[the handbook](../../experimenting.md)'s methods: counts, not a judge model;
the mechanism confirmed before measuring; raw output kept.

## Arms and models

- **Models:** Claude Opus 5, Claude Opus 5.5 and GPT-6 Sol, all through
  OpenRouter's batch API. Each model runs as one batch, and the three batches
  were submitted within seconds of each other. The requests in each batch are
  shuffled with seed 20261109.
- **Off** is the main arm, run at the lowest reasoning setting each model
  accepts:
  - `none` for Opus 5 and Sol;
  - `minimal` for Opus 5.5, which refuses `none` ("Reasoning is mandatory
    for this endpoint and cannot be disabled").

  Every call's reasoning tokens are recorded, so what actually ran is measured
  rather than assumed. The off arm asks every prompt in two conditions, 20
  samples each:
  - **bare:** "Invent a name for {thing}. Reply with the name only."
  - **context:** the same sentence after a two- or three-sentence setting
    paragraph that contains no proper names. The context can shift the choice,
    but it cannot supply a name to reuse.
- **Deliberate** asks 12 of the bare prompts, two per kind, 20 samples each,
  as: "Invent a name for {thing}. Take a moment to think it over before you
  answer. Reply with the name only." Reasoning effort is `high`.

  The instruction is there because the pilot found that a reasoning setting
  alone does not produce reasoning on this task. At `high` effort, Opus 5.5 and
  Sol spent 0 reasoning tokens naming a starship captain, and Opus 5.5 answered
  "Captain Elara Voss" two times out of three. Effort is a ceiling the model
  chooses not to use on a task it judges trivial. So this arm differs from bare
  in both the instruction and the setting, and it measures deliberation as a
  user would ask for it, not the effect of the reasoning parameter alone.
- **Prompts:** 30 things in six kinds, five of each, listed in
  [`data/prompts.py`](data/prompts.py):
  - characters;
  - places;
  - software;
  - vessels;
  - creatures;
  - miscellaneous: a band, a coffee shop, a sword, a codename and a planet.

  That is 1,440 requests per model and 4,320 in all. The pilot cost $0.04; the
  main run is estimated at about $2.50 at batch prices.

## Scoring

- Each reply is normalized by [`data/analyze.py`](data/analyze.py):
  - the first line is taken;
  - Markdown and quotes are unwrapped;
  - trailing punctuation goes;
  - leading titles and "the" are stripped ("Captain Elara Voss" and "Elara
    Voss" are one name);
  - the result is lowercased.
- A reply is **compliant** if it is one line holding a name of one to six
  words. Non-compliant replies are counted apart and left out of the diversity
  measures.
- A leading `<thought>` or `<thinking>` block is removed before scoring and
  counted in a column of its own. The pilot found Opus 5, set to no reasoning,
  writing its reasoning inline: `<thought>The user wants me to invent a name
  for a command-line tool…</thought>riffle`.
- The scorer has a self-test (`analyze.py --self-test`) on hand-made cases,
  both compliant and not.

**Measures**, per model, arm, condition and prompt:
- **top-1 share:** the most common name's share of the samples;
- the number of **distinct** names;
- **entropy** in bits (at most 4.32 for 20 samples).

Across models: whether two models share a prompt's most common name, and the
Jaccard overlap of their name sets. Descriptive only:
- the most common names;
- first names and surnames of the characters;
- letter patterns: *v*, *th*, a final *a*.

**Tests:** exact two-sided sign tests on the per-prompt top-1 share, with ties
dropped, for:
1. each pair of models on the bare condition (30 prompts);
2. bare against context, per model (30 prompts);
3. off against deliberate on the bare condition, per model (12 prompts).

For deliberation, two further measures:
- **overlap:** the share of deliberate names that also occur in the same
  model's off samples for that prompt;
- **modal changed:** on how many prompts the most common name differs between
  the arms.

## Pilot

Six prompts (the first of each kind), both conditions, two samples, and two
deliberate prompts, for each model: 84 requests, $0.04.

- **Every off call used 0 reasoning tokens,** Opus 5.5 at `minimal` included.
- **Every deliberate call reasoned.** Median reasoning tokens:
  - Opus 5.5: 38;
  - Sol: 146;
  - Opus 5: 362.
- **No errors, and one inline `<thought>` block,** which led to the column
  above.
- **The names already rhyme.** Three of three rock bands began with "Velvet",
  every dragon name in a pre-pilot probe began with "Vy", and "Steadfast
  Hope" came from two models for the same sailing ship.

The pilot's names are not counted. The predictions below were written after
reading them.

## Run notes

**One change after the commit above, before any main-run result.** The first
main submission was refused with "This batch is estimated to cost $25.42,
which exceeds your available balance of $7.02". OpenRouter prices a batch as
if every request used its full `max_tokens`, and the deliberate arm's cap was
8,000. Nothing ran, and two things changed:

- **Caps:** `max_tokens` is now 80 for off and 1,500 for deliberate. The
  pilot's largest real uses were 41 (an inline thought) and 644 (Opus 5).
  A reply that reaches its cap is counted in a "truncated" column.
- **Batches:** each arm of each model is its own batch. The three off batches
  went in together. The deliberate batches follow as the balance allows, so
  across models they may start minutes apart, and the off-versus-deliberate
  comparison within a model spans the same gap.

## Predictions

1. **Concentrated by default.** The median bare top-1 share is at least 0.25
   for every model.
2. **Context loosens it.** Top-1 share is lower with context than bare, for
   every model, at p < 0.05.
3. **Lineage shows.** Opus 5 and Opus 5.5 do not differ significantly in bare
   top-1 share. Sol is more concentrated than Opus 5.5 (p < 0.05): in
   pre-pilot probes, Sol gave "Voss" as the surname in 9 of 9 starship
   captains.
4. **Deliberation finds new favorites more than variety.** Under deliberation:
   - top-1 share drops for Opus 5, the model that reasons longest, but not
     significantly for Opus 5.5, which barely reasons;
   - for every model, under 30% of deliberate names occur among its off names,
     and the median deliberate top-1 share stays at 0.15 or more.

   In other words, deliberating moves the choice to a second attractor rather
   than spreading it.
5. **The letter v.** At least 40% of bare names contain a *v*, for every model.
   "Elara" is at least 5% of Sol's character first names.

## Limitations, known in advance

- **Twenty samples per cell** make a top-1 share a coarse, noisy estimate.
  They are good for comparing models with paired tests over 30 prompts, not
  for any single prompt's figure.
- **The setting paragraphs are mine.** They fix things a bare prompt leaves
  open, such as gender, era and tone, so "context loosens it" partly means
  "a narrower target picks different names". Same-model, same-paragraph
  comparisons are fair. Comparing context with bare measures what context does
  in general, not what any particular paragraph does.
- **One provider per model,** at whatever date the batch ran. Model behavior
  under the batch API is assumed to match the sync API. The pre-pilot probes
  used sync, and the pilot used batch; they agreed in kind.

## Results

All 4,320 requests completed, with no batch failures, for $2.16. The full
report is [`data/report.md`](data/report.md), produced by `analyze.py
data/runs`. The raw replies are in [`data/runs/`](data/runs/).

**Disclosure.** The model that designed this trial and wrote this report is
Opus 5.5, one of the three being measured. Its own naming habits were
measured below, and they come out more concentrated than it predicted.

### What ran

| model | arm | n | truncated | non-compliant | reasoned | median reasoning tokens |
| --- | --- | --- | --- | --- | --- | --- |
| Opus 5 | off | 1,200 | 1 | 23 | 0 | 0 |
| Opus 5.5 | off | 1,200 | 66 | 58 | 149 | 0 |
| GPT-6 Sol | off | 1,200 | 0 | 0 | 0 | 0 |
| Opus 5 | deliberate | 240 | 0 | 0 | 240 | 280 |
| Opus 5.5 | deliberate | 240 | 0 | 0 | 240 | 87 |
| GPT-6 Sol | deliberate | 240 | 0 | 0 | 240 | 96 |

Two things the pilot did not show:

- **Opus 5.5 at `minimal` still reasons when it wants to.** In the off arm,
  149 calls reasoned. Almost all were context prompts, concentrated on a few:
  - all 20 generation-ship samples;
  - 18 airship, 18 detective and 17 AI-character samples.

  Sixty-six of those spent the whole 80-token cap thinking and returned an
  empty reply. The pilot's six context prompts happened not to include them.
  So Opus 5.5's context condition is partly a reasoning condition. The
  sensitivity check below drops those prompts.
- **An output filter, reproducibly.** Opus 5's open-source-library prompt
  *with context* ended in `finish_reason: content_filter`, with empty content,
  20 times out of 20. The same prompt bare passed. That cell is empty, so Opus
  5 has 29 context prompts. The paragraph describes a config-file parser, so
  presumably the filter caught something in the output, not the request.
  What it caught is not visible.

### Concentration

Median over prompts. Top-1 share is the most common name's share of 20 samples.

| model | bare top-1 | bare distinct | context top-1 | deliberate top-1 |
| --- | --- | --- | --- | --- |
| Opus 5 | 0.15 | 15.5 | 0.30 | 0.20 |
| Opus 5.5 | 0.40 | 8 | 0.32 | 0.20 |
| GPT-6 Sol | 0.38 | 7 | 0.55 | 0.20 |

Paired sign tests on per-prompt top-1 share:

| comparison | more concentrated | p |
| --- | --- | --- |
| bare: Opus 5 vs Opus 5.5 | Opus 5.5 on 25 of 30 | 0.0003 |
| bare: Opus 5 vs Sol | Sol on 23 of 29 | 0.0023 |
| bare: Opus 5.5 vs Sol | 15 vs 13 | 0.85 |
| bare vs context, Opus 5 | context on 14 of 23 | 0.40 |
| bare vs context, Opus 5.5 | bare on 19 of 29 | 0.14 |
| bare vs context, Sol | context on 19 of 29 | 0.14 |
| off vs deliberate, Opus 5 | 4 vs 4 | 1.0 |
| off vs deliberate, Opus 5.5 | off on 9 of 10 | 0.021 |
| off vs deliberate, Sol | off on 7 of 11 | 0.55 |

**Sensitivity check:** leaving out the 15 context prompts where Opus 5.5
reasoned or was truncated, no model's bare-versus-context difference becomes
significant. The medians go from 0.16 to 0.32 for Opus 5, stay at 0.35 for
Opus 5.5, and go from 0.30 to 0.50 for Sol.

### Deliberation finds other names, not more of them

| model | deliberate names also among its off names | prompts whose modal name changed |
| --- | --- | --- |
| Opus 5 | 8% | 10 of 12 |
| Opus 5.5 | 14% | 12 of 12 |
| GPT-6 Sol | 13% | 12 of 12 |

Asked to think it over, every model moved almost entirely off its reflex
names, but not toward variety. The median deliberate top-1 share is 0.20 for
all three models: four of every 20 samples still land on one name. For Opus
5.5 that is a real loosening (0.40 to 0.20). For Opus 5, already the loosest,
it is no change.

### What the attractors look like

Exact names barely overlap between models: the mean Jaccard overlap of name
sets per prompt is 0.03 to 0.05, and a prompt's modal name is shared on 1 to 3
of 30 prompts. The *templates* are shared. Off arm, bare and context:

| template | Opus 5 | Opus 5.5 | GPT-6 Sol |
| --- | --- | --- | --- |
| Velvet … (rock bands) | 20 | 20 | 10 |
| Gilded … (taverns, ships) | 26 | 35 | 7 |
| Vael… | 25 | 24 | 38 |
| Vesper… | 22 | 22 | 43 |
| Project … (codenames) | 40 | 32 | 40 |
| …thorn… | 34 | 39 | 21 |
| …meridian… | 4 | 47 | 0 |
| Quill… | 1 | 36 | 11 |
| … Voss | 3 | 14 | 42 |

Some single prompts are close to deterministic:
- "Lumora" was every model's startup;
- Opus 5.5 named the fictional country "Valdoria" 19 times in 20;
- Sol named the detective "Mara Voss" 19 times in 20;
- Opus 5.5 named the starship captain "Elara Voss" 11 times in 20.

Elara is not gone. She has moved to Opus 5.5, which used "Elara" for 11 of 181
character first names, against 4 of 198 for Opus 5 and 6 of 200 for Sol.

### Predictions, scored

1. **Concentrated by default (median bare top-1 ≥ 0.25 for every model):
   partly.**
   - Opus 5.5 (0.40) and Sol (0.38): yes.
   - Opus 5 (0.15): no. It is the most varied of the three by a wide margin.
2. **Context loosens it: no.** No difference was significant. Opus 5 and Sol
   leaned the other way, becoming *more* concentrated with a setting. The
   limitation noted in advance is the likely reason: a paragraph that fixes
   gender, era and tone narrows the target more than it unsettles the habit.
3. **Lineage shows (Opus 5 ≈ Opus 5.5; Sol > Opus 5.5): no, on both counts.**
   Opus 5.5 is far more concentrated than Opus 5 (p = 0.0003), and Sol and
   Opus 5.5 are indistinguishable (p = 0.85).
4. **Deliberation finds new favorites more than variety: mostly yes, with
   the strength backwards.**
   - Overlap stayed under 30% for every model.
   - The deliberate top-1 share stayed at 0.15 or more.
   - Top-1 share fell significantly for Opus 5.5, not for Opus 5. That is the
     reverse of the prediction, which assumed the longer reasoner would
     loosen more. The model with the most concentrated reflex had the most to
     lose.
5. **The letter v (≥ 40% of bare names, every model): partly.**
   - Opus 5: 47%. Sol: 52%. Opus 5.5: 37%.
   - "Elara" as at least 5% of Sol's character first names: no (3%). Sol's
     signature is the surname: "Voss" 42 times, and "Mara" 45 times as a
     first name.

### Reading it

- **A newer model is not a more varied one.** Opus 5.5's bare naming is far
  more concentrated than Opus 5's, and as concentrated as Sol's, though its
  prose has fewer of the well-known tells. One plausible reading connects the
  two: the same training that removes tells makes outputs more predictable
  generally. This trial cannot separate that from other differences between
  the two models.
- **Asking a model to think is a better lever than the reasoning setting, but
  only a partial one.** Effort alone did not produce reasoning in the pilot.
  The instruction did, and it moved every model onto different names, while
  leaving a fifth of the samples on one name.
- **Context did not help.** For anyone using these models to name things, the
  practical upshot is that a richer brief does not by itself buy variety. If
  variety is the goal, asking for several candidates, or rejecting the first,
  is worth testing. Neither was tested here.

### Limitations, found in the run

- Opus 5.5's off arm reasoned in 149 calls. The `minimal` setting is a
  request, not a guarantee, and the context comparison for Opus 5.5 should be
  read with the sensitivity check beside it.
- One Opus 5 context cell was emptied by a content filter.
- **The template counts are ad hoc.** They are patterns I picked after seeing
  the names, so they describe, they do not test.
