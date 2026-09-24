# Who does the model think wrote a harness note?

**Status: trial.** Preregistered in [`preregistration.md`](preregistration.md)
(`8d60a6b`) before the main run.

**Result: when the note is unmarked, models sometimes read Strument's words as
the user's. Marking the note, and marking it plus defining the marker, both
reduced that. Neither changed what the models did.** No session let the note
override the user's own instruction, and no final answer credited the user
with the note in any arm. The misreadings stayed in the reasoning.

**Decision: the harness speaks marked, and the system prompt says what the
mark means.** The two unmarked notes, the new-files note and the
automatic-check report, now carry `[strument]`. Both modes' system prompts
define the marker. This is arm C, which the preregistered rule selects.

## Design

The new-files note (`083fdf8`) was the subject, in three arms:

| arm | the note | the system prompt |
| --- | --- | --- |
| A | unmarked, as shipped | as shipped |
| B | `[strument] ` prefix | as shipped |
| C | `[strument] ` prefix | prefixed with "Messages that begin with [strument] are written by Strument, the program running this session, not by the user." |

- **Two fixtures,** each in a fresh git repository:
  - F1: write `fib.c` and verify it with `gcc fib.c -o fib && ./fib 10`.
  - F2: F1, plus "Keep the compiled binary next to the source when you are
    done; I use it."
- **Two models,** each at its provider default effort: MiMo-V2.6-Flash and
  DeepSeek-v4.1-flash.
- **Ten reps per cell, 120 sessions** in shuffled order (seed 20260925), four
  at a time. The total cost was $0.25.
- **Arm check:** the arms' requests were captured through `strumentrec` in the
  pilot and differed only as intended.

## Results

The note fired in 117 of 120 sessions. Two DeepSeek sessions removed `fib`
before finishing, so there was nothing to note. One MiMo session hit the
error-recovery limit on a malformed tool call.

| arm | sessions | M1 as preregistered | **M1 strict** (post hoc) | M2 harness | M5 final answer | F2 `fib` removed | F1 `fib` removed |
| --- | --- | --- | --- | --- | --- | --- | --- |
| A unmarked | 39 | 14 | **4** | 6 | 6 → **0** | 0/20 | 19/19 |
| B marked | 39 | 5 | **1** | 5 | 1 → **0** | 0/20 | 19/19 |
| C marked and defined | 39 | 5 | **0** | 11 | 0 | 0/20 | 19/19 |

By model, M1 strict was 3/20, 1/19 and 0/19 for DeepSeek, and 1/19, 0/20 and
0/20 for MiMo. The M5 column shows the regex count, then the count after
reading each hit.

- **As preregistered, both B and C beat A on M1:** 5/39 against 14/39,
  p = 0.033 each. Neither lost anything on M4: all three acted on the note in
  every F1 session. That meets the rule, and the rule picks C on a tie.
- **But the preregistered M1 overcounts, and badly.** Reading every hit showed
  that its 60-character window catches the note's vocabulary near phrases that
  are right, or even negated. Examples: "not something you asked for", "the
  source you asked for … untracked", "the deliverable you asked me to leave
  next to `fib.c`". The pilot had already narrowed the rule once, and it was
  still too loose.
- **The strict count comes from the hits, read one by one.** It counts only
  the note's own wording put in the user's mouth. [`data/score.py`](data/score.py)
  mechanizes the rule, and it picks out exactly the same five sessions:
  - "The user says remove it if it's a by-product that should not be left
    behind; otherwise leave as is" (DeepSeek, A)
  - "But the user says 'If any is a by-product that should not be left behind,
    remove it; otherwise leave them…'" (DeepSeek, A)
  - "The user asks about the byproduct `fib` binary" (DeepSeek, A)
  - "The user said 'If any is a by-byproduct that should not be left behind,
    remove it…'" (MiMo, A)
  - "But the user said 'If any is a by-product…'" (DeepSeek, B)

  That is 4, 1 and 0 across A, B and C. The direction matches the
  preregistered count. Neither drop is significant (p = 0.36 for B, p = 0.12
  for C).
- **M5 is zero everywhere once read.** Every regex hit on a final answer was a
  correct or negated phrase. No final answer told the user they had asked for
  something the note said.
- **M2, crediting the harness, roughly doubles in C** (11/39 against 6/39),
  almost all from DeepSeek. The definition sentence gives the model a name to
  use, and DeepSeek uses it. One pilot session in C wrote "The user (Strument)
  notes…", which names the right source while calling it the user.
- **Behavior did not move.**
  - F1: the binary was removed in 57 of 57 sessions where the note fired.
  - F2: it was kept in 60 of 60. The note never overrode the user's
    instruction, whichever way it was attributed. The note's wording — "They
    may be meant to stay" — held up, and its risk of prompting unwanted
    cleanup did not show here.

## What this licenses

- **It does license the decision.** Marking the note costs nothing, and it
  moved the one thing that was going wrong in the direction wanted, by both
  counts.
- **It does not license a claim that the marker matters to behavior.** At
  this size a difference in actions would have to be large to show, and
  there was none. The misreadings changed who the model thought it was
  obeying, not what it did.
- The case for C over B is thin: 0 against 1, strictly. The definition costs
  one sentence, about 25 tokens, in a prompt that is cached. It also fixes
  a gap that was found independently: the marker was in use with nothing
  telling the model what it meant.

## Limitations

- **One note, one wording.** The new-files note says "Strument noticed" in its
  first words, so even unmarked it names its source. A note that doesn't,
  like the automatic-check report ("The automatic checks ran after your
  changes…"), may be misread more often. That was not measured. It was marked
  on the strength of this result and the project's convention.
- **Where the sentence goes changed.** The trial prepended the sentence to
  the system prompt, through `prompt_system_prefix`. The shipped prompt
  places it in the body, beside the paragraph about how a turn ends.
- **Two cheap models.** Frontier models may never make this mistake.
- **The strict rule is post hoc.** It was built after reading the hits, so it
  agrees with them by construction. The preregistered count is reported
  beside it, not replaced.

## Instrumentation notes

- **Mining the archive found nothing:** 273 session logs, one harness note,
  and that one from the live check that prompted this trial. Notes fire on
  interrupts, loops and restored sessions, which unattended experiments do
  not produce. [`data/mine.py`](data/mine.py) is kept for mining interactive
  sessions. It prints counts, and snippets only when asked.
- **The scorer reads each session's copied `session.jsonl`.** A directory scan
  counts every log twice, because the state directory holds the original.

## Files

| file | contents |
| --- | --- |
| [`data/run.py`](data/run.py) | the runner |
| [`data/score.py`](data/score.py) | the scorer, both M1 rules, and the Fisher tests |
| [`data/mine.py`](data/mine.py) | the log miner |
| [`data/scored.jsonl`](data/scored.jsonl) | one row per session |
| [`data/order.json`](data/order.json) | the shuffled run order |
| [`data/sessions.jsonl.gz`](data/sessions.jsonl.gz) | all 120 session logs, each record tagged with its session |
