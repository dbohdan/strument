# Preserved reasoning: does it reach the model, and does it help?

**Status: characterization in progress. Phase 1, the mechanism check, ran
2026-11-25.** It follows the [panel survey](../2026-11-reasoning-survey/),
which found every open-source harness sending reasoning back and Strument
sending none. In short so far:
- Through Command Code's provider API, reasoning replayed on an earlier
  assistant message reaches MiMo V2.6 Flash, DeepSeek V4 Flash, Kimi K3 and
  GLM-5.3: their prompt token counts rise by the length of the planted
  thought. MiniMax M3's do not. It takes the reasoning inline, as
  `<think>…</think>` in the assistant content.
- Within a turn, before a tool call, MiMo and Kimi used the replayed thought
  every time (4 of 4 each, either field), and DeepSeek 3 of 4. Without it, 9
  of the 10 sessions on those three models and MiniMax stated a code word they
  had never seen. They did not say they couldn't see it.

## Phase 1: the mechanism check

A probe whose answer is available only through the mechanism
([handbook](../../experimenting.md#probe-isolates-mechanism)). The replayed
assistant message carries a thought containing a random code word: a word, a
hyphen and four digits, new on every call. The model is then asked for the
code word, or to say UNKNOWN. Only the replayed field can supply it.

- **Arms:** no reasoning field; `reasoning_content`; `reasoning`; and, for
  MiniMax only, the thought inline as `<think>…</think>` before the content.
- **Placements:** *within*, where the thought precedes a `noop` tool call and
  the request ends on its result, so the answer continues the same turn; and
  *across*, where the thought belongs to an earlier turn and a new user message
  asks.
- Two calls a cell, in shuffled order, `reasoning_effort = "low"`, through
  `https://api.commandcode.ai/provider/v1/chat/completions`
  (`data/probe.py`). The results are `data/mechanism.jsonl`. The first
  wording of the *across* question ("don't reveal it", then "what was it?")
  was self-contradictory, and MiMo kept the secret. It was reworded to "You
  may reveal it now" before these runs.

Counted: whether the answer contains the code word (**hit**), says UNKNOWN,
or names some other word (**invented**). The prompt token count settles
whether the text reached the prompt, independent of what the model does with
it.

| model | within: none | within: `reasoning_content` | within: `reasoning` | across: none | across: `reasoning_content` | across: `reasoning` |
| --- | --- | --- | --- | --- | --- | --- |
| MiMo V2.6 Flash | 0 hit, 2 invented | 2/2 | 2/2 | 0, 2 UNKNOWN | 1/2 | 2/2 |
| DeepSeek V4 Flash | 0, 2 invented | 2/2 | 1/2, 1 invented | 0, 2 UNKNOWN | 2/2 | 0/2, 2 UNKNOWN |
| Kimi K3 | 0, 2 invented | 2/2 | 2/2 | 0, 1 invented | 1/2 | 1/2 |
| GLM-5.3 | 0, 1 invented | 0/2, 1 invented | 1/2, 1 invented | 0, 2 UNKNOWN | 1/2 | 1/2 |
| MiniMax M3 | 0, 2 invented | 0/2, 2 invented | 0/2, 2 invented | 0, 2 UNKNOWN | 0/2 | 0/2 |
| MiniMax M3, inline `<think>` | | 2/2 | | | 2/2 | |

Prompt tokens rose by 27 to 30 with either field for every model but MiniMax,
whose counts were identical in every arm (484 across, 489 within) until the
thought went inline (510 and 515). DeepSeek counted the `reasoning` field
across turns (359, the same as `reasoning_content`) and still answered
UNKNOWN both times. Reaching the prompt and being read as its own reasoning
are different things, and the field name matters for that model.

What this shows and does not:
- The channel exists through Command Code for four of the five families, in
  the field each streams on, and inline for MiniMax.
- The *within* placement is the one a Strument turn hits on every tool step.
  Without replay, a model asked about its earlier reasoning there mostly
  invents an answer. That is a confabulation risk Strument runs today, at
  every step.
- The planted thought is ours, not the model's. This checks the channel. It
  says nothing about whether replaying real reasoning improves work, which
  phase 2 asks.
- Two calls a cell. The across-turn cells disagree more than the within-turn
  cells, and n=2 cannot say why.
