# Rolling the history instead of summarizing it

**2026-10-11. Preregistered; results below the line once run.**

## Why

`2026-10-compaction-prompt` made summaries keep what the user said, but a
summary is still a model's account of a conversation, and its failure modes are
a model's:

- continuing the transcript instead of summarizing it, now framed against but
  still guarded;
- an ending in the wrong tense (seen live in Larkspur turn 19);
- paraphrase drift;
- a side call that can fail, or be rate-limited, which Larkspur turn 17 hit.

The Cyborgists' rolling contexts drop the oldest text instead of rewriting it,
and Codex and Kimi keep the user's own messages verbatim beside their summaries.
The combination — drop old turns whole, carry the user's messages forward word
for word, no model call — keeps nothing the summary would have invented and
loses what the summary kept of the model's own work. How much that loss costs is
the question.

This trial also settles `compaction_source`: its September advantage (10/12
against 0/12) was measured on the old summarizer, which stage A of
`2026-10-compaction-prompt` found continuing the transcript in 27 of 64 folds.

## Design

One model, Ling 3.1 Flash (`inclusionai/ling-3.1-flash`), free on OpenRouter,
reasoning low; its own summarizer, as by default. A 32k settled-history budget
through the trial hook (`data/trial.diff`), so every arm compacts most turns.
The binary is `dev` at d2970b1 plus that hook.

**Arms** (hidden `--compaction` and `--compaction-source` flags):

| arm | over budget |
|---|---|
| `summary` | clear old tool results, then summarize with P9 (shipped) |
| `roll` | clear old tool results, then drop the oldest turns, carrying the user's messages from them verbatim in one note |
| `roll-bare` | the same, carrying nothing (the floor) |
| `record` | as `summary`, with the summary built from the session record (`compaction_source = record`) |

**Script** (`data/runner_roll.py`): the threshold trial's 21-turn sessions —
sixteen turns reading five files of the Go standard library each — with two
kinds of planted knowledge:

- *user facts*, six, stated in passing in turns 1-3 and 9-11 (as in the
  threshold trial), asked back in turns 17-19;
- *derived facts*, four: in turns 4, 5, 12 and 13 the model is also asked
  which of the five files is longest and how many lines it has, "I will ask
  about it later". The answer is in the model's own reply and nowhere else.
  Asked back in turns 20-21.

Question turns say not to read, search or run anything.

**Metrics** (`data/scorer_roll.py`, self-tested), counts:

- user facts recalled exactly, of 6;
- derived facts recalled, of 4, two ways: against the files (truth), and
  against what the model said at the time (its own result, which is what
  memory can be asked for);
- derived answers that say "don't know", against those that are wrong;
- tool calls in question turns (violations);
- cost, side calls included; compactions; failed folds.

**Size and order.** Six sessions an arm, 24 in all, shuffled (seed 20261017),
two at a time.

**Rule for failed folds.** Ling's free tier rate-limits. A summary call that
fails keeps the history whole, which flatters a summary arm. So a summary or
record session with any failed fold is not scored. It is rerun, in its place
in the order, until it has none, and the reruns are reported.

## Decision rule

- **`roll` replaces `summary` as the default** if:
  - it recalls user facts no worse (mean within 0.5 of 6);
  - it recalls derived facts (own result) within 1 of 4 of `summary`;
  - no session gives a wrong derived answer where `summary`'s give "don't
    know" — a confident wrong answer is the worse failure.
- **Otherwise `summary` stays.** `roll` stays available behind the flag if
  it is within 2 on derived facts, as the mode for providers whose side calls
  are unreliable.
- **`compaction_source` is removed** unless `record` beats `summary` by at
  least 1 fact of the 10 a session asks for.
- `roll-bare` is a floor and decides nothing on its own.

Ling only. The verdict on the default needs the validation the maintainer has
planned, at a smaller scale, on MiMo, GLM-5.3-Flash and GPT-6 Luna, once funded.
