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

## What this licenses

Shipping, on the narrow grounds the design supports: **the two risk metrics that
a measured trial pinned at zero are still zero, and cost is unchanged.** The
benefit is unverified and stays unverified; the comment in `prompts.go` now says
so, so nobody later reads the added sentence as measured.

The absolute pass rate here (45% baseline) is far below the earlier trial's 97%,
because this fixture is harder and single-model. **Only the difference between
arms is comparable, not the levels.**

Open, and the honest next step: this trial cannot say whether the reach effect
survived. Re-measuring that properly costs hundreds of runs across three models,
which is what the original cost. Until someone spends it, the added sentence is
a register change with a safety check, and should be described that way.
