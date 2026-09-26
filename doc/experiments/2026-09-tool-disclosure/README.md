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

## What the panel does

A survey of the six harnesses AGENTS.md names, read on 2026-09-26. Each
entry is a dated observation of the source at the commit given, not a fact
about the project.

| harness | commit | how tools are disclosed |
| --- | --- | --- |
| deepseek-harness | `477b4f4` (2026-09-24) | `native`, `ptc` (code mode) or `both`, one choice per agent |
| OpenCode | `b65de4d` (2026-09-26) | code mode shows a token-budgeted catalog that says whether it is complete |
| Codex | `e72da2b` (2026-09-26) | each tool declares its exposure surfaces |
| Kimi Code | `be7d5f5` (2026-09-24) | a core set, then `<tools_added>`/`<tools_removed>` diffs |
| Pi | `2b0a123` (2026-09-26) | an "Available tools" prose section, built from the selected tools |
| Claude Code | closed source | deferred tools named in a reminder, schemas loaded on request |

**deepseek-harness** is the closest to this trial's question, and it has
three rules worth keeping:

- **It separates program bindings from tools in so many words.** The code
  mode prompt says: "The declarations below are SDK bindings for this program.
  A declaration does not make its name a directly callable tool; only names
  supplied as separate tool schemas may be called directly." The
  declarations sit under the heading "Program-only SDK bindings:". This is
  exactly the confusion behind the two `read_text`/`read_bin` answers here.
- **The prompt's rule and the enforcement share one predicate.** The comment
  at `packages/core/tools/src/index.ts:880`: "The SAME predicate the executor
  denies by, so the prompt cannot state a rule the registry does not
  enforce." Arm C is what happens without that guarantee.
- **Order is part of the disclosure.** In pure code mode, the rule that only
  `run_code` may be called comes earlier in the prompt than the declarations,
  "so the model reads which tools it may call before it reads what each one is
  for". A second presentation declared for the same agent is refused: "two
  answers to 'which form does the model see' is a contradiction, not an
  override."

**OpenCode's** code mode catalog "states exactly how comprehensive it is —
overall (COMPLETE vs PARTIAL) and per namespace". Every namespace is listed,
even at a budget of zero, and a search call is always available. Strument's
`run_code` sentence says "exactly" about a list that is complete only for
program bindings, and OpenCode's rule would make it say which.

**Codex** models the question in its types. Each tool has exposure surfaces:
- `Direct`, in the tool list;
- `Deferred`, found through `tool_search`;
- `CodeModeOnly`, callable from programs "without including it in the initial
  model-visible tool list".

Strument's `read_text` and `read_bin` are `CodeModeOnly` in those terms. Its
search tool tells the model its list is partial ("Some of the tools may not
have been provided to you upfront"). Its base instructions correct a name
directly: "NEVER try `applypatch` or `apply-patch`, only `apply_patch`".

**Kimi Code** discloses incrementally. Tools beyond a core set are announced
as `<tools_added>` and `<tools_removed>` blocks, and the model is told to
"fold all announcements in this conversation in order to get the current
list". Its wording is the most prohibitive in the panel: "never passed to
select_tools", "plugin, skill, or category names do not work", and "Names
listed as removed are no longer loadable — do not select them". An example
script probes each model live, per model, much as this trial did.

**Pi** keeps a prose "Available tools" section, as Strument does, but builds
it and its guideline bullets from the selected tools, so the prose follows
the schema. Its default selection is `read, bash, edit, write`.

**Claude Code** is closed source. As its own sessions show, tools beyond a
core set are named in a system reminder. Their schemas load through a search
tool, and the reminder says that calling a named tool before loading it
fails.

### What the survey suggests for Strument

Nothing here is measured. These are candidates, not decisions.

1. **Label the program-only names.** Say in `run_code`'s description that
   `read_text`, `read_bin` and the bridged names exist inside programs and are
   not tools to call directly, in deepseek-harness's terms. Arm B did no harm,
   and this targets the one confusion the probe actually saw.
2. **Build prose mentions of tools from the same predicate as the schema.**
   That is Pi's and deepseek-harness's rule, and arm C shows why. First, check
   whether any Strument mode (ask mode, `observation_via_run_code`) names a
   withheld tool in its prompt.
3. **A guess, not a finding.** GLM's narrowed answers resemble Pi's default
   of `read, bash, edit, write`. Models trained on transcripts from minimal
   harnesses may fall back on that set when their picture of the actual tools
   blurs. Nothing here tests that.

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
