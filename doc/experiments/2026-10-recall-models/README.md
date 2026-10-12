# Searching the record, with pointers, on four models

**2026-10-12. Preregistered; results below the line once run, one model at a time.**

## Why

In `2026-10-recall` (Ling 3.0 Flash VL), sessions that called `recall`
answered 15 of 16 derived questions correctly. Few called it: 4 of 18
sessions where it was offered. The roll note said the dropped work "is not
kept" and pointed at re-reading the files, and so did the cleared-result
placeholder. `2026-10-recall-pointers` rewrote both, and the summary note,
after six models reviewed the wording.

This trial asks two things:

- **Do the pointers get models to search?**
- **Does `recall` hold up across models,** given that the last trial found a
  strong coder can be a weak summarizer?

## What changed since `2026-10-recall`

When `recall` is offered:

- **The roll note** says the rest of the dropped turns is in the session
  record. After the quoted messages it says when to search it, and that file
  contents need `from="all"` (`prompts.RollRecallLabel`, `RollRecallTail`).
- **The cleared-result placeholder** leads with `recall`
  (`clearedToolResultRecall`).
- **The summary note** (`--recall-note`) uses the same wording.

Without `recall`, every text is as before.

**The question turns** say "do not read the files again or run commands",
instead of "do not read or search the files, or run anything". Reviewers
read the old wording as possibly forbidding a search of the record.

Both changes apply at once. A comparison with `2026-10-recall` on Ling 3.0
is therefore a before-and-after across two runs, with two changes, and is
reported as such. Within each model the arms are randomized.

## Design

**Binary.** `dev` at bbefc38 plus the budget hook (`data/trial.diff`): a
4,000-token history budget, so every arm compacts.

**Arms**, the same five as `2026-10-recall`: `summary`, `summary+recall`,
`summary+recall+note`, `roll`, `roll+recall`. Every `+recall` arm now gets
the placeholder pointer; `roll+recall` also gets the roll note's; the note
arm adds the summary note's.

**Script** (`data/runner_recall.py`): as in `2026-10-recall`, apart from the
question wording above. Sessions run `--unattended` with `--yes steps`.

**Models**, each a stratum and never pooled, run in this order:

| model | where | context | concurrency | seed |
|---|---|---|---|---|
| Qwen3.8 27B, the community fine-tune `Qwen3.8-27B-Uncensored-HauhauCS-Aggressive`, quantized Q4_K_P | the maintainer's llama.cpp server, one slot | 131,072 | 1 | 20261021 |
| Ling 3.0 Flash VL | OpenRouter, pinned to Novita | 262,144 | 4 | 20261022 |
| MiMo-V2.6-Flash | OpenRouter, pinned to Xiaomi | 262,144 | 4 | 20261023 |
| Ling 3.1 Flash | OpenRouter, free pool | 262,144 | 2 | 20261024 |

Reasoning is "low" in every config. Qwen's server ignores the effort level
(about 200 characters of thinking either way, in a probe), so it reasons at
its default. Its record keeps the thinking, so its length can be reported.

**Size.** Six sessions per arm per model, 30 per model, shuffled with the
model's seed. One exception, fixed now, before any Qwen result: if Qwen's
smoke session (one `roll+recall` session, excluded from analysis) took more
than 30 minutes, Qwen runs three per arm. A session that fails on a provider
or server error is rerun in its place and reported.

**Metrics** (`data/scorer_recall.py`, `data/aggregate_recall.py`). These are
`2026-10-recall`'s, including its fix for "I don't know" in prose:

- user facts, of 6;
- derived facts, of 4: true, "don't know", **wrong** (answered, not true,
  not "don't know");
- sessions that called `recall` in the question turns;
- `recall` calls and arguments;
- violations (file reads or searches in question turns);
- cost.

## Decision rule

**Per model**, as in `2026-10-recall`:

1. **`recall` rescues a roll** if `roll+recall`'s mean of true derived facts
   per session is at least 2 above `roll`'s.
2. **The note works** if `summary+recall+note`'s mean is at least 1 above
   `summary+recall`'s.
3. **Wrong answers veto:** an arm with `recall` fails rules 1–2 if its wrong
   answers exceed its comparison arm's by more than 1 in 6 sessions (by more
   than 0 at three per arm).

**Across models:**

- **`recall` is offered by default** if rule 1 or rule 2 holds on at least
  two models, and no model's `recall` arm fails rule 3. The roll and
  placeholder pointers ship with it.
- **The summary note ships** if rule 2 holds on at least two models.
- **Otherwise `recall` stays hidden,** and the per-model results say where
  it falls short.
- **Whether `roll` should replace `summary` as the default** is reported and
  not decided here.

**Reported in full:** `summary+recall` against `summary` (the placeholder
pointer alone, on summary sessions), and the share of sessions that searched,
per arm and model.
