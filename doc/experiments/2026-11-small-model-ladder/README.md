# Climbing from the smallest model that drives Strument

**Status: characterization, run 2026-11-23 to 2026-11-24.** A ladder of small
models, sorted by total parameters, each given the same headless Tetris task
through Strument. In short:
- No model below 26B passed. The bar was 63 of 69 hidden tests in at least two
  of three runs. Inkling 2 Mini (26B, 3.4B active) met it with 69 and 68.
  Gemma 4 26B A4B scored 69 in its one run at bf16. That run is promising, but
  it is one run, so under the ladder's own rule it has not passed.
- Size predicted badly. Granite 4.2 8B came closest (50, 49, 61). Mistral Small
  3.2 24B, LFM 2.5 24B and gpt-oss-20b scored 0 in every run.
- Serving was as large an effect as the model. Gemma 4 26B scored 69 at bf16,
  0 and 0 at fp4, and 46 and 0 at int4, while Inkling passed at fp4. Every
  cell served by Darkbloom scored 0 apart from that 46.
- Most failures below 26B were in how a reply reached Strument or how long a
  model deliberated, not in the program it wrote. Four of those failures are
  gaps in Strument, listed under [What this means for Strument](#what-this-means-for-strument).
- 83 sessions, 14 of them ended by the rig, not the model. They are kept in
  the table and left out of the results.

## Why

The question was whether any recent very small model can drive Strument well
enough to build something meaningful in one turn, the way someone working off
the grid would use it. Gemma 4 26B A4B, Qwen3.6 and 3.8 27B and Inkling 2 Mini
were known to be strong, so the ladder climbed from the bottom toward them. It
was to stop at three models that pass or at the first known-strong rung.

## Method

**The task** (`data/fixture/SPEC.md`) is a headless Tetris in Python. Pieces
arrive in a fixed order and move only on command, with no timer or
randomness. The spec gives all seven pieces' four rotation states as tables, a
fixed kick order (left 1, right 1, left 2, right 2), soft- and hard-drop
scoring, line clears, game over and an exact output format. The model sees the
spec and three sample games with their expected output. The prompt
(`data/prompt.txt`) asks for `tetris.py` and asks it to check every sample
before finishing.

**The grader** (`data/grade.py`) runs 69 hidden tests (`data/hidden/`) and
counts exact matches. The tests were generated from a reference implementation
written from the spec (`data/ref/tetris.py`, `data/make_tests.py`). They cover
every rule, including the traps: an `O` may sit at column −1, a two-way kick
goes left first, a `D` that cannot move locks the piece and scores nothing,
and commands are case-sensitive. Inkling's 69/69 is a check on the suite, too:
a second implementation written only from the spec agrees with the reference
on every test.

**The bar** is 63 of 69 (90%) in at least two of three runs. It is loose on
purpose: the code of a very small model is expected to need some patching.

**The ladder** (`data/ladder.txt`) is sorted by total parameters, with active
parameters recorded. Total parameters are what decide whether a model fits in
memory off the grid. Each session ran
`strument chat --yes steps,bash` in a fresh copy of the fixture under a
30-minute timeout (`data/run.sh`). Every OpenRouter config sets
`reasoning = "low"` where the model accepts one. From the second batch on,
each also sets `max_output = 32768`, after OpenRouter refused requests for the
output allowance it filled in when none was sent. The
planned rule was to rerun at `high` any model whose transcripts showed it
needed more thought. Budget allowed that only for Granite.

**Positive controls first.** Gemma 4 26B and Inkling 2 Mini ran before the
climb, to show the task could be done at all. Inkling passed at once. Gemma
failed at int4 on Darkbloom and at fp4 on NextBit, then passed at bf16 on
PrimeIntellect. After that, the protocol stopped pinning 4-bit endpoints. The
main ladder runs each model at OpenRouter's default routing, and models that
ship in a low-bit format (gpt-oss-20b in MXFP4, Ternary Bonsai) stay in it.
Qwen3.5 9B got two extra runs pinned to fp4.

**The local rungs** ran on llama.cpp: first on a VPS's CPU, then on a borrowed
RTX 3050 6 GB under Windows, on the Vulkan build. They cover Spark-X2.6-4B
(stock and Sharp-Spark builds), Ling 3.0 Tiny (its OpenRouter provider never
answered) and Qwen3.5 4B, as a second 4B reference against Spark. Their
configs differ by run, as recorded in `data/configs/`: context, sampling and
whether the template's thinking is on.

**Builds.** The OpenRouter rungs before 2026-11-24 02:24 UTC ran Strument at
`9e1cf85`, and every run after that ran at `adeaceb`. The two builds differ
only in the keepalive stall fix (`761c1d2`), the output-cap hint (`a524af9`)
and the `-m`/`-p` swap. `data/run.sh` is the final version, which uses `-p`.

## Results

`data/runs.tsv` has one row per session, produced by `data/tabulate.py`. Each
row records the score, whether `tetris.py` exists and parses, how the session
ended, the hidden tests failed by cause, the provider that served each
request, the cost and the wall-clock. Every column is read off a file. How a
session ended is matched from the end of its transcript.

### OpenRouter and Command Code

| total / active | model | served by | scores /69 | how the runs ended |
| --- | --- | --- | --- | --- |
| 1.7B | LFM-3 1.7B | Liquid | 0, 0, 0 | output cap ×3: the whole program in one reply, cut off at the endpoint's ~9k; no file |
| 3B | Ministral 3B | Mistral | 0, 0, 0 | error-recovery limit ×2, answered ×1; every program crashed or printed the wrong board |
| 8B | **Granite 4.2 8B** | DeepInfra | **50, 49, 61** | a tool call written into its reasoning ×3 (closing tags only) |
| 8B | Granite 4.2 8B, `high` | DeepInfra | 8, 43, 0 | the same ×3 |
| 8B | Granite 4.2 8B, pinned | CoreWeave | 0, 54, 2 | the same ×3 (one with the opening tag, caught) |
| 8B | Ministral 8B | Mistral | 0, 0, 0 | context full ×2 after 87 and 282 steps, error-recovery limit ×1 |
| 8B | Qwen3-VL 8B | Parasail | 0, 0, 0 | error-recovery limit; loop detector; 30-minute timeout while repeating three lines inside a `write` argument |
| 8B | Llama 3.1 8B | Groq | 0, 0, 0 | answered ×3: JavaScript through `run_code`, one tool call written as text (`<function=run_code>`) |
| 8B / 1B | Ling 3.0 Tiny | Ant Digital | — | 6 sessions, the provider never answered (rig) |
| 9B | Qwen3.5 9B | mostly Darkbloom | 0, 0, 0 | tool call in the reasoning ×3 (2 closing tags only, 1 caught) |
| 9B | Qwen3.5 9B, pinned fp4 | Darkbloom | 0, 0 | error-recovery limit; answered with a program that crashes |
| 9B | Ternary Bonsai 2 9B | Darkbloom | 0, 0, 0 | tool call in the reasoning ×3, caught, each within 20 s |
| 12B | Gemma 3 12B | DeepInfra | 0, 0, 0 | no native tool calls: `[{"read": …}]` in a code fence; no file |
| 14B | Ministral 14B | Mistral | 45, 4, 0 | context full ×3 after 370–410 steps of edits that changed nothing |
| 14B | Qwen3 14B | DeepInfra | 0, 0, 56 | reasoning and no answer ×2; loop detector ×1 |
| 20B / 3.6B | gpt-oss-20b | Darkbloom | 0, 0, 0 | quits within 5 s at `low`: "I'm not able to write the full program in this format right now" |
| 24B | Mistral Small 3.2 24B | DeepInfra | 0, 0, 0 | 30-minute timeout ×2, error-recovery limit ×1 |
| 24B / 2B | LFM 2.5 24B-A2B | Liquid | 0, 0, 0 | output cap ×3: worked through the piece table in its answer, cut off at the endpoint's 8192 |
| 26B / 4B | Gemma 4 26B A4B, int4 | Darkbloom | 46, 0 | loop detector ×2: "I'll use `write` now." without calling it |
| 26B / 4B | Gemma 4 26B A4B, fp4 | NextBit | 0, 0 | loop detector ×2, the same |
| 26B / 4B | **Gemma 4 26B A4B, bf16** | PrimeIntellect | **69** | answered, 17 requests |
| 26B / 3.4B | **Inkling 2 Mini**, fp4 | PrimeIntellect, via Command Code | **69, 68** | answered, in 13 s and 36 s |

### Local, llama.cpp

| total / active | model, build | where | scores /69 | how the runs ended |
| --- | --- | --- | --- | --- |
| 4B | Spark-X2.6-4B Q4_K_M | VPS CPU | — | 5 sessions, all ended by the rig (server gone, ended by hand, container restart) |
| 4B | Spark-X2.6-4B Q4_K_M, 32k | RTX 3050 | 0, 0, 0 | output cap (`max_output = 20480`) ×3, two before writing a file: hand-simulating sample games in its reasoning |
| 4B | Spark-X2.6-4B Q4_K_M, 64k | RTX 3050 | 0 | output cap (32768), no file |
| 4B | Sharp-Spark Q6_K_XL, 64k | RTX 3050 | 0 | output cap (32768), no file |
| 4B | Sharp-Spark Q6_K_XL, 64k, thinking off | RTX 3050 | 0 | 60-minute timeout after 57 requests; it ran its code throughout and never drew the falling piece |
| 8B / 1B | Ling 3.0 Tiny Q4_K_M, 128k, thinking off | RTX 3050 | 0, 0, 0 | output cap ×3: deliberating in the answer, and once in a 32k `write` argument |
| 4B | Qwen3.5 4B Q4_K_M, 64k, thinking off | RTX 3050 | 0, 0 (+1 rig) | output cap with a tab-indented file that never parsed; loop detector, with a program that never locks a piece |
| 4B | Qwen3.5 4B Q4_K_M, 64k, thinking on | RTX 3050 | 0 (+2 rig) | loop detector, with a program that reads `sys.argv[1]` instead of stdin |

The 14 sessions ended by the rig are named in the `equipment` column of
`data/runs.tsv`. The 402s from an exhausted key killed four sessions in the
first batch, two each of Ministral 8B and Qwen3-VL 8B. Those were rerun, and
the reruns replaced them.

Recorded cost was $2.84. That is a floor: a session ended by its timeout
writes no cost record, which is why Mistral Small's two timeouts show $0.

## What the ladder shows

**The threshold is at 26B, and at 26B the serving decides.** Inkling passed
at fp4, the only precision its endpoints offer, and Gemma passed at bf16. In
all four of Gemma's 4-bit runs, on two providers, the failure was the same: it
announced a `write` and never made the call, until the loop detector stopped
it. So 4-bit is not fatal by itself. Whether Gemma's 4-bit weights are, or the
two providers share a broken serving setup, the ladder cannot say.

**Darkbloom is a confound across five cells.** OpenRouter's default routing
sent Qwen3.5 9B to Darkbloom in 26 of 30 requests, so its main runs were not
the bf16 baseline they were meant to be. Darkbloom also served every request
of Bonsai, gpt-oss-20b and Gemma's int4 runs. Everything it served scored 0,
apart from one Gemma 46. None of those cells is evidence about the model alone.

**Below 26B, family mattered more than size.** An 8B Granite scored 61, and
three models of 20–24B scored nothing. The partial scores that did appear
(Granite's, Ministral 14B's 45, Qwen3 14B's 56, Gemma int4's 46) came from
runs that wrote a parsing program early and then lost the turn to something
else: a leak, a loop, a filled context. The program was not the limit.

**More reasoning made Granite worse.** At `high` it scored 8, 43 and 0,
against 50, 49 and 61 at `low`, on the same provider. The extra reasoning
gave its tool-call leak more room to happen.

**Turning thinking off moves the deliberation; it does not remove it.** With
its template's thinking off, Ling deliberated in its visible answer and, in one
run, inside a `write` argument. LFM 2.5 did the same in its answer. Spark
with thinking on spent every cap on hand-simulating games. With thinking off
(the Sharp build), it was the one 4B run that ran its code steadily for the
whole hour.

**A healthy agent loop is not a solution.** Qwen3.5 4B wrote, ran, wrote a
debug script and traced, which is the loop a strong model uses. It still
miscounted cells on a board, read the wrong input and indented a file with
tabs. At 4B, the agent behavior arrives before the bookkeeping does.

**For a 6 GB card:** every configuration that fit in its VRAM failed. The
route this ladder did not test is a mixture of experts with the experts in
system RAM, such as Qwen3.6-35B-A3B, where the card holds attention and the
shared weights and each token reads about 3B active parameters from RAM.
That is the configuration to try next for local use.

## What this means for Strument

Two fixes came out of the ladder while it ran:
- **`761c1d2`:** a stream that sends only keepalives (`: OPENROUTER
  PROCESSING`) for ten minutes now ends with a message saying so. Ling's
  provider held three sessions for 30 minutes each that way.
- **`a524af9`:** a 402 refusal over `max_tokens` now says where the number
  came from and which `max_output` changes it. Two Qwen3-VL sessions died on
  that refusal with a nearly empty key.

These are open, and none of them is started:

1. **A tool call in the reasoning, with only its closing tags.** Ten sessions
   (8 Granite, 2 Qwen3.5 9B) ended on reasoning whose tail was
   `</parameter></function></tool_call>`. The provider had dropped the
   openers, probably as special tokens, so the opener check in
   `reasoningleak.go` did not fire. The user saw "The model returned reasoning
   but no answer and no tool call". Five other sessions had the opening tag,
   and the existing diagnosis caught them. Recognizing the closers fixes the
   message. Telling the model that its call did not run, and continuing the
   turn, is the change that could move a score. It is a design change to how a
   turn ends.
2. **Edits that change nothing are reported as applied.** In
   `data/noop_edits.tsv`, Ministral 14B made 1,091 of its 1,101 edits with
   `old_string` identical to `new_string`, and Ministral 8B 162 of 223.
   Strument answered "Applied edit" to each. Refused with an error, each 14B
   run would have stopped at the error-recovery limit after three of them,
   not after $0.60 and a full context.
3. **The loop detector does not see tool-call arguments.** It is fed the
   answer and the reasoning (`internal/coder/send.go:206`, `:212`). Qwen3-VL
   repeated the same three lines 1,045 times inside a `write` argument until
   the 30-minute timeout. Ling's 32k `write` of planning comments ended only
   on the output cap.
4. **A configured context larger than the server's.** One Qwen3.5 4B session
   was configured for 131,072 tokens against a server at 65,536 and ended on
   HTTP 400. llama.cpp reports `n_ctx` in `/props`, so Strument could warn at
   startup.

The ladder also cost two hours of retries against a dead tunnel. That lesson
is in the handbook, as
[gone or busy](../../experimenting.md#gone-or-busy).

## Follow-up, written down before the fix

If item 1 is built as "tell the model that its call did not run, and continue
the turn", Granite 4.2 8B is the case that tests it. The design is set here,
before the fix exists:
- Granite 4.2 8B at `reasoning = "low"`, pinned to DeepInfra, which served
  its best cell. Three runs on the current build, three on the build with the
  change, in shuffled order, on this fixture and grader.
- **Primary:** hidden-test score, against the same bar of 63 in two of three.
- **Counted alongside:** leak events per session, and sessions whose turn ends
  on a leak.
- **Counter-metric:** steps and cost per session, and sessions that leak
  again straight after the note. A note the model ignores turns a clean
  stop into a loop.
- **Prediction:** Granite's runs lost their turns while still fixing failures,
  so continuing should raise the scores. Whether they clear 63 in two runs is
  a coin flip.

## Limitations

- Three runs per cell at most, and one or two in several cells, including the
  bf16 Gemma that passed. This is a probe for where the threshold sits, not a
  comparison between small models.
- The `high` reruns the protocol called for ran only for Granite. gpt-oss-20b,
  Qwen3 14B and Qwen3.5 9B also looked like they needed more thought.
- The main ladder did not pin providers. As the Darkbloom cells show, that
  measured providers as well as models.
- The local rungs differ from each other in build, context, sampler and
  thinking setting. Each was set up to give the model its best chance on 6 GB,
  not to match the others.
- A program that crashes at start scores 0, whatever it gets wrong. The
  `program` and `failing_tests` columns separate no file, a file that does not
  parse, a crash on every test, and a wrong board.
- `--yes steps,bash` did not include `context`, so script mode declined the
  context-full prompt. That ended five Ministral runs. After hundreds of steps
  of unchanged edits, the outcome would not have differed.

## Files

- `data/fixture/`, `data/prompt.txt`: the task.
- `data/hidden/`, `data/ref/tetris.py`, `data/make_tests.py`: the hidden tests,
  the reference implementation and the generator.
- `data/grade.py`: the grader.
- `data/run.sh`: one session; the binary and `data/configs/` sit beside it.
- `data/configs/`: one per cell. Keys come from the environment, and the local
  server's address is replaced by `llama-server.example`.
- `data/ladder.txt`: the rungs, with sizes, reasoning settings and precision.
- `data/tabulate.py`, `data/runs.tsv`: one row per session.
- `data/noop_edits.py`, `data/noop_edits.tsv`: edit calls that change nothing,
  per session.
- Raw transcripts, grades, final programs and session records:
  `dbohdan/experimental-data`,
  `strument/2026-11-small-model-ladder/runs.tar.xz` at `459db72`.
