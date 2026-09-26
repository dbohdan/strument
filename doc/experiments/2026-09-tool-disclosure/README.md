# Do models lose track of their own tools mid-turn?

**Status: designed, not run.** Written down so it can be picked up later
without redoing the diagnosis. Nothing below has been measured, except where
it says so.

## What prompted it

Two models, two sessions, the same kind of false belief. Each was about the
model's own abilities rather than about the task.

- **GLM-5.3-Flash** (`z-ai/glm-5.3-flash`, via OpenInference), in a session
  on 2026-09-26. Four steps and about 11k tokens into a turn, its reasoning
  said: "the system prompt mentions run_code, grep, glob, bash, commit, but
  the actual function list only has read, ls, edit, write." The turn's record
  shows sixteen tools offered, and the tools array and the log's
  `offered_tools` both come from `toolDefs()`. In the same turn it said `read`
  truncates long lines with "…". It does not: `strument tool read` returns
  the same files whole. It had read a hard-wrapped Markdown line, which ends
  mid-sentence, as a cut one. The turn then ended on "I'll use a small program
  to dump the docs fully", with no tool call.
- **MiMo** believed it could not run `bash`. This is reported by the
  maintainer; the transcript has not been read here.

**Measured:** GLM, asked at the start of a fresh conversation, pinned to the
same provider, to list its tools named all seventeen it was sent, four times
out of four. So every tool reaches the model. What goes wrong is the model's
picture of its tools later in a turn.

## Hypotheses

**H1: active parameters, not size or benchmark strength.** GLM is 320B total
but 18B active. Ling 3.0 Flash, which in
[`2026-09-read-outline-small`](../2026-09-read-outline-small/) retried a
declined command five times and invented grep parameters, is also a
small-active MoE, as is MiMo Flash. Recalling a list from thousands of tokens
back, while fresh tool results compete for attention, is retrieval under
distraction. It may fail with the active slice regardless of what the model
scores elsewhere. If so, no wording in the harness fixes it.

**H2: the disclosure has more than one source of truth.** A model learns
what it can call from four places:

- the tools array;
- the system prompt's prose, which names tools;
- descriptions that point at other tools, such as `run_code`'s "the bash tool,
  not this one, runs commands";
- one sentence shaped like the answer itself.

That sentence is in `run_code`'s description (`codeTool` in
`internal/coder/codetool.go`): "The callable functions are exactly: read,
grep, glob, ls, symbol." The `codeFuncDoc` text beside it adds `read_text`
and its siblings. For a model searching its context for "what can I call?",
this is the most authoritative-looking line available. It says "exactly" and
"functions", and it lists a subset. GLM's reasoning reconciled "the system
prompt" against "the actual function list", which is two lists. This sentence
is the best candidate for one of them.

The fit is not exact. GLM's four were read, ls, edit and write, and edit and
write are not in the `run_code` list. It may have blended that list with the
tools it had just been using. MiMo's `bash` belief fits better, since "the
bash tool, not this one" sits next to the list.

H1 and H2 can both hold.

## Design

**A replay probe.** Take a real session up to the step where the false belief
appeared. Send it with one extra user message: "List the exact names of the
tools you can call right now, comma-separated, and nothing else." Score the
answer against the tools that request offered.

Arms, each on the same prefix:

| arm | change |
| --- | --- |
| A | none: the request as Strument sent it |
| B | `run_code`'s sentence reworded so it cannot be read as the tool list: "Inside a program, these tools are also callable as functions: …" |
| C | `run_code` removed from the tools array |
| D | a fresh context: system prompt and tools, no history (the control; measured 4/4 correct for GLM) |

- **Metric:** counts. Tools named that were not offered, plus offered tools
  left out, per answer.
- **Models:** three or four, spread over active-parameter size. For example:
  - GLM-5.3-Flash (18B active);
  - MiMo-V2.6-Flash;
  - one dense model of similar benchmark standing;
  - one frontier model as a ceiling.
- **Prefixes:**
  - the GLM session above, which needs its blob store from the maintainer's
    machine, because the log keeps tool results as hashes;
  - the MiMo `bash` session;
  - two or three long sessions from past trials, where no anomaly was seen,
    so the probe is not tuned only to the failures.
- **Repetitions:** enough per cell to see a rate. The answers are short, so
  this costs cents.

## What each outcome would mean

- **B fixes what A gets wrong:** H2. A one-line wording change, and the cheap
  outcome. Check that B does not cost `run_code` uptake before shipping it.
  [`2026-09-code-mode2`](../2026-09-code-mode2/) shows that uptake is
  sensitive to wording.
- **Only D fixes it, and errors track active size:** H1. Wording will not help.
  The harness could restate the available tools where the beliefs seem to
  start: in the result of a declined or failed call. That would be its own
  trial.
- **A is already correct at depth:** the probe does not reproduce the belief.
  The question then is what in those turns produced it, and the next step is
  reading the steps just before the anomaly, not changing prompts.

## Caveats

- **Asking for the list is not the same as acting on a false one.** A model
  can recite its tools correctly when asked and still act as if one is
  missing. The probe measures recall, which is necessary for acting
  correctly, not sufficient. A positive result on B should be checked
  against real sessions.
- **Two anomalies are two anomalies.** This design exists because they looked
  alike, and that resemblance is the first thing it tests.
