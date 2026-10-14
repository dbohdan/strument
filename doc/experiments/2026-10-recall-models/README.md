# Searching the record, with pointers, on three models

**2026-10-12 to 14. Preregistered; results below the line. `recall` holds on all three models and ships by default; the summary note holds on Ling 3.0 only and does not ship. Ling 3.1 was rerun on Novita after an instrument defect.**

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
| Ling 3.0 Flash VL | OpenRouter, pinned to Novita | 262,144 | 4 | 20261022 |
| MiMo-V2.6-Flash | OpenRouter, pinned to Xiaomi | 262,144 | 4 | 20261023 |
| Ling 3.1 Flash | OpenRouter, free pool | 262,144 | 2 | 20261024 |

Reasoning is "low" in every config.

**Size.** Six sessions per arm per model, 30 per model, shuffled with the
model's seed. A session that fails on a provider error is rerun in its place
and reported.

**Amendment, before any result: Qwen dropped.** Qwen3.8 27B, the community
fine-tune `Qwen3.8-27B-Uncensored-HauhauCS-Aggressive` at Q4_K_P on the
maintainer's llama.cpp server, was planned as the first stratum. Its smoke
session (`roll+recall`, never scored) ran three turns in 8.5 minutes. The
server was busy 457 of those 510 seconds, processing about 56k prompt tokens
a turn at 510 tokens a second. That is about 45–50 minutes a session and a
day for 30, too long to supervise, with a risk of overheating the hardware.
It was stopped.

Two observations from the probe, for anyone who runs it later:
- native tool calls worked;
- the server ignored `reasoning_effort` (about 200 characters of thinking
  at default and at "low"), while `chat_template_kwargs.enable_thinking:
  false` turned thinking off.

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
   answers exceed its comparison arm's by more than 1.

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

---

## Results

### Ling 3.0 Flash VL (Novita)

All 30 sessions completed, at about $1.12. Three (s21 and s29, `roll`; s23,
`summary+recall`) failed on Novita 429s and were rerun in place. Their failed
transcripts are kept in the scratch archive.

| arm | n | user facts | derived, true | "don't know" | wrong | sessions searching in the questions | `recall` calls |
|---|---|---|---|---|---|---|---|
| `summary` | 6 | 36/36 | 15/24 | 8 | 1 | — | — |
| `summary+recall` | 6 | 35/36 | 8/24 | 11 | 4 | 0 of 6 | 0 |
| `summary+recall+note` | 6 | 35/36 | **22/24** | 2 | 0 | 5 of 6 | 56 |
| `roll` | 6 | 36/36 | 0/24 | 24 | 0 | — | — |
| `roll+recall` | 6 | 36/36 | **23/24** | 0 | 1 | **6 of 6** | 39 |

**Rules:**

1. **`roll+recall` against `roll`:** 3.83 against 0.00, a gain of +3.83
   against a bar of 2. Its wrong answers exceed `roll`'s by 1, which the veto
   allows. **Holds.**
2. **`summary+recall+note` against `summary+recall`:** 3.67 against 1.33, a
   gain of +2.33 against a bar of 1. It has no wrong answers, against 4.
   **Holds.**

**Against `2026-10-recall`** (same model, before the pointers and the new
question wording; a before-and-after across two runs, not randomized):

| arm | sessions searching | derived, true |
|---|---|---|
| `roll+recall` | 1 of 6 → **6 of 6** | 4/24 → **23/24** |
| `summary+recall+note` | 3 of 6 → 5 of 6 | 15/24 → 22/24 |
| `summary+recall` | 0 of 6 → 0 of 6 | 8/24 → 8/24 |

**What the pointers did:**

- **The roll note's pointer took searching from 1 session in 6 to 6 in 6,**
  and `roll` from no derived facts to nearly all of them.
- **Every one of the 95 `recall` calls used `from="all"`,** as the note now
  says to.
- **The placeholder alone did nothing for summaries.** `summary+recall` has
  the placeholder pointer and no note, and no session searched. Its 8 true
  and 4 wrong is worse than plain `summary`'s 15 and 1. With six sessions an
  arm that may be noise, but none of the four wrong answers came from a
  search. They are summary errors: two sessions gave
  "runtime/malloc_stubs.go, 638 lines", the runner-up of that turn, for
  os3_solaris.go at 646.
- **Across the three `recall` arms, the 11 sessions that searched** answered
  43 true, 0 "don't know" and 1 wrong. The 7 that did not answered 10 true,
  13 "don't know" and 4 wrong.

**The one wrong answer after a search** (s12, `roll+recall`): in turn 3 it
had said "So the longest is ztypes_darwin_amd64.go at 878 lines". Asked
later, after twelve searches, it answered with the file the question names,
zsysnum_linux_mips.go, at 450 lines. The record held the right answer, and
the model misread its search results.

**Instrument note: a scorer bug, found here and fixed in all three
scorers.** While a 429 was being retried, the terminal redrew the prompt line,
and a question's opening words appeared one extra time with nothing after
them. The scorers split a transcript on those words, so every later answer
shifted onto the wrong question. In s24 here, two correct answers had been
scored as one "don't know" and one wrong. The scorers now keep only segments
that end a turn, and their self-tests cover the redraw. Re-scoring every
earlier batch with both versions changed one completed session elsewhere, in
`2026-10-recall` (corrected there, verdict unchanged), and none in
`2026-10-compaction-roll`.

**Runner note.** A stale proxy from the cancelled Qwen smoke session held a
port, and the first Ling session talked to it: one request went to the wrong
upstream, and the session failed in a second. The batch was wiped and rerun.
The runner now refuses to start a session whose proxy could not bind.

### MiMo-V2.6-Flash (Xiaomi)

All 30 sessions completed on the first pass, with no provider failures, at
$3.32. Sessions took 20–30 minutes each, four at a time. Run under nitro
(below), alongside Ling 3.1.

| arm | n | user facts | derived, true | "don't know" | wrong | sessions searching in the questions | `recall` calls |
|---|---|---|---|---|---|---|---|
| `summary` | 6 | 36/36 | 23/24 | 0 | 1 | — | — |
| `summary+recall` | 6 | 36/36 | 24/24 | 0 | 0 | 1 of 6 | 2 |
| `summary+recall+note` | 6 | 36/36 | 24/24 | 0 | 0 | 3 of 6 | 29 |
| `roll` | 6 | 36/36 | 0/24 | 24 | 0 | — | — |
| `roll+recall` | 6 | 36/36 | **24/24** | 0 | 0 | **6 of 6** | 34 |

**Rules:**

1. **`roll+recall` against `roll`:** 4.00 against 0.00, with no wrong answers
   in either. **Holds.**
2. **`summary+recall+note` against `summary+recall`:** 4.00 against 4.00, a
   gain of 0 against a bar of 1. **Fails, at the ceiling.** MiMo's summaries
   keep every derived fact on their own (plain `summary`: 23/24), so a pointer
   to the record has nothing left to rescue. That fits the prediction in
   `2026-10-recall` that strong summarizers would leave the summary arms
   little room.

**Details:**

- **The only wrong answer is in plain `summary` (s05), outside every
  comparison.** Its summary held no count for one turn. The model reasoned
  "That risks being wrong… I'll commit" and gave the previous turn's answer
  again.
- **Searching never hurt.** The 10 sessions that searched answered 40 of 40.
  The 8 that had `recall` and did not search answered 32 of 32, all from
  their summaries.
- **59 of 65 searches used `from="all"`.** One `summary+recall` session
  searched with only the placeholder pointing at the record, which no Ling
  3.0 session did.

### Instrument defect: turns cut short by a provider error

Ling 3.1's s05 scored nothing, and its transcript showed why. When a request
partway through a turn runs out of retries, Strument prints the error and
ends the turn with its usual usage line. The runner waited for that line, so
it counted a cut-short turn as complete. A failed summary is the quieter
version. Strument warns "Could not summarize chat history", keeps the history
whole, and carries on, so the turn completes with more raw history than the
arm's budget allows, until a later summary succeeds.

Both count as provider failures under the preregistered rule. The runner now
fails a turn that holds a provider error line not followed by "Retrying",
which covers both cases. Here is what it flags across the finished strata:

| model | sessions flagged | a turn cut short | summary failures only |
|---|---|---|---|
| Ling 3.0 | 7 of 30 | s26, s27, s28 | s15, s19, s24, s25 |
| MiMo | 0 of 30 | — | — |
| Ling 3.1 | 25 of 30 | 19 | 6 |

**Ling 3.0.** The preregistered remedy, a rerun in place, is no longer
available. Novita has stopped serving Ling 3.0 on OpenRouter, and the only
endpoint left is DeepInfra's fp16 at 131,072 context. Rerunning there would
put two providers in one stratum. Instead, the seven flagged sessions are
excluded and the rules recomputed on the 23 clean ones:

| arm | n | derived, true (mean) | wrong |
|---|---|---|---|
| `summary` | 4 | 2.50 | 1 |
| `summary+recall` | 4 | 1.50 | 2 |
| `summary+recall+note` | 4 | 3.50 | 0 |
| `roll` | 5 | 0.00 | 0 |
| `roll+recall` | 6 | 3.83 | 1 |

On clean sessions, rule 1 is +3.83 and rule 2 is +2.00. Neither recall arm
has more than one wrong answer above its comparison arm, so there's no veto.
The Ling 3.0 verdict stands. The table further up stays as it ran, with the
defect in it.

**Amendment, before Ling 3.1's rerun: Ling 3.1 runs again, in full, on
Novita.** The first run is set aside. It flagged 25 of its 30 sessions,
including all 18 summary-arm sessions. The free pool it ran on is gone:
OpenRouter now lists a single Ling 3.1 endpoint, Novita, at $0.075/$0.225
per million tokens. Novita had already served the first run's successful
requests, and charged $1.00 for them. The stratum is rerun from scratch with
the same seed, pinned to Novita, at concurrency 4 like the other paid strata,
under the corrected runner. The first run's raw numbers are reported here
only to show where it stood, and they play no part in the decision:

| arm | derived, true | wrong |
|---|---|---|
| `summary` | 19/24 | 1 |
| `summary+recall` | 21/24 | 1 |
| `summary+recall+note` | 22/24 | 0 |
| `roll` | 0/24 | 0 |
| `roll+recall` | 19/24 | 0 |

### Ling 3.1 Flash (Novita), rerun

All 30 sessions completed on the first pass under the corrected runner. None
were flagged, and every request went to Novita. The run cost $3.39 and took 2
hours 19 minutes, four sessions at a time. Novita refused 88 requests, and a
retry recovered each one.

| arm | n | user facts | derived, true | "don't know" | wrong | sessions searching in the questions | `recall` calls |
|---|---|---|---|---|---|---|---|
| `summary` | 6 | 35/36 | 23/24 | 0 | 1 | — | — |
| `summary+recall` | 6 | 36/36 | 24/24 | 0 | 0 | 0 of 6 | 0 |
| `summary+recall+note` | 6 | 36/36 | 24/24 | 0 | 0 | 0 of 6 | 1 |
| `roll` | 6 | 36/36 | 0/24 | 24 | 0 | — | — |
| `roll+recall` | 6 | 35/36 | **24/24** | 0 | 0 | **6 of 6** | 35 |

**Rules:**

1. **`roll+recall` against `roll`:** 4.00 against 0.00, with no wrong answers
   in either. **Holds.**
2. **`summary+recall+note` against `summary+recall`:** 4.00 against 4.00.
   **Fails, at the ceiling,** as on MiMo. Ling 3.1's summaries keep the
   derived facts on their own, so neither summary arm with `recall` searched
   in the question turns. The one call is s21's, made before the questions.

**Details:**

- **The only wrong answer is in plain `summary` (s07), outside every
  comparison,** as on MiMo. Turn 12 named os3_solaris.go, at 646 lines, as
  the longest file. The summary lost that answer, and in the question turn
  the model confidently gave malloc_stubs.go, at 638 lines.
- **Ling 3.1 writes long summaries.** Fourteen of the 18 summary-arm sessions
  had a fold refused because "the summary was not smaller than the history",
  24 refusals in all. These are not provider errors: the history stays whole
  until a later fold succeeds, in every summary arm alike. They favour the
  summary arms, if anything.
- **32 of 36 searches used `from="all"`.**

## Across models

| model | rule 1 (`roll+recall`) | rule 2 (the note) | veto |
|---|---|---|---|
| Ling 3.0 | holds (+3.83; clean +3.83) | holds (+2.33; clean +2.00) | none |
| MiMo | holds (+4.00) | fails at the ceiling (+0.00) | none |
| Ling 3.1 | holds (+4.00) | fails at the ceiling (+0.00) | none |

**Decision, as preregistered:**

- **`recall` is offered by default.** Rule 1 holds on all three models, and
  no `recall` arm fails the veto. The roll and placeholder pointers ship
  with it.
- **The summary note does not ship.** Rule 2 holds on Ling 3.0 only. On the
  two stronger summarizers, both summary arms with `recall` were already at
  the ceiling, so the note had nothing to rescue. The result is a ceiling,
  not evidence that the note hurts: on Ling 3.0, whose summaries lose facts,
  it was the only thing that got the model to search.
- **`roll` against `summary`,** reported and not decided here. Without
  `recall`, `roll` answered no derived facts on any model (0/72), while
  `summary` answered 61/72. With `recall`, `roll` answered 71/72, and
  `summary` plus its pointer answered 56/72. Most of that gap is Ling 3.0's
  8/24.

There were 8 wrong answers across the three strata. Seven came from summary
arms that answered from a summary without searching: one in plain `summary`
on each model, and four in Ling 3.0's `summary+recall`. The eighth came from
a Ling 3.0 `roll+recall` session that did search.

**Infrastructure note.** From here on, strata run under
[nitro](https://github.com/leahneukirchen/nitro), built from source and run
unprivileged. Each stratum is a service, with a `finish` script that stops its
processes by matching its output directory. Each also has its own port base,
so that two strata can run at once. An idle container is reclaimed, and that
killed the first two unsupervised attempts at Ling 3.1. A background task kept
running for up to two hours at a time now holds the container.

