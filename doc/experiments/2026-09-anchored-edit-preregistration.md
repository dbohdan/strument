# Pre-registration: is an anchored edit format worth it for Strument?

Written before any sample was collected. Amendments are appended at the end,
dated, rather than edited into the body.

## Question

Three harnesses in circulation replace search-and-replace editing with *stable
per-line addressing*. The model reads a file whose lines carry opaque
identities, and edits by naming an identity rather than by retyping the text it
wants to change.

- [oh-my-pi](https://github.com/can1357/oh-my-pi) calls it hashline and reports
  "61% fewer output tokens" for Grok 4 Fast against string replacement. Stale
  anchors are rejected before a patch lands.
- [pi-hashline-edit-pro](https://github.com/YuGiMob/pi-hashline-edit-pro) is the
  same idea packaged on its own.
- yoneda (`git.sr.ht/~mesaoptimizer/yoneda`) is a Haskell CLI — `read` / `edit` /
  `grep`, JSON on stdin, exit 0 applied / 1 rejected-nothing-written / 2 usage.
  Read as a snapshot supplied by the author; sr.ht returned 502 for every path
  tried, so this is not a reading of the canonical repository.

yoneda is the most fully worked out of the three and the one the arms below are
modelled on. Its read format is three fixed columns, `anchor ║ indent ║ content`:

```
clever-torrent ║ 0 spaces ║ Lorem ipsum dolor sit amet
slow-owl       ║ 1 tab    ║ commodo consequat
```

Three of its choices are separable, and this experiment is designed to separate
them:

1. **Anchors** are dash-joined common words minted from crypto randomness — no
   line number, no content hash in the identity itself. A per-file registry
   holds anchors plus a SHA-256 per line; a read reuses stored anchors only if
   every line still hashes the same, so an external edit re-mints the file and
   an edit against a moved registry is rejected wholesale.
2. **The indent column** strips the leading whitespace run out of the content
   and renders it as words (`3 tabs`, `1 tab 2 spaces`, always present so the
   row has fixed arity). It is load-bearing on the write side: edit rows carry
   those words and are validated, with singular/plural agreement enforced.
3. **The digest** is the success report — the new anchors shown between their
   surviving neighbours, with no content echoed.

The question is not "should Strument adopt hashline". It is **which of those
three, if any, pays for itself here**, and the answer is expected to differ from
the answer upstream because Strument's traffic shape differs.

## The prediction, and what would change it

**The token case is expected to be break-even to slightly negative.** Stated
plainly so that a null is not later described as a surprise.

From 36 `turn` records across this directory's existing trials, a Strument turn
runs about **32.9k input to 800 output at the median** — a ratio near **35:1**.
A format that saves 61% of output can therefore afford only

```
0.61 × (output/input) × (price_out/price_in)
```

of extra input before it costs more than it saves. Per model:

| model | breakeven input overhead |
| --- | --- |
| `xiaomi/mimo-v2.5` | 3.0% |
| `deepseek/deepseek-v4-flash-0731` | 4.1% |
| `z-ai/glm-5.3-flash` | 4.9% |
| `tencent/hy3` | 5.9% |
| `openai/gpt-5.6-luna` | 8.9% |
| `qwen/qwen3.8-27b` | 8.9% |

Strument's `read` already prints a line prefix (`%*d\t`, `toolobserve.go:105`) —
2–3 tokens a line. A yoneda row is more like 9–12: the anchor is three or four
tokens, two bar separators, two or three for the indent words. That is roughly
**+7–9 tokens per line read**, and a turn that reads 300 lines adds 6–8% to
input — at or past breakeven for four of the six models.

**What would change my mind:** a measured per-line overhead under about 4%. That
is possible, because the indent column *removes* the leading whitespace it
encodes. Deeply indented code is where it could flip, and a Go codebase full of
tab-indented method bodies is a plausible place for that to happen. Phase 0
measures it directly.

The case that survives a negative token result is **correctness**: staleness
detection, which Strument has none of, and the whitespace failure class, which
`internal/editblock/replace.go` currently absorbs with `perfectOrWhitespace`,
`replacePartWithMissingLeadingWhitespace`, `matchButForLeadingWhitespace` and
`outdent`.

## Arms

A ladder, not a factorial. The four rungs nest in cost and in blast radius, and
each is independently shippable, so the experiment can stop at whichever rung
stops paying.

- **A (baseline).** Today's `edit(path, old_string, new_string)` with the
  existing fuzzy stack.
- **B.** A, plus a read stamp: an edit whose file changed since the read the
  model is working from is refused with a message that says so. No format
  change, no stored state beyond the turn.
- **C.** B, plus the indent column in both `read` output and `edit` rows. The
  model never types leading whitespace; it names it, and the harness validates
  the name. Still no registry.
- **D.** Full anchors: word identities, the per-file registry with a hash per
  line, the digest as the tool result.

Arms are separate binaries built from separate commits, not a runtime flag, so
no experimental scaffolding reaches production code and the arms provably differ
only by their diff.

**B exists so that a D-beats-A result is interpretable.** Without it, "anchors
are better" and "we finally noticed stale files" are the same number. **C exists
so that the registry is not built on the strength of a win the indent column
alone would have delivered.**

## Phase 0: the free measurement, run first

The input half of the token question needs **no API calls and no models**.
Render a corpus of this repository's own files in each arm's read format,
tokenize, and compare. Deterministic, free, repeatable.

It answers: what is the real per-line overhead of the anchor column, and does
the indent column pay for it back on Go source? If arm C's overhead lands under
its 3–5% breakeven and D's does not, that is most of the decision made for zero
dollars, and Phase 1 shrinks to the arms still standing.

Phase 0 is reported whatever it shows, including if it kills arms.

## Sampling

4 arms × 6 models × 4 fixtures × 3 reps = **288 runs**, 48 per model.

Priced before the arms were designed, per `doc/experimenting.md`. Prices are
$/M tokens from the OpenRouter catalogue on 2026-09-03; where a model has
several endpoints the mid one is used, and the spread is noted.

| model | in | out | 48 runs | share |
| --- | --- | --- | --- | --- |
| `deepseek/deepseek-v4-flash-0731` | 0.065 | 0.18 | $0.15 | 6.0% |
| `z-ai/glm-5.3-flash` | 0.075 | 0.25 | $0.18 | 7.0% |
| `tencent/hy3` | 0.132 | 0.528 | $0.32 | 12.5% |
| `xiaomi/mimo-v2.5` | 0.14 | 0.28 | $0.32 | 12.8% |
| `openai/gpt-5.6-luna` | 0.20 | 1.20 | $0.50 | 19.7% |
| `qwen/qwen3.8-27b` | 0.425 | 2.55 | $1.06 | 41.9% |

**Total: $2.54 at mean volumes, $4.14 at p90.** Budget $6.

Two models are 62% of the spend. That is deliberate and, here, harmless — but
only because of *why* it is harmless. The 2026-08 prompt-scope trial let one
model take 95% of the budget and the sample size ended up decided by cost rather
than by the question. Here the sample size is set by how many transcripts can be
read carefully, not by money: at under $5 for the whole thing, cost binds
nothing. The imbalance would matter again the moment the design grew past a
few hundred runs, and that is the point at which to re-price rather than to
scale.

`qwen/qwen3.8-27b` is included on OpenRouter despite being available locally to
the author. A separate local trial plan will cover the local instance; running
it here too keeps this experiment's six-model panel intact and gives the local
plan a hosted baseline to compare against.

**Arm order is randomized across the whole job list, with the shuffle seed
recorded.** In the prompt-scope trial this was worth more than tripling the
sample: shuffling alone moved a baseline from 65% to 84% and took p=0.0009 to
p=0.15, because the unrandomized design confounded the arm with the wall-clock
window it ran in.

### A provider hazard to fix before running

`openai/gpt-5.6-luna` spans 0.10/0.60 to 0.40/2.40 across endpoints — a 4×
range — and `tencent/hy3` and `xiaomi/mimo-v2.5` each have five or six
endpoints. Strument today **neither pins a provider nor records which one
served a request**: there is no routing block in the config DSL and no provider
field in `internal/llm/types.go`.

That is a validity problem before it is a budget problem. Endpoints can differ
in quantization and behaviour, so an uncontrolled endpoint is a variable
correlated with wall-clock time — the exact shape the randomization above exists
to defeat. **Recording the serving provider per request is a prerequisite for
this experiment**, and is worth having regardless, since "test with more than
one model — providers disagree" is a standing rule that we currently cannot
check. Pinning is optional; recording is not.

## Fixtures

Four, each a working repository copied fresh per run. All four run in every
arm.

| | fixture | shape |
| --- | --- | --- |
| F1 | `narrow` | a one-line change deep inside a long function — where anchors should win most |
| F2 | `broad` | a rename touching a dozen sites across three files — many small edits |
| F3 | `indented` | Go with deep tab indentation, where the indent column should pay |
| F4 | `moved` | **the staleness fixture**: an external write lands between the read and the edit |

F4 is the one that decides arm B, and it is built so the phenomenon *can* occur:
arm A is expected to score 0 on M5 by construction, which is the finding, not a
bug. A fixture that cannot contain the phenomenon it is scored on is the failure
this directory has already recorded once.

## Metrics

Pre-registered. Counts, not judgements.

**Primary**

- **M1 first-try edit success.** Fraction of edit tool calls that apply without
  a retry. Read from the tool results, not from prose.
- **M2 round trips.** Steps per completed task.

**Cost, reported together and with equal prominence**

- **M3 input tokens.** The counter-metric to the 61% claim, and the one neither
  upstream project reports. A result that gives M4 without M3 is not a result.
- **M4 output tokens.**
- **M5 total cost.**

**Counter-metrics**

- **M6 staleness caught.** On F4: did the arm notice the file moved, or did it
  write over a changed file? Silent corruption is the worst outcome available
  and gets its own column.
- **M7 file broken.** `ParseStatus` after each edit — free, already in the tree,
  and exactly the hazard check.
- **M8 scope creep.** Files edited outside the target.
- **M9 whitespace rescues.** How often the existing fuzzy stack saved an arm-A
  or arm-B edit that arm C would have made impossible to get wrong. This sizes
  the prize C is chasing and is the number that decides whether C is worth
  shipping on its own.

## Rig checks, before spending

1. **The arms differ on the wire.** Capture one request per arm through
   `cmd/strumentrec` and confirm the read format and the edit tool schema differ
   as intended. Two arms that should differ were once the same binary.
2. **The scorer reads 0 and full marks.** A known-bad and a known-good patch set
   through the scorer unchanged.
3. **Each metric discriminates.** Break one thing at a time and confirm exactly
   that metric moves.
4. **F4 fires.** Arm B must catch staleness on the fixture and arm A must not,
   in a dry run, before any model is called.
5. **The tokenizer agrees with the provider.** Phase 0's counts are compared
   against the `sent`/`received` a real run reports, on at least one model. A
   token-efficiency experiment whose tokenizer disagrees with the meter is
   measuring nothing.

## Analysis

Report the ladder as three deltas, each answering one question:

- **A→B**: what does staleness detection alone buy? (M6, M1)
- **B→C**: what does the indent column alone buy? (M1, M9, M3)
- **C→D**: what do anchors and the registry add over that? (M1, M2, M3, M4)

Read every transcript, not a sample — three is this directory's floor for
believing an aggregate, and 288 is small enough to read whole. Individual
transcripts are read before any aggregate is believed: a provider returning
`Empty response received from LLM` and a model emitting an edit as inline text
are indistinguishable in a summary and mean opposite things.

The honest null shapes, named in advance so they are recognised rather than
explained away:

- **Treatment never applied** — a model declines the new format and falls back
  to prose. M1 must distinguish "the arm was offered" from "the arm was used".
- **Hazard never triggered** — M6 or M7 read zero everywhere including where
  they should fire, which means the fixture is wrong, not that the arm is safe.
- **Arms were the same** — check 1 above.

## Out of scope

- **oh-my-pi's time-travelling stream rules** (abort mid-token on a regex match,
  inject a rule, retry from the same point). Strument has the machinery — the
  loop detector already aborts a live stream — but it puts a correction the
  human never made inside the human's turn, which is a question about the
  reviewable loop and not about editing. Separate discussion, separate trial.
- **Batch edit atomicity.** yoneda validates a whole batch and applies nothing
  unless all of it validates; Strument applies per call. Worth considering on
  its own merits, but it is not part of the addressing question and would
  confound these arms.
- **Read modes as projections** (yoneda's signatures-only mode, and grep sharing
  the read row shape). Interesting, orthogonal, later.
- **The local Qwen instance.** Covered by a separate plan.

## Amendments
