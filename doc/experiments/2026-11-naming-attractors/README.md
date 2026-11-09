# Naming attractors: what do models call things when you don't tell them?

**Status: trial, preregistered.** Written and committed on 2026-11-09, after a
pilot and before the main run. Results will follow below.

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
