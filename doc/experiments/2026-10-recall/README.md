# Searching the record after compaction

**2026-10-12. Preregistered; results below the line once run.**

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
