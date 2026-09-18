# Preregistration: two run_code ergonomics changes

Written before the batch ran, and before any result was seen. The design is
fixed here so the write-up cannot quietly become the design.

## The two changes

Both are model-facing text, which `CLAUDE.md` treats as a prompt change, hence
a trial rather than a commit.

**`--code-signatures`** adds the bridged tools' positional order to the
`run_code` description, rendered from `codeToolParams` so the documented order
and the bound order are one list.

**`--code-read-text`** offers `read_text(path, offset=0, limit=0)`, returning a
file's text as stored. `read`'s result inside a program is the *formatted* tool
output — a `notes.md (423 lines)` header and an `N\t` prefix per line — so a
program computing over contents measures the harness unless it strips that
first.

2×2: `base`, `sigs`, `text`, `both`.

## What the prior says will happen

The [namespace trial](../2026-09-code-namespace/README.md) found that the
mistake a description is meant to prevent is made in the **first** program —
46% of first programs, 10% of second, ~0% after — and concluded that nothing in
a tool description reaches a habit executed that early.

That is a prediction of failure for both arms, and the reason every count below
is binned by program position. The two are not equally exposed to it:

- `sigs` competes with a habit (write Python the way Python is written) and is
  the arm the finding predicts will do nothing. Its target also shrank before
  the trial began: positional calls now *bind*, so the residual benefit is the
  order of the second and later arguments.
- `text` offers a function that does not otherwise exist. That is not a habit
  to overcome but a fact the model cannot know, and a model that meets `read`'s
  formatting adapts within the session — a different curve from one that keeps
  reaching for `os.walk`.

## Tasks

A generated fixture: `notes.md` at 423 lines with 80 blank ones, three
`FIXME(n)` markers and one 290-character line; `data/a.txt` and `data/b.txt` at
137 and 91 rows. Answers are computed from the bytes, never written by hand.

| task | question | correct | what a program measuring `read`'s output answers |
| --- | --- | --- | --- |
| `blank` | lines that are completely empty | 80 | **0** — every line begins `N\t` |
| `longest` | length of the longest line | 290 | **294** — prefix width |
| `total` | characters across two files | 3556 | inflated by prefixes and two headers |
| `cite` | line number of `FIXME(3)` | 108 | **110** — the header shifts it |

The naive value is *distinct* from the correct one in every task, so "measured
the formatting" is its own column rather than being pooled into "wrong".

`cite` is the counter-metric: `read`'s numbering is the answer there, so an arm
that pulls a model toward `read_text` can make it worse. Reported as prominently
as any effect.

## Metrics

Counts, not judgments.

- **correct** — exact match against the computed answer (primary).
- **naive** — exact match against the measuring-the-formatting answer.
- **steps**, **programs**, **cost**, **t/s**.
- **first-program usability**, and every count binned by program position.
- **`read_text` uses** and **positional calls**, to confirm the mechanism fired
  at all before any effect is believed.

## Design

3 models (`mimo`, `deepseek`, `glm`) × 4 tasks × 4 arms × 3 reps = 144 runs,
shuffled with seed 20260918 so an arm is not confounded with the hour it ran, 4
concurrent. `reasoning="low"` set once in the shared config rather than per
model, so forgetting one is impossible.

Raw transcripts stay in scratch space; only this directory's write-up is kept.

## What the pilot changed

Recorded because a pilot that silently reshapes the design is how a trial
becomes an argument for its own conclusion.

1. **The first fixture was twelve lines.** Five of eight pilot runs answered by
   eye without writing a program at all. The fixture is long enough now that
   computing is the cheaper option.
2. **`read_text` did not round-trip its file** — `splitLines` drops the final
   newline, so a file ending in a blank line lost that line. A model wrote a
   correct program and answered 3 where the file had 4. Fixed and landed
   separately (`c7f3c99`) before the batch; a run on that build would have
   measured the bug rather than the arm.
3. **Two pilot runs answered 81 where the answer is 80** — `read_text` keeps
   the terminator, so a naive `.split("\n")` over-counts by one, exactly as it
   does in real Python. Monty has `.splitlines()`, which is correct, so the
   function's summary now names it. A factual description of the function, not
   a persuasion tweak, but recorded here because it was made after seeing pilot
   output.
