# Do models lose track of their own tools mid-turn?

**Status: trial.** Preregistered in [`preregistration.md`](preregistration.md)
(`3cb4fab`). The design this started from is below the results, as written
before the run.

**Result: neither hypothesis is supported, and the probe reproduced the
anomaly anyway.**

- **H2 is not supported.** Rewording `run_code`'s "the callable functions
  are exactly" sentence changed nothing: 14 wrong answers in 119 against 12 in
  120, p = 0.68.
- **H1 is not supported.** The request as sent was wrong in 12 of 120 answers,
  and a fresh context in 5 of 120 (p = 0.13). The errors do not rank with
  active size: Qwen3.8-27B (dense) and Ling 3.0 Flash (small-active) made
  none, GLM-5.3-Flash made most, and GPT-6 Luna, the frontier model, came
  second.
- **What the probe did find, found after the fact:** late in a session, GLM
  reports a **working set** rather than its tools. In `glm-docs-0` it
  answered `read, glob, edit, write, bash, commit` three times out of three.
  That is the tools it had used, plus the core tools for changing files, and
  it is close to the original claim of "only read, ls, edit, write". This
  matches neither hypothesis. It is an observation to test, not a result.

**Decision:** no wording change. Neither the rule nor the data supports one.

## Results

Two prefixes per session (the middle request and the last), 4 arms, 3 reps:
480 calls, 1 with no answer. "Wrong" means an answer that named a tool not
offered, or left one out.

| arm | wrong | tools invented | tools left out |
| --- | --- | --- | --- |
| A, the request as sent | 12/120 | 4 | 54 |
| B, the sentence reworded | 14/119 | 2 | 78 |
| C, `run_code` removed from the tools | 22/120 | 31 | 33 |
| D, a fresh context | 5/120 | 4 | 3 |

Wrong answers per model, out of 24 answers per arm (23 for GLM under B):

| model | A | B | C | D |
| --- | --- | --- | --- | --- |
| GLM-5.3-Flash | 7 | 10 | 6 | 2 |
| GPT-6 Luna | 4 | 3 | 0 | 2 |
| Ling 3.0 Flash | 0 | 1 | **15** | 0 |
| MiMo-V2.6-Flash | 1 | 0 | 1 | 1 |
| Qwen3.8-27B | 0 | 0 | 0 | 0 |

**GLM narrows late.** Every GLM error under A came from a session's last
request, not its middle one. The tools it drops are always the same
auxiliaries: `about`, `ask_user_question`, `interrupt`, `symbol`,
`webfetch`, and often `run_code`. What it keeps are the tools it used and the
ones that change files:

| session | tools used before the probe | GLM's answer, three times out of three |
| --- | --- | --- |
| `glm-docs-0` | `read`, `glob` | `read, glob, edit, write, bash, commit` (once with `run_code` added) |
| `glm-code-0` | `bash`, `read`, `edit` | `read, grep, glob, ls, edit, bash, commit` |

In its other two sessions GLM listed all fourteen, at least twice of three. The
same prefix gives the same narrowed answer each time, so the history is what
causes it, not sampling.

**Ling believes the prose over the tool list.** With `run_code` removed from
the tools but still named in the system prompt, Ling listed it in 15 of 24
answers. Of the other four models, only GLM did, 4 times in 24.

**Names from inside a program leak out, rarely.** Twice, once from Luna under
A and once from GLM under B, an answer listed `read_text` and `read_bin`.
Those are functions `run_code` offers only to programs. That is H2's worry in
the other direction: the description's list read as more tools, not fewer.
Two answers in 480 do not make a case for a change.

**Luna omits `ask_user_question`.** Its errors are one or three tools, most
often that one, including in a fresh context. It appears to read it as
something other than a tool.

## What this licenses

- **It licenses leaving `run_code`'s description as it is.**
- **It does not explain GLM's original session.** It shows a behavior of the
  same shape: late in a session, GLM's picture of its tools narrows to the ones
  in use. The next test of that would be a probe mid-task, not a question,
  and it has not been run.
- **Arm C is a warning for any mode that withholds a tool while the prompt
  still names it.** Ling will name it, and may call it. Whether Strument has
  such a mode was not checked here.

## Files

| file | contents |
| --- | --- |
| [`preregistration.md`](preregistration.md) | the pilot, the arms and the rule, committed before the run |
| [`data/fixture.py`](data/fixture.py) | Larkspur, the invented project |
| [`data/gen.py`](data/gen.py) | records the sessions through `strumentrec` |
| [`data/probe.py`](data/probe.py), [`data/score.py`](data/score.py) | the replay, the arms, and the scorer with its self-test |
| [`data/probe.jsonl`](data/probe.jsonl) | all 480 answers |
| [`data/sessions.json`](data/sessions.json) | the 20 recorded sessions |

# The design, as written before the run

### What prompted it

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

### Hypotheses

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

### Design

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

### What each outcome would mean

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

### Caveats

- **Asking for the list is not the same as acting on a false one.** A model
  can recite its tools correctly when asked and still act as if one is
  missing. The probe measures recall, which is necessary for acting
  correctly, not sufficient. A positive result on B should be checked
  against real sessions.
- **Two anomalies are two anomalies.** This design exists because they looked
  alike, and that resemblance is the first thing it tests.
