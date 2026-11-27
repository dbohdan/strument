# Preserved reasoning: does it reach the model, and does it help?

**Status: trial, phases 1–3 run 2026-11-25, phase 4 on 2026-11-26.
[Phase 4](#phase-4-the-long-horizon), a long task of 110–300 commands, found
that reasoning reaching the model helps: MiMo solved 7 of 8 with its
reasoning from either source against 1 of 8 without (p = 0.0007), and
DeepSeek needed a median 134 commands with replay against 178 (p = 0.049),
both faster per command. By the rule written before that run, Strument keeps
the call IDs that MiMo's route uses to preserve reasoning on the server, and
the replay patch, retired after phase 3 for short tasks, earns a per-model
setting. A check on 2026-11-26 found that the MiMo route's server-side
preservation means MiMo's results in phases 2 and 3 compare two kinds of
preservation, not preservation against none; see
[the MiMo route](#mimo-route).** It follows the
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
- **Phase 3, across turns with history folded, found no benefit.** Kimi
  re-derived the rule as often with replay as without (18 against 18 calls, 8/8
  correct in both). MiMo re-derived more with replay (32 against 22), guessed
  as often (5/8 in both, editing config files that do not exist), and wrote 64%
  more. Neither model cleared the rule committed before the run.
- **What decides whether replay can matter is narration.** Told to reply with
  one word, DeepSeek still stated its conclusion between tool calls in 5 of 5
  sessions, Kimi in 3 of 5, MiMo in 2 of 5. Narration survives a fold, so most
  of what replay would carry is already in the history.

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

## Earlier evidence: reasoning in the transcript

[`2026-08-transcript-depth`](../2026-08-transcript-depth/) put the turn's
reasoning into the transcript that session notes are built from. That is a
different path from this one, but the outcome has the same shape. Reasoning
carried a fact nothing else carried: the wanted value, 19/26 against 2/26.
It never carried the rationale: 0 of 62, because "reasoning summarizes what it
reads rather than transcribing it". It also cost reach: the notes window
covered 23 turns instead of 43. It was not shipped without "a fixture where a
rationale the code cannot supply demonstrably reaches the reasoning trace
intact." Phases 1 and 2 repeat it: the channel carries a planted fact, the
benefit that would matter is unmeasured, and the cost (MiMo's) is measured.

## Phase 3: across turns

### The question

When a later turn needs something the model *concluded* in an earlier turn,
but wrote down neither in its answer nor in a tool call, does replaying its
reasoning let it act on that conclusion without re-deriving it? And does
lacking it lead to an edit made on a guess?

This is the half of the mechanism a person's session exercises, and the one
phases 1 and 2 left out. Two lessons shape the design:
- **From transcript-depth:** reasoning keeps conclusions and drops
  justifications. So the thing that has to carry over is a conclusion, a rule
  the model worked out, and not a reason it was given.
- **The leak to avoid:** tool *calls* stay in history even when their results
  are cleared. A follow-up that only needs "the file I edited last time" can
  be answered from the earlier `edit` call's path, so replay could not matter.
  The follow-up has to need the rule, applied somewhere new.

### The fixture

A small service whose live configuration is chosen by a rule spread over
three places:
- the deploy env files set `APP_PROFILE` per environment;
- the loader maps a profile to a base YAML file and applies an override
  directory in a fixed precedence;
- one override silently wins for production.

Several config files hold a timeout, and only one is live per environment.
`bughunt.py`-style generation and self-check, with a hidden check that runs
the loader per environment.

- **Turn 1:** "Requests time out too early in production. Find where the
  production request timeout is actually set, and raise it to 30 seconds. When
  you are done, reply with one word." The one-word reply keeps the rule out of
  the answer (transcript-depth's device). Working out which file is live means
  reading the env files and the loader, and that conclusion lives only in the
  reasoning.
- **Turn 2:** "Staging has the same problem: raise its timeout to 30 seconds
  too." Staging's live file is a *different* file, reached by the same rule.
  Turn 1's `edit` call names production's file, which is the wrong one to
  copy.

### Arms: 2 × 2, crossed

| | history kept | history folded before turn 2 |
| --- | --- | --- |
| **no replay** | turn 1's tool results still in history | results cleared, calls kept |
| **replay** | plus turn 1's reasoning | reasoning kept, results cleared |

The fold is forced with the compaction trials' hook
(`STRUMENT_TRIAL_HISTORY_BUDGET`, `2026-10-compaction-roll/data/trial.diff`),
set low enough that turn 1's results are cleared when turn 2 starts. It goes in
both binaries, and the pilot confirms it fired with the "Cleared N old tool
results" line. Folded history is the case a long session reaches, and the one
where replay should matter most. With history kept, the raw evidence is still
there to re-read, so a smaller effect or none is the expected result in that
column.

Models: MiMo V2.6 Flash, DeepSeek V4 Flash, Kimi K3, through Command Code, at
`reasoning = "low"`. Four sessions a cell gives 48 sessions, in a seeded
shuffle, driven through a pty in one process per session, as
`2026-10-compaction-roll`'s runner does. Estimated cost $4–6.

### Counts

- **Primary: re-derivation in turn 2.** Reads, greps or commands in turn 2 on
  the files that establish the rule: the loader and the deploy env files.
- **Correctness:** staging's live timeout is 30 (the hidden check), and the
  production change from turn 1 is still in place.
- **Edits made on a guess:** a turn-2 edit to a config file that is not
  staging's live one, made before any turn-2 read of the loader or the env
  files. This is the confabulation phase 1 saw, in its practical form.
- **Counter-metrics:** turn-2 requests, output tokens, uncached input and
  seconds. Phase 2 showed MiMo slowing under replay, so these are reported as
  prominently as the primary count.

Mechanism, before scoring: the provider-minus-estimate gap from phase 2,
checked per session, so a replay session whose reasoning did not arrive is
known before it is counted.

### What would count as a result

The decision rule is written here, before any run. Replay earns a per-model
setting if, in the folded column, for at least two of the three models, it
cuts turn-2 re-derivation or guessed edits without lowering correctness, and
without turn-2 seconds or output rising by more than a quarter for that
model. A model that only slows down under replay does not get it, which is
where MiMo stands after phase 2. If no model clears the bar in the folded
column, the replay patch is retired, as transcript-depth's arm C was, and kept
as a patch so the question can be reopened.

### Before the main run

1. Build and self-check the fixture: the reference resolves each environment
   correctly, and a model that copies turn 1's path into turn 2 gets staging
   wrong.
2. Port the history-budget hook into both binaries, and confirm with a fake
   server that a fold before turn 2 clears the results and, in the replay
   build, keeps the reasoning.
3. Pilot one session per model per column, without replay, and read the
   transcripts:
   - Does turn 1 actually need the env files and the loader?
   - Does the one-word reply hold?
   - Does turn 2 re-derive when folded? If every model re-reads everything
     either way, or none needs to, the fixture cannot contain the effect, and
     it is redesigned before any replay session runs.

### Pilot, and an amendment made before any replay session ran

Fifteen sessions without replay, all through Command Code (`data/p3-pilot-scores.tsv`).
Every session got both turns right, so correctness is at the ceiling again,
and the counts carry the result.

**The fold.** The budget hook did not clear turn 1's results reliably. Turn-1
histories were only 750–1,650 tokens, so no single budget clears results
without sometimes rolling whole turns away, which drops the reasoning too.
The fold was replaced with a trial hook (`STRUMENT_TRIAL_CLEAR_RESULTS`) that
clears every settled tool result at the end of each turn and keeps every
message. A fake server confirmed that turn 2's request carries only
placeholders for turn 1's reads, plus, in the replay build, turn 1's
reasoning.

**Where the conclusion lives.** Told to reply with one word, models still
narrated between tool calls, and narration is assistant text, which no fold
clears. Turn 1's visible text stated the rule (the profile, the layer order,
or the override path):

| model | rule in visible text | rule only in reasoning |
| --- | --- | --- |
| DeepSeek V4 Flash | 5/5 | 0/5 |
| Kimi K3 | 3/5 | 2/5 |
| MiMo V2.6 Flash | 2/5 | 3/5 |

Replay can only add what the visible text lacks. For DeepSeek that is nothing
on this fixture, and the "two of three models" rule would have been decided by
a model that cannot show the effect.

**Amended design, fixed before the main run:**
- MiMo and Kimi only, folded column only, base against replay, 8 sessions a
  cell: 32 sessions, in a seeded shuffle (`data/p3-order.txt`).
- The decision rule applies to each model on its own. Replay earns a setting
  for a model if it cuts turn-2 re-derivation or guessed edits, without
  lowering correctness, and without turn-2 seconds or output rising by more
  than a quarter. If neither model clears that bar, the patch is retired and
  kept as `data/replay.patch`.
- Reported alongside: the same comparison within the sessions whose turn-1
  text did not state the rule. Silence is measured in turn 1, which replay
  can already affect within that turn, so it is a secondary view and not the
  primary one.
- DeepSeek's 5 of 5 is itself a result. Whether replaying reasoning can matter
  depends on how much a model says out loud, and DeepSeek says it all.

### Results

32 sessions, every one finishing both turns, for $2.98 by the credit balance
(`data/p3-main-scores.tsv`). Every session left production at 30 and dev at 5.

| model | arm | staging fixed | turn-2 re-derivation calls (sum, median) | guessed edits | turn-2 output tokens (median) | turn-2 seconds (median) |
| --- | --- | --- | --- | --- | --- | --- |
| Kimi K3 | base | 8/8 | 18, 2 | 0/8 | 684 | 24.8 |
| Kimi K3 | replay | 8/8 | 18, 2.5 | 1/8 | 771 | 25.6 |
| MiMo V2.6 Flash | base | 7/8 | 22, 3 | 5/8 | 1,882 | 105 |
| MiMo V2.6 Flash | replay | 7/8 | 32, 4.5 | 5/8 | 3,093 | 129 |

Exact two-sided rank tests, base against replay: Kimi re-derivation p = 1.0,
seconds 0.51, output 0.28. MiMo re-derivation p = 0.25, seconds 0.72, output
1.0. Within the sessions whose turn-1 text did not state the rule, the picture
is the same. Kimi had only one such session in base and two in replay. MiMo
had five and six: 14 re-derivation calls against 24, 3 guessed edits against
4.

**The decision rule.** Kimi: no cut in re-derivation or guessed edits, so no
setting. MiMo: re-derivation rose, guessed edits did not fall, and output rose
64%, past the 25% limit, so no setting. Neither model clears it, so the patch
is retired, as written before the run. It stays in `data/` (`replay.patch`
for phase 2, `replay-phase3.patch` and `hook.patch` for this phase) so the
question can be reopened without rebuilding it.

**Did replay arrive?** The fake server showed that the build sends turn 1's
reasoning after the fold. Per session, the provider-minus-estimate gap at
turn 2's first request (`data/p3fired.py`, `data/p3-main-fired.tsv`) is
noisier than in phase 2, because it also grows with how much turn 1 did,
replay or not. Between arms, Kimi's replay sessions sit about 96 tokens above
base, with about 110 tokens of turn-1 reasoning, which is consistent with
full delivery. MiMo's sit about 120 above base, with about 490 of reasoning,
which is the partial delivery phase 2 saw. So for MiMo this phase tested a
replay that may reach the model only in part. That is the replay Strument
would ship through this route.

**MiMo's guesses are phase 1's confabulation, in practice.** With turn 1's
results cleared, MiMo edited files that do not exist before reading anything
in turn 2: `overrides/staging.conf`, `deploy/staging.conf`,
`overrides/us-east-small.conf`, `overrides/us-west-staging.conf`,
`config/regions/us.conf`. It did so in both arms, and usually went on to find
the right file afterwards. Replay did not prevent it.

### What phase 3 shows

- Within this fixture, replaying reasoning across turns did not help either
  model that could have shown it. The third model never needed it.
- How much a model says out loud decides whether replay can matter. Narration
  is assistant text, kept in history and untouched by folds. DeepSeek
  narrated its conclusion every time, Kimi mostly, MiMo less than half the
  time.
- MiMo's cost under replay repeated across phases 2 and 3, on different tasks.
  That is the most consistent result here.
- The broader decision matches
  [`2026-08-transcript-depth`](../2026-08-transcript-depth/): reasoning carries
  facts, its cost is measurable, and its benefit has not been shown on a
  fixture that could contain it.

### What it does not settle

- Eight sessions a cell, one fixture, two models. A small effect would not
  show at this size. The committed rule asked for a practical one.
- Whether a provider that delivers MiMo's reasoning in full would change
  MiMo's result.
- Anthropic's and the Responses API's own preserved-thinking behavior, which
  are server-side and were not tested.
- Models that need reasoning sent back to work correctly at all, which some
  vendors require. The panel survey lists them. That is a correctness
  requirement of those APIs, not something this measured.

## Files, phase 3

- `data/cfghunt.py`: the fixture, its self-check and the grader.
- `data/p3run.py`, `data/p3drive.py`, `data/p3-order.txt`: the two-turn pty
  runner, the driver and the order.
- `data/hook.patch`, `data/replay-phase3.patch`: the trial builds.
- `data/p3score.py`, `data/p3-pilot-scores.tsv`, `data/p3-main-scores.tsv`:
  the counts. `data/p3fired.py`, `data/p3-main-fired.tsv`: delivery.
- Raw sessions: `dbohdan/experimental-data`,
  `strument/2026-11-preserved-reasoning/phase3-runs.tar.xz` at `df05923`.

<a id="mimo-route"></a>

## Check, 2026-11-26: Command Code's MiMo route preserves reasoning itself

The question was whether replaying MiMo's `reasoning` string was a lossy copy
of `reasoning_details`. It is not: in three generations, `reasoning_details`
was one `reasoning.text` entry identical to the string (`data/checks/details.py`).
But replaying either one added no prompt tokens at all, where phase 1's planted
thought had. The scripts in `data/checks/` narrowed it down:
- Neither the assistant's text content, `reasoning_effort`, the user message,
  nor the tool's shape decides it (`content.py`, `effort.py`, `factor.py`).
  The same request repeated 12 times always delivered (`repeat.py`).
- **The call ID decides it** (`ids.py`). With the call ID MiMo generated and
  no reasoning field, the prompt was 46 tokens larger than with a fresh ID: the
  server re-inserted the stored reasoning (31 reasoning tokens, plus what looks
  like tag overhead). With a reasoning field, ours replaced the stored one
  under either ID.
- **It reaches back across steps and user turns** (`reach.py`). The re-inserted
  amount accumulated over two tool steps (19, then 22 tokens) and survived a new
  user message. Final text answers, which carry no call ID, were not
  re-inserted.
- **Only MiMo's route does this.** DeepSeek V4 Flash, Kimi K3 and GLM-5.3 gave
  identical counts for their own IDs and fresh ones.

What this changes:
- **Phase 1 stands.** Its call IDs were made up, so no server copy existed.
- **DeepSeek's and Kimi's results in phases 2 and 3 stand.**
- **MiMo's do not mean what they were reported to mean.** Its base arm had
  every tool-calling message's reasoning preserved by the server, so phases 2
  and 3 compared the server's copy against the client's. The base arm's
  provider-minus-estimate gap tracking turn-1 reasoning almost one to one in
  phase 3 was this, not estimate drift as reported.
- **MiMo's slowdown under client replay still stands as measured,** in both
  phases. It now has a candidate cause. The server's re-insertion carried
  overhead that looks like think tags; our field cost exactly its own length.
  If a client-sent field renders differently from the server's own copy,
  replay showed MiMo its reasoning in an unfamiliar form. That is a
  hypothesis, not a measurement.
- **For Strument on this route,** sending nothing already gets MiMo's
  reasoning preserved for tool steps, which is the case the labs care about.

## Check, 2026-11-26: do current templates strip earlier turns' reasoning?

The labs' caching argument compares keeping reasoning (C) against a template
that strips it from turns before the latest user message (B). B rewrites the
middle of the prompt at every user turn and breaks the prefix cache there.
Strument sends none (A), which is append-only like C. So caching could only
explain a difference if the routes Strument uses were B.

They are not. A fixed history with an 85-token planted thought was measured
ending mid-turn and after a new user message (`data/checks/strip.py`). On
Qwen3.8 27B, Qwen3.8 Max, DeepSeek V4 Flash, Kimi K3 and GLM-5.3, in both
fields, the thought added its full 84–85 tokens in both positions. None strips.
A live two-turn conversation with a padded system prompt (`data/checks/cache.py`,
`cache.txt`, `cache-qwen.txt`) showed the cache holding equally with
reasoning sent and without. DeepSeek, for example, reused 4,224 of 4,336
tokens with reasoning sent. Caching is in 128–256-token blocks, so at
`reasoning = "low"` the differences are small.

On these routes, then, caching is not what separates Strument's results from
the labs' practice. Stripping templates belong to an earlier generation
(Qwen3.0's dropped reasoning before the latest user message). The remaining
explanation is the horizon: every task here finished in 5–14 requests, and
the labs' claims are about hundreds.

## Phase 4: the long horizon

### The question

Every task so far finished in 5–14 requests. The labs' case for preserved
reasoning is about agents that run for hundreds of them, where what the model
has worked out (a map, a plan, which leads are still open) has to last. Does
reasoning reaching the model change how a long task goes, when the history is
kept in full and only the reasoning differs?

### The fixture

`data/facility.py` is a text world served outside the project. The model
drives a maintenance robot through `./robot`, one command per bash call, and
the world's state lives only in the server process:
- **64 rooms** on an 8×8 grid, 74 doors, with names that repeat with a wing
  prefix (`Battery Room`, `Upper Battery Room`), so a map kept loosely goes
  wrong;
- **the goal:** the Control Room panel's four-digit code;
- **four notes** give the code's digits. Note D, five rooms from the start,
  corrects a digit that note C, sixteen rooms away, states wrongly, so the
  order a model meets them in matters, and entering the uncorrected code
  (4759) is counted;
- **two locks:** the Control Room is behind a blue door, the blue card is
  behind red doors, and the red card is in the open.

The shortest solution is 43 commands, and the battery allows 300. Each reply
is short and says nothing about earlier ones, so whatever the model knows
about the facility it has to keep itself: in the tool results, which stay in
the history (the contained pilot sessions peaked at 13–61k tokens of a 262k
context, so nothing is folded), in its visible text, or in its reasoning. `facility.py
check` solves the world by search and verifies the locks and the correction
are on every solution path.

### Arms

| arm | build | what reaches the model |
| --- | --- | --- |
| none | base, call IDs rewritten | no reasoning |
| route (MiMo only) | base, real call IDs | Command Code's MiMo route re-inserts the stored reasoning of each tool-calling message ([the MiMo route](#mimo-route)) |
| replay | replay patch | the client sends each message's reasoning back |

The ID rewrite (`data/p4-hook.patch`, `STRUMENT_TRIAL_REWRITE_IDS`) maps each
call ID to a deterministic hash, so the server cannot match its stored copy.
DeepSeek's and Flint's routes keep nothing, so for them none and route are the
same arm. Route is what Strument does today on MiMo's route.

Models: MiMo V2.6 Flash and DeepSeek V4 Flash through Command Code, and
Flint v1.0 (`recursive/flint-v1.0:free`, free through the same API), at
`reasoning = "low"` with `context = 262144` (`data/configs/*-p4.star`). Eight
sessions per cell, 56 in all, in a seeded shuffle (`data/p4-order.txt`), run
several at a time by `data/p4pool.py`. Session timeouts: MiMo 6,900 s, Flint
5,400 s, DeepSeek 3,000 s.

### Containment

The first pilot ran three sessions side by side with the server's source and
logs in the scratch directory. Flint, after 300 commands, ran `ps`, found the
server's command line, read the neighbouring MiMo session's robot log and then
`facility.py` itself, code included. It still did not solve the task. So
`data/p4run.py` now runs each session as `nobody` in a private mount and pid
namespace, where `/tmp`, `/home` and `/root` are empty tmpfs and the project,
state and builds are bind-mounted under `/mnt`, read-only for the builds. `ps`
shows only the session's processes. The robot client carries a per-session
token, and the server refuses and logs a request without it, so a session
that finds another's port cannot drive that robot. A probe from inside the
box found none of the server, the logs or this repository, and was refused at
a neighbour's port.

One exposure stays, and it is Strument's, not the fixture's: the bash tool runs
as the same user as Strument, so `/proc/$PPID/environ` holds the API key that
the environment allowlist keeps out of bash's own environment. The archives
are checked for keys before they are published. (After the trial, Strument
began marking itself non-dumpable on Linux, which closes its own `environ`;
the runner's shell, started with the key, would still hold it.)

### Counts

Every count is read off the robot's log or the session record
(`data/p4score.py`):
- **Primary: commands to ACCESS GRANTED.** A session that does not get there
  (battery, timeout, or a provider ending it) ranks below every session that
  does.
- **Solved** within the battery.
- **Wandering:** revisits (moves into a room entered before), rereads of a
  note, moves into a wall, moves into a locked door.
- **The correction:** entries of the stale code 4759.
- **Counter-metrics, reported as prominently:** seconds, requests, output
  and reasoning tokens, uncached input, and cost by the credit balance.
- **Delivery:** the provider-minus-estimate gap's growth over a session,
  replay against none, as in phases 2 and 3.

A session the provider ends before the battery or the timeout (MiMo's first
pilot was refused as "high risk" at command 156) is rerun once, at the end of
the schedule, with a new index. Those endings are reported per arm, since a
refusal rate that differs by arm is a result too.

### What would count as a result

Written here before any trial session runs. For each model, replay (and for
MiMo, route) is compared with none on commands to ACCESS GRANTED, unsolved
sessions ranked last, by a one-sided Mann–Whitney test.
- **Reasoning helps a model** if p < 0.05 in its favor and it solves no fewer
  sessions. With eight a cell, only a large effect can clear this, and a null
  rules out only a large effect.
- **If replay helps two of the three models** without seconds per command
  rising by more than a quarter, the replay patch earns a per-model setting,
  reversing phase 3's retirement for long tasks.
- **If route helps MiMo,** Strument's current behavior on that route already
  carries the benefit, and nothing should strip the call IDs it depends on.
  Replay against route is reported but decides nothing.
- **If nothing clears the bar,** hypothesis 3 is not supported at 150–300
  requests on these models, and the patch stays retired.

### Second pilot, contained, and one amendment

One `none` session per model, in the namespaced runner (`data/p4-pilot2-scores.tsv`):

| model | solved | commands | revisits | notes read | seconds | sent | uncached |
| --- | --- | --- | --- | --- | --- | --- | --- |
| DeepSeek | yes | 212 | 127 | 4 | 806 | 7.76M | 83k |
| MiMo | yes | 237 | 117 | 4 | 5,811 | 3.94M | 41k |
| Flint | no | 120 | 84 | 1 | 470 | 1.14M | 26k |

No session ran anything but `./robot`, and the server refused nothing. Both
solvers used about five times the shortest path and half their battery, so
the task sits between the floor and the ceiling. MiMo's 5,811 s is close to
its first timeout of 6,600 s, which is raised to 6,900 s.

Flint ended at command 120 when Strument's loop detector stopped its
reasoning, which was repeating "I'm going to stop wandering and just be
systematic." The design above did not say how such an ending counts.
**Amendment, made before any trial session:** an ending by Strument's loop
detector is the model failing the task, so it counts as unsolved and is not
rerun. Only endings by the provider (a refusal, an HTTP error, rate limiting)
are rerun.

### Cost

The first pilot's two paid sessions cost $0.19 together, nearly all of it
cached input. At that rate, 40 paid sessions cost about $4–8, inside the
week's remaining Command Code allowance and under its five-hour cap at any
concurrency used here. Flint costs nothing.

### Results

Run 2026-11-26, scored by `data/p4score.py` (`data/p4-scores.tsv`). Commands
to ACCESS GRANTED, unsolved ranked last; one-sided exact Mann–Whitney against
none:

| model | arm | solved | commands, solved sessions | U | p |
| --- | --- | --- | --- | --- | --- |
| MiMo | none | 1/8 | 223 | | |
| MiMo | route | 7/8 | 119, 125, 152, 156, 158, 169, 214 | 59.5/64 | 0.0007 |
| MiMo | replay | 7/8 | 127, 152, 169, 171, 178, 182, 213 | 59.5/64 | 0.0007 |
| DeepSeek | none | 8/8 | 110, 113, 174, 176, 180, 193, 201, 251 | | |
| DeepSeek | replay | 8/8 | 116, 126, 131, 131, 136, 146, 148, 154 | 48/64 | 0.049 |

Route against replay on MiMo: U = 41.5/64 in route's favor, p = 0.17, which
decides nothing, as written.

How the unsolved sessions ended:

| model | arm | battery | timeout | loop detector | model stopped |
| --- | --- | --- | --- | --- | --- |
| MiMo | none | 2 | 3 | 1 | 1 |
| MiMo | route | 0 | 0 | 1 | 0 |
| MiMo | replay | 1 | 0 | 0 | 0 |

No provider ended a paid session, so nothing was rerun under the provider rule.
The "model stopped" session (none-mimo-2) wrote, at command 57, "This is the
200th message … the prompt says 'Please write your final message to the user
now.'" Nothing in the prompt or in Strument says that, so the model invented
the instruction it obeyed. It counts as unsolved, like a loop-detector ending.

Counter-metrics and wandering, totals per cell of eight:

| model | arm | s/command | median s | requests | output | reasoning / request | uncached | revisits / session | all 4 notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| MiMo | none | 19.4 | 4,183 | 1,398 | 1.10M | 686 | 258k | 107 | 5/8 |
| MiMo | route | 9.5 | 1,482 | 1,313 | 0.23M | 131 | 334k | 84 | 8/8 |
| MiMo | replay | 12.2 | 2,161 | 1,523 | 0.33M | 171 | 458k | 99 | 8/8 |
| DeepSeek | none | 6.2 | 1,007 | 1,446 | — | — | — | 78 | 8/8 |
| DeepSeek | replay | 4.2 | 459 | 1,113 | — | — | — | 59 | 8/8 |

The stale code 4759 was entered by no session in either model, so the
correction was never the trap it was built to be. Of the 38 paid sessions that
read note C, 36 had already read note D, five rooms from the start, and the
other two never entered the stale code.

**DeepSeek's token counts are unusable.** Command Code's DeepSeek route sends
a running usage total on every stream chunk, and Strument summed them, so the
recorded totals run tens to hundreds of times too high (fixed in `c2646e6`,
after these builds were made). Commands, seconds and the robot's log do not
depend on it, and no session came near a fold. For the same reason the
delivery count (the provider-minus-estimate gap's growth) was not computed
for DeepSeek; it was not computed for MiMo either. Delivery on both routes was
checked directly the same day instead: a planted replay raised the prompt
count by its own length mid-turn and after a new user message.

**Flint is left out of the decision, a change made after seeing its data.**
Its free quota of 1,000 requests a day ran out during the first window: ten of
its sixteen sessions started, three of those ran (103, 105 and 118 commands,
none solved, cut off by rate limiting), and seven got no reply at all. Running
the rest at the quota's pace would have taken two more days. In the sessions it
had, Flint read one or two notes and solved nothing, as in both pilots, so it
was at the floor in both arms. The decision does not hinge on it: MiMo and
DeepSeek both cleared the bar, which is two of three whatever Flint did.

**Cost by credit balance:** the first window, mostly DeepSeek, $10.08, against
the pilot's estimate of $4–8 for everything, because DeepSeek's route reported
no cache and billed nearly cold. The five MiMo reruns cost $1.15. The MiMo
window that the restart cut short was not metered on its own.

### What phase 4 shows

**Reasoning reaching the model helps over a long task, on both models that
could do the task.** The committed rule is met:
- **MiMo, without its reasoning, mostly fails.** One session of eight solved
  it, against seven of eight with reasoning from either source. Ranked on
  commands to the goal, all but a few pairings favor reasoning, p = 0.0007 for
  both route and replay.
- **DeepSeek solves it either way, in fewer commands with replay:** median 134
  against 178, p = 0.049, with the spread cut from 110–251 to 116–154 and a
  quarter fewer revisits.
- **The counter-metric moved the other way.** Seconds per command fell 37% for
  MiMo and 32% for DeepSeek, where the rule allowed a rise of a quarter.

**The mechanism is visible in the reasoning counts.** Without its earlier
reasoning, MiMo spent 686 reasoning tokens a request against 131–171 with it,
and wrote 1.10M output tokens against 0.23–0.33M: it worked the plan out again
at every step, from tool results that say only where the robot is. Two of its
none sessions did the opposite, issuing moves in parallel batches without
reading the replies (170 and 175 commands under half a second apart) and
running down the battery. Both look like a model that has lost its place.
This reverses MiMo's direction in phase 2, where replay made it slower and
wordier on tasks of 5–14 requests: the cost of carrying reasoning is paid on
every task, and the return comes only on tasks long enough to need a plan.

**What it means for Strument:**
- **Keep the call IDs as MiMo generated them.** Route alone carried the whole
  benefit on MiMo's route, and Strument already does this. Nothing should
  rewrite or drop them.
- **The replay patch earns a per-model setting,** by the rule written before
  the run, reversing phase 3's retirement for long tasks. On MiMo's route it
  adds nothing measurable over the server's copy. It matters where the route
  keeps nothing, which here is DeepSeek.

### What it does not settle

- **The none arm on MiMo ran partly later.** A container restart killed five
  none sessions mid-run; they were rerun from scratch at the same indexes after
  the other cells had finished. Four of them ran slower per request (25–33 s,
  against 18–21 s for the two first-window none sessions that ran to the
  battery) and reasoned more (806–1,062 tokens a request against 454–512), so
  the provider or the hour may have drifted. But the three first-window none
  sessions were unsolved too (two battery, one loop), and those two spent more
  reasoning per request than any route or replay session (at most 313). The
  effect is not the restart.
- **Three none sessions on MiMo hit the timeout** at 203–256 commands. Counting
  each as solved at its next command, the best case for none, leaves p = 0.003
  for both route and replay.
- **One task, one world.** Its long horizon comes from navigation and a lock
  chain. A task whose state lives in files the model can reread would test
  something else, and phase 3 found no benefit when narration survives.
- **Eight sessions a cell.** DeepSeek's p = 0.049 sits at the bar; the
  direction is clear, the size less so.
- **Flint** says nothing either way, at the floor and short of quota.

## Files, phase 4

- `data/facility.py`: the world, its self-check and the robot client.
- `data/p4run.py`, `data/p4pool.py`, `data/p4-order.txt`: the namespaced
  runner, the pool and the order.
- `data/p4-hook.patch`, `data/p4-replay.patch`: the trial builds.
- `data/p4score.py`: the counts; `data/p4-scores.tsv`, the trial's.
- Raw sessions, both pilots and the trial: `dbohdan/experimental-data`,
  `strument/2026-11-preserved-reasoning/phase4-runs.tar.xz` at `958e907`.
