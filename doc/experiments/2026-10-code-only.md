# Code-only observation: does forcing `code` beat persuading it?

2026-08-31. The 2026-09 trial (`2026-09-code-mode2.md`) moved `code` uptake
from 0/36 to 8/24 with two sentences of prose, and left the round-trip claim
unproven. This trial runs the complementary condition: instead of persuading
the model to prefer the program, **remove the direct read-only tools** and
make every observation go through `code`.

## Design

One factor, two arms, one task, three models, three reps — 18 runs, shuffled,
seed 20260901.

| arm | read/grep/glob/ls/symbol | code | config |
| --- | --- | --- | --- |
| **BASE** | offered directly | offered | default |
| **CO** | withheld from the schema | forced on | `observation_via_code = True` |

Under CO a direct observation call the model makes anyway is answered with a
redirect — the tool name, the model's own arguments, and a worked program —
rather than `"Unknown tool"`. The prompt tracks the schema in both directions:
the read/grep/glob/ls paragraph and the nothing-pinned note have force-arm
variants that route observation through programs.

**Task**: the 2026-09 caps task, answer key re-verified against the tree by
hand before the run. It is a multi-lookup question (three constants, three
files), so it is CO's home stratum: one program can fetch all three.

**A fix first, found by the smoke run.** `runCode` had never registered
Monty's print handler: every `print()` output was dropped, and a
print-driven program — most model-written programs — returned `None`. The
2026-09 BASE runs must have carried this silently (its programs used `print`
too; its description promised "print() shows intermediate values"). Fixed
before this trial; both arms here run with the fix. Any comparison to the
2026-09 numbers carries that difference.

## Results

| | BASE | CO |
| --- | --- | --- |
| median round trips | **4** | **13** |
| mean round trips | 4.3 | 14.8 |
| answers fully correct | 17/27 | 17/27 |
| answers ≥2/3 correct | 27/27 | 27/27 |
| `code` calls per run | 0–3 | 7–23 |
| program failures (JSONL) | — | 10 across 116 programs |
| total cost | $0.036 | $0.224 |

Per model (round trips, reps pooled):

| model | BASE | CO |
| --- | --- | --- |
| mimo | 7, 6, 4 | 12, 10, 17 |
| qwen | 4, 3, 4 | 11, 23, 24 |
| glm | 5, 3, 3 | 13, 8, 15 |

**The force arm lost its own headline claim.** Round trips did not fall; they
rose by a factor of 3–4, in every model, in every rep. The mechanism is
visible in every CO transcript: the model writes **one program per round
trip**, each doing exactly one or two lookups — `mimo-CO-0` made 9 program
calls over 10 round trips. Forcing observation through `code` converted the
*medium* (grep calls → programs) without converting the *granularity* (one
lookup per request). Batching is a planning skill, not a tool-availability
property, and the schema cannot supply it.

**Correctness held.** Every run in both arms answered at least 2 of 3 items;
the misses cluster in the scorer, not the transcripts: answers write "60,000"
or "60_000" and the key matches bare "60000", and two runs cite the file a
search surfaced rather than the definition's file. Reading the transcripts,
there is no case of a wrong value retrieved.

**Zero redirects fired** — 0 direct read-only calls across all 9 CO runs. With
the tools absent from the schema, habit did not reach for them; the redirect
net went unused. The models that would have needed it spent their effort
elsewhere.

**Program failures were real but not fatal**: 10 failed programs in 9 runs
(116 programs), from three causes — full-Python attempts (`import os`,
`import glob`, `from subprocess import ...`), a `glob()` call with a positional
second argument looping until the 5 s limit, and one program that tripped the
50-call bridge cap. No run got stuck in a repair loop; each failure cost one
round trip.

Two smaller finds, from reading transcripts:

- The bridge cap's error text says "stop and do the rest with direct tool
  calls" — advice that is wrong under CO, where the direct tools do not
  exist. The text needs a force-arm variant if the mode ships.
- `jsonlog` rendered several program results one character per JSON record
  (`"T"`, `"h"`, `"e"`...), mangling the log that every scorer here reads.
  The rendered transcript was fine; the count came out of the right half by
  luck of the regex. This is a recording bug worth its own fix before the
  next JSONL-scored trial.

## What to conclude

- **Availability is not granularity.** The 2026-09 result — prose moves
  uptake — and this result — forcing the medium does not batch the work —
  are the same finding from two sides: models adopt the tool they are
  pointed at, but neither prose nor removal of alternatives changes how many
  lookups they plan per step. If round-trip saving is the goal, the lever is
  planning (few-shot examples showing a multi-lookup program, or the runtime
  nudge from fable.md), not the schema.
- **The mode is safe but expensive.** No correctness cost, no redirect
  thrash, no repair loops — but 3–4× the round trips and 6× the cost for the
  same answers. `observation_via_code` stays off by default and is now
  documented as the arm it is.
- **The null was worth the run.** It closed the cheapest remaining
  explanation ("the tools compete with `code`; remove them and programs
  win") and it paid for the print-output fix, which affects the tool in its
  normal mode.

**Verdict: the force arm fails its primary metric and is not a mode to
ship.** The feature stays as the counterfactual arm; the open experiments are
the ones that target planning — few-shot examples first, the nudge second.

## Method notes

- Runner: `/tmp/code-only-trial/run.py` (per AGENTS.md, bulk data stays out
  of the repository; this report is the durable part). 18/18 runs completed;
  no timeouts, no empty responses, no loop stops in either arm.
- Metrics are counts from the JSONL transcripts, not the rendered output —
  except program failures, where the JSONL had to be re-read: the first-pass
  scorer keyed on `"The program failed"` in the rendered text and missed the
  mangled records (see the jsonlog note above).
- The 2026-09 report's BASE arm ran the unfixed `code` tool; this trial's
  BASE is the first BASE that delivered print output. BASE `code` uptake
  here (9 of 27 runs used it) is consistent with 2026-09's DESC arm at this
  smaller n; no cross-trial comparison is made beyond that note.
