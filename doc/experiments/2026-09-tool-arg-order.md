# Naming `path` first: a real effect, and three kinds of model

**2026-09-05.** 112 live sessions, seven models, two arms, arm order shuffled
(seed 20260905). $0.24. Data, runner and scorer in
`2026-09-tool-arg-order-data/`.

## Why

A local Qwen3.6-35B-A3B wrote a 663-line file and the diff appeared in one
block when the call ended, instead of scrolling past as it was written. Another
model streamed the same kind of write line by line.

The cause was the order of the fields inside the call's arguments. Qwen sent
`{"content": …, "path": …}`, and `render.ToolDiff` cannot draw a diff line
until it knows which file the line belongs to, so every line sat in its pending
buffer until the path arrived at the end.

The order came from us. `llm.ToolDef.Parameters` was a `map[string]any` and
`encoding/json` sorts map keys, so the schema Strument advertised led with
`content` for `write`, and read `new_string`, `old_string`, `path` — path
*last* — for `edit`. A model that fills arguments in schema order sends the
payload before naming the file.

The fix is one line of intent and a small type: advertise `path` first. The
question this trial answers is whether models care.

## Design

Two binaries, differing only in the serialized schema. Verified on the wire
before spending a cent (`wire-check.jsonl`):

| arm | `write` | `edit` |
| --- | --- | --- |
| **A** map, sorted | `content, path` | `new_string, old_string, path` |
| **B** ordered | `path, content` | `path, old_string, new_string` |

One message per run into an empty directory, asking for a file to be created
and then changed, so a run exercises both tools. 7 models × 2 arms × 8 reps,
job list shuffled.

**Metric:** does the call name `path` before the payload field? A count, read
off the raw arguments string — not a judgement, and not `json.loads`, which
would report what the keys *are* rather than the order they arrived in. The
scorer self-tests offline in both directions before the runner spends
anything, including against the trap that broke the first draft: a file whose
*contents* mention `"path":`.

Primary unit is the first `write` and first `edit` call of each run — one
independent observation each, and the one a user meets, since it is the first
diff of the turn. Every-call counts are reported beside it and agree.

## Result: `write`

| model | A | B | p |
| --- | --- | --- | --- |
| deepseek-v4-flash | 8/8 | 8/8 | 1.00 |
| hy3 | 8/8 | 8/8 | 1.00 |
| qwen3.6-35b-a3b | 6/8 | 7/8 | 1.00 |
| mimo-v2.5 | 0/8 | 5/8 | 0.026 |
| qwen3.8-27b | 3/8 | 8/8 | 0.026 |
| gpt-5.6-luna | 0/8 | 8/8 | 0.0002 |
| glm-5.3-flash | 0/8 | 0/8 | 1.00 |
| **pooled** | **25/56** | **44/56** | **0.0004** |

## Result: `edit`

| model | A | B | p |
| --- | --- | --- | --- |
| deepseek-v4-flash | 8/8 | 4/4 | 1.00 |
| hy3 | 8/8 | 8/8 | 1.00 |
| qwen3.6-35b-a3b | 6/8 | 7/8 | 1.00 |
| mimo-v2.5 | 0/8 | 2/8 | 0.47 |
| qwen3.8-27b | 1/3 | 6/6 | 0.083 |
| glm-5.3-flash | 0/8 | 0/8 | 1.00 |
| **pooled** | **23/43** | **27/42** | **0.38** |

Luna never called `edit` at all: it did the whole task in one `write` in all
sixteen runs. The `edit` denominators vary for the same reason — a run that
wrote the finished file once has no edit to score.

**`write` moves; `edit` does not, at this n.** The gap is not a contradiction.
`edit`'s arm-A order was the *worse* of the two — path last, not merely second
— yet three of six models already put path first regardless. A model with a
strong prior about how an edit call is shaped has nothing to gain from the
schema, and the models without that prior are the two that also ignore it for
`write`.

## Three kinds of model

Reading the transcripts rather than the totals, the panel splits cleanly, and
the split is more useful than the pooled p-value:

- **Follows the schema.** Luna (0/8 → 8/8) and qwen3.8-27b (3/8 → 8/8) emit
  arguments in the order they were offered. For these the change is the whole
  difference between a diff that streams and one that does not.
- **Follows a convention.** DeepSeek, hy3 and (mostly) qwen3.6-35b put `path`
  first in both arms. They were already fine; the change costs them nothing.
- **Ignores the schema.** GLM-5.3-flash emitted `content, path` and
  `new_string, old_string, path` in **34 of 34** calls across both arms —
  byte-identical behaviour whichever order it was given. MiMo is the same
  prior, partially overridden: 5/8 and 2/8.

GLM's fixed order happens to be alphabetical. This trial cannot separate "it
sorts the keys" from "it has a convention that coincides with the alphabet",
because both tools' payload fields sort before `path` and GLM called no other
multi-argument tool. The probe that would separate them — a payload field named
so that it sorts *after* `path` — was not run, because renaming `content` would
cost more in model familiarity than the streaming is worth.

**So the honest claim is that this makes the fast path reachable more often,
not that it fixes streaming.** GLM will still print a write in one block, and
the render-side buffering remains what makes that output correct.

## Counter-metrics

Reordering a schema is exactly the kind of change that could nudge a model into
filling the fields differently and getting an edit's span wrong, so that is the
thing to watch.

| | A | B |
| --- | --- | --- |
| edits whose search text did not match | 0 | 1 |
| runs not ending in Success | 2 | 0 |
| median steps per run | 2.0 | 2.0 |
| mean cost per run | $0.00205 | $0.00219 |

Nothing moved. The single missed edit and the two non-Success runs are all
qwen3.6-35b, and reading them settles what they are: both "failures" completed
the task and then hit `Empty response received from LLM` on a follow-up
request — a provider artifact, and their landing in arm A is a coincidence of
n=2, not a result.

Five runs were flagged as anomalous (non-Success, or more than six steps) and
all five were read. The long ones are models trying to verify their own work
with `run_code` and fighting the restricted interpreter, in both arms.

## What shipped

`orderedProps` in `internal/coder/schemaorder.go`, used by `edit`, `write` and
the anchored `edit` — the three tools whose arguments are rendered as a
streaming diff. Everywhere else a map is still the plainer thing to write, and
the order changes nothing anyone can see.

## The rig note worth keeping

The suite could not have caught the original bug, and the reason generalises.
Every ordering test in `internal/render/toolargs_test.go` compared the
*finished* diff, which is byte-identical whichever order the arguments arrive
in. The whole defect lived in *when* the bytes reached the terminal, and
nothing asserted that. `TestToolDiffStreamsWhenPathComesFirst` now does.

Its control is the interesting part. Disabling the header resolution in
`emitLine` left the test green; so did disabling the one in `onArg`. Two
independent places resolve the header, and only removing both makes the test
fail. A reading of the source would have called either one sufficient to break
streaming, and been wrong — which is the argument for running the control
rather than reasoning about it.
