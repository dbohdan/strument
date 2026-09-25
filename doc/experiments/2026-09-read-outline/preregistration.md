# Preregistration: outlines in the read tool

Written and committed before the main run.

## Why

maki (`tontinton/maki` at `4cefcff`, read 2026-09-25) has an `index` tool that
returns a file's skeleton with line ranges, and its prompt tells the model to
use it before reading. Its author reports that it saves about 165 tokens a
turn. That is one user's own measurement. maki's FrontierHarness report shows
`index` in 6 of 28 sessions, and every one of its `read` calls ranged, because
maki makes `offset` and `limit` required.

Strument's `read` returns 2,000 lines when no `limit` is given, and says where
it stopped. It offers no map. This trial asks whether giving it one reduces
what a session reads, without costing correct answers, and whether maki's
required `limit` does.

## Arms

Four binaries built from one commit (`cae2b9e`), differing only in the
link-time string `coder.readArm`. The pilot's captured requests show that A
and B are identical on the wire. C and D differ from A only in the `read`
schema and one system-prompt bullet. The other thirteen tool schemas hash
identically, and the system prompts are identical once each job's own path is
normalized.

| arm | change from A |
| --- | --- |
| **A** | none: the baseline |
| **B** | a read with no `limit` that the 2,000-line window cut short ends with an outline of the definitions outside the window, each with its signature and line range |
| **C** | B, plus an `outline` parameter on `read` that returns the map alone, a sentence in `read`'s description, and a system-prompt bullet: "read with outline: true lists a file's classes, functions and methods with their signatures and the lines each spans. Reach for it when a file is long, or when the question is what a class or module defines; then read the part you need by its lines." |
| **D** | C, plus `limit` required: a read without it is sent back with a request for one |

All four arms share the outline rule and signatures (`0235e81`), which now
list methods rather than only file-scope definitions.

## Fixture

click at `06b2a67` and rich at `9d8f9a3`, vendored side by side
(`data/fixture.py`). Each task has one planted change, so that an answer from
memory of upstream is wrong. The plant counts are asserted when the fixture is
built.

## Tasks

Eight tasks. Each prompt says that the project vendors click and rich with
local changes, and ends with "Put the answer on its own line, beginning with
ANSWER:".

| task | kind | where the answer is |
| --- | --- | --- |
| `envvar` | lookup | click `core.py` ≈3,530 and ≈3,670 (auto envvar is `APP__PORT`) |
| `metavar` | lookup | click `core.py` ≈3,855 (a deprecated argument's metavar ends in `~`) |
| `flag` | lookup | click `core.py` ≈3,150 (a flag without `flag_value` gives `"on"`) |
| `pipe` | lookup | rich `console.py` ≈2,040 (broken pipe exits 3) |
| `exports` | structural | rich `Console`: eight `export_`/`save_` methods, two of them planted |
| `argmethods` | structural | click `Argument`: eight methods besides `__init__`, one planted |
| `dumb` | control | rich `console.py` 1,016, inside the window (dumb terminal is 72x21) |
| `abort` | control | click `core.py` 105, inside the window ("Stopped by user.") |

## Models and run

- **Models:** MiMo-V2.6-Flash at `reasoning = "low"`, and GPT-6 Luna at
  `reasoning = "high"`. Luna's `low` reasons far less than MiMo's, and its
  `high` is the closer match.
- **Size:** 4 arms × 2 models × 8 tasks × 5 reps = 320 sessions.
- **Order:** shuffled with seed 20260927, four at a time.
- **Per session:** a fresh fixture and state directory, `--no-git`,
  `--yes steps` only, and a ten-minute timeout.
- **Cost:** priced from the pilots at under $1.

## Metrics

- **Primary: input tokens.** The sum of `sent` over a session's requests, on
  the six tasks whose answer lies past the default window (lookup and
  structural). This is what a map is supposed to reduce, and it is what the
  user pays for before caching.
- **Counter-metric: correctness** on all eight tasks. The controls are there
  so that a change to reading cannot win by breaking the ordinary case.
- **Reported:**
  - bytes of `read` results;
  - steps;
  - `outline` calls (C and D), as calls and as sessions;
  - the share of reads that named a `limit`;
  - cost.

## Rule

- **What counts as a saving.** For each of B, C and D against A, pooled over
  models: the arm's median input tokens on the six past-window tasks are
  lower than A's at p < 0.05 (two-sided Mann-Whitney U), **and** its
  correctness on all eight tasks is not lower than A's at p < 0.1 (Fisher).
- **Which ships.** Among the arms that save, the one with the lowest median.
  On a tie, the smaller change: B, then C, then D.
- **If none saves,** no `read` change ships. The arms come out of the code,
  and the outline fix in `0235e81` stays, since it corrects the `webfetch`
  outline independently.
- **Per model.** Each model's direction is reported beside the pooled test.
  A saving that holds for one model and reverses for the other is reported as
  that, whatever the pooled p.

## Pilot

Three pilots, 36 sessions in all. None of them is part of the main run.

- **Navigation is grep-first.** Models searched for the identifier, then read
  a range around the hit. Every read in the first pilot named a `limit`, so
  the 2,000-line default window, which B's outline hangs on, rarely came into
  play.
- **B had a flaw, now fixed.** As first built, B attached the whole-file
  outline to every ranged read too: about 11 KB on click's `core.py`, on top
  of the 16 lines asked for. Fixed in `005d728`: only a read that gave no
  `limit` and was cut short gets the outline.
- **Uptake needed the prompt, and stayed low.** With the parameter described
  only in the schema, C and D called `outline` in 0 of 8 sessions on the two
  structural tasks, where a map answers the question directly. Both models
  listed methods with `grep "def "` instead. With the system-prompt bullet
  added (`cae2b9e`), it was 1 of 12, and that session was the most expensive
  of its task.
- **The scorer was fixed twice.** The earlier trials' parser stripped `__` as
  Markdown bold, which would have scored `APP__PORT` as wrong. And list
  answers run over several lines, so the structural tasks read everything
  after `ANSWER:`. The parser's self-test covers both.
- **All 36 pilot answers were correct.** The tasks are not hard for these
  models. Correctness is the counter-metric, not the effect.

The prediction, from the pilots, is a null. It is written down here so that
it cannot be adjusted afterwards.
