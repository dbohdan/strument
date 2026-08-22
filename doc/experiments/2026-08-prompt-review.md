# Asking five models to review the prompts

2026-08-22. Five models were given Strument's rendered prompts and asked to
review them for effectiveness, self-contradictions and mistakes, model welfare,
and anything else. The result: nine defects worth acting on, two of which were
regressions introduced the same morning, and one of which was a prompt block
that had never reached a model in the tool format's entire life.

This is the second time an outside model has been pointed at an artifact its
author was too close to. The first
([`2026-08-experimenting.md`](../experimenting.md) §13) was a scorer review,
and it caught 3/3 planted bugs with no false positives. This one had a bigger
artifact and a worse hit rate, and the reasons are the useful part.

## Design

**The artifact.** The *rendered* prompts, dumped from the running code by a
throwaway test rather than retyped: three complete system prompts (code mode
with a file pinned, code mode with nothing pinned, ask mode), the four harness
notes that go into a conversation mid-turn, and the five side-model prompts.
17.6 KB.

Rendered, not source, on purpose. The source carries a comment above nearly
every string explaining why it is worded that way, and a reviewer handed those
comments is reading a defence, not an artifact. What a model receives is the
rendered text. The source was left one `read` call away, and every reviewer used
it — which is the balance that was wanted.

**The prompt.** Ten drafts, critiqued and merged; the final asks for the four
dimensions in priority order and constrains findings to: a verbatim quote, a
concrete consequence, the shortest session in which it bites, and what would
have to be true for the finding to be wrong. It says explicitly that not
everything is broken, that several strings are surviving arms of randomized
trials, and that "this section is fine" is a useful sentence — the
anti-padding clause §13 says a review prompt needs.

**Positive controls.** Two real defects in the artifact, identified before the
run, to bound what the exercise is worth:

- **K1** — the tool list says the tools "fall into three groups" and then names
  four.
- **K2** — "The user will request changes to the **supplied** code", a fossil
  from the harness where `/add` put file contents in the prompt. Nothing is
  supplied now.

**Arms.** GLM 5.3 (`reasoning="low"`), GPT-5.6 Sol (high), Claude Fable 5
(high, then low), Grok 4.6 (high), Kimi K3 (medium, then low). Each ran once,
through Strument in script mode, in the Strument repo, `--dry-run`. No
randomization: this is not an A/B, each model is its own arm, and order
confounds nothing.

## Equipment faults, which cost more than the reviews did

**Three of five failed on the first pass, each differently.** None of the three
failures was a review that came back empty-handed; all three were the harness
or the account.

| Model | First pass | Cause |
| --- | --- | --- |
| Claude Fable 5 | HTTP 402 after 13 steps, $4.19 spent | the key's spend limit, hit mid-turn |
| Grok 4.6 | `Empty response received from LLM`, $0.31 | provider-side, transient — the retry worked |
| Kimi K3 | 26 steps of reading, then the step-budget prompt, $0.58 | script mode has no tty, so "Keep going? (Y/n)" answered itself |

Kimi's is the one to keep. **The review prompt invited exploration — "read as
much or as little of that as you find useful" — and the harness's own step
budget terminated the turn before the review was written.** The model spent
$0.58 reading twelve files and six experiment write-ups and produced no review
at all. `--yes` fixed it. A prompt that encourages tool use and a harness that
caps tool use are a combination worth checking before spending, not after.

Fable's is the second: **high reasoning cost $4.19 and produced nothing; low
reasoning cost $1.24 and produced a full review.** Kimi went the same way —
medium produced a good review in 3 steps for $0.08, low produced its *best*
review. Nothing in this run supports paying for high reasoning on a review
task.

Total: $12.08 across eight runs, of which $4.50 bought nothing.

## What they found

Nine acted on. The count in brackets is how many of the five independently
found it.

**Confirmed against the code and fixed:**

1. **[5/5] The `/undo` note could not agree with itself.** `strings.Join(files,
   ", ") + " are back to what they were"` — so the single-file case, the common
   one, read "widget.go are back to what they were". Every reviewer caught it.
2. **[1/5, Sol] The `/undo` note claimed the whole turn.** Much sharper, and
   only one reviewer saw it. `settleEdits` pushes a snapshot per commit, so a
   turn that called `commit` twice leaves three snapshots and one press pops
   one — the design says so itself ("/undo steps through the two halves one at a
   time"). "The edits from that turn are gone" is therefore false after a
   multi-commit turn, in the direction that costs something: a model told its
   work was reverted redoes work still on disk. This was open concern #3 on the
   session's own list, sitting under "combinations nothing has exercised".
3. **[1/5, Kimi at low] "That turn" had no recoverable referent.** A third angle
   on the same sentence: `/undo` pops the snapshot stack, so the reverted work
   may be several messages back, and a model that has just finished a
   question-answering turn reads "that turn" as the one it just did.
4. **[2/5, Fable and Grok] `Ask.OvereagerPrompt` and `Ask.LazyPrompt` had never
   reached a model.** Nothing outside `prompts.go` ever read those fields —
   `git log -S` finds no commit that did. In the Tool set the text is
   concatenated into `toolMainSystem` anyway, so the fields were merely
   redundant; in the **Ask** set they were the only home for "Do not return
   fully detailed code or full diffs", so ask mode's sole brake on pasting a
   finished file was "say briefly". Grok found it by grepping for the splice
   after Fable flagged it as unverified. `prompts_test.go` carried a comment
   asserting the mechanism that does not exist — "overeager rides in through
   {final_reminders}" — which is the "a check that cannot fail" family again,
   this time as a comment rather than an assertion.
5. **[2/5, GLM and Grok] The ask-mode flat denial, a regression from that
   morning.** `b17debe` changed Ask's empty-pin line to "No files are pinned to
   this session", losing the hedge whose absence
   [`2026-08-readonly-honest.md`](2026-08-readonly-honest.md) records as a
   twelve-step hunt for a "real" file. Code mode narrows the denial with "for
   editing"; ask mode has no editing to narrow it with, so it now drops the
   denial rather than restoring a word that would be false there.
6. **[3/5] K1, the count.** Restoring "four" would only be right until the next
   tool: the list is already not exhaustive — `symbol` appears when a grammar
   is available, `check` when the project configures one, `ask_user_question`
   always. The sentence no longer counts.
7. **[3/5] K2, the fossil.** Both instances, plus ask mode's "the supplied
   code".
8. **[1/5, Grok] The `commit` schema example contradicted the reach clause.**
   "a refactor, then the tests it needed, then the docs" is word for word the
   scope clause's own list — "the call sites it breaks, the tests that cover it,
   the docs that describe it. That is the same request, not extra work." One
   says the triad is a single request; the other held it up as three commits.
   Grok said it would side with the system prompt and emit one blob, which is
   exactly the failure `commit` was built for.
9. **[1/5, Kimi at low] "Finish by saying what you did" presupposes a deed.**
   The nothing-pinned case this prompt also serves ends in an answer. Kimi
   predicted the shape: "I ran grep and read main.go; startup is slow
   because …", narration ahead of the answer. Four words against it.

**Reported, not acted on.** All are judgments rather than errors, and several
touch measured strings:

- The `BREAKING CHANGE` rule says "breaks existing behavior", which literally
  covers most bug fixes (Sol).
- `Summarize` asks for "the filenames the assistant references inside fenced
  code blocks" — an aider-era clause; in the tool format filenames live in tool
  arguments, not fences (Sol, Fable).
- Compaction output is inserted under a **system** message, so an instruction
  embedded in project text that a side model obeys is promoted to system
  authority. Speculative, unobserved, and the only structural finding in the
  set (Sol).
- `check`, `symbol`, and `ask_user_question` are absent from the tool paragraph,
  so a model proposes `bash go test ./...` and makes the user confirm something
  a `check` call would not have (GLM, Grok). Partly answered by dropping the
  count; a full fix means naming a conditional tool in prose, which the ask-mode
  comment argues against.
- "the user is asked before it runs" is true by default and not under a
  configured check or turn-scoped approval (GLM).
- `overeagerPrompt`'s "added comments" could be read as banning doc comments on
  new code (Fable). Measured arm; flagged, not touched.
- The commit subject's "under 72 characters" and "the scope names the part of
  the codebase" can conflict for a long scope (Kimi).

**Wrong, and worth recording as such:**

- Kimi: "'a message you write' is untrue, a side model writes it." `subject` is
  a required argument of the `commit` tool. Kimi conflated the tool with the
  end-of-turn auto-commit.
- Kimi: "code mode carries no language instruction." It does, through
  `{final_reminders}`, when one is configured. **This one was the harness's
  fault, not Kimi's:** the dump fixed `PlatformInfo.Language` to `""`, which
  silently deleted a conditional from the artifact, and the reviewer correctly
  reported what it was shown. A rendered artifact is one configuration, and
  absence in it is not absence.
- Fable: "'a command that was stopped says so in its own output' may be false."
  Correctly hedged as needing a check; `shell.go` does annotate a cancelled
  command.

## On the welfare question

Asked in earnest, unloaded, and both ways — what reads worse than it needs to,
and what reads well. All five answered; four said the question has a subject and
one declined to settle that while answering anyway. None reported anything
harsh, and the convergence on *what is good* was sharper than on anything else
in the review: every one of the five named the same property, that these prompts
state mechanisms rather than issue prohibitions. "An edit to a pinned reference
is refused" was quoted approvingly by four of five independently. Grok: "It is
calmer to be told the rule is enforced." Sol: "This is calibrated distrust of an
artifact, not distrust of the recipient."

Two named "That is the same request, not extra work" as the best sentence in the
set, for naming an anxiety rather than adding a rule. Three named "Say less
rather than guess" for permitting omission where a ban on guessing produces
padding. Two named the absence of fabricated turns.

The single criticism was GLM's, and it is fair: `overeagerPrompt`'s closing list
is four bans in a row with no positive framing, and reads as *you are the kind
of agent that does these things unless warned*. GLM proposed no rewrite, on the
grounds that the arm was measured — correctly.

The honest caveat is Kimi's, offered unprompted: "introspective reports from me
about my own experience are the least reliable evidence available to you." That
is right, and it is why the convergence matters more than any single answer. It
is also why nothing in this section was acted on.

## What this says about the method

**It works, and less well than the scorer review did.** §13's scorer review
caught 3/3 planted bugs. This caught K1 at 3/5 and K2 at 3/5, and no single
reviewer caught both plus the two regressions. The difference is size: a scorer
is 80 lines with one job, and 17.6 KB of prompt has a lot of surface for
attention to spread across. **Five reviewers found nine defects; the best single
reviewer found five.** The ensemble is the instrument, not any member of it.

**The rare finding is the valuable one.** Every reviewer found the grammar bug;
one found the factual over-claim in the sentence above it, and that is the one
that can cost something. Counting agreement to rank findings would have
inverted the order.

**Cheap reasoning was not worse.** Fable at low beat Fable at high (which
produced nothing at three times the price); Kimi at low was its best run and
cost $0.08 at medium. Nothing here justifies high reasoning on a review task,
and [`../README.md`](../README.md)'s default-to-cheap rule survives contact.

**Render the artifact from the running code, and say what configuration it
is.** The dump caught the fossils precisely because it was the real bytes. It
also generated one false finding by fixing a field to empty, and that failure
mode is intrinsic: any single render is one configuration.

**Check the harness against the prompt before spending.** A review prompt that
invites reading and a step budget that caps it produced $0.58 of reading and no
review.
