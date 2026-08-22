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
| located all 3 call sites | 24/24 | 24/24 | — |
| named all 3 enclosing functions | 9/24 | 15/24 | 0.148 |
| claimed a caller that does not exist | 4/24 | 2/24 | 0.67 |
| mean tool calls | 5.3 | **3.4** | — |
| total cost | $0.282 | **$0.108** | — |

**Read that second row first.** Every run in both arms found all three call
sites. `grep settleEdits` finds them; nothing here was a task failure. What
varies is whether the model then *names the enclosing function* — which is the
one thing `symbol` supplies and grep cannot. The metric first written up as
"full recall" was measuring that, not task success, and the label overstated it.

Usage doubled. At n=24 per arm that is **suggestive, not established** — p=0.08
is not a result to build on, and the honest statement is "roughly doubled, and
worth confirming."

**The user's hedging hypothesis is not supported.** The worry was that models
would reach for a safer-looking tool out of caution, inflating usage while doing
no less work. The opposite happened: usage rose *and* tool calls fell 36% and
cost fell 2.6×. Had it been hedging, calls would have held flat or risen.

**Per model, functions named:**

| | base → new |
| --- | --- |
| MiMo | 2/12 → **8/12** |
| Kimi | 5/6 → 6/6 |
| GLM | 2/6 → 1/6 |

Nearly all the aggregate gain is MiMo.

### GLM did not regress

This was first written up as GLM going the wrong way. Reading the two
zero-scoring runs says otherwise: both answered **correctly**, and named no
functions at all.

> - `Coder` turn-end logic in `coder.go:431` — passes `""`
> - Another path in `coder.go:579` — passes `""`
> - `toolcommit.go:135` (the commit tool handler) — passes `args.message()`

Every value right, every site right, no name claimed. GLM located all three call
sites in **6/6 runs in both arms**. The 2/6 → 1/6 is a shift in answer *style*
at n=6, not a loss of capability, and the earlier claim is withdrawn.

What GLM's runs *do* show is a real defect, in the schema rather than the tool.
GLM reached for `symbol` twice in the new arm. In `glm-new-2` it called
`kind=definition` (1 site — useless for "who calls this"), then `kind=reference`
(8 sites), and answered 3/3 in 3 steps for $0.0035, the cheapest correct run of
its twelve. In `glm-new-4` it made the same first call, got the definition, and
went back to grep for the remaining four calls.

So the rewritten description got GLM to pick up the tool without conveying that
`kind=reference` is what answers a "who calls this" question. `definition` is
the default and the description leads with it. A model that asks the natural
first question gets an answer to a different one, and half the time abandons the
tool on the strength of it.

### Acting on it, and what happened

Two changes. The schema now opens by mapping question to kind — *"where is this
declared?" is `definition`; "what calls this?" is `reference`* — and says the
thing the measurement says is the differentiator: a reference names the
enclosing function, which grep cannot. And a `definition` answer for a name that
is used elsewhere now offers the other kind, because the miss path already did
and a confident short answer is the one nobody follows up.

Six GLM runs against the fixed binary, `runs-glm-fix/`:

| GLM | used `symbol` | named all 3 | mean tools | cost |
| --- | --- | --- | --- | --- |
| base | 0/6 | 2/6 | 7.2 | $0.0644 |
| new | 2/6 | 1/6 | 4.8 | $0.0425 |
| **fixed** | **6/6** | **6/6** | **1.0** | **$0.0167** |

All six went straight to `kind=reference` and answered in a single call. The
hit-path nudge never fired, because nothing asked for a definition — the
description alone did the work, which makes the nudge insurance rather than the
mechanism.

**This is a confirmation cell, not an arm.** It was run after the fact against
the same task, so it is confounded with time in exactly the way
`2026-08-prompt-scope.md` warns about, and n=6. What it does establish is that
the lever is real and points the right way: the model that never once chose the
tool now chooses it every time, at a seventh of the tool calls and a quarter of
the cost of its own baseline.

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

Counted across all 48 runs, six claimed a caller that does not exist as one:
`commitEdits`, `commitToolCall`, `Coder.send`, `Run`, and `toolCommit` twice
(a tool-name constant, not a function). Four were in the base arm, two in the
new one — p=0.67, so the arms are indistinguishable on it. The conditional is
where the signal is: **five of those six runs never called `symbol`**, against
27 non-symbol runs out of 48 overall.

This is the count that could not fire the first time it was asked. A check for
confabulated names returned 0/21 and 0/27 while `Coder.send` sat in a transcript
quoted an hour earlier — because it looked for a hardcoded list of names instead
of extracting what each answer actually claimed. Extraction is the version that
works.

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
- **The description should lead with what the tool is uniquely for.** Every run
  in both arms found the call sites; what separated them was naming the
  functions. That is the sentence the schema does not say, and it is the sentence
  a model needs in order to choose the tool for the right reason.
- **`kind=reference` needs to be reachable from the natural first question.**
  GLM asked `kind=definition` — the default — of a "who calls this" question,
  and half the time abandoned the tool on the useless answer. The miss path now
  points at the other kind; the *hit* path does not, and a one-site definition
  answer to a callers question is the case that needs it.
- GLM did not regress; that claim is withdrawn above.
- Untested: whether the schema-description rewrite alone accounts for the
  uptake. That is the cheap decomposition, and it is one binary away.
