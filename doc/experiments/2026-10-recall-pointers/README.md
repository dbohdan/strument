# Pointing at recall: six models review the wording

**2026-10-12. Review: models reading an artifact, not running against it.**

## Why

In `2026-10-recall`, sessions that called `recall` answered 15 of 16 derived
questions correctly, and sessions that did not, 11 of 56. Few called it. The
roll note told the model "The work those turns did is not kept: where it
matters, check the files or run the commands again". With `recall` offered,
that sentence is false, and it points at the recovery the trial's questions
forbid. One model reasoned "I would need to re-read the files" and stopped at
"I don't know". The cleared-result placeholder ("make it again if the output
is needed") points the same way.

A revised note and placeholder were drafted and put to the models the next
trials use.

## Method

One brief (`data/brief.md`) went to every reviewer: the situation, the
failure, the current and proposed texts, and eight questions. The first asks
what the model would do in that session, before any critique. The rest probe
specific risks: whether "do not read or search the files" forbids `recall`,
whether the note says file contents are in the record, which recovery comes
first, where the pointer goes, the placeholder, what would make the model
skip either text, and up to three edits.

| reviewer | how | cost |
|---|---|---|
| Qwen3.8 27B | OpenRouter (DekaLLM), reasoning low | $0.007 |
| MiMo-V2.6-Flash | OpenRouter (Novita), reasoning low | $0.003 |
| Ling 3.0 Flash VL | OpenRouter, pinned to Novita, reasoning low | $0.0002 |
| Ling 3.1 Flash | OpenRouter, free pool, reasoning low | $0 |
| Claude Sonnet | subagent of the session | — |
| Claude Haiku | subagent of the session | — |

The four OpenRouter answers are in `data/`. The subagents' answers are
summarized below. Haiku made one tool call outside its instructions (a
session listing), said so, and did not use it.

## What they said

**Unanimous (6 of 6):**

- **First move:** asked first, each would call `recall`. That is what
  reviewers say, not what models in the trial did. The gap between the two
  is the finding of `2026-10-recall`.
- **"Where it matters" is vague,** and a vague gate is read past. Qwen: "my
  default under uncertainty in a test-like question is to say 'I don't know'
  rather than spend a tool call."
- **The two recoveries need their own triggers:** the record for what was
  said, read or concluded; the files only for their current state.
- **The pointer belongs after the quoted messages.** Quotes read as content,
  not instruction, and can run to thousands of tokens. Sonnet and Haiku would
  keep a short pointer at the top too.
- **Say that file contents are in the record,** and that they need
  `from="all"`. Without it, a default search for tool output finds nothing,
  and MiMo noted that an empty search "confirms a false 'I don't know'".

**Most:**

- **The placeholder sits where information was lost,** so it is the strongest
  pointer (MiMo, Ling 3.1). It should lead with `recall`, not with
  re-running (Sonnet, Haiku, MiMo, Ling 3.1). Sonnet and Haiku split it into
  "then" and "now".
- **"Search the record" shares a verb with the question's "do not search the
  files"** (Qwen, Sonnet, Ling 3.0, Ling 3.1). MiMo and Ling 3.1 add that "run
  anything" can be read as forbidding any tool call.
- **"Kept whole" is a strong promise** (MiMo, Ling 3.0, Ling 3.1). A payload
  can be pruned from disk, and one empty search would discredit the note.

**Single points kept:** MiMo said a note with no "you" and no imperative
reads as "background scenery". Ling 3.0 suggested naming the model's own
replies as part of what the record holds.

**Not adopted:** Sonnet's and Ling 3.1's suggestion that the note or the tool
description say outright that searching the record "is not reading files or
running anything". It answers the trial's question wording rather than any
real session's. The question is changed instead (below).

## What changed

When `recall` is offered:

- **The roll label** keeps its first sentence and goes on: "What the user said
  in them is quoted below, word for word, oldest first; the rest of those
  turns is in the session record." (`prompts.RollRecallLabel`)
- **After the quotes** (`prompts.RollRecallTail`): "The rest of those turns,
  including your own replies and the output of every tool call, is kept in
  the session record, and the recall tool searches it. When a question
  concerns something said, read or worked out in those turns, search the
  record before deciding you don't know; file contents and command output
  need from="all". Re-read a file only when its current contents are what you
  need."
- **The placeholder** (`clearedToolResultRecall`): "[This result was cleared
  from the history to save space. To see what it returned, search the record
  with recall, using from="all"; to get the current output, make the call
  above again.]"
- **The summary note** (`prompts.SummaryRecallNote`, still behind
  `--recall-note`) uses the same wording, so the three pointers agree.

Without `recall`, every text is byte for byte what it was.

**The trials' questions** change from "do not read or search the files, or
run anything" to "do not read the files again or run commands". That removes
both of the readings reviewers flagged.
