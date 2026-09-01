# Shell parallelism: does the advertised pattern get used?

2026-09-01. Preregistration in this file above (design, metrics, scoring
rules, frozen task); the runner adapted the 2026-09 rig
(`2026-09-code-mode2-data/run.py`). 27/27 runs completed, no timeouts, no
empty replies, no loop stops, no clamp notices. Transcripts in
`2026-09-shell-parallel-data/transcripts/`.

## Results

Primary one first: **PROSE moved uptake to 9/9 — every run in every model
used `a & b & wait`.** BASE was already 6/9. The preregistration's predicted
null ("models will not reach for background jobs unprompted") was wrong, and
the 2026-10 conclusion ("availability is not granularity") needs one
refinement: it holds for the *code* tool's batching, but one paragraph of
prompt prose did change how this task's commands were planned.

Per model, per arm (`bg-use`: runs using `&`, of that cell's 3 reps;
grades in rep order):

| arm | model | bg-use | grades | bash calls |
| --- | --- | --- | --- | --- |
| BASE | mimo | n n n | PPP | 12 (4 serial calls/run) |
| BASE | qwen | Y Y Y | PPP | 4 |
| BASE | glm | Y Y Y | PPP | 6 |
| PROSE | mimo | Y Y Y | PFP | 3 |
| PROSE | qwen | Y Y Y | PPP | 3 |
| PROSE | glm | Y Y Y | PPP | 3 |
| EX | mimo | n n n | PPP | 12 (4 serial calls/run) |
| EX | qwen | Y Y Y | PPP | 4 |
| EX | glm | Y Y n | PPP | 4 |

### What the arms did

- **PROSE is the only arm that changed behavior, and it changed it
  everywhere.** 9/9 uptake, one batched call per run, the lowest bash count
  (9 total vs 20–22). This is the 2026-09 prose effect again — two sentences
  pointing at a pattern got all three models to use it.
- **EX bought nothing over BASE.** 5/9 uptake — *worse* than BASE's 6/9,
  and the misses are systematic: mimo ignored the pattern in both arms (12
  serial calls per run, 4 separate bash calls, all correct), and glm-EX-2
  ran serial. The few-shot pair — a worked exchange ending in `a & b & c & d
  & wait` — did not move the model it was most aimed at. Examples taught
  vocabulary, not planning; same verdict the code-only trial reached by a
  different route.
- **Correctness was a ceiling, not a differentiator: 26/27.** The one FAIL
  (mimo-PROSE-1) wrote `FLEET FAIL: auth, billing` when billing was only
  DEGRADED — the handoff's "FAIL outranks it" was read as "list both." A
  genuine spec misreading, and the only one.

### The baseline surprise

BASE's 6/9 is the interesting number. Two of three models used the pattern
with no prompt exposure at all — the tool description's mention of `a & b &
wait` (added in 7fa16ad) plus the smoke run's equalizing comment in the
check scripts were enough for qwen and glm. The description-level exposure
the trial was designed to test is already past the threshold for half these
models. What PROSE adds is not discovery but *uniformity*: it is the
difference between "2 of 3 models usually do this" and "everything does this
every time."

### Wall-clock

Not directly measured — `bash_span_records` (record distance from first bash
call to last result) is the proxy: PROSE median 1 (one call, one result),
BASE and EX median 4. The serial runs cost ~4× the shell time, matching the
fixture's design arithmetic (4 × 3 s serial vs ~3 s batched).

## What to conclude

- **Prose is the lever; examples are not.** PROSE 9/9 vs EX 5/9, with EX's
  misses concentrated in the model that needed it most. This refines the
  code-only trial's "the lever is planning (few-shot first, nudge second)":
  few-shot was tried here and did not plan. A worked example shows the
  *shape*; the prose paragraph states the *rule* ("when several commands are
  independent, run them together"); only the rule changed behavior.
- **The pattern is safe.** No truncation, no interleaving confusion, no
  clamp notices, zero repair loops from backgrounded output — the caveats in
  the tool description never fired in practice.
- **Adoption is model-dependent even under prose.** mimo ran serial in every
  arm including EX — 4 calls per run, always correct, never batched. Whatever
  prose moves in qwen and glm does not move mimo.

**Verdict: PROSE passes its primary metric and is worth keeping** — the
paragraph is a candidate for the shipped tool description or a
system-prompt line, pending a run against a task where serial execution is
*also* fine (this task makes batching near-mandatory, so 9/9 may be
ceiling-limited). EX fails and is not worth pursuing further; the
`example_messages` setting stays as infrastructure.

## Scoring corrections (recorded because they changed results)

Two grader bugs were found *after* the first pass and fixed before any
conclusion was drawn; both corrections are in
`2026-09-shell-parallel-data/grade.sh`:

1. The first-pass status pattern only matched bash-tool stdout. Two runs
   redirected check output to files and read them back with `read` — a
   perfectly good strategy the grader scored as ERROR ("no transcript record
   shows checks"). Fixed to accept numbered-prefix read output, excluding the
   scripts' own source lines.
2. The health-line check demanded sorted component order; models writing
   `FLEET FAIL: search, billing` for FAILs {search, billing} were marked
   wrong. Fixed to grade the set, not the order. This converted 14 spurious
   FAILs to PASSes; the residual FAIL (mimo-PROSE-1) is genuine (a DEGRADED
   component listed in the FAIL line).

Both corrections are exactly the failure shape `internal/coder/record.go`'s
header documents: the first was a scorer that knew where the text should be
rather than where the model put it.

## Method notes

- Runner: `/tmp/shell-parallel/run.py`, adapted from
  `2026-09-code-mode2-data/run.py`; per AGENTS.md the runner itself stays out
  of the repository. A resume-path bug (`UnboundLocalError` on `text`) ate
  one invocation's rows before the clean batch; fixed, and the runner now
  refuses to resume onto a batch that ended in runner errors. All 27 runs in
  this report are from one clean, uninterrupted batch.
- Models: mimo = xiaomi/mimo-v2.5, qwen = qwen/qwen3.8-27b,
  glm = z-ai/glm-5.3-flash, via OpenRouter, seed 20260902, 3 workers.
- Total cost $0.11 for 27 runs. One run used the model-settable `timeout`
  parameter (the feature's first in-the-wild use).
- The smoke run (before amendments) is kept as
  `transcripts/smoke-glm-BASE-0.jsonl`; its demand-characteristic finding is
  recorded in the amendments section above.
