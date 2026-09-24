# Monty, Monty with `open()`, or JavaScript: which run_code fails least?

**Decision, revised after the report: JavaScript replaced Monty** (`460c92c`),
on the evidence below read against the right cost — see *What this licenses*.

**Result: do not make `open()` work. Making it work made first programs fail
more, not less — 18/73 against 5/70 (p = 0.006) — because a legitimate
`open()` invites the rest of Python's file toolbox, and most of that is not
there. JavaScript failed least (1/69), but the difference from Monty is not
significant (p = 0.21), and correctness was identical in all three arms: every
session that wrote a program answered correctly.** Neither arm meets the
preregistered rule, so both trial arms were removed.

Preregistered in [`preregistration.md`](preregistration.md) and committed
before the main run reported. Follows the text-only probe
[`../2026-09-run-code-js`](../2026-09-run-code-js/README.md).

## Design

One binary (`030ab98`), three arms behind a hidden flag, 324 real Strument
sessions in script mode: three models (MiMo-V2.6-Flash, GLM-5.3-flash,
DeepSeek-v4.1-flash) at reasoning effort low, six tasks with answer keys, six
reps, shuffled, four in flight. Each session got its own copy of the probe's
fixture and its own state directory. `bash` was not approved, so a model
could not compute in `python3 -c` and bypass the arm.

| arm | what a program is |
| --- | --- |
| **monty** | the shipped Monty: `open` and `pathlib` reads refused |
| **monty-open** | Monty where `open(path)` for reading, with or without `with`, and `Path(path).read_text()` work, answered through `read_text` |
| **js** | goja, with the same bridge, data shapes, caps, time limit and hint placement |

The arms' requests were captured through a stub and diffed before the run:
monty-open differs from monty in one description sentence, js in the three
passages the probe changed, and monty's tools are byte-identical to HEAD's.

## Results

| arm | sessions | wrote a program | **first program failed** | **correct** | silent wrong | median steps | median input |
| --- | --- | --- | --- | --- | --- | --- | --- |
| monty | 108 | 70 | **5/70 (7%)** | **100/108** | 0 | 2 | 16.6k |
| monty-open | 108 | 73 | **18/73 (25%)** | **105/108** | 0 | 2 | 17.0k |
| js | 108 | 69 | **1/69 (1%)** | **100/108** | 0 | 2 | 16.9k |

Against monty: monty-open's first-program failures p = 0.006 (worse), js's
p = 0.21. Correct answers p = 0.21 and 1.0.

| model | monty | monty-open | js |
| --- | --- | --- | --- |
| DeepSeek | 1/29 | 3/28 | 0/29 |
| GLM | 1/18 | 8/21 | 0/18 |
| MiMo | 3/23 | 7/24 | 1/22 |

(first program failed / sessions that wrote one)

$0.61 for the 324 sessions.

### Why monty-open lost

Its 18 failed first programs, read one by one:

| failure | count |
| --- | --- |
| `import glob` or `import csv` beside a working `open()` | 9 |
| `Path(...).glob(...)` | 2 |
| `for line in open(...)` — Monty's file object does not iterate | 1 |
| unrelated: `__import__`, a header row into `int()`, an invented `open_text`, a list passed to `re.findall`, a time-out | 6 |

The `open()` calls themselves worked — 38 of 73 first programs used `open()` or `Path()`. What
failed was everything around them. Telling a model that files "can be read the
ordinary way" is telling it to write ordinary Python file handling, and that
is `glob.glob`, `csv.DictReader`, `Path.glob` and `for line in f` as much as
it is `open`. The fix moved the wall one step further out and put more
programs in front of it.

The probe could not have seen this: it stopped at the first program's text,
and in the Monty arm `open()` was still a mistake. That is the handbook's
[`clean-null`](../../experimenting.md#clean-null) warning from the other side
— the text-only measurement was right about what it measured and silent
about what a change to the environment would do next.

### Correctness is the same in every arm

**Every session that wrote a program answered correctly: 70/70, 73/73,
69/69.** No arm produced a silent wrong answer (a wrong ANSWER in a session
where no program failed). All 19 incorrect sessions — 8, 3 and 8 — wrote no
program at all, and 18 of them are GLM:

- it announced a program ("Sort whole file via run_code", "I'll compute this
  in one program") and ended the turn without the tool call;
- it returned an empty response, or reasoning with no answer;
- it read the 400-row CSV and ranked it by eye, wrongly;
- it looped for the full 600 seconds re-reading the CSV and repeating a
  declined `bash sort` (three sessions; see *A harness bug* below).

None of these touches the arm: no program ran. The monty-open arm's higher
count (105 against 100) is GLM doing this less often there, by chance.

All four time-outs fell in the js arm, which looked like an engine hang and
was checked: none of the four ran a program. Three are the GLM loop above; the
fourth is a MiMo request the provider never answered.

### The baseline is smaller than the probe suggested

Live, Monty's first programs failed 5 times in 70, four of them `import
glob` — a host reach of 4/70 (6%), against the probe's 12/97. In a real
session the model has usually looked at the file before it writes a program,
and `read_text`, shipped since the namespace trial, is the obvious call. Each
failure costs one step and corrects itself. That is the whole prize a
language change could win here: about one step in fourteen program-writing
sessions.

## What this licenses

- **Do not ship monty-open.** It is significantly worse on the primary
  metric, and the mechanism is clear enough to generalise: making one wrong
  reach succeed legitimises the neighbouring ones.
- **JavaScript is not shown better, and is shown no worse.** Its direction
  is the right one (1/69 against 5/70) and it cost nothing in correctness,
  but the difference is not significant, and confirming a gain of this size
  at 80% power would take roughly three times the sample. The preregistered
  rule, which asked for a significant gain, is not met.
- **Replace Monty with JavaScript anyway, on cost.** The first version of
  this report weighed JavaScript against "a second engine, three new
  dependencies and a second implementation of the run_code contract to keep
  in step" and kept Monty. That priced running two engines side by side,
  which was never the proposal: the proposal was replacing Monty. A
  replacement *removes* the Monty wrapper, the vendored 5 MB WebAssembly
  blob, the Rust shim with its toolchain (rustc 1.95 to rebuild), and wazero,
  and adds goja and three small Go dependencies; the binary came out 1.25 MB
  smaller. An engine no worse on anything measured, with a smaller stack, is
  the better one to carry. Done in `460c92c`, after this report; the trial
  arms themselves were removed in `583908a` as the rule said.

The two JavaScript hazards the probe could not measure did not appear when
the code ran: no silent wrong answers, and the numeric-sort trap never fired
(all 12 js `top5` sessions that wrote a program sorted with a comparator).

## A harness bug the trial found

Three GLM sessions made the same `read` call 68 times and the same declined
`bash` call 39 times without the loop note stopping them. The tool-loop
watcher says its piece once per turn and leaves the rest to the step budget —
and `--yes steps`, which unattended runs use, removes the step budget. Filed
separately; it is why those sessions ran to the 600-second time-out rather
than ending.

## Instrument notes

- **The host-reach classifier had a false negative**, found here: it matched
  a forbidden module only at the head of an import list, so `import re, glob`
  passed as clean. Fixed, with both shapes in its self-test, and the probe
  rescored — which moved its Monty count from 11/97 to 12/97, in the
  direction it already pointed. The probe's report carries the correction.
- The scorer's answer parser self-tests on crafted ANSWER lines in both
  directions, and every incorrect session was read by hand.
- The nine pilot sessions are part of the dataset: the rig did not change
  after them.
- The first-program failure count is read from the tool result ("The program
  failed"), not inferred from the program's text, so it counts what the
  interpreter did.

## Data

`data/`: `fixture.py`, `run.py`, `score.py`, `classify.py`, and
`scored.jsonl` — one row per session with its programs, the first 600
characters of each result, the answer and its score. `score.py` rescores from
it when the raw runs are absent; the rescore matches the original exactly.
Raw session records stayed in scratch space.
