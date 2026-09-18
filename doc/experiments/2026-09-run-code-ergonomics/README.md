# Two `run_code` ergonomics changes: `read_text` wins, signatures do not

**Result: `read_text` is shipped. Every one of the 51 runs that called it
answered correctly, against 14/27 for the arm without it (p = 0.000036), in 1.6
steps per turn rather than 2.7 and for less money. Documenting the tools'
positional order did nothing measurable (19/27, p = 0.26) and, combined with
`read_text`, was the worse of the two.**

Preregistered in [`preregistration.md`](preregistration.md), written before the
batch ran.

## The question

Inside a `run_code` program, `read` returns the *formatted* tool output — a
`notes.md (423 lines)` header and an `N\t` prefix on every line. A program
computing over a file's contents therefore measures the harness unless it
strips that first. DeepSeek V4.1 Flash hit this in a live session and diagnosed
itself: "my first two attempts were measuring the tool's own formatting rather
than the file".

Two proposed answers, both model-facing text, which `CLAUDE.md` treats as a
prompt change:

- **`read_text`** — a function returning the file as stored.
- **signatures** — the bridged tools' positional order in the description,
  rendered from `codeToolParams` so documentation and binding are one list.

2×2, 3 models (`mimo`, `deepseek`, `glm`) × 4 tasks × 3 reps = 144 runs,
shuffled with a fixed seed, `reasoning="low"` set once for all models. $0.22.

## Results

| arm | correct | vs base | naive | mean steps | cost |
| --- | --- | --- | --- | --- | --- |
| `base` | 23/36 (64%) | — | 5 | 2.7 | $0.053 |
| `sigs` | 28/36 (78%) | p = 0.30 | 5 | 3.6 | $0.082 |
| `text` | **36/36 (100%)** | **p = 0.00007** | 0 | **1.6** | $0.043 |
| `both` | 33/36 (92%) | p = 0.0093 | 1 | 1.6 | $0.042 |

Excluding `cite`, which every arm answers 9/9 and which dilutes the comparison:
base 14/27, `sigs` 19/27 (p = 0.26), `text` **27/27** (p = 0.000036), `both`
24/27 (p = 0.0063).

### The counter-metric, which is clean

`cite` asks which line a marker is on — the one task where `read`'s numbering
*is* the answer, and where a model pulled toward `read_text` would do worse.

**Every arm answered it 9/9, and in the arms that offer `read_text` not one run
used it there.** The models discriminated unprompted: in the `text` arm the
only nine runs that did not call `read_text` were the nine `cite` runs. That is
the result this trial most needed and least expected.

### The mechanism, not just the effect

| arm | correct when `read_text` was used | when it was not |
| --- | --- | --- |
| `text` | 27/27 | 9/9 (all `cite`) |
| `both` | 24/24 | 9/12 |

Every failure in either arm is a run that did not call the function. Nothing
else distinguishes them.

## Why `both` is worse than `text` alone

All three `both` failures are non-`cite` runs that used `read` instead of
`read_text` — two of them `read(...).splitlines()`, straight into the
formatting. The extra paragraph cost three uses of the function doing the work.

`text` vs `both` is p = 0.24, so this is a point estimate and not a
demonstrated harm. But there is no evidence for the signatures arm in either
direction, and it costs a step per turn and half again the tokens, so it stays
off.

## What the baseline was actually doing

13 baseline failures, four disguises, one cause:

```python
print(max(len(l.rstrip('\n')) for l in open('notes.md')))   # 294, not 290
tot += len(read(p).replace("\n",""))                        # header + prefixes
line = read(path=f"data/a.txt", offset=i, limit=1)          # then strip by hand
lines = read(path="notes.md").split("\n")                   # 59 blanks, not 80
```

Only 5 of the 13 produced the exactly-naive value. The rest are ad-hoc attempts
to strip the formatting, each wrong in its own way — which is why counting
"naive answers" alone understates the problem by more than half.

## The namespace trial's prediction, and why it did not hold

The [namespace trial](../2026-09-code-namespace/README.md) found the mistake it
targeted was made in the first program — 46% of first programs, 10% of second,
~0% after — and concluded that **nothing in a tool description reaches a habit
executed that early**. Preregistered here as a prediction of failure for both
arms.

It was wrong about this one, and the shape of the counts says why:

| position | measured `read`'s output |
| --- | --- |
| program 1 | 36/105 (34%) |
| program 2 | 22/44 (50%) |
| program 3 | 9/21 (43%) |
| program 4+ | 18/43 (42%) |

The namespace mistake decays because the model *finds out*: `os.walk` raises,
and nobody makes that error twice in one session. This one does not decay,
because the formatted output looks exactly like plausible file content. The
model never learns it is wrong, so it never stops.

**The generalization worth keeping is not "descriptions cannot reach early
habits". It is that a description cannot fix a mistake the model would correct
on its own — and is the only thing that can fix one the model never notices.**

## Post-hoc: what the signature line actually did

Not preregistered. Prompted by the question "is `--code-signatures` worth
testing properly?", and answered from the saved programs rather than by running
more.

The arm was built for the order of the *second and later* positional arguments.
Across all 144 runs' 236 bridged calls:

| | calls |
| --- | --- |
| total | 236 |
| with at least one positional argument | 88 |
| with **two or more** positional arguments | **9** |

All nine are in the `sigs` arm. All nine are `read_bin`. **In every arm that
does not advertise an order, no model ever passed two positional arguments to
anything.** The hazard the arm addresses does not occur unless the arm induces
it — and a model that writes one positional argument, which 79 of those 88 did,
is already served: `codeToolParams` binds it.

What the line did measurably was advertise `read_bin`:

| arm | runs using `read_bin` | `total` task correct |
| --- | --- | --- |
| `base` | 4 | 4/9 |
| `sigs` | 10 | 7/9 |
| `text` | 0 | 9/9 |
| `both` | 0 | 8/9 |

The `sigs` arm's largest gain is the `total` task, and it comes from models
reaching for `read_bin` to get raw bytes — a workaround for exactly the
formatting problem `read_text` was built to solve. Once `read_text` is offered,
`read_bin` use falls to zero and the same task is answered better.

**So the signature line's one measured benefit was a detour around the problem
the other arm fixes directly.** That is a better reason not to ship it than p =
0.26 was: not "no effect was detected" but "the effect was a workaround, and it
has been superseded".

It also means a follow-up trial is not worth running. A fixture built to force
two-positional calls would be a fixture built to produce the effect, and the
natural rate it would be extrapolating from is zero.

## Threats

- **One fixture.** Generated, but one shape: a line-oriented text file. Nothing
  here says how `read_text` behaves on the tasks these four do not resemble.
- **`sigs` was built for a gap that shrank underneath it.** Positional
  arguments bind now, so what a signature line still buys is the order of the
  *second* and later arguments — and almost every call the models made took
  one. The arm is kept behind its flag rather than deleted for that reason.
- **`cite` is answerable without a program**, which is why it separates the
  arms so poorly. It earns its place as the counter-metric, not as a task.
- **Zero two-positional calls is evidence that the hazard is rare here, not
  proof that it is absent.** These four tasks read whole text files. A workload
  that pages through a large one with `offset` and `limit`, or reaches for
  `read_bin`'s window, is where a second positional argument would arise, and
  nothing here measures that.

## What the pilot cost, and paid for

The pilot found a bug in the function under test: `read_text` did not
round-trip its file, because `splitLines` drops the final newline and nothing
recorded that it had, so a file ending in a blank line lost that line. A model
wrote a correct program and answered 3 where the file had 4. Fixed in `c7f3c99`
before the batch. **A trial on that build would have measured the bug rather
than the arm** — the handbook's "your instrument is made of the thing you are
testing", collected one more time.

It also found the first fixture was twelve lines long, which five of eight
pilot runs answered by eye without writing a program at all.

## Data

144 runs in `data/results.jsonl`: one row per run with the arm, model, task,
answer, program texts, steps, tokens, cost and throughput. Raw transcripts were
kept in scratch space and not preserved.
