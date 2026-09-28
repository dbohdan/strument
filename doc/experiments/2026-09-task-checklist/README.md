# A task checklist tool: piloted, not run

September 2026. Two pilots, no main run, nothing preregistered: the pilots
showed the planned measure had nothing to measure.

**Result: on a one-turn request, models do not drop items, so a checklist has
no completion problem to fix there. Offered with no nudges, two of four models
never called it and one used it as a three-phase progress bar. Nothing ships;
the prototype stays out of the tree.**

## The idea

Claude Code gives its model a task list: create items, mark them in progress
and done, visible to the user. Strument has considered and rejected both
hidden notes-based memory and plan mode. A small checklist looked like a
reviewable alternative: every write a visible tool call, the whole state a few
lines, nothing in it that belongs in `AGENTS.md`. The question was whether it
does anything for the work, offered the Strument way — in the tool list, with
its description as its only mention, and no reminders.

## Prototype

[`data/prototype.diff`](data/prototype.diff): a `task` tool taking `add` (item
subjects) and `set` (id and status: pending, in_progress, done, dropped). At
most 12 items, the whole list returned on every call, each change printed on
screen and recorded in the session log as a `task` row. Exempt from the
read-only loop count. Its description:

> Keep a short checklist of this turn's work, which the user sees as it
> changes. Add the parts of a request as items, then set each to in_progress
> and to done as you go, or to dropped, saying why in your answer. Every call
> returns the whole list. At most 12 items. Optional: for a request with
> several parts.

## Pilots

A small Go CLI ([`data/world/`](data/world/)) and one request written as a
paragraph, not a list, since a paragraph is where items get lost. A script
([`data/run.py`](data/run.py)) checks each item, several by running the
program; the untouched world scores 0 and a reference solution scores every
item, with `go vet` and `go test` passing.

**First pilot, 11 items** (page size, decimals, a date layout with README note
and test, a rename, an error message, a `-version` flag, a version bump, a
deletion, a changelog section). Baseline only, GPT-6 Luna and MiMo-V2.6-Flash,
two runs each: **11/11 in all four**, vet and tests green. These run
directories were deleted when the pilot was rerun; the numbers are from the
session that ran them.

**Second pilot, 17 items**: the same, plus a CSV output mode first (flag,
header row, quoting of memos containing commas, tests) and two more small
items after (a test for `+1250` amounts, README notes for both new flags).
Luna, MiMo, GLM-5.3-Flash and Ling 3.0 Flash, one run per arm
([`data/pilots.jsonl`](data/pilots.jsonl), [`data/records/`](data/records/)).

| model | baseline | checklist arm | checklist calls | what the list held |
|---|---|---|---|---|
| Luna | 17/17, 23 steps | 17/17, 24 steps | 4 | three phases: inspect, implement, tests and docs |
| MiMo | 17/17, 13 steps | 17/17, 10 steps | 0 | — |
| GLM-5.3-Flash | 17/17, 10 steps | 17/17, 10 steps | 0 | — |
| Ling 3.0 Flash | 17/17, 74 steps | 17/17, 28 steps | 1 | nothing: its call was refused |

Every run completed every item, with vet and tests passing and no unrequested
file changed. Ling's 74 against 28 steps is one run each and says nothing.

## What the pilots show

**No completion problem in one turn.** Ten runs, two task sizes, four models,
all complete. The models include GLM-5.3-Flash, which benchmarks highest of
the four and was included for its known habits of looping and of losing track
of its tools, and Ling 3.0 Flash, which reached everything in 74 steps. A
checklist cannot improve 100%.

**Uptake, unprompted, is thin and coarse.** Luna's list tracked phases rather
than requests: a dropped requirement could not have shown on it. Ling made the
most useful attempt, a list of seven items by file, and sent each as an object
where the schema asked for a string. The prototype answered "The arguments
were not valid JSON", which was false — the JSON was valid, the shape was
wrong — and Ling did not try again. That is a defect in the prototype, and a
reminder that a failure message that misnames the failure costs the call it
answers.

## Where the value would be, and why it is doubtful

A checklist's plausible value is not completion within a turn but state that
outlives the context: across turns, and across a compaction that folds the
request into a summary. The Claude instance that ran these pilots used Claude
Code's task list across several compactions in the same session, and the list
came back each time — but by re-injection, in reminders the harness sends
unprompted ("The task tools haven't been used recently…", with the list
attached). Without those, a model that has lost its context has no reason to
know the list exists. Strument does not send unprompted nudges, so the
mechanism that made the list survive is the one it would not have. A trial
with a forced compaction mid-task could test that, and seems unlikely to find
more than this.

If a checklist is ever built, the user gets a `/task` command to read and
change it, and the list never becomes a reason for a turn to continue past what
the user asked.

## Decision

Not built. Recorded so the one-turn design is not rerun expecting a different
answer.
