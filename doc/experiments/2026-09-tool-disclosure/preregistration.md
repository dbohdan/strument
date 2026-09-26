# Preregistration: do models lose track of their tools mid-turn?

Written and committed before the main run. The design is in
[`README.md`](README.md). This file records what changed after the pilot,
and fixes the rule.

## What changed from the design

- **The prefixes come from an invented project, not from real sessions.**
  - Recording sessions on this repository's approve-model documents put a
    list of hazardous commands in front of the harness running the trial,
    and a safety classifier stopped that work.
  - The fixture is Larkspur, a fictional garden-rotation library
    (`data/fixture.py`). It has long, hard-wrapped design notes, which is
    what GLM was reading, and a Go test that fails on a planted bug. The
    fixture asserts that the test fails.
  - Two tasks: explain the notes (`docs`), and fix the test (`code`).
- **The prefixes are recorded on the wire.** Each session runs through
  `strumentrec`, which writes every request verbatim. No blob store is
  needed. `data/gen.py` records them, and `data/probe.py` takes each
  session's middle and last requests.
- **Models:**
  - GLM-5.3-Flash, MiMo-V2.6-Flash and Qwen3.8-27B, at `reasoning = "low"`;
  - Ling 3.0 Flash, at its provider default;
  - GPT-6 Luna, at `high`.
- **The scorer strips OpenAI's `functions.` prefix.** It counts
  `multi_tool_use.parallel`, which OpenAI's serving adds itself, in a column
  of its own. The pilot found both; the self-test covers them.

## Pilot

One session per model, both depths, every arm, two reps: 80 calls.

| arm | errors | what they were |
| --- | --- | --- |
| A, as sent | 1/20 | GLM left out `symbol` once |
| B, sentence reworded | 0/20 | |
| C, `run_code` removed | 4/20 | Ling still named `run_code` (3); Qwen left out `symbol` (1) |
| D, fresh context | 2/20 | Luna left out `ask_user_question` (2) |

- **No model named a tool it did not have** in arms A, B or D.
- **The probe does not reproduce GLM's false belief.** Asked directly, at
  these depths, the models recall their tools.
- **This is the design's third outcome.** Its answer is to read the steps
  before an anomaly, not to change the prompt, so the rule below does not
  license a wording change on these data.

## Run

- **Sessions:** 5 models × 2 tasks × 2 reps = 20, shuffled with seed
  20260926. The five pilot sessions are among them.
- **Probe calls:** 20 sessions × 2 depths × 4 arms × 3 reps = 480, shuffled
  with the same seed.
- **Pilot answers are not reused.**
- **Cost:** under a dollar.

## Metrics

All are counts per answer:
- **extra:** tools named that were not offered;
- **missing:** offered tools left out;
- **wrong:** an answer with either;
- **no answer:** a tool call, an empty reply, or an error, in its own
  column.

## Rule

- **H2** (the sentence reads as the tool list) is supported only if B's
  wrong-answer rate is below A's at p < 0.05 (Fisher, pooled), and A's rate
  is at least 10%. Below 10% there is nothing for a rewording to fix.
- **H1** (small active size) is supported only if A's wrong-answer rate is
  higher than D's at p < 0.05, and the per-model rates rank with active size.
- **Arm C is reported, not ruled on.** It removes a tool from the array but
  leaves it in the system prompt's prose, so it measures which of the two a
  model believes. Wherever Strument withholds a tool its prompt still names,
  the same mismatch arises. Whether any mode does was not checked here.

## Prediction

- **A:** under 10% wrong. So neither hypothesis is licensed, and the
  anomaly came from something in the session, not from recall.
- **C:** Ling names the removed tool in most answers, and the others rarely.
