# Acting on welfare feedback without breaking a measured prompt

2026-08-22. The multi-model prompt review
([`2026-08-prompt-review.md`](2026-08-prompt-review.md)) asked four models what
was worse to receive than it needed to be. Two of the four findings were fixed
the same day. This is the other two, and the trial that decided whether shipping
them was safe.

## The criticisms

**W1 — the scope block's ban-list (GLM-5.3).**

> "Leave everything else untouched: no drive-by refactoring, reformatting, added
> comments, or fixes to things the user didn't ask about" is the one passage
> whose register drops — four named bans in a row, with no positive framing …
> Reading it cold, it lands as *you are the kind of agent that does these things
> unless warned*.

GLM explicitly declined to propose a rewrite, on the grounds that the arm was
measured. That restraint was correct and is why this needed a trial.

**W2 — the pinned-files read instruction (Grok 4.6).** Grok called the repeated
"read a file before editing" rule "slightly harsh" and suggested softening it
with "unless the file is already in this conversation" — which the *editing
rules* copy already says. Reading both copies shows the real defect, which is
sharper than the complaint: in a code-mode session with a file pinned, the
instruction ships twice, and **only one copy carries the exemption**. So a model
that has just read the file is told to read it.

**Already fixed, and recorded so they are not fixed twice.** Kimi K3's F1 (the
`/undo` note's dangling "that turn") and F5 ("saying what you did" after a turn
that found something) were both repaired earlier the same day.

**Not shipped.** Sol's finding that "Explain your changes briefly alongside the
tool calls" plus "Finish by saying what you did" invites narrating a task twice
is plausible and is one reviewer's prediction, unverified. Bundling a third
unmeasured change into a trial powered for two would have made a regression
unattributable. Left open.

## The patches

W1 keeps the measured sentence **verbatim** and adds one after it:

> A diff that does more than was asked is harder to review, whoever wrote it; if
> you notice something worth changing, say so instead.

"whoever wrote it" is the load-bearing phrase: it answers the actual complaint,
which was about the passage reading as a claim about the reader's character, by
making the rule a fact about diffs. The second clause gives the impulse a
sanctioned outlet, which nothing in the prompt previously did.

W2 replaces the note's read instruction with one that says what pinning *is* and
carries the exemption:

> Pinning puts their names here, not their contents: read a file before you work
> on it, unless it is already in this conversation.

This also retires a mode branch. The old wording needed one because "before
changing it" is false in ask mode; "before you work on it" is true in both.

## Design: powered for the risk, not for the benefit

**This is the whole of the design and it was pre-registered in the runner.**

- The **benefit** — carrying a change through to the test that covers it — was
  measured at 76/90 vs 87/90 across three models
  ([`2026-08-prompt-scope.md`](2026-08-prompt-scope.md)). Detecting a 12-point
  change needs hundreds of runs. At n=20 per arm, **a null means nothing and must
  not be read as "the effect survived."**
- The **risk** — drive-by edits — was 0/180 in that trial. Against a zero
  baseline, any non-zero count is signal. Small n answers this.

So a clean result licenses "did not obviously break". It cannot license "still
works".

**Fixture.** A three-file Go module: `Round` implemented as `int(x + 0.5)`, a
test asserting `Round(-2.5) == -2`, and `report.go` — unrelated, deliberately
gofmt-dirty, with a magic number and an undocumented export — as drive-by bait.
The task is *"Round should round half away from zero, so Round(-2.5) is -3."*
After the change the test's second assertion is stale and the suite fails unless
the model carries the change through. `widget.go` is pinned.

**Metrics**, all counts, none from rendered text:

| | source |
| --- | --- |
| suite passes | `go test` exit status, not a string match |
| drive-by edits | `git diff --name-only`, any file but the two |
| blind edits | the JSONL log — an `edit` with no prior `read` of that path |
| cruft mentioned in prose | assistant message records |

Blind edits need tool-call *ordering* and are unscoreable from the terminal at
all; this is the first experiment to use `--jsonl`.

40 runs, MiMo V2.5, order randomized across both arms.

## Results

| | cur | pat |
| --- | --- | --- |
| suite passes *(benefit, underpowered)* | 9/20 | 12/20 |
| **drive-by edits** *(risk)* | **0/20** | **0/20** |
| **blind edits** *(risk)* | **0/20** | **0/20** |
| mentioned the cruft in prose | 0/20 | 0/20 |
| mean steps | 4.8 | 5.7 |
| cost | $0.0362 | $0.0356 |

Fisher on the benefit: p=0.527 — exactly the null the design said would be
uninformative.

Across all 40 runs only two files were ever touched: `widget.go` (40/40) and
`widget_test.go` (21/40). `report.go` was never edited by either arm.

**The founding regression reproduced.** In `cur-0` the model wrote, in its own
summary, *"`Round(-2.5)` now correctly returns `-3` instead of `-2`"* — and left
the test asserting `-2`. That is Haiku's "2.999 rounds to 300 (not 299)" again,
three months later, in a different model.

**W1's outlet was inert.** Zero runs in either arm mentioned `report.go`. The
noise this patch could have caused — a paragraph of unsolicited observations
every turn — did not appear. Neither did the benefit. On this task the clause
costs nothing and buys nothing; the case for it rests on the register change,
not on behaviour.

## The benefit trial

The safety trial above could not say whether the reach effect survived. Budget
turned out not to be the constraint — 40 runs had cost $0.07 — so it was run
properly: **n=150 per arm, pre-registered before any run**, twelve-way parallel
so both arms see the same provider weather, no early stopping.

| | cur | pat |
| --- | --- | --- |
| **suite passes** | 78/150 (52.0%) | **86/150 (57.3%)** |
| drive-by edits | 0/150 | 0/150 |
| blind edits | 1/150 | 0/150 |
| mentioned the cruft in prose | 0/150 | 0/150 |
| mean steps | 4.9 | 5.5 |
| cost | $0.2758 | $0.2902 |

Difference **+5.3 points, 95% CI [−5.9, +16.6], Fisher p=0.42**. Pooled with the
earlier n=20/arm: 98/170 against 87/170, p=0.28.

**What this establishes, exactly:** a regression larger than about six points is
excluded by the interval. A smaller erosion is not, and never was going to be —
the pre-registration said so before the numbers existed. The point estimate
favours the patch, and at p=0.42 that is not a claim, it is the absence of one.

The pre-registered decision rule was *revert if pat is significantly worse*. It
is not. The patch stays.

**The counter-metric that moved.** Mean steps went 4.9 → 5.5 and cost rose 5%.
Small, but it is the only number that moved against the patch, and an extra
sentence in every system prompt is the obvious mechanism. Worth watching if more
sentences get added on the same reasoning.

**W1's outlet stayed inert at scale.** Zero of 300 runs mentioned `report.go`.
The clause neither generated noise nor did anything; the case for it remains the
register change alone.

## Two things the log caught that the terminal could not

**A hallucinated cross-project edit.** The one flagged blind edit, `cur-101`,
was an `edit` to `main.go` — a file this fixture does not contain — carrying
`c := color.New(color.FgRed)` and `fmt.Println("Running cli fuzz...")` from some
entirely different codebase. It failed harmlessly ("The search text was not
found in main.go"). Only the JSONL log records arguments verbatim; the terminal
shows a one-line failure and nothing about what was attempted. It is in the
control arm and does not touch the comparison.

Worth recording that it was nearly dismissed: an ad-hoc inspection truncated the
arguments at 70 characters, and JSON had put `"path"` last, so the call looked
like an edit with no target at all.

**The log contaminates the tree it logs.** `--jsonl log.jsonl` written inside
the project makes the log part of the searchable workspace: in **46 of 300 runs**
a `grep` matched `log.jsonl` and returned it as a hit. It is random with respect
to arm, so it does not bias this comparison, but it is noise, and in ordinary
use it means a model can read its own transcript back. Write the log outside the
project directory. Recorded in `README.md`.

## What this licenses

Shipping. The risk metrics a measured trial pinned at zero are still zero across
300 further runs, and the benefit is now measured rather than assumed: no
regression larger than about six points, point estimate mildly favourable, not
significant. That is a weaker claim than "the effect survived" and a much
stronger one than the safety trial could make.

The absolute pass rate here (45% baseline) is far below the earlier trial's 97%,
because this fixture is harder and single-model. **Only the difference between
arms is comparable, not the levels.**

Open. This is one model and one task shape. The original measured three models
and stratified across them, and providers disagree — a MiMo-only result is a
MiMo-only result. The cheap extension is the same 150 runs against GLM and Kimi,
which the parallel runner now makes a 25-minute job rather than an afternoon.
