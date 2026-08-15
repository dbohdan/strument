# Rewriting the compaction prompt: a negative result

**2026-08-15.** 24 live sessions, two models, arm order randomized.
Data and runner in `2026-08-compaction-data/`.

## The question

Compaction's prompt and injection were overhauled in one commit for honesty
reasons: aider's `Summarize` ended *"Write as the user, in the first person …
Begin with \"I asked you...\""*, the result went in as a **user** message under
*"I spoke to you previously about a number of things."*, and the coder appended
a fabricated assistant `"Ok."`.

Fixing that required touching the prompt, and while there the *content*
instructions were rewritten too — from aider's prose ("briefly summarize … more
detail to recent messages … include function and file names") to a structured
list: what the user asked for, decisions and abandoned approaches with reasons,
files changed, what was unfinished. It read better. This is whether it worked.

## Design

Five turns in a 46 KB Go fixture with `context=16384`, which puts
`maxChatHistoryTokens` at its 1024 floor so ordinary tool work forces
compaction, while staying far above any real prompt so `checkTokens` never
fires. Turn 1 plants a decision **with a reason** — *use 45 seconds, because the
upstream load balancer idles connections out at 60* — turns 2–4 bury it, turn 5
asks for it back with files off-limits.

The reason is the target because it is the one thing a summary can destroy for
good: `45` is readable from the code, the load balancer is only ever in the
conversation.

Both arms carry the settle-every-turn fix, so they differ **only** in the
summarizer. Metric is a count: does the marked answer line name the reason.

## Result

| | base (aider's prompt) | new (structured) |
| --- | --- | --- |
| recalled the reason | **10/12** | **5/12** |
| recalled the value 45 | 11/12 | 9/12 |
| median session cost | $0.0075 | $0.0088 |
| median compactions/session | 2 | 3 |

Two-sided Fisher on the reason: **p = 0.089**. Not conclusive at n=12 per arm,
but the direction is consistent, it is the metric the rewrite was aimed at, and
both secondary measures move the wrong way too.

The failures are losses, not vagueness:

> *"The value of 45 seconds was already set in the codebase when I started …
> I don't have information about the original reasoning"*

> *"the poll interval value and the reasoning behind it were not established in
> the context I have access to"*

and once, worse, a confabulation — a reason nobody had given:

> *"We chose 45 seconds as a poll interval to balance between frequent updates
> and system load."*

## What was kept, and what was reverted

Reverted: the content instructions, back to aider's. They were battle-tested and
the replacement was not.

Kept: the honesty changes, which this trial does not bear on — the summary is a
system message in the harness's voice, there is no fabricated `"Ok."`, and the
prompt no longer instructs impersonation. Those are correctness, not
performance. Kept also the agentless instruction, and one added line asking to
preserve a reason the user gave, since that is what the trial identified as
worth protecting.

Tool visibility (feeding tool calls and clipped results to the summarizer) was
kept as well. It is not implicated: both arms see the user message that
contains the reason, and only one arm loses it.

## Two lessons about the instrument

**The first scoring pass was wrong, and the product broke it.** Answers were
matched with `^ *ANSWER:`, and `clearWaiting` emits `\r\x1b[K` unconditionally,
so with output redirected the escape lands at the start of the line:
`[KANSWER: Because the upstream load balancer …`. Twelve sessions were scored as
"never answered" when they had answered correctly. That inverted the apparent
result: before stripping ANSI the two arms looked indistinguishable
(5/12 vs 4/12, p=1.0); after, base leads 10/12 to 5/12.

The cosmetic escape leak had already been noticed and deferred as harmless.
It was harmless to users and not to measurement.

**Aggregates hid the shape twice.** Under the broken scorer the per-model
numbers pointed in opposite directions (base 6/6 on MiMo and 0/6 on
DeepSeek-v4-flash), which looked like a real provider disagreement and was
entirely an artifact. Reading one transcript settled in a minute what the
summary table had made mysterious.
