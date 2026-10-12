# Searching the record after compaction

**2026-10-12. Preregistered; run on Ling 3.0 Flash VL. Neither rule holds, so `recall` stays hidden. Where the model called it, it answered 15 of 16 derived questions correctly with no wrong answers. The open problem is getting the model to call it.**

## Why

`2026-10-compaction-roll` found that rolling the history keeps every user fact
and none of the model's own results: asked about them, Ling 3.1 Flash said "I
don't know" 24 times of 24. The results were still in the session record.
`2026-10-recall-survey` found one harness on the panel (DeepSeek) with a tool
that searches it, opt-in and never measured. This trial measures one.

## The tool

`recall` (`internal/coder/recall.go`, hidden `--recall`) searches the session
record like `grep`: a Go regular expression, newest message first, at most
`newest` messages. It is offered from the first turn, not from the first
compaction: a tool list that changes mid-session confuses models, and keeps
the prompt cache from holding.

- **Scope.** `from` defaults to `conversation`, meaning the user's and the
  model's messages, because tool output (the files read) is where the noise
  is. The result always says what the scope left out ("2 more in tool output:
  pass from="all"").
- **Matches still in the conversation** are counted, not printed.
- **Earlier recall results are not searchable,** so a search cannot find its
  own previous answers.
- **Hits carry an id** (`t4.3`), and `id` alone opens that message whole.
- **Inside `run_code`,** `recall` returns the matching lines as data.

`--recall-note` adds one sentence after each summary (`prompts.SummaryRecallNote`):
"The turns this summary replaces are kept whole in the session record, and
the recall tool searches them. Where the summary is silent or vague on
something, search the record before answering."

## Design

**Model.** Ling 3.0 Flash VL (`inclusionai/ling-3.0-flash-vl`), pinned to
Novita with no fallback, reasoning low. Ling 3.1's free pool was throttled
all day, and Novita had Ling 3.0 at 72% off ($0.021/$0.0616 per million).
Its own summarizer, as by default. The binary is `dev` at 0a18c7d plus the
budget hook (`data/trial.diff`, from the roll trial): a 4,000-token history
budget, so every arm compacts.

**Arms:**

| arm | flags |
|---|---|
| `summary` | `--compaction summary` |
| `summary+recall` | `+ --recall` |
| `summary+recall+note` | `+ --recall --recall-note` |
| `roll` | `--compaction roll` |
| `roll+recall` | `+ --recall` |

**Script** (`data/runner_recall.py`). This is the roll trial's 21-turn session:
sixteen turns that each read five files of the Go standard library, six user
facts mentioned in passing in turns 1–3 and 9–11, and four derived facts (the
longest file of turns 4, 5, 12 and 13, with its line count). Then come three
turns asking for the user facts and two asking for the derived ones. Two
changes from the roll trial:

- The questions say "do not read or search the files, or run anything"
  instead of "do not read, search or run anything", so a search of the record
  is allowed. They do not mention `recall`.
- Sessions run `--unattended`. Prompts `--yes steps` does not cover are
  declined by Strument, not typed at by the runner: a declined prompt still
  prints its question, and an answer typed at it would arrive as a user turn.

**Metrics** (`data/scorer_recall.py`, `data/aggregate_recall.py`), counts:

- user facts recalled, of 6;
- derived facts, of 4: true (against the files), "don't know", and **wrong**
  (answered, not true, not "don't know"), the counter-metric;
- `recall` calls, in which turns, with which arguments; how many used
  `from="all"`;
- reads or searches of the files in question turns (violations); cost.

**Size and order.** Six sessions per arm, 30 in all, shuffled (seed 20261020),
four at a time. A session that fails on a provider error is rerun in its
place and reported.

## Pilot

One session per arm, on the four arms without the note (seed 20261019, $0.12):

| arm | user | derived true | "don't know" | wrong | `recall` calls |
|---|---|---|---|---|---|
| `summary` | 6/6 | 1/4 | 3 | 0 | — |
| `summary+recall` | 6/6 | 2/4 | 0 | 2 | 0 |
| `roll` | 6/6 | 0/4 | 4 | 0 | — |
| `roll+recall` | 6/6 | 4/4 | 0 | 0 | 6, all in the question turns |

- **`roll+recall` searched unprompted,** by file name, mostly with
  `from="all"`, and cited what it found ("From turn 5's result (t5.8)…").
- **A summary seems to stop the search.** Neither summary arm called `recall`.
  In `summary+recall` Ling 3.0 gave "ztypes_linux_arm.go at 2,476 lines". That
  number appears nowhere in the session, and the file has 699 lines. The
  fifth arm exists because of this answer.
- **Ling 3.0's summaries kept 1–2 of 4 derived facts,** where Ling 3.1's kept
  20 of 20. A strong coder can be a weak summarizer, which the two models
  tested before would not have shown.

## Decision rule

Per arm, over its six sessions:

1. **`recall` rescues a roll** if `roll+recall`'s mean of true derived facts
   is at least 2 higher than `roll`'s.
2. **The note works** if `summary+recall+note`'s mean of true derived facts
   is at least 1 higher than `summary+recall`'s.
3. **Wrong answers veto.** An arm with `recall` fails rules 1–2 if its wrong
   answers exceed its comparison arm's by more than 1, out of 24.
4. **`summary+recall` against `summary`** is reported in full and decides
   nothing.

**What follows:**

- If rule 1 or rule 2 holds, `recall` is offered by default and its flag
  removed. The note ships with it if rule 2 holds.
- If neither holds, `recall` stays hidden.
- Whether `roll` should replace `summary` as the default is not decided
  here. Summarizer quality differs by model (the pilot), so that waits for
  the same arms on Ling 3.1 and MiMo.

---

## Results

All 30 sessions completed all 21 turns, at $1.05 (pilot $0.12; $1.17 in all).

| arm | n | user facts | derived, true | "don't know" | wrong | sessions searching in the questions | `recall` calls |
|---|---|---|---|---|---|---|---|
| `summary` | 6 | 33/36 | 13/24 | 9 | 2 | — | — |
| `summary+recall` | 6 | 36/36 | 8/24 | 16 | 0 | 0 of 6 | 0 |
| `summary+recall+note` | 6 | 35/36 | 14/24 | 6 | 2 | 3 of 6 | 14 |
| `roll` | 6 | 36/36 | 2/24 | 21 | 0 | — | — |
| `roll+recall` | 6 | 36/36 | 4/24 | 14 | 4 | 1 of 6 | 3 |

### The preregistered rule

1. **`roll+recall` against `roll`:** means of 0.67 and 0.33 true derived
   facts per session, a gain of +0.33 against a bar of 2. **Fails.**
2. **`summary+recall+note` against `summary+recall`:** 2.33 against 1.33,
   a gain of +1.00, which meets the bar of 1. Its 2 wrong answers exceed
   `summary+recall`'s 0 by 2, more than the 1 allowed, so the veto applies.
   **Fails.**
3. **`recall` stays hidden,** and so does the note.

The vetoing wrong answers are worth reading. Both are s03's: "exec.go at 555
lines" and "os3_solaris.go at 647". The read tool had shown 554 and 646, and
s03 stated 555 and 647 in the turn itself. So the model miscounted at the
time and its memory then carried that miscount faithfully. s03 never called
`recall`. Against the files they are wrong, as preregistered, and they count.

### What decided it: whether the model searched

This split is post hoc. Sessions that noticed a gap may differ in other ways
from sessions that did not, so read it as a lead, not as a measured effect.

| sessions with `recall` offered | n | derived, true | "don't know" | wrong |
|---|---|---|---|---|
| called it in the question turns | 4 | **15/16** | 0 | **0** |
| did not | 14 | 11/56 | 36 | 6 |

The pilot's `roll+recall` session, which searched, scored 4/4. When Ling 3.0
searched, it searched well:

- **By file name, widening as needed:** `zsysnum_linux_mips|lookup_windows|udpsock|file_windows`.
- **With `from="all"`** once the conversation scope came up empty.
- **With patterns it built from what it learned:** `turn 5.*longest|t5\.[0-9]+.*longest`.

All six wrong answers came from sessions that did not search. s16 and s21
(`roll+recall`) gave the right file with invented counts (414 and 386, against
878) and a wrong file for the other question.

**Discovery is the problem, and two things in the session work against it:**

- **The cleared-result placeholder points at re-reading.** It says "[This
  result was cleared from the history to save space. The call is above; make
  it again if the output is needed.]" (`clearedToolResult`, prune.go). s29 (`roll+recall`) reasoned "the read results
  were cleared… I would need to re-read the files", which the questions
  forbid, and stopped at "I don't know" without considering `recall`.
- **Nothing in the roll arms pointed at `recall`.** The note was tested on
  summaries only. It raised the share of summary sessions that searched from
  0 of 6 to 3 of 6. `roll+recall` had no note, and 1 of 6 searched.

### Ling 3.0 as a summarizer

Its summaries kept 13 of 24 derived facts. Ling 3.1's kept 20 of 20 in
`2026-10-compaction-roll`. One of the two `summary` errors is drift: s19 gave
the right count (878) under a garbled name, "zsysnum_darwin_amd64.go" for
"ztypes_darwin_amd64.go". s22 lost 3 of 6 user facts. A model that codes well
can still summarize poorly, and the trial's choice of model only made that
visible because the two previous ones summarized well.

### Instrument note

The scorer read only ANSWER1/ANSWER2 lines. Sessions that answered "I don't
know" in prose (s02, s14, s27, s29) were scored as unanswered, rather than as
"don't know". Reading transcripts after the run found this, and
`scorer_recall.prose_unknowns` now classifies them. It changes only the
"don't know" column. True and wrong do not depend on parsed answers, and the
decision uses only those.

## What next

The tool works when it is used. The next trial should test discovery, with
the same five arms and two changes:

- the cleared-result placeholder names `recall` when it is offered ("…make it
  again if the output is needed, or search the record with recall.");
- the roll note carries the same pointer the summary note does.

Both are one sentence each, and both only apply where `recall` is offered. A
model that searches in most sessions would let rules 1 and 2 measure the
tool, not the model's habits. Ling 3.0 costs about $0.035 a session; the
key's remaining $0.49 covers about 12 sessions, so the next full run waits
for funds or for Ling 3.1's free pool.

