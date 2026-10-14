# Does the summary label keep models from searching the record?

**2026-10-14. Preregistered; results below the line.**

## Why

In `2026-10-recall-models`, every `roll+recall` session searched the session
record in its question turns. Among `summary+recall` sessions, Ling 3.0 never
searched and never even mentioned the tool. The transcripts suggest why. The
summary's label says "It replaces those messages", and Ling 3.0 read it
literally: s23 reasoned that the messages "are not available to me anymore"
and answered "don't know". The roll note says instead that the rest of the
turns is in the record. The summary note (`--recall-note`), a paragraph after
the summary, got Ling 3.0 to search (22/24 against 8/24). Every note session
that searched quoted it first, so the note worked by overruling the label.

This trial changes the label itself. When `recall` is offered, "It replaces
those messages" becomes where they are: it "stands in for those messages
here; the messages themselves, including your own replies and the output of
every tool call, are kept in the session record, and the recall tool
searches it." The new label carries no instruction to search. The question is
whether correcting what the label claims is enough, without the note's
"search the record before deciding you don't know".

## Design

**Binary.** `dev` at 11a5385 plus `data/trial.diff`:

- the 4,000-token history budget hook from `2026-10-recall-models`, so every
  session compacts;
- `prompts.SummaryLabelRecord`, used when `STRUMENT_TRIAL_SUMMARY_LABEL=record`;
- recognition of either label as a summary. The control arm's text and
  behavior are those of `dev`.

At 11a5385, `recall` is offered by default. A hit id passed as a pattern
opens the message, a search names the replies its cutoff hid, and each
compaction is recorded with its text. Both arms have all of these.

**Arms:**

- `summary+recall`: the current label;
- `summary+recall+label`: the new label.

**Script** (`data/runner_label.py`): `2026-10-recall-models`'s runner with the
two arms, including its fix for turns a provider error cut short. The fixture,
the turns and the question wording ("do not read the files again or run
commands") are unchanged. Sessions run `--unattended` with `--yes steps`.

**Models**, each a stratum and never pooled:

| model | where | context | sessions per arm | concurrency | seed |
|---|---|---|---|---|---|
| Ling 3.0 Flash VL | OpenRouter, pinned to DeepInfra | 131,072 | 6 | 2 | 20261031 |
| MiMo-V2.6-Flash | OpenRouter, pinned to Xiaomi | 262,144 | 3 | 4 | 20261032 |

Reasoning is "low" in every config, and `retry_timeout` is 600 seconds. The
first pilot lost a turn to DeepInfra 429s that outlasted the default minute;
about 20% of single small requests are refused, and more under load, hence
also concurrency 2. Ling 3.0 is on DeepInfra because Novita,
which served it in `2026-10-recall-models`, no longer does. This is a new
stratum, not comparable with that trial's Ling 3.0 numbers. Arms are shuffled
within each model. A session that fails on a provider error is rerun in
place and reported.

**Metrics**, as in `2026-10-recall-models` (`data/scorer_recall.py`):
derived facts of 4 (true, "don't know", wrong); user facts of 6; sessions
that called `recall` in the question turns; calls; cost.

## Decision rule

**Ling 3.0 decides.** Its summaries lose facts, so there is room to move.

- **The label ships** if `summary+recall+label`'s mean of true derived facts
  per session is at least 1 above `summary+recall`'s, or its wrong answers
  are at least 2 fewer.
- **Veto:** it does not ship if its wrong answers exceed the control's by
  more than 1.

**MiMo is the counter-check,** reported and not decided on. Its summaries were
already at the ceiling, so the risk there is a new wrong answer or a search
that should not have been needed. Any MiMo wrong answer in the label arm is
read in full and reported.

**Pilot:** one session per arm on Ling 3.0, unscored. It confirms that the
label reaches the model, that `recall` works, and that compaction records
are written.

---

## Results
