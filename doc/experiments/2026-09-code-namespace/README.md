# Does a `tools` namespace help a program orient?

**Result: no, and the reason generalizes. The mistake it was meant to fix is the
first program a model writes, before any description has been consulted — 46% of
first programs, 10% of second, ~0% after. Nothing we can put in a tool
description reaches a habit executed that early.**

## The question

Models reach for Python's own filesystem inside `run_code`: `import os`,
`os.walk`, `open(`, `import glob`, `pathlib`. Unlike the code-result trial's
hazard, this one was measured before any arm existed — over the 530 programs
saved from that trial:

| model | programs | wrong reach | rate |
| --- | --- | --- | --- |
| MiMo-V2.5 | 188 | 32 | 17.0% |
| DeepSeek-v4-flash | 143 | 12 | 8.4% |
| GLM-5.3-flash | 199 | 12 | 6.0% |
| **total** | **530** | **56** | **10.6%** |

45 of 108 sessions wasted at least one program that way, in sessions whose tool
description already says os/sys/pathlib reach no filesystem and names `glob` as
the substitute.

The proposal was to gather the callables under a single `dir()`-able `tools`
object, on the intuition that a namespace orients a model better than a
paragraph — `tools.read(path=…)` says "this environment provides its own I/O" in
the shape of the call, where prose has to be believed.

## What was and was not possible

Probed against the vendored `monty.wasm` rather than read off upstream's docs:

| | |
| --- | --- |
| class with function attributes, `tools = _Tools()` | works |
| `__str__`, so `print(tools)` lists the calls | works |
| `dir()`, `vars()`, `globals()`, `__dict__` | **absent** |
| `types.SimpleNamespace` | absent |

So the `dir()` half of the intuition is unavailable, and `print(tools)` is the
closest thing there is. A test asserts `dir()` is still missing, so a future
Monty that grows it reopens the design rather than leaving a `__str__` nobody
needs.

One implementation cost worth recording: the prelude that builds the namespace
shifts every traceback line by eight, and an error naming the wrong line is
worse than one naming none. The offset is subtracted back out.

## Arms

Four, behind `--code-namespace`, each differing in one paragraph of the
description and its worked example:

| arm | how a program reaches the tools |
| --- | --- |
| `flat` | bare names — the shipped arrangement |
| `both` | a `tools` namespace beside the bare names |
| `only` | the namespace alone; `read(...)` raises `NameError` |
| `hint` | flat names, but a reach for the filesystem is answered with the tool that serves it |

`hint` is not the namespace idea. It treats this as a signposting problem at the
moment of the mistake rather than at the moment of the description.

3 models, 3 tasks, 4 reps, shuffled with a fixed seed, all at `reasoning="low"`
per [`mechanism-must-fire`](../../experimenting.md#mechanism-must-fire). 144 runs, about $0.20.

## Results

| arm | n | correct | programs | wrong reach | rate | sessions with one | p vs flat | median input |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `flat` | 36 | 36 | 250 | 24 | 9.6% | 22/36 | — | 36.2k |
| `both` | 36 | 35 | 240 | 29 | 12.1% | 19/36 | 0.634 | 39.6k |
| `only` | 36 | 36 | 209 | 17 | 8.1% | 16/36 | 0.238 | 31.7k |
| `hint` | 36 | 36 | 231 | 22 | 9.5% | 19/36 | 0.634 | 34.4k |

Nothing is significant. `only` trends better on every metric, and **the trend
shrank as the sample grew** — its input-token p-value went from 0.114 at n=18 to
0.191 at n=36, which is what a first-batch fluke looks like from the inside. The
per-model split says the same: `only` helps MiMo (4/12 against 8/12, p=0.220) and
does nothing for GLM (7/12 against 7/12).

**The namespace is adopted when offered** — 86% of programs under `both`, 95%
under `only`, use `tools.`. Models take it up readily. It just does not change
what they reach for first.

## The finding: it is a first-move habit

| program position in the session | wrong reach |
| --- | --- |
| #1 | 66/144 = **45.8%** |
| #2 | 14/141 = 9.9% |
| #3 | 6/137 = 4.4% |
| #4 | 4/127 = 3.1% |
| #5 and later | 2/352 = 0.6% |

Two thirds of every wrong reach in the trial is the *first* program of a session.
By the second the model has met the error and corrected, and the recovery works:
the rate falls by a factor of five in one step and to nothing after that.

That is why no arm moved it. Sessions whose first program reached:

| arm | |
| --- | --- |
| `flat` | 18/36 |
| `both` | 18/36 |
| `only` | 14/36 |
| `hint` | 16/36 |

The `hint` arm's null is the one predicted by construction — it fires only after
a failure, so its ceiling is the 9.9% at position #2, and there is nothing there
to win. The namespace arms had the whole 45.8% available and did not take it.

**What it costs.** A session whose first program reached spends about one extra
step and 4k more input than one that did not (9 against 8, p=0.091; 36.4k against
32.5k, p=0.108) and answers just as well: 66/66 correct against 77/78. So the
phenomenon is real, cheap, and self-correcting — roughly half a step per session
in expectation.

## What this licenses

Keep `flat`. Do not ship a namespace on this evidence: it costs a prelude, a
traceback correction, and a paragraph of description, and buys nothing
measurable. Do not ship the `hint` either — its design was falsified by *where*
the mistake happens rather than by its own numbers.

The wider lesson is about which lever can reach a given failure. A tool
description can only act on a model that is consulting it, and the first program
of a session is written before that. Three separate description fixes have now
failed against this same reach: naming the substitutes explicitly (shipped days
before this trial), the namespace, and the after-the-fact hint. If it is worth
chasing further, the untried lever is the **system prompt**, which is read
differently from a tool schema — that is a testable arm and this trial does not
speak to it.

It is also worth asking whether half a step per session is worth any of this.
The measured cost of the failure is smaller than the cost of the last two
attempts to fix it.
