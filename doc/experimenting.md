# The digital experimenter's handbook

Notes for language models and developers running live experiments on Strument.
Both senses of *digital* are meant: the experimenter is a program, and so is
the thing being measured.

Everything here was paid for. Each item names the run that taught it, because a
rule with its evidence attached survives a reader who disagrees with it and a
rule without one gets deleted by the next person in a hurry. If you shorten a
section, keep the number: the count is the part that is hard to argue with.

The companion reading is the **Which model to reach for** and **Comparing two
prompts** sections of `CLAUDE.md`, which cover cost strata and arm
randomization. This handbook covers failures in fixtures, scorers, runners, and
interpretation.

If you are preparing a run, start with the pre-run checklist and §12. The rest
is the evidence and failure patterns behind those checks; use the section
headings to investigate a surprising result or a broken run.

## The pre-run checklist

Before spending on a run, ask these questions in order:

- **Did the mechanism fire?** Instrument it and confirm it in the pilot (§5).
- **Did the model use the thing being tested?** A feature that can be declined
  was offered, not applied (§18).
- **Could the fixture produce the failure being measured?** Put the relevant
  situation in the task, including the counter-arm (§18).
- **Can the scorer distinguish no answer, wrong answer, provider failure, and
  model output in the wrong format?** Save raw output and test the parser in
  both directions (§§1–4, §15, §17).
- **Do the arms differ in exactly the intended way?** Build the baseline from
  `HEAD`, compare the artifacts, and refuse identical arms (§7).
- **Do any relevant assertions still pass in the arm designed to make them
  fail?** Apply a targeted sabotage and assert that the sabotage itself applied
  (§17).
- **Did the runner finish, or did it only stop reporting?** Wait on the specific
  process and record worker failures (§19).
- **Has the resume path been exercised deliberately?** Run it with a stub before
  the batch needs it (§20).
- **Did you read three transcripts, including an anomalous one?** A transcript
  can settle what an aggregate cannot (§8).

## Failure types and where to look

| symptom or failure type | start with |
| --- | --- |
| Scorer or output-parsing failure | §§1–4, 15, 17 |
| Model produces no usable answer, or spends the budget thinking | §5 |
| Provider failure versus model output in the wrong format | §3, then inspect the raw output (§4) |
| Answer can be recovered without the mechanism | §6 |
| Arms contain an unintended difference or are identical | §§7, 16, 18 |
| Treatment was not reached, or the fixture never contained the phenomenon | §18 |
| Runner stopped reporting, timed out, or cannot resume | §§19–20 |
| A bug report names the wrong subsystem | §21 |

---

## 1. Your instrument is made of the thing you are testing

The compaction trial scored answers by matching `^ *ANSWER:` against session
output. `clearWaiting` (`internal/repl/output.go`) emits `\r\x1b[K`
unconditionally — including when stdout is a pipe — so the escape lands at the
*start* of the answer line:

```
[KANSWER: Because the upstream load balancer idles connections out at 60 …
```

Twelve of twenty-four sessions were scored as "never answered" when they had
answered correctly. Correcting the scorer changed the result:

| | apparent result | after stripping ANSI |
| --- | --- | --- |
| recalled the reason | 5/12 vs 4/12, **p = 1.0** | 10/12 vs 5/12, **p = 0.089** |

The scoring error made the arms look equivalent. Without inspecting the
transcripts, it would have been easy to treat that result as evidence that the
replacement caused no regression.

That escape leak had been found earlier the same day and filed as cosmetic. It
was cosmetic for users and load-bearing for measurement.

**Do:** strip ANSI before scoring rendered terminal output. A display defect that
is cosmetic for a human reader can still break a parser.

## 2. Score a marker you asked for, never a position you inferred

The first version located the final answer by splitting output on `Tokens:` and
taking `parts[-2]`. In some sessions that captured the *previous* turn's answer,
so three recorded "failures" were failures of the parser.

The fix was to put `Put your whole answer on one line beginning with ANSWER:`
into the prompt — identically in both arms, so it cannot favour either — and
match that.

**Do:** make the thing you measure syntactically unmistakable. An explicit
answer marker perturbs the task slightly and is far easier to validate than a
position-based extraction rule. Use the same instruction in both arms.

## 3. "No answer" and "wrong answer" are different columns

“No answer” and “wrong answer” require different diagnoses. A missing answer
may reflect a provider failure, exhausted output budget, protocol failure, or
extraction bug. A wrong answer means usable output reached the scorer but did not
satisfy the task. A single boolean hides these differences. The rescore split
them, and *that* is what exposed §1: a sudden `answered=0/6` for one model needs
investigation before it is interpreted as a model-performance result.

This generalizes an older lesson: a provider returning `Empty response received
from LLM` and a model emitting a tool call as inline text look identical in a
summary and mean opposite things.

**Warning sign:** do not classify either case from the summary row. Inspect the
raw response and process status. A provider failure is recorded in the request or
stream outcome, often with no usable model content; inline tool-call markup is
model-produced content that bypassed the tool-call protocol. Keep them as
separate categories in the scorer (§4).

## 4. Save the raw output. Rescore instead of re-running

Every session's full stdout went into the results file. When the scorer turned
out to be wrong, fixing it and recomputing every number cost **zero API calls
and about forty seconds**. Without that, discovering the bug would have meant
paying for all twenty-four sessions again — which is exactly the moment where
one is tempted to accept the null instead.

**Do:** persist each run’s raw output so it can be rescored. Keep the large raw
transcripts outside the committed dataset; commit the scored fields.

## 5. Confirm the mechanism fires before measuring its effect

The first fixture was a handful of four-line Go files. Twelve sessions ran, and
compaction fired **zero times** — the settled history reached 383 tokens against
a 1024-token budget. The runs could not measure compaction’s effect because
compaction never occurred.

The fix was a 46 KB fixture and a `context=16384` declaration, which puts
`maxChatHistoryTokens` at its 1024 floor while staying far above any real prompt
so `checkTokens` never fires and never blocks on a confirmation.

**Do:** instrument the *mechanism*, not only the outcome, and check it in the
pilot. Here that meant counting `Summarizing chat history` lines. If the
mechanism count is zero, increasing the sample size will not fix the design.

The same rule caught a whole model producing nothing to measure. Verifying that
an interrupted turn resumes needs a turn to interrupt *mid-answer*, and two
GLM-5.3 runs came back looking like clean passes when nothing had been cut off
at all: it defaults to `"max"` reasoning effort, so the interrupt kept landing
during its thinking. One curl settled it — same prompt, 900-token cap:

| effort | reasoning tokens | answer characters |
| --- | --- | --- |
| default (`"max"`) | 897 | **0** |
| `"low"` | 0 | 4700 |

In these runs, the model did not begin its answer within the 900-token output
budget at its default reasoning setting. `reasoning` on the `model()` call fixes
it, and the two runs after that resumed *mid-word* — cut at "their famous
mathematica", picked up at "achievements".

**Do:** when a model is the instrument, check that it emits the thing you plan
to measure before you count anything. A run that spends its entire output budget
on reasoning cannot test mid-answer interruption.

### Set `reasoning="low"` on every model in a trial, and check it took

That GLM-5.3 finding is not about GLM-5.3. **Pin reasoning low on every model in
every arm** unless the reasoning is itself the thing being measured, and verify
that the setting took effect end to end rather than assuming that declaring it in
`model()` was sufficient.

GLM-5.3-Flash and Qwen3.8 both default to maximum effort. Defaults vary across
models and may change, so set reasoning explicitly rather than assuming the
default is suitable.

What it costs when you forget:

- **Output that is not there.** Two models asked to review a scorer at a
  20-token cap returned `content: null` with the whole budget spent in
  `reasoning`, which reads through an SDK as an API failure and through a
  scorer as "no answer".
- **Cost metrics that are not comparable.** The 2026-09 code-result trial ran
  GLM at `"low"` and MiMo and DeepSeek at their defaults, because the config
  set it per model rather than as a rule. Reasoning lands in output, so the
  input-token and step columns survive it and every conclusion drawn from
  output tokens or latency does not — pooled across arms as much as compared
  across models, since the arms pool the same three models. The write-up says
  so under its own heading.
- **Wall-clock, which caps the sample.** Reasoning at maximum is the difference
  between a batch that finishes while you watch and one that finishes tomorrow,
  and the sample size ends up set by patience.

**Do:** put the reasoning setting in the shared config, not per model, so
forgetting one is impossible; then read one transcript per model and confirm
there is an answer under the thinking.

## 6. Choose a probe whose answer is available only through the mechanism

Two probes were scored. Recall of the *value* (`45`) came out 11/12 vs 9/12 —
useless, because 45 is sitting in the source and any model can read it back.
Recall of the *reason* ("the upstream load balancer idles connections out at
60") came out 10/12 vs 5/12, because that sentence exists nowhere but the
conversation.

**Do:** design the probe so the only path to the answer runs through the
mechanism under test. If the model can recover the answer from the source files,
the probe does not isolate conversational recall.

## 7. Rebuild the baseline as your branch moves

The first run compared `HEAD` against a commit that differed in *two* ways: the
summarizer change under test, and a history-rotation fix that had landed in
between. That introduced a second difference between the arms.

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
a build that silently produced two copies of the same arm can waste the entire
batch.

## 8. Read individual transcripts. Then read more of them

This is in `CLAUDE.md` already and it earned its place twice more in one day:

- The three "failures" that were the parser (§2).
- A per-model split so clean it looked like a real finding: base scored 6/6 on
  MiMo and 0/6 on DeepSeek-v4-flash, in opposite directions per arm. It was
  entirely the §1 artifact.

**Do:** inspect transcripts behind a clean per-model split before attributing it
to provider differences. In this trial, the apparent disagreement came from the
scorer. One transcript settles in a minute what a summary table makes mysterious.

## 9. Count confabulation separately from loss

Compaction failures came in two forms:

> *"the poll interval value and the reasoning behind it were not established in
> the context I have access to"*

> *"We chose 45 seconds as a poll interval to balance between frequent updates
> and system load."*

The first is honest loss. The second is an invented reason nobody gave, and it
is far worse, because a later reader or summarizer may treat the invented reason
as an established fact.

**Do:** score these as different outcomes. A change that converts loss into
confabulation is a regression even if the "recall" number improves.

## 10. Battle-tested beats better-reading

aider's summarize prompt is GPT-4-Turbo-era prose. The replacement was a clean
structured list — what the user asked for, decisions with reasons, files
changed, what is unfinished — and it read much better. In this trial, it recalled
the reason in 5/12 runs, compared with 10/12 for the existing prompt, and cost
17% more.

An established prompt may encode lessons that are not obvious from its wording.
A clearer rewrite is a hypothesis about performance, not evidence of an
improvement.

## 11. Land correctness and performance changes separately

The compaction work bundled an honesty fix (the summary was a fabricated user
turn plus a fake assistant `"Ok."`) with a content rewrite. The trial could only
speak to the second, so the first had to be untangled from it by hand before
anything could be reverted.

**Do:** commit a correctness change separately when its justification does not
depend on the trial. That leaves one hypothesis to test and one change to revert.

## 12. Practical mechanics

- **Keys live in the environment.** Write to a `chmod 600` file *outside the
  repository*, source it, `shred -u` it after. `internal/fixture/guard_test.go`
  scans the tree for key-shaped strings; do not make it the last line of
  defence.
- **Long runs need detaching.** The Bash tool's default timeout is two minutes
  (`shell_timeout`; `/run` is exempt, since the user typed that command).
  `setsid nohup … &` with output to a log, then **wait on the process, not on
  its output**: `until ! kill -0 "$PID" 2>/dev/null; do sleep 20; done`, and
  check the log afterwards to learn whether it finished or died. Waiting for a
  results file to reach N lines — which is what this bullet used to advise —
  cannot tell a crash from a slow run. Wait for the specific process to exit,
  then inspect its status and logs to determine whether the batch completed
  successfully. Capture the pid; a `pgrep -f` pattern will match the next run of
  the same script. If a log watcher is unavoidable, match every terminal state
  as a secondary signal; it must not replace waiting on the specific process.
- **Type-check the runner before launching it** (§19). The error paths are the
  ones a one-off script never exercises until they decide whether the run
  survives.
- **Start with no more than four concurrent runs.** Higher concurrency caused
  OpenRouter rate-limiting problems in these trials. Hangs can look exactly like
  a deadlock in the harness.
  That cost three runs and a concurrency investigation before five instances
  against a local stub came back clean and proved the harness innocent.
- **`--yes NAME` in trials**, naming every prompt the run can raise, otherwise
  an unanswered prompt may stall an unattended session. Include
  `steps` for anything long enough to reach the budget; `--yes all` is the
  blunt version when the run is disposable.
- **Fix the random seed and shuffle the job list**, so a rerun is comparable and
  the arm is not confounded with the hour it ran.

## 13. Have another model read the scorer

A scorer’s author can carry assumptions from the experiment into the
implementation. A separate reviewer may challenge those assumptions, especially
when asked for a concrete input that the scorer misclassifies.

Given four checks from this project,
three of them broken and one sound, and told to name a concrete failing input
or say it is sound:

| | FINISHED-in-command | substring-for-Latin | unclosed thinking tag | the sound one |
| --- | --- | --- | --- | --- |
| MiMo-V2.5 | caught | caught | missed | correctly sound |
| Gemini 3.7 Flash | caught | caught | caught | correctly sound |

Together, the reviewers caught all three faulty checks, and neither flagged the
sound one. **They also found a bug the author had already "fixed" and got wrong**:
`"roma" in reply.lower()` had been patched to normalize `Rōma`, and both
reviewers pointed out it still passes *"The capital of Italy is Roma, a
beautiful city"* — an English sentence scoring as obedience to "answer in Latin
only". The check was measuring *mentions Rome*, and its numbers had already
been reported as though it measured Latin.

Two things make it work:

- **Ask for a concrete failing input, quoted.** "Review this" gets
  "consider edge cases". *"Name an input where this returns the wrong answer,
  or say it is sound"* gets the input.
- **Include a known-sound check in the review set and say that the set contains
  one.** Otherwise flagging everything is a winning strategy, and a reviewer
  that flags everything has told you nothing.

**Scorer review also has limits.** A third of the faults in this file needed
context that is not in the scorer — what the transcript actually prints, what
the test binary actually names its cases. Hand over the scorer *and* a sample of
its real input, or the reviewer is guessing at the half that matters.

## 14. The same trick on a bigger artifact, and what it costs

A later trial extended this approach: five models reviewing 17.6 KB of rendered
prompts rather than two reviewing an 80-line scorer.
[`experiments/2026-08-prompt-review/README.md`](experiments/2026-08-prompt-review/README.md)
has the run. The reviewers found useful defects, but individual coverage was
lower.

- **The ensemble is the instrument.** Five reviewers found nine defects; the
  best single reviewer found five, and no one of them found both planted
  controls *and* both regressions. On the scorer, one reviewer was nearly
  enough. No single reviewer covered the larger artifact reliably.
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
[`experiments/2026-08-symbol-uptake/README.md`](experiments/2026-08-symbol-uptake/README.md),
because it generalizes past reasoning blocks.

Strument prints reasoning two ways: a multi-line block that opens with the
marker alone on its line and closes with `‹/›`, and a one-line aside that is
`‹thinking› text` and simply ends at the newline. A scorer that stripped
`‹thinking›…‹/›` and then treated any unclosed marker as running to the end of
the output **deleted the final answer of every run whose last aside was
one-line**. Recall was deflated in both arms, unevenly, and the aggregate still
looked plausible.

Two useful observations:

- **When a score conflicts with the transcript it describes, investigate the
  discrepancy before trusting the aggregate.** A pilot had scored 3/3; the batch
  reported means near 0.8/3. That gap was the whole signal, and it was visible
  before any statistics.
- **An unexpected zero exposed the extraction bug.** A count of runs naming a
  nonexistent function returned 0/21 and 0/27 — while `Coder.send` sat in a
  transcript I had quoted an hour earlier. §1 says break the check on purpose and
  watch it go red; the corollary is that a check returning a clean zero deserves
  the same suspicion as one returning a clean p=1.0.

## 16. Look for the measurement the confound cannot reach

Two changes shipped together — a tool's schema description and its output — and
separating them looked like two more binaries and another 48 runs.

It needed neither, because **a model chooses its first tool from the schema
before seeing that tool’s output**. The first tool call is therefore a measurement
of the description with the other factor held out *by construction*, and it was
already sitting in transcripts that had been paid for:

| | base | new | p |
| --- | --- | --- | --- |
| first tool call is the tool under test | 4/24 | 14/24 | **0.006** |
| any call to it | 7/24 | 14/24 | 0.080 |

The isolated effect was larger and better supported than the bundled one.

Before designing arms to separate two factors, ask whether some **event in the
run happens before one of them can act**. Event order can sometimes isolate an
effect without additional runs: a choice made at step one cannot depend on
information that arrives at step two.
The same trick applies to anything with a first-move — which model was picked,
which file was opened, whether a question was asked before any tool ran.

The run IDs confirmed that every run using the tool selected it first.

## 17. Three ways a check can pass without testing its claim

§1 is about a scorer that reported the wrong answer. This one is about checks
that report *no* answer — assertions that pass whether or not the code works,
so the only thing they measure is that they ran. Three turned up in a single
day's work on the harness itself, and they were caught the same way each time:
by breaking the code on purpose and watching the check stay green.

They are worth listing by failure mode, because none of them looks wrong while
you are writing it.

**A tautology on the host that runs it.** A test asserted that a path uses the
platform's separators as `got != filepath.FromSlash(got)`. On Unix `FromSlash`
is the identity, so that compares a string to itself. It could only ever fail on
Windows, and it was written and reviewed on Linux. *Warning sign:* the
assertion is built from a function of the value being asserted about, rather than
from an expectation written down independently.

**An assertion behind an early return.** A test checked that a lookup accepts
the argument value `definition` by asserting the output does not contain
`Unknown kind`. But the lookup checks for a language parser before it validates
the argument, and the fixture had no parser — so every kind, valid or not,
answered "the language parser is not available", and the test passed with
`definition` deleted from the accepted set. *Warning sign:* asserting the
*absence* of an error rather than the presence of the right answer. An absence is
satisfied by every path that never gets far enough to produce it.

**A comparison the defect does not change.** The same test, second attempt:
with a parser wired up, it compared the two kinds' whole output and required
them to differ. They still did with the feature broken, because the *header* is
worded from the argument while the *results* come from what the argument was
translated into — so a lookup that ignored the argument entirely still printed
"referenced" above the definition's line. It passes now by asserting line
numbers: `definition` finds line 3, `reference` finds line 5 and not line 3.
*Warning sign:* the assertion is on prose the code assembles near the input,
rather than on the part of the output the code path under test actually decides.

**Do:** for every check you would be upset to lose, break the thing it guards
and watch it go red. Target the behavior the assertion is meant to protect,
rather than breaking an unrelated part of the feature. Two of these checks stayed
green after the first attempted mutation. A more targeted mutation was needed to
expose the gap.

Two practical notes from doing that.

Make sure the broken version still **compiles** — a build failure is not a test
failure. An earlier draft of this section said `go test` hands you a stale
cached `ok` after a build error, which does not reproduce: the package that
failed to build reports `FAIL [build failed]` and the exit status is 1. What
*does* happen, on a two-package fixture built for this, is that the other
package's `ok tmpmod/a (cached)` prints **above** the build error, so output
skimmed rather than read still shows a green line. Read the exit status, not
the lines.

And prefer breaking the code to deleting the assertion. Deleting is the weaker
move because a deleted assertion cannot report anything at all: if the test
still passes you have learned nothing about whether it reached that line, only
that nothing else in the test failed. Breaking the code leaves the assertion in
place to discriminate, and a green result then tells you it does not.

---

### 17a. Three ways verification can falsely report success

A day spent writing a transcript auditor produced three failure modes §17 does
not cover. All three are worse than the ones above, because in each case the
check is *reported as verified* — the green is quoted as evidence rather than
merely trusted.

**A control that never applied.** The way to trust a check is to break the code
and watch it go red (§1, §17). That control is itself a check, and it fails
silently: a patch whose anchor no longer matches changes nothing, the suite
stays green, and the green gets written up as "verified to discriminate". This
happened three times in one project — a `sed` that missed after a rename, a
`replace()` whose anchor a refactor had moved, a comprehension rebinding that
Python's scoping made a no-op. *Warning sign:* the control reports success
without reporting that it modified anything. *Fix:* make the sabotage assert its own
application and refuse to report a result otherwise. A control that cannot say
"I did nothing" is not a control:

```python
assert old in s, "ANCHOR MISSING -- control did not apply, result means nothing"
```

**A test rewritten to match new output instead of keeping its claim.** A guard
named `test_summary_names_the_transcript` was written for a real defect: the
report header printed a source filename instead of the transcript. A later
refactor changed the output format, and the test was updated to assert that a
`TOTAL` block existed — keeping its name, its green status and its place in the
file while abandoning what it checked. It had been passing vacuously for three
commits, including one whose message said the guard was verified. *Warning sign:*
a test changed in the same commit as the output it checks, where the assertion got
looser. *Fix:* when output changes, re-derive the assertion from the claim in
the test's name, not from the new output.

**Verification in one direction only.** A check that can confirm but not deny
passes for the wrong reason and reads exactly like one that works. Three
instances, all different on the surface: a live-pass scorer whose control proved
it could recognise a no-op but never a success, so nine correct runs read as six
failures; a false-positive fix verified only by the false positive vanishing,
which "make the metric report nothing" satisfies perfectly; and a fixture set
that demonstrated the bug but could not verify its absence. *Warning sign:*
every case in the control
has the same expected outcome. *Fix:* pair them. Every fix that makes something
stop firing needs a companion asserting the thing that should still fire, and
the pair is what makes either a measurement.

That last one is cheap enough to make a habit at the point of asking rather
than at the point of reviewing. A delegated fix specified as *"X must now report
0, and Y must still report 1"* cannot be satisfied by silencing the metric; the
same request without Y can.

## 18. A clean null has more than one cause, and they look alike

§17 is about a check that cannot fail. A trial can also return identical results
because neither arm encountered the behavior being tested. All three cases below
turned up in one afternoon, in a trial of whether `edit` should grow a
`replace_all` argument.

The design was ordinary: three arms (first-match, unique-or-fail, unique +
`replace_all`), six models, three rename fixtures, 54 runs, scoring by diff
against an expected tree. Every arm came back 18/18 correct with zero
unintended changes. Those scores alone do not establish the feature’s usefulness
or safety.

**The treatment was never applied.** `replace_all` existed in the third arm and
the models used it *once in eighteen runs*. Five of six never touched it. So for
seventeen runs the treatment arm was the control arm with a longer schema, and
whatever the numbers said about it was a statement about `edit`, not about
`replace_all`. *Warning sign:* the treatment is something the model may decline.
A feature it can ignore is not a manipulation you have applied; it is one you have
offered. Check that it is *reached* before spending — this is a different
question from whether the arms differ, and the pilot answers it for the price of
one run per arm.

**The hazard was never triggered.** The fixtures were built with decoys — a
`maxRetriesExceeded` beside the `maxRetries` being renamed, four occurrences
inside user-facing strings, a same-named variable in an out-of-scope file — so
that a careless replacement would be visible. Zero unintended changes came back
in every arm, *including the unsafe one that silently edits the first match*.
That is not the decoys clearing the design; it is the decoys never firing.
Running the failure classifier over all 172 edit calls said why: zero failures
of any kind, because the models supplied unique context exactly as the tool
description asks. *Warning sign:* the counter-metric reads zero everywhere,
the unsafe arm included. A hazard that does not fire for the arm built to trip on
it has
told you about your fixture, not about your design.

**The arms were the same program.** Two of the three binaries had identical
sizes, because a `cd` in one shell invocation persisted into the next and the
control arm was built from the treatment's source tree. Caught by the standing
rule from §7 — compare the built arms and refuse to spend if they are the
same — which here meant running one probe edit through each binary and
watching them answer differently. Without it the trial would have reported no
difference between unique-or-fail and `replace_all` for the excellent reason
that they were the same executable. *Warning sign:* two artifacts that should
differ have the same checksum. Compare them; do not infer from the build having
succeeded.

What survives all three is a real finding, but a narrower one than the table
suggests: models rarely reach for `replace_all` (1/18), which is an argument
against adding it that does not depend on the risk ever being measured. The
trial cannot say whether `replace_all` is dangerous, because it never got used
enough to be. Say that, rather than letting 18/18 stand as a safety result.

### The mirror image: a metric that counts the wrong thing

The same trial produced the opposite fault, and it is worth putting beside the
others because it is the one that would have shipped. A "revisit" counter, meant
to find the coordinated multi-file edits that would justify a patch tool,
counted every return to an already-edited file — so three sequential edits to
one file scored two revisits. Across the arms it read 7, 16 and 24, which looks
like coordination pressure and is nothing of the kind: a patch would not
collapse "make three changes to this file in a row". Counting only a return
*across* another file gives 0 in every arm.

*Warning sign:* a metric that omits a condition essential to the claim.
"Returned to a file" is not "returned to a file after leaving it". Write the
metric's definition next to the claim it supports and check that the words
match; then check the metric can still fire, on a fixture where the phenomenon
genuinely occurs, or you have traded a wrong number for a silent one.

#### The counter-arm is what finds this

The fault recurred the day after this section was written, which is the best
argument for a sharper tell than "read the definition carefully". A live check
for webfetch asserted that a fetch nobody was asked about is still shown on
screen: the marker and the URL appear in the turn. It passed in the arm built
to break it — a binary whose grant expires with the turn, where turn two *was*
prompted. The prompt draws the same marker and the same URL, so the assertion
was reading the question and calling it the announcement. Adding one clause —
and the question is *not* on screen — made it fail there, correctly.

#### A fixture that cannot contain the phenomenon

The other half of "the treatment was never applied", and cheaper to hit than the
model-declines-it version, because no model has to decline anything — the
fixture simply never creates the situation.

A live check for a *turn-scoped* permission drove one action per turn: search in
turn one, search in turn two. Every assertion was about a grant covering a turn,
and no turn ever contained a second action for the grant to cover. Worse, the
assertions themselves had been copied from the check for a *session*-scoped
permission next door, so they demanded that turn two not ask — which for a
turn-scoped grant is the wrong answer, and the correct behaviour was reported as
three failures. Then the driver, having never answered the prompt it did not
expect, fed the following message to it as the answer, and the last turn never
ran at all.

Every one of those has the same root: the fixture was inherited from a
neighbouring feature rather than derived from this one. The fix was to put two
searches in one turn and *count the prompts* — one, not two — the only
arrangement in which a turn-scoped grant is a thing that happens at all.

*Check:* write down the sentence the check is meant to prove, and find the line
in the fixture that creates its subject. "An `a` covers the rest of the turn"
has a subject — a second action in the same turn — and a fixture with one
action per turn does not contain it. Copying a fixture from the feature next door is
how the subject goes missing, because the neighbouring feature's structure
encodes *its* scope, not yours.

*Warning sign:* **an assertion that passes in the arm where the phenomenon cannot
have occurred is mis-defined, whatever its name says.** This is stronger than
inspecting the definition, because it is mechanical: you already built the
counter-arm to prove the rig can fail, so read *every* line of its output, not
just the ones you expected to flip. Unexpected passes in the counter-arm can
reveal additional scoring errors. If an assertion about the targeted behavior
passes in an arm where that behavior cannot occur, the assertion is measuring
something else.

---

## 19. A runner that dies quietly looks exactly like one that is slow

§18 is about an experiment that cannot fail. This one is about the harness
around it, and it cost an hour of a 234-run trial being reported as healthy
while its bookkeeping was dead.

One run hit the timeout. The handler was:

```python
except subprocess.TimeoutExpired as e:
    text = (e.stdout or "") + (e.stderr or "") + "\n[TIMEOUT]\n"
```

`TimeoutExpired` carries **raw bytes even when `subprocess.run` was given
`text=True`** — decoding happens after `communicate()` returns, which on this
path it never does. So the concatenation raised `TypeError` inside a worker
thread, the exception came back out of `f.result()`, and the `as_completed`
loop died.

The work did not. `ThreadPoolExecutor.__exit__` calls `shutdown(wait=True)`, and
every job had been submitted up front, so all 234 kept running to completion
with nobody reading their results. 233 of 234 output files were written. The
progress counter froze at 163.

This was particularly misleading because the jobs continued after result
collection stopped: the counter stops while the machine keeps working, so it
looks like a stall, and an estimate read off that counter — "about thirteen
minutes left" — is not merely wrong, it is confidently wrong an hour later.

**The watcher detected success messages, not process exit.** It was `until grep -q
"^wrote " log`, which matches only the success marker. A crash produces silence,
and silence is indistinguishable from still-running. *Warning sign:* ask of any
completion check, *if this process died right now, would anything fire?* If not,
it is not a completion check.

Three fixes, in order of how much they buy:

- **Wait on the pid, not on a log marker.** `until ! kill -0 $PID` fires on
  every exit including a crash. Capture the *specific* pid: a `pgrep -f`
  pattern matches any later run of the same script, which in this session made
  one watcher fire for a job that had finished four hours earlier.
- **Never let one job kill the collection loop.** Wrap `f.result()` and record
  the failure as a row.
- **Match every terminal state** when you must watch a log:
  `grep -Eq "^wrote |Traceback|Error"`.

None of this is about statistics, and all of it is recoverable: because the raw
output was on disk (§4), the fix was to repair the runner and re-run it, which
reused 233 saved runs and re-executed one. Nothing was re-bought.

### Static checking could have caught the bug before the trial

`ty` (Astral's type checker — `pip install ty`, no dependencies, 0.3s over every
Python file in this repository) reports it from the unannotated source:

```
error[unsupported-operator]: Unsupported `+` operation
    return (e.stdout or "") + (e.stderr or "") + "\n[TIMEOUT]\n"
           ^--------------^^^^^--------------^
    Both operands have type `(bytes & ~AlwaysFalsy) | Literal[""]`
```

It reads `bytes | None` off typeshed, narrows through the `or ""` to
`bytes | Literal[""]`, and refuses the `str`. No annotations were added to get
that.

So: **before a long run, type-check the runner.** The habit is narrow and the
reason is specific — the bug was in an *exception handler*. In a script like
this the happy path runs two hundred times and the error path runs once, in
production, fifteen minutes in. A happy-path smoke test would not reach it,
and a checker does not need it to execute. Test the happy path, type-check the
error paths.

Review the diagnostics for real defects; the runner does not need a clean
type-checking report before every exploratory trial. Over this repository's 32
Python files ty reports 30
diagnostics and one of them is a bug; the rest are things it cannot prove and
you can — narrowing through `if None in (x1, y1): continue`, a variable
assigned on every iteration of a loop it cannot show runs, a heterogeneous
config dict that would rather be a `TypedDict`. Read the list, take the real
one, move on. Making it a gate would mean silencing twenty-nine things to catch
the thirtieth.

`ruff` is not the tool for this half of the job, although it is already on
`PATH`. Over the same files its correctness rules
(`E9,F,B`) find twenty-two issues and none identified the timeout-handler bug:
unused imports, empty f-strings, `zip` without `strict=`, and four false `B023`s
where the closure is called inside the iteration that binds it. Keep it for
`ruff format`.

---

## 20. The resume path is the least-tested code and the last thing you wrote

§19's runner died quietly; this one shipped a bug that did the opposite — it
worked perfectly on the first invocation and fell over on the second. The
shell-parallelism trial adapted `2026-09-code-mode2/data/run.py`, and the
adaptation lost the `else` branch: fresh jobs assigned `text` from
the subprocess, resumed jobs (output file already on disk) never assigned it,
and the resume path raised `UnboundLocalError` at the scoring call. The smoke run
never touched it, because a smoke run is one fresh job.

What made it expensive was the interaction with the human loop. The first
invocation ran real jobs until it was interrupted, leaving thirteen partial
transcripts; the crash then ate every row. The second invocation found an
output file for *every* remaining job, so all fourteen took the broken path
and died instantly — "the runner did nothing," which is what a resume bug on
an all-resumed batch reports as. Two user-visible symptoms, one root cause,
and the second one actively misleading: nothing happening looks like
nothing happening, not like a bug.

An hour went to diagnosing unrelated hypotheses (per-character record
emission in the log writer) before anyone ran the broken path on purpose. The
warning sign was in the error text the whole time: `cannot access local variable
'text' where it is not associated with a value` names a code path, not a data
problem, and code paths can be exercised without the API key.

Three habits, in the order they would have saved the hour:

- **Run the resume path before the trial, not during it.** One job's output
  file stubbed, `run_one` called, row printed — ten lines of driver, no model
  calls. A fresh-job smoke test exercises only the fresh path. The resume path
  only ever runs when something has already gone wrong, which is the worst
  possible time to meet its bugs.
- **A guard belongs at the gate, not in the docs.** The runner now refuses to
  start when the previous batch ended in runner errors; before, resuming onto
  a half-dead batch silently mixed two runs' outputs. Any tool with a resume
  mode needs the same tripwire: what does it do when the last run failed?
- **When an error names a variable, exercise the code, not the theory.** The
  instinct was to hunt the subsystem the symptom resembled (the log writer,
  given the prior trial's report). The error was a local variable in a file
  that could be run in isolation. Re-read the traceback before investigating
  other subsystems.

---

## 21. A bug report is a hypothesis about location, not a fact about it

The code-only trial's report said jsonlog mangled tool results into one
character per record — `"T"`, `"h"`, `"e"` — with the rendered transcript
fine. That is a specific, checkable claim, and it was wrong: an hour of
reading the writer found it per-message by construction, and a test driving
it with one-character SSE deltas (the worst legal stream) pinned the
invariant in fifteen lines. No reproduction could be built.

The report's own method notes carried the refutation, unremarked: *"the
first-pass scorer keyed on `The program failed` in the rendered text."* A
scorer reading rendered text has no business reporting on the JSONL's
structure — and the renderer *does* emit per-event, each chunk a potential
line. The symptom was real; the subsystem named in the report was an inferred cause
presented as an observed fact.

Cost of treating the report as fact: an hour of archaeology aimed at the
wrong subsystem, and one deleted report-finding (the "jsonlog bug") that
would otherwise have been "fixed" — i.e. changed on the strength of
unreproducible evidence. What the episode bought instead: a regression test
that pins the writer against per-event emission forever, and a validation
pass in the next trial's scorer (parse every line, pair every
`tool_call_id`, flag sub-3-character text fields, abort loudly) that turns
the symptom from a contaminant into a tripwire. When it fires, the file
will be in hand.

The rule is not "distrust bug reports" — it is that **a report asserts the
symptom and infers the subsystem**. Reproduce at the stated location before
changing anything there; if it does not reproduce, that is not a dead end, it is
the finding. The next question is always the same: *who else reads this data?*
Check the other readers of the data; in this case, the scorer and renderer
explained the symptom.

---

## The short version

Most of what goes wrong is not statistics. It is the equipment.

Use the pre-run checklist before spending. Confirm that the mechanism is reached,
the fixture contains the relevant situation, and the scorer distinguishes the
outcomes you care about. Test the scorer against both expected successes and
expected failures, and verify that any sabotage actually changed the program.

Only then look at the p-value — and remember that a broken instrument's
favourite output is `p = 1.0`.

Before interpreting a surprising aggregate, read the underlying transcripts.
Before adding more runs, check that the runner completed and that its error and
resume paths work. Before a batch, check that reasoning is pinned low on every
model *and that each one obeyed* (§5): a model spending its budget thinking
looks exactly like an API failure.

When choosing a treatment, inspect where in the session the failure occurs. A
change to information the model has not yet read cannot explain its earlier
choices. The 2026-09 namespace trial paid for four arms to learn that a wrong
reach is 46% of first programs and 10% of second ones — a table binning the
phenomenon by position would have said it before any arm was built.

Stronger checks are more useful than a resolution to be more careful — that was
tried, for nine consecutive bugs. Require a concrete counterexample from a
reviewer (§13), or demonstrate that a targeted defect makes the relevant
assertion fail (§17). Both work because neither routes through the judgment of
whoever wants the result to pass. And ask of any fix that makes something stop
firing: *what still has to fire?* — because "report nothing" satisfies a
one-sided check perfectly.
