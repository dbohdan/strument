# Preserved reasoning: does it reach the model, and does it help?

**Status: characterization, run 2026-11-25.** It follows the
[panel survey](../2026-11-reasoning-survey/), which found every open-source
harness sending reasoning back and Strument sending none. In short:
- **Phase 1:** the channel works through Command Code for four of five
  families, and without it models state reasoning they cannot see (below).
- **Phase 2 could not measure a benefit.** Two fixtures put MiMo, DeepSeek V4
  Flash and Kimi K3 at the ceiling in 5 to 14 requests: the models read
  everything in parallel and fix it in one go, so there is little between
  tool steps for replay to act on. FrontierHarness put Strument's median at 7
  steps too. The trial was run as a harm and cost check instead.
- **Nothing broke:** 24 of 24 sessions passed 9/9 hidden tests, in both arms.
  Uncached input barely moved, since the replayed reasoning sits in the cached
  prefix.
- **MiMo got slower and wordier with replay:** 276–326 s against 162–192 s,
  all four against all four, and 37k output tokens against 23k. DeepSeek and
  Kimi went the other way, with 28% and 14% less output. MiMo is Strument's
  default, so this counter-metric matters more than the others.

Phase 1 in short:
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

## Phase 2: replay in Strument, as a harm and cost check

### The treatment

A local branch (`data/replay.patch`, built as `850b25a` on `d878d69`, not
merged) keeps each reply's reasoning on its assistant message, along with the
field it streamed on. The chat-completions client sends it back in that field,
only to the model that wrote it, from every earlier step. That is Pi's and
DeepSeek Harness's rule. A fake server checked it end to end: the next
request carried the planted reasoning, in `reasoning` or `reasoning_content`
to match how it came in.

### Two fixtures that could not show a benefit

The design was an effect trial: MiMo V2.6 Flash, DeepSeek V4 Flash and Kimi K3
through Command Code, base against replay, 4 sessions a cell, shuffled, at
`reasoning = "low"`. Kimi K3.1 Pro was considered and not used: by the credit
balance before and after one identical request it cost about twice what K3 did
(about $7.9 against $3.7 per million tokens, with output weighted four to
one).

A one-session baseline pilot on each fixture:

| fixture | MiMo | DeepSeek V4 Flash | Kimi K3 |
| --- | --- | --- | --- |
| [small-model ladder](../2026-11-small-model-ladder/) Tetris | 69/69, 10 requests | 66/69, 8 | 69/69, 5 |
| bug hunt (below) | 9/9, 9 requests | 9/9, 9 | 9/9, 9 |

Replay can only help between tool steps, and these sessions have few: each
model read every file it needed in parallel and then wrote or fixed
everything at once. The bug hunt was built for this trial to need more
investigation. The models did not take more steps, and adding files would not
change how they work.
[FrontierHarness](../2026-09-frontier-harness/) put Strument's median at 7
steps on real tasks, so short turns are the normal case, not a property of
these fixtures. The effect trial was dropped. The same 24 sessions were run on
the bug hunt to answer what they can: does replay break anything, and what
does it cost?

**The bug hunt** (`data/bughunt.py`) is a stdlib Python shop library: money,
catalog, cart, discounts, tax, shipping and an invoice module, with `SPEC.md`.
It has six bugs, one per module: a cents-padding parse error, a merge that
overwrites quantities, a multi-buy formula, a coupon threshold checked before
discounts, a weight tier that excludes its boundary, and banker's rounding
where the spec says half up. The nine visible tests are mostly end-to-end
invoice totals, so the bugs surface as wrong numbers. The nine hidden tests
check each rule directly. `bughunt.py check` verifies the reference passes
both suites, the shipped project fails both (3/9), and each bug alone fails at
least one visible and one hidden test. The first version failed that check:
banker's rounding broke no visible test, because no basket hit exactly half a
cent, until one coffee (7% of 7.50 is 52.5 cents) was added. The prompt
(`data/bh-prompt.txt`) asks for the suite to pass without changing the
tests.

### Results

`data/bh-scores.tsv`, one row per session (the `-0` rows are the pilot).

| model | arm | hidden 9/9 | requests | prompt tokens, sum | uncached | output tokens, sum | seconds |
| --- | --- | --- | --- | --- | --- | --- | --- |
| MiMo | base | 4/4 | 10, 8, 9, 10 | 428k | 69k | 23.5k | 162–192 |
| MiMo | replay | 4/4 | 10, 9, 10, 8 | 495k | 82k | 37.2k | 276–326 |
| DeepSeek V4 Flash | base | 4/4 | 6, 7, 13, 7 | 628k | 97k | 44.0k | 33–55 |
| DeepSeek V4 Flash | replay | 4/4 | 12, 9, 7, 7 | 763k | 95k | 31.8k | 30–38 |
| Kimi K3 | base | 4/4 | 12, 14, 6, 8 | 365k | 32k | 8.7k | 38–56 |
| Kimi K3 | replay | 4/4 | 6, 11, 6, 8 | 280k | 26k | 7.5k | 30–60 |

No session changed a test. Every session ended with an answer. The 24
sessions cost $1.73 by the credit balance.

**Did replay fire?** The session record does not keep request bodies.
Strument's own token estimate does not count the new field, so under replay
the gap between the provider's prompt count and the estimate should grow by
the reasoning replayed so far (`data/fired.py`, `data/bh-fired.tsv`). The gap
grows in the base arm too, as the estimate drifts over a session, so the test
is the difference:
- **DeepSeek:** replay's gap grew about 5,200 tokens more than base's, with
  about 4,400 replayed. It fired.
- **Kimi:** about 500 more, with about 490 replayed. It fired.
- **MiMo:** about 2,700 more, with about 4,300 replayed. Most of it arrived.
  Whether Command Code or MiMo's template drops part of it, this cannot say.

### What it shows

- **No harm to correctness on this task:** 12 of 12 replay sessions solved it,
  as did 12 of 12 base sessions.
- **Cache absorbs most of the input cost.** Replayed reasoning is attached to
  messages that are already in the prefix, so it is cached from the next
  request on. Uncached input moved by +18% (MiMo), −2% (DeepSeek) and −19%
  (Kimi).
- **MiMo is the exception, and it is the default.** With replay it wrote
  about 58% more output and took about 55% longer. Every replay session was
  slower than every base session, which at 4 against 4 is an exact two-sided
  p of about 0.03. Its reasoning tokens rose from a median of 2.5k to 4.3k.
  Seeing its earlier reasoning, MiMo reasoned more, not less. DeepSeek did the
  opposite: its reasoning median fell from 7.6k to 4.5k.
- **It does not show a benefit,** because neither fixture had room for one.
  Where one would show is long turns of investigation and sessions across
  many turns with a person. Neither fits a one-turn, unattended trial.

### What it does not settle

- Four sessions a cell, one task. The MiMo slowdown is the only difference
  without overlap, and one task is one task.
- The *across turns* half of the mechanism, the one a person's session
  exercises, was never part of phase 2: every session was one turn.
- The reasoning MiMo is shown may be partial (above).
- A shipped version would also need the session record, `/resume`, folds that
  drop Anthropic signatures, and the Anthropic and Responses dialects. The
  patch covers chat completions only.

## Files

- `data/probe.py`, `data/show.py`, `data/mechanism.jsonl`: phase 1.
- `data/replay.patch`: the treatment, on `d878d69`.
- `data/bughunt.py`: the bug hunt's project, reference, hidden suite and
  self-check. `data/bh-prompt.txt`, `data/bh-grade.py`, `data/bh-run.sh`:
  one session and its grade. `data/run.sh` is the Tetris runner for the pilot.
- `data/configs/`: the three Command Code configs; the key comes from the
  environment.
- `data/order.txt`, `data/drive.py`: the shuffled order and the runner, three
  at a time.
- `data/score.py`, `data/bh-scores.tsv`, `data/tetris-pilot-scores.tsv`: the
  counts. `data/fired.py`, `data/bh-fired.tsv`: the check that replay fired.
- Raw sessions: `dbohdan/experimental-data`,
  `strument/2026-11-preserved-reasoning/runs.tar.xz` at `4c5a7f6`.
