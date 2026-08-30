# Does the `code` tool get used, and does it buy round trips?

2026-08-30. Parts 1–3 of the plan shipped a `code` tool — Monty, a restricted
Python interpreter in WASM, plus a bridge that lets a program call the five
read-only tools. The claim motivating it was arithmetic in reasoning
(`2026-08-skill-uptake.md`: ~2,300 reasoning tokens per run spent on
arithmetic) and runs of observation calls costing a round trip each
(`2026-08-symbol-uptake-data/`: 4.0 removable round trips per run on an
exploration task). This trial asks the question that decides whether it earns
its place, and asks it first: **do models use it at all?**

**They do not. 0 of 36 runs called `code` — including the arm built around it.**
The recommendation is against shipping it as a default tool, on the evidence
below. The tool itself works; that was verified separately, and it is the
interesting part.

## Design

Three arms, one tree, one task, six models, two reps each — 36 runs, order
shuffled across the whole plan, seed `20260830`.

| arm | binary | what it offers |
| --- | --- | --- |
| **A** `no-code` | feature reverted | the baseline |
| **B** `code` | `code` tool | the feature |
| **C** `code+bridge` | `code` tool with the read-only bridge | the full plan |

The trial intended B to be pure computation only, with C adding the bridge.
That arm was never built: B and C are the same program. Both were built from
the bridge commit — B from HEAD, C from the bridge commit, and HEAD *is* the
bridge commit. The wire check caught it (see the rig notes below), and rather
than spend on a two-arm trial mislabeled as three, the run went ahead as
A vs `code`+bridge: the question "does the tool get used" does not depend on
which half of the tool is offered, and uptake was the pre-registered first
metric.

**Task.** *"In this repository's Go code: the coder caps the size of a single
tool result, caps the number of work steps per turn, and caps the chat history
budget. Give the value of each of those three caps and the file the constant
lives in."* It names no tool and needs three separate finds
(`toolobserve.go`, `coder.go`, `send.go`) — the exploration shape that left
4.0 removable round trips per run in `2026-08-symbol-uptake`.

**Answer key**, verified by hand before any run: `maxToolOutputBytes = 60_000`
(`toolobserve.go`), `MaxSteps = 25` (`coder.go`), `maxChatHistoryTokens =
context/8` with a 2048 floor (`send.go`).

**Models.** deepseek-v4-flash, gpt-5.6-luna, qwen3.8-27b, hy3, MiMo V2.5,
glm-5.3-flash — the usual six, two reps per arm, shuffled (seed `20260830`).

## Results

| | A no-code | B code+bridge | C code+bridge |
| --- | --- | --- | --- |
| **called `code`** | — | **0/12** | **0/12** |
| mean round trips | 7.8 | 7.8 | 6.8 |
| answered correctly | 12/12 | 12/12 | 11/11 answered |
| mean tool calls | 11.4 | 13.4 | 11.6 |
| mean cost | $0.0098 | $0.0108 | $0.0088 |
| longest consecutive observation run | 14 | 27 | 12 |

**The primary metric is zero everywhere, and that is the result.** The feature
was offered 24 times (arms B and C) and taken 0 times. Per model, per arm, per
rep — nothing. This is the `replace_all` shape from [§18](../experimenting.md):
a feature the model may decline was offered, not applied, and the round-trip
numbers across arms are the same number wearing different labels (7.8 vs 7.8).
No amount of additional arms would have extracted an effect from a treatment
that was never applied.

The counter-metric came back clean, for what it is worth with n=0 treated:
answers were correct wherever the run finished, and cost was flat
($0.0098/$0.0108/$0.0088 per run). Nothing broke. Nothing improved either,
because the tool never ran.

### The mechanism, which the probes isolate

The zero is a model *choice*, not a broken arm. Established live, after the
trial, at probe cost:

- **The tool is reachable.** Asked to list its tools, MiMo under arm C names
  `code` among the twelve. Asked whether the code tool can call
  `read`/`grep`/`glob`/`ls`/`symbol` from inside a program, it answers yes.
- **The tool works.** *"Use your code tool to compute 17×23 + √2 to 4 decimal
  places"* → MiMo calls `code` with `round(17*23 + math.sqrt(2), 4)`, returns
  `392.4142`, 2 steps, $0.0001.
- **The bridge works.** A program `grep(pattern='TODO', glob='**/*.go')` ran
  through Monty, the bridged call was announced as `‹code› grep` exactly like a
  direct call, and the count came back correct — one round trip where a direct
  `grep` plus a follow-up would have been two.
- **Uptake is the model's choice.** Same binary, same session shape: when the
  *task names the tool*, MiMo calls it immediately. When the task does not, it
  greps. Over 36 runs it never occurred to any model that a program was an
  option.

That last sentence is the finding. The plan's motivating measurements —
arithmetic eating a quarter of the reasoning, 4.0 removable round trips per run
— are both real, and both remained in the treatment arm: arm B still averaged
6.8 consecutive-observation runs, and the longest single run of observation
calls in the whole trial was **27 assistant messages** (`mimo-B-1`, which also
burned $0.03 of a $0.12 trial compacting a 98k-token history it had built by
grepping in circles). The remedy sat in the schema the whole time. Models do
not reach for a calculator to do arithmetic they can do in their head, even
when the head-work is the expensive part.

## The rig, and what went wrong running it

Four things, all caught, each one a handbook section confirmed:

- **The data directory did not belong in the repo.** The first run put
  `worlds/` — a 257 MB repo copy per run — under `doc/experiments/`, and the
  trial's own `copytree` initially recurred into itself (the rig now excludes
  `worlds/` and `runs/` by name). The whole tree moved to `/tmp`, which meant
  `run.py`'s `REPO = HERE.parent.parent.parent` silently resolved to `/` and
  the retried worlds were built from `/` — two `boot`/`home` directories and a
  config. *Tell:* a world that contains `boot`. The runner now names the
  checkout explicitly. The 7.4 GB of worlds were deleted after scoring; the
  transcripts in `runs/` are the record.
- **Ten runs died mid-stream** in the first pass — streaming began, then
  nothing, no `[TIMEOUT]` marker, arms A/B/C all affected. The resumable
  runner re-ran them; they completed on retry. Proxy flakiness, not arm
  effects, and the reason the runner is resumable by design.
- **Three runs were killed by the operator, not the model.** A foreground
  `for` loop over arms hit the 2-minute shell timeout and SIGTERM took the
  in-flight Strument processes with it; the JSONL turn records read
  `outcome: "Interrupted"` at identical timestamps. Re-run cleanly, all three
  succeeded. This is the §19 tell from the other side — a runner that dies
  quietly looks like a slow run — and the reason the runner is resumable.
- **The pre-registered scorer had two false negatives**, found by reading
  transcripts against the table: it misses `60_000` (three runs answered with
  the underscore form) and `**ANSWER:**` (markdown bold around the marker).
  Both directions are the same bug as §15's two reasoning forms: the renderer
  has more shapes than the scorer. `results.json` records the lenient re-score
  per row; the direction of every correction was upward, so the trial's null
  is not an artifact of it.

The arms were compared before spending, per the standing rule. Arm A has no
`code` tool (live probe: "Do you have a tool named `code`?" → "No"). Arms B and
C **do not differ**: identical symbol tables, identical `code` schema. The
report from the interrupted session had flagged this; the symbol table settled
it. The consequence is honest and limits the trial: it measures A vs
code+bridge, and says nothing about the bridge *versus* pure computation. Given
the uptake result, settling it would have been spending to answer a question
about a tool nobody used.

## What to conclude

- **Recommend against shipping `code` as a default tool.** 0/36 uptake on the
  exact task shape the feature targets, with the tool working and the bridge
  verified live. The `replace_all` precedent is now 1/18 and 0/36: two
  features, both correct, both unused when unnamed. A tool that costs a schema
  entry and ~200 tokens of description on every request needs an uptake number
  above zero, and there is no sign of one here.
- **The task shape is not the explanation.** It was built to the measured
  specification that produced 4.0 removable round trips per run, and the
  observation runs happened — 13 in arm A, 12 in B, 12 in C, longest 27. The
  phenomenon the bridge targets is present in the transcripts. The models
  grep and read their way through it and never consider a program.
- **The one live use of `code` in this whole effort was the probe that named
  it.** Uptake follows the prompt, not the schema. If the tool ever ships, the
  description would have to do what the `symbol` fix did — lead with the
  question it answers — and this trial gives no evidence that any sentence
  fixes a zero this clean.
- **What the trial does not say:** whether `code` helps on tasks that are
  arithmetic-shaped (the fixtures of `2026-08-skill-uptake`, where 2,300
  reasoning tokens per run went to joining numerals). This trial measured
  exploration, because that is where the round trips were measured. A
  calculator-only arm on an arithmetic fixture is the experiment that remains,
  and it is cheap: the probe run — one `code` call, correct answer, $0.0001 —
  is the shape to pilot it with.
