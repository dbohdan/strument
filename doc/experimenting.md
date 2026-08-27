# The digital experimenter's handbook

Notes for running live experiments on Strument. Both senses of *digital* are
meant: the experimenter is a program, and so is the thing being measured.

Everything here was paid for. Each item names the run that taught it, because a
rule with the evidence attached survives a reader who disagrees with it, and a
rule without one gets deleted by the next person in a hurry.

The companion reading is the **Which model to reach for** and **Comparing two
prompts** sections of `CLAUDE.md`, which cover cost strata and arm
randomization. This is about everything that goes wrong *after* you have a
sound design.

---

## 1. Your instrument is made of the thing you are testing

**The single most expensive lesson so far.**

The compaction trial scored answers by matching `^ *ANSWER:` against session
output. `clearWaiting` (`internal/repl/output.go`) emits `\r\x1b[K`
unconditionally — including when stdout is a pipe — so the escape lands at the
*start* of the answer line:

```
[KANSWER: Because the upstream load balancer idles connections out at 60 …
```

Twelve of twenty-four sessions were scored as "never answered" when they had
answered correctly. And this is the part that matters:

| | apparent result | after stripping ANSI |
| --- | --- | --- |
| recalled the reason | 5/12 vs 4/12, **p = 1.0** | 10/12 vs 5/12, **p = 0.089** |

The broken scorer produced a *clean null*. A null result from a broken
instrument is indistinguishable from a null result, and the action it invites —
"no difference, ship the nicer-looking version" — is exactly the wrong one. The
change would have gone in.

That escape leak had been found earlier the same day and filed as cosmetic. It
was cosmetic for users and load-bearing for measurement.

**Do:** strip ANSI before scoring anything. Treat every defect you have ever
deferred as "cosmetic" as a candidate instrument fault, because cosmetic means
"does not change meaning for a human", and your scorer is not one.

## 2. Score a marker you asked for, never a position you inferred

The first version located the final answer by splitting output on `Tokens:` and
taking `parts[-2]`. In some sessions that captured the *previous* turn's answer,
so three recorded "failures" were failures of the parser.

The fix was to put `Put your whole answer on one line beginning with ANSWER:`
into the prompt — identically in both arms, so it cannot favour either — and
match that.

**Do:** make the thing you measure syntactically unmistakable, at the cost of
slightly perturbing the task. A small identical perturbation in both arms is
cheaper than an extraction heuristic that fails asymmetrically.

## 3. "No answer" and "wrong answer" are different columns

They mean opposite things — one is the instrument failing, the other is the
system failing — and a single boolean lumps them into a number that cannot be
interpreted. The rescore split them, and *that* is what exposed §1: a sudden
`answered=0/6` for one model is not a finding about that model.

This generalizes an older lesson: a provider returning `Empty response received
from LLM` and a model emitting a tool call as inline text look identical in a
summary and mean opposite things.

## 4. Save the raw output. Rescore instead of re-running

Every session's full stdout went into the results file. When the scorer turned
out to be wrong, fixing it and recomputing every number cost **zero API calls
and about forty seconds**. Without that, discovering the bug would have meant
paying for all twenty-four sessions again — which is exactly the moment where
one is tempted to accept the null instead.

**Do:** persist raw output per run, always. Strip it before committing the data
(it is megabytes); keep the scored fields as the record.

## 5. Confirm the mechanism fires before measuring its effect

The first fixture was a handful of four-line Go files. Twelve sessions ran, and
compaction fired **zero times** — the settled history reached 383 tokens against
a 1024-token budget. The metric was measuring nothing at all, and every number
was noise about an event that never happened.

The fix was a 46 KB fixture and a `context=16384` declaration, which puts
`maxChatHistoryTokens` at its 1024 floor while staying far above any real prompt
so `checkTokens` never fires and never blocks on a confirmation.

**Do:** instrument the *mechanism*, not only the outcome, and check it in the
pilot. Here that meant counting `Summarizing chat history` lines. If the
mechanism count is zero, no amount of n will help you.

The same rule caught a whole model producing nothing to measure. Verifying that
an interrupted turn resumes needs a turn to interrupt *mid-answer*, and two
GLM-5.3 runs came back looking like clean passes when nothing had been cut off
at all: it defaults to `"max"` reasoning effort, so the interrupt kept landing
during its thinking. One curl settled it — same prompt, 900-token cap:

| effort | reasoning tokens | answer characters |
| --- | --- | --- |
| default (`"max"`) | 897 | **0** |
| `"low"` | 0 | 4700 |

At its default this model cannot be observed streaming an answer at all inside
a short window, because within 900 tokens it has not started one. `reasoning`
on the `model()` call fixes it, and the two runs after that resumed *mid-word*
— cut at "their famous mathematica", picked up at "achievements".

**Do:** when a model is the instrument, check that it emits the thing you plan
to measure before you count anything. A model that spends the budget thinking
is not a null result, it is a broken instrument, and the two are indistinguishable
in a summary table.

## 6. Choose a target the system cannot leak

Two probes were scored. Recall of the *value* (`45`) came out 11/12 vs 9/12 —
useless, because 45 is sitting in the source and any model can read it back.
Recall of the *reason* ("the upstream load balancer idles connections out at
60") came out 10/12 vs 5/12, because that sentence exists nowhere but the
conversation.

**Do:** design the probe so the only path to the answer runs through the
mechanism under test. If the answer is recoverable from the artifact, you are
measuring the artifact.

## 7. Rebuild the baseline as your branch moves

The first run compared `HEAD` against a commit that differed in *two* ways: the
summarizer change under test, and a history-rotation fix that had landed in
between. That confounds.

The fix: a throwaway `git worktree` at `HEAD` with only the files under test
reverted to their old contents, built there, worktree removed.

```sh
git worktree add -q --detach /tmp/wt HEAD
git -C /tmp/wt checkout <older> -- <only the files under test>
(cd /tmp/wt && go build -o …/strument-base ./cmd/strument)
git worktree remove --force /tmp/wt
```

**Do:** define the baseline as *HEAD minus the change*, not as *an older
commit*. Then `cmp` the two binaries and refuse to spend if they are identical —
a build that silently produced two copies of the same arm is a whole afternoon.

## 8. Read individual transcripts. Then read more of them

This is in `CLAUDE.md` already and it earned its place twice more in one day:

- The three "failures" that were the parser (§2).
- A per-model split so clean it looked like a real finding: base scored 6/6 on
  MiMo and 0/6 on DeepSeek-v4-flash, in opposite directions per arm. That is a
  textbook provider-disagreement result. It was entirely the §1 artifact.

**Do:** when a split reverses cleanly across models, suspect the scorer *before*
you write the paragraph about how providers disagree. Real disagreement is
usually messier than that. One transcript settles in a minute what a summary
table makes mysterious.

## 9. Count confabulation separately from loss

Compaction failures came in two shapes:

> *"the poll interval value and the reasoning behind it were not established in
> the context I have access to"*

> *"We chose 45 seconds as a poll interval to balance between frequent updates
> and system load."*

The first is honest loss. The second is an invented reason nobody gave, and it
is far worse, because downstream nothing distinguishes it from a real answer —
not the user, not the next turn, not a later summary that folds it in again.

**Do:** score these as different outcomes. A change that converts loss into
confabulation is a regression even if the "recall" number improves.

## 10. Battle-tested beats better-reading

aider's summarize prompt is GPT-4-Turbo-era prose. The replacement was a clean
structured list — what the user asked for, decisions with reasons, files
changed, what is unfinished — and it read much better. It lost, 5/12 to 10/12,
on the metric it was written for, and cost 17% more.

Prior art that survived years of contact carries information that reading it
cannot recover. Rewriting it is a hypothesis, not an improvement, and it owes a
trial like any other.

## 11. Land correctness and performance changes separately

The compaction work bundled an honesty fix (the summary was a fabricated user
turn plus a fake assistant `"Ok."`) with a content rewrite. The trial could only
speak to the second, so the first had to be untangled from it by hand before
anything could be reverted.

**Do:** if part of a change is right regardless of the measurement, commit it on
its own first. Then the trial has one job and the revert has one target.

## 12. Practical mechanics

- **Keys live in the environment.** Write to a `chmod 600` file *outside the
  repository*, source it, `shred -u` it after. `internal/fixture/guard_test.go`
  scans the tree for key-shaped strings; do not make it the last line of
  defence.
- **Long runs need detaching.** The Bash tool's default timeout is two minutes
  (`shell_timeout`; `/run` is exempt, since the user typed that command).
  `setsid nohup … &` with output to a log, then wait on the results file:
  `until [ "$(wc -l < results.jsonl)" -ge N ]; do sleep 20; done`.
- **Four-way parallelism is about the ceiling.** Beyond that OpenRouter
  rate-limiting produces hangs that look exactly like a deadlock in the harness.
  That cost three runs and a concurrency investigation before five instances
  against a local stub came back clean and proved the harness innocent.
- **`--yes` in trials**, or a confirmation prompt will silently stall a session
  that was supposed to be unattended.
- **Fix the random seed and shuffle the job list**, so a rerun is comparable and
  the arm is not confounded with the hour it ran.

## 13. Have another model read the scorer

The cheapest fix for §1, and it went unused for nine bugs.

A scorer is written in the same breath as the belief it is meant to test, by
whoever holds that belief. So it reaches for the string that was on screen a
moment ago — which is exactly the string that is present for reasons other than
the one being measured. The check inherits the expectation instead of testing
it.

A second model does not hold the belief. Given four checks from this project,
three of them broken and one sound, and told to name a concrete failing input
or say it is sound:

| | FINISHED-in-command | substring-for-Latin | unclosed thinking tag | the sound one |
| --- | --- | --- | --- | --- |
| MiMo-V2.5 | caught | caught | missed | correctly sound |
| Gemini 3.7 Flash | caught | caught | caught | correctly sound |

Three of three by union, no false alarm on the sound one, about two-tenths of a
cent. **They also found a bug the author had already "fixed" and got wrong**:
`"roma" in reply.lower()` had been patched to normalize `Rōma`, and both
reviewers pointed out it still passes *"The capital of Italy is Roma, a
beautiful city"* — an English sentence scoring as obedience to "answer in Latin
only". The check was measuring *mentions Rome*, and its numbers had already
been reported as though it measured Latin.

Two things make it work:

- **Ask for a concrete failing input, quoted.** "Review this" gets
  "consider edge cases". *"Name an input where this returns the wrong answer,
  or say it is sound"* gets the input.
- **Say that at least one is sound.** Otherwise flagging everything is a
  winning strategy, and a reviewer that flags everything has told you nothing.

The ceiling is real: a third of the faults in this file needed context that is
not in the scorer — what the transcript actually prints, what the test binary
actually names its cases. Hand over the scorer *and* a sample of its real
input, or the reviewer is guessing at the half that matters.

## 14. The same trick on a bigger artifact, and what it costs

§13 scaled up: five models reviewing 17.6 KB of rendered prompts rather than
two reviewing an 80-line scorer.
[`experiments/2026-08-prompt-review.md`](experiments/2026-08-prompt-review.md)
has the run. It works, less well, and the reasons generalize.

- **The ensemble is the instrument.** Five reviewers found nine defects; the
  best single reviewer found five, and no one of them found both planted
  controls *and* both regressions. On the scorer, one reviewer was nearly
  enough. Attention spreads across surface area.
- **Do not rank by agreement.** All five caught a grammar bug in one sentence;
  one caught the false factual claim in the sentence above it, and that is the
  one that can cost something. Counting votes would have inverted the order.
- **Render the artifact from the running code, and record which configuration
  it is.** The dump caught three fossils precisely because it was the real
  bytes — but it fixed one field to empty, silently deleting a conditional, and
  a reviewer correctly reported what it was shown as missing. Absence in one
  render is not absence.
- **Check the harness against the prompt before spending.** The review prompt
  invited reading; the step budget capped it; script mode has no tty to answer
  "Keep going? (Y/n)". One model spent $0.58 reading twelve files and returned
  no review at all.
- **Cheap reasoning was not worse.** High reasoning on the priciest model spent
  $4.19 and returned nothing; the same model at low returned a full review for
  $1.24. The cheapest model's *best* run was its lowest setting. The
  default-to-cheap rule in [`README.md`](README.md) survives contact with a task
  that looks like it wants deliberation.

## 15. The renderer has two forms, and your scorer knows one

The eleventh scorer bug, from
[`experiments/2026-08-symbol-uptake.md`](experiments/2026-08-symbol-uptake.md),
because it generalizes past reasoning blocks.

Strument prints reasoning two ways: a multi-line block that opens with the
marker alone on its line and closes with `‹/›`, and a one-line aside that is
`‹thinking› text` and simply ends at the newline. A scorer that stripped
`‹thinking›…‹/›` and then treated any unclosed marker as running to the end of
the output **deleted the final answer of every run whose last aside was
one-line**. Recall was deflated in both arms, unevenly, and the aggregate still
looked plausible.

Two things worth carrying:

- **When an aggregate disagrees with a transcript you have read, the transcript
  wins.** A pilot had scored 3/3; the batch reported means near 0.8/3. That gap
  was the whole signal, and it was visible before any statistics.
- **The check that could not fail is the one that found it.** A count of runs
  naming a nonexistent function returned 0/21 and 0/27 — while `Coder.send` sat
  in a transcript I had quoted an hour earlier. §1 says break the check on
  purpose and watch it go red; the corollary is that a check returning a clean
  zero deserves the same suspicion as one returning a clean p=1.0.

## 16. Look for the measurement the confound cannot reach

Two changes shipped together — a tool's schema description and its output — and
separating them looked like two more binaries and another 48 runs.

It needed neither, because **a model chooses its first tool from the schema
alone**. It has not seen any output yet. The first tool call is therefore a
measurement of the description with the other factor held out *by construction*,
and it was already sitting in transcripts that had been paid for:

| | base | new | p |
| --- | --- | --- | --- |
| first tool call is the tool under test | 4/24 | 14/24 | **0.006** |
| any call to it | 7/24 | 14/24 | 0.080 |

The isolated effect was larger and better supported than the bundled one.

Before designing arms to separate two factors, ask whether some **event in the
run happens before one of them can act**. Ordering is a free instrument: a
choice made at step one cannot depend on information that arrives at step two.
The same trick applies to anything with a first-move — which model was picked,
which file was opened, whether a question was asked before any tool ran.

And verify the set relation rather than inferring it from totals. "14 used it
and 14 used it first, so they must be the same 14" is true here, and was checked
by listing both sets, because the alternative reading — some other 14 — is the
kind of thing that is obvious right up until it is wrong.

## 17. Three shapes of a check that cannot fail

§1 is about a scorer that reported the wrong answer. This one is about checks
that report *no* answer — assertions that pass whether or not the code works,
so the only thing they measure is that they ran. Three turned up in a single
day's work on the harness itself, and they were caught the same way each time:
by breaking the code on purpose and watching the check stay green.

They are worth listing by shape, because none of them looks wrong while you are
writing it.

**A tautology on the host that runs it.** A test asserted that a path uses the
platform's separators as `got != filepath.FromSlash(got)`. On Unix `FromSlash`
is the identity, so that compares a string to itself. It could only ever fail on
Windows, and it was written and reviewed on Linux. *Tell:* the assertion is
built from a function of the value being asserted about, rather than from an
expectation written down independently.

**An assertion behind an early return.** A test checked that a lookup accepts
the argument value `definition` by asserting the output does not contain
`Unknown kind`. But the lookup checks for a language parser before it validates
the argument, and the fixture had no parser — so every kind, valid or not,
answered "the language parser is not available", and the test passed with
`definition` deleted from the accepted set. *Tell:* asserting the *absence* of
an error rather than the presence of the right answer. An absence is satisfied
by every path that never gets far enough to produce it.

**A comparison the defect does not change.** The same test, second attempt:
with a parser wired up, it compared the two kinds' whole output and required
them to differ. They still did with the feature broken, because the *header* is
worded from the argument while the *results* come from what the argument was
translated into — so a lookup that ignored the argument entirely still printed
"referenced" above the definition's line. It passes now by asserting line
numbers: `definition` finds line 3, `reference` finds line 5 and not line 3.
*Tell:* the assertion is on prose the code assembles near the input, rather than
on the part of the output the code path under test actually decides.

**Do:** for every check you would be upset to lose, break the thing it guards
and watch it go red. Not the whole feature — the specific line. Two of the three
above survived a plausible-looking break and failed only on the second, more
precise one — which is itself the finding: *how* you break it is part of the
check.

Two practical notes from doing that. Make sure the broken version still
**compiles** — a build failure is not a test failure, and `go test` will hand
you a stale cached `ok` from the last good build if you read past the error.
And prefer breaking the code to deleting the assertion: deleting tells you the
assertion runs, while breaking tells you it discriminates.

---

## The short version

Most of what goes wrong is not statistics. It is the equipment.

Before believing any result, ask in this order: *did the mechanism fire, did the
scorer see what I think it saw, do the two arms differ in exactly one thing, and
have I read three transcripts?* Only then look at the p-value — and remember
that a broken instrument's favourite output is `p = 1.0`.

And get the verdict out of the hands of whoever wants it to pass: break the code
on purpose and watch the check go red (§1, §17), or hand the check to a model
that does not share your expectation (§13). Both work because neither routes
through your judgment. Resolving to be more careful does not; it was tried, for
nine consecutive bugs.
