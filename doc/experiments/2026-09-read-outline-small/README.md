# Outlines in the read tool, smaller models

**Status: trial.** Preregistered in [`preregistration.md`](preregistration.md)
(`ccaa68a`). A follow-up to
[`2026-09-read-outline`](../2026-09-read-outline/README.md).

**Result: the same null as the parent, pooled over three smaller models.**
Median input tokens on the past-window tasks were 39k (B) and 44k (C) against
33k (A), at p = 0.91 and 0.87. Correctness was 60/63 and 61/63 against 59/63.
No arm meets the rule, so nothing ships, and the arms stay out of the code.

**One model looked different, and a placebo shows why that is not an
effect.** Ling 3.0 Flash's medians fell by half in both arms: 279k in A,
against 135k in B and 138k in C. But B never fired. No read in any of the
trial's 189 sessions was cut short by the default window without a `limit`,
so B was A on the wire. Ling's "saving" in B is its own variance, and C's
(p = 0.068 for Ling alone) is the same size as B's.

## What did differ between models: uptake

| model | reasoning | `outline` called, arm C | median steps, past-window tasks (A) | correct (A) |
| --- | --- | --- | --- | --- |
| Ling 3.0 Flash | provider default | **8 of 21 sessions** | 24 | 17/21 |
| Qwen3.6 35B A3B | provider default | 3 of 21 | 4 | 21/21 |
| Qwen3.8 27B | `low` | 0 of 21 | 3 | 21/21 |

The weakest navigator reached for the map most. The two parent models used it
in 2 of 80 sessions (C) and 6 of 80 (D). Ling's sessions with an outline call
include its short ones:

- **`ling-C-exports-1`:** 6 steps, 49k tokens, correct. The path was bash,
  ls, grep, `read {outline: true}`, then the answer.
- **The baseline for the same task:** a median of 278k tokens.

Beside them in arm B, where nothing changed, the same task's median was 59k.
With this much variance, 21 sessions per arm cannot separate the two.

## Results

| arm | model | correct, past-window tasks | correct, controls | median input tokens, past-window tasks | outline calls (sessions) |
|---|---|---|---|---|---|
| A | Ling | 12/15 | 5/6 | 278,880 | — |
| A | Qwen3.8 27B | 15/15 | 6/6 | 24,924 | — |
| A | Qwen3.6 35B | 15/15 | 6/6 | 31,139 | — |
| B | Ling | 12/15 | 6/6 | 135,457 | — |
| B | Qwen3.8 27B | 15/15 | 6/6 | 25,319 | — |
| B | Qwen3.6 35B | 15/15 | 6/6 | 31,358 | — |
| C | Ling | 14/15 | 6/6 | 137,611 | 8 (8/21) |
| C | Qwen3.8 27B | 14/15 | 6/6 | 25,204 | 0 (0/21) |
| C | Qwen3.6 35B | 15/15 | 6/6 | 31,272 | 3 (3/21) |

Per model, against A: Ling at p = 0.26 (B) and 0.068 (C); Qwen3.8 at 0.59 and
0.36; Qwen3.6 at 0.59 and 1.0.

Three Ling sessions ended without an answer and a non-zero exit (two in A, one
in B), after 83 to 125 steps. They are scored as wrong. There were no
timeouts. The run cost $1.03.

## Reasoning ran at provider defaults for two models

OpenRouter offers no effort levels for Ling 3.0 Flash or Qwen3.6 35B A3B: only
the provider default or off, and off changes what the model is. Both ran at
the default. Neither hit the timeout, and Qwen3.6 was quick, so the
handbook's reason to pin reasoning low did not bite here. Qwen3.8 ran at
`low`.

## What happened along the way

- **The free Qwen3.8 route is unusable for a run.** All three pilot sessions
  timed out. A direct request returned 429: "temporarily rate-limited
  upstream", from a pool shared across users. The trial used the paid route,
  $0.42/$3.00 per million, at about $0.006 a session.
- **The pilot found a bug in arm C.** `read {outline: true}` with an absolute
  path answered "There is no outline", because the path was joined to the
  project root without being resolved. Ling uses absolute paths, and hit it.
  Fixed for this run by [`data/absolute-path.patch`](data/absolute-path.patch).
  All eight of the parent's outline calls used relative paths, so its result
  stands.
- **Ling's failure modes are the harness's to know about,** whatever happens
  to outlines:
  - It retries a declined bash command identically, five times over.
  - It passes grep parameters that do not exist (`output_mode`).
  - It emits tool names with markup in them (`ls …</arg_value>`).
  - It writes Python in `run_code`, which runs JavaScript.

## What this licenses

- **It licenses the parent's decision, over three more models.** Nothing ships.
- **It does not license a claim that outlines help weak models.** The one
  model that used the map was also the one whose placebo arm moved as much.
  Testing that would need a model like Ling and many more sessions per arm,
  or a within-session design, before the variance allows an answer.
- **It does support one observation for a future design:** uptake of a map
  runs inverse to navigation skill. The models that grep well ignore it.

## Transcripts read

- **`ling-C-exports-1`:** outline in step 6, correct, 49k tokens.
- **`ling-A-pipe-0`:** 87 steps to answer "3". It tried grep variants, bash
  greps that were declined, Python in `run_code`, then paged through the file.
- **`qwen35-C-abort-0`:** after eight greps, the outline located
  `_echo_aborted`, and the next call read exactly lines 100–115. The session
  then spent 13 more calls confirming `echo`'s behavior in `utils.py`, at 265k
  tokens. The map found the line. It did not make the model stop.

## Files

| file | contents |
| --- | --- |
| [`preregistration.md`](preregistration.md) | what differs from the parent, committed before the run |
| [`data/absolute-path.patch`](data/absolute-path.patch) | the fix to arm C that this run used |
| [`data/run.py`](data/run.py), [`data/score.py`](data/score.py) | the runner and scorer, adapted from the parent's |
| [`data/scored.jsonl`](data/scored.jsonl) | one row per session |
| [`data/order.json`](data/order.json) | the shuffled run order |
