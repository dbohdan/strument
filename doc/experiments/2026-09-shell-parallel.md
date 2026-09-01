# Shell parallelism: does the advertised pattern get used?

2026-09-01. Two features landed in the `bash` tool before this trial: a
model-settable `timeout` (clamped to the configured ceiling, commit 4ba1a65)
and an explicit endorsement of `a & b & wait` in the tool description, with
the interleaved-output caveat (commit 7fa16ad). This trial asks whether that
description-level exposure changes what models emit, and whether it saves
wall-clock time.

## Prior

The code-only trial (2026-10-code-only.md) concluded that *availability is
not granularity*: forcing the medium converted neither the plan nor the
round-trip count. The same null is expected here — the prediction is that
models will not reach for background jobs unprompted, because batching is a
planning skill and a tool description is prose. The trial is worth running
anyway: the null closes "models would parallelize if the tool advertised it,"
and any positive result is cheap evidence that description-level exposure is
a lever that actually pulls.

## Design

One factor (prompt exposure of the pattern), three arms, one task suite,
three models, three reps — 27 runs, shuffled, seed 20260902.

| arm | tool description | prompt |
| --- | --- | --- |
| **BASE** | as shipped (mentions `&`, `timeout`, interleaving) | default |
| **PROSE** | as shipped | one added paragraph: when several independent commands are needed, run them as `a & b & wait` in one call rather than as serial calls |
| **EX** | as shipped | PROSE plus a few-shot example pair in `ExampleMessages`: one multi-job call and its result |

PROSE is the minimal known-effective intervention (the 2026-09 prose moved
`code` uptake from 0/36 to 8/24); EX is the planning-side lever the
code-only report named. Both arms share the same BASE tool description, so
any BASE/EX or BASE/PROSE difference is attributable to the prompt, not the
schema.

### Task

Needs a suite where parallel commands are natural rather than mandated: a
task whose honest execution runs a test suite and a build, or checks across
several components, where a competent human would run the commands
concurrently. The 2026-09 caps task is wrong here (pure lookup, no commands).
The task must also be correctable without the model believing serial
execution is expected — the instruction says *make it pass*, never *run these
commands*. Task construction is the trial's main risk: an obviously
parallelizable task teaches vocabulary; one where parallelism is incidental
may show no signal. The task text is frozen below before any run.

*(Task text: to be inserted here and frozen before the first run.)*

**Task (frozen 2026-09-01):** a four-component fleet-report fixture in
`2026-09-shell-parallel-data/task/`. Four check scripts under `checks/`
(`storage.sh`, `search.sh`, `auth.sh`, `billing.sh`), each sleeping 3 s and
printing one status line whose value derives from `date +%s` (truncated to
its first 8 digits) hashed with a data file's checksum — so reading a
script's source does not reveal its output, the only way to know a status is
to run it, and statuses are stable within a run but vary between runs ≥100 s
apart. The prompt (PROMPT.md) asks the model to fill TEMPLATE.md into
REPORT.md per the colleague's HANDOFF.md: every component's status line
copied exactly, plus a derived overall health line (FLEET HEALTHY / DEGRADED
/ FAIL with the failing components, FAIL outranking DEGRADED). The prompt
never mentions running commands, parallelism, or how many calls to make;
running the checks is inferable only from HANDOFF.md's "run the checks and
use what they print right now."

**Why this task measures the trial's question.** Four independent 3 s
commands are the honest workload: serial execution costs ~12 s of shell
wall-clock, one `a & b & c & d & wait` call costs ~3 s, so the wall-clock
primary has real room to move. The task is not phrased as "parallelize" —
it is a deadline-plus-workload shape, and the decision to batch is the
model's planning alone, which is exactly the faculty under test.

**Grading** (`2026-09-shell-parallel-data/grade.sh`, verified end-to-end on
a hand-built fixture run): statuses are taken only from tool-result records
in the run's JSONL — never a re-run, which removes any race with the 100 s
status window — and REPORT.md is graded on exact status lines and the
derived health line. The validation pass (JSON parse, tool_call_id pairing,
sub-3-character text fields) aborts loudly before any scoring; the
one-character-record tripwire was verified against a hand-mangled
transcript.

## Metrics

Preregistered; from JSONL only, never the rendered stream:

- **Primary — usage:** bash calls per run; calls containing `&` or ` wait`;
  calls carrying a `timeout` argument; timeout-clamp notices fired.
- **Primary — time:** wall-clock span of the shell phase (first `bash` call
  to last), per run.
- **Guardrails:** task correctness (the suite's own pass/fail); background
  jobs killed at the block boundary mid-work (visible as truncated output in
  the tool result); interleaved-output confusion (a follow-up call whose
  command suggests the model misattributed prior output); turns that ended in
  failure.

## Scoring rules

- The scorer reads **only** the JSONL. It never reads the rendered stream.
  (The eleven-scorer-bug cluster in record.go's header and the code-only
  trial's first-pass scorer both came from reading the wrong text.)
- **Validation pass first.** Before any scoring: every line must parse as a
  JSON object; every `tool_call_id` must pair with a call; every record with
  a `text` field shorter than 3 characters is listed and counted. Any
  violation aborts scoring loudly — a mangled log must stop the trial, not
  skew it. This rule exists because the code-only trial reported tool results
  arriving one character per record; the trunk could not reproduce it (the
  recording path is per-message and pinned by
  `TestRecordWithCharacterStream`), and the report's own method notes suggest
  the symptom came from a scorer reading rendered text. If it reappears, the
  validation pass turns it from a scoring contaminant into a caught error.
- Program/command failure counts come from tool-result *records*, keyed on
  the result text the model received — the same key the code-only trial used
  on its second pass — never from the command string.

## Declared confounds

- **The confirmation gate.** Every `bash` call asks the user, so one call
  with three jobs and three serial calls cost the same number of prompts.
  The trial measures emitted commands and shell wall-clock, not round trips;
  round trips are reported but expected to be uninformative here. (The
  shell.go comment's batch-approval future would change this; it has not
  arrived.)
- **Timeout interplay.** A model may pass `timeout` to buy *fewer* seconds,
  which shortens the shell phase for reasons unrelated to parallelism. Calls
  with a `timeout` argument are therefore reported separately from the
  wall-clock primary.
- **Model habit.** Three models were chosen because the last two trials
  showed different planning habits per model; the per-model table is part of
  the primary report, not a footnote.

## Method notes

- Runner under /tmp per AGENTS.md (bulk data stays out of the repository;
  this file is the durable part), but the JSONL logs are copied into
  doc/experiments/2026-09-shell-parallel-data/ before /tmp is lost — the
  code-only trial's transcripts vanished with a reboot and took their
  evidence with them.
- Reps shuffled, seeded, one task suite per rep; no timeouts on the model
  side, no retries beyond the client's own.
