# Does the improved symbol tool get chosen?

2026-08-22. `symbol` went unused. Two models, asked why, blamed the exact-name
requirement; measuring instead showed the real problem was that a bare
`file:line` forces a follow-up `read` while `grep` returns the matching line in
one call, and that struct fields were not indexed at all. `121e1a6` fixed both
and made a miss explain itself honestly.

This is the live half: **do models actually reach for it now?**

## Design

**Arms.** A = `strument-base` (`e1ea92f`). B = `strument-new` (`121e1a6`). Same
tree, same task, same models. Arm B bundles three changes — source lines, named
struct fields, and a rewritten schema description — and this trial cannot say
which one moves anything.

**Task.** *"Which non-test functions call settleEdits, and what does each one
pass as the message argument? Keep it brief."* It names neither tool.

Chosen because **both arms can answer it**. The first candidate keyed on
`editFormat`, a struct field — arm A cannot find fields at all, so a usage drop
would have been mechanical rather than a preference. On `settleEdits` both arms
return the same 8 sites; only B shows what is passed. A difference in usage is
therefore a difference in *choice*.

**Answer key**, verified by hand against the tree before any run: `runOne` (`""`),
`afterInterrupt` (`""`), `runCommitTool` (`args.message()`).

**Models.** MiMo V2.5 (12/arm), GLM 5.3 low (6/arm), Kimi K3 low (6/arm). 48
runs, order randomized across the whole plan.

**Metrics**, pre-registered: symbol calls per run (primary), and — reported as
prominently — full recall, total tool calls, and cost.

## Results

| | base | new | p |
| --- | --- | --- | --- |
| **used `symbol`** | 7/24 | **14/24** | 0.080 |
| full recall (3/3) | 9/24 | 15/24 | 0.148 |
| mean tool calls | 5.3 | **3.4** | — |
| total cost | $0.282 | **$0.108** | — |

Usage doubled. At n=24 per arm that is **suggestive, not established** — p=0.08
is not a result to build on, and the honest statement is "roughly doubled, and
worth confirming."

**The user's hedging hypothesis is not supported.** The worry was that models
would reach for a safer-looking tool out of caution, inflating usage while doing
no less work. The opposite happened: usage rose *and* tool calls fell 36% and
cost fell 2.6×. Had it been hedging, calls would have held flat or risen.

**Per model, and this is the counter-signal:**

| | base → new |
| --- | --- |
| MiMo | 2/12 → **8/12** |
| Kimi | 5/6 → 6/6 |
| GLM | 2/6 → **1/6** |

GLM went the *wrong* way. n=6, so noise is a live explanation, but it is not
evidence for the change and it is reported here at the same size as the effect.
Nearly all the aggregate gain is MiMo.

## The mechanism, which is not the arm

Conditioning on whether a run *used* `symbol`, rather than on which arm it was
in:

| | full recall | mean tools |
| --- | --- | --- |
| used `symbol` | **17/21** | 3.2 |
| did not | **7/27** | 5.2 |

p=0.0004. **This is observational, not randomized** — models that chose `symbol`
may differ from those that did not — but the mechanism is visible in the
transcripts and it is specific.

A run that greps and then reads a narrow window knows the file and line but not
which function it is inside, so it *guesses the enclosing name*. `mimo-base-4`
read `coder.go:426-435` — ten lines inside a defer nineteen lines into its
function — and reported the caller as **`Coder.send`**. It also reported
`runCommitTool` as **`toolCommit`**. Both confidently, both wrong. `symbol
kind=reference` states the enclosing function as fact, which is the one thing
grep structurally cannot do.

**And among the runs that used `symbol`, arm matters for cost:** base averaged
6.4 tool calls, new averaged **1.6**. Same instrument chosen, a quarter of the
work — which is exactly what echoing the source line was for. MiMo's best runs
answered the whole question in **one call, 2 steps, $0.0006**.

## The eleventh scorer bug

The first scoring pass reported full recall of 5/24 and 8/24 — far below a pilot
run I had watched answer 3/3. Reading the transcript that the aggregate
disagreed with found it.

The reasoning renderer has **two** forms. A multi-line block opens with the
marker alone on its line and closes with `‹/›`; a one-line aside is
`‹thinking› text` and ends at the newline, never closed. The scorer stripped
`‹thinking›…‹/›`, then treated any *unclosed* marker as running to the end of
the output — which deleted the final answer of every run whose last aside was
the one-line form. Recall was deflated in both arms, unevenly. Corrected: base
5→9, new 8→15.

It was caught by a cross-check that could not fire. A count of runs naming a
function that does not exist returned 0/21 and 0/27 — while I had read
`Coder.send` in one of them with my own eyes. **A check that cannot fail is not
a check**, and this time the check that could not fail was the one that found
the bug.

Both leak directions are now pinned: reasoning that solves the task before an
answer that fails to, in each of the two block forms.

## What to conclude

- The change is worth keeping: usage up, work down, cost down, quality not
  worse.
- The usage effect is **not established** at p=0.08. Three runs per cell on one
  task shape is a pilot.
- The strongest claim available is the *mechanism*, and it argues for `symbol`
  as a tool rather than for this change specifically: **grep plus a narrow read
  produces confabulated function names, and `symbol` does not.** That is worth
  saying in the tool's own description.
- GLM's regression needs a look before any of this is load-bearing.
- Untested: whether the schema-description rewrite alone accounts for the
  uptake. That is the cheap decomposition, and it is one binary away.
