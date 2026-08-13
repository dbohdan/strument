# Pre-registration: does instructing beat injecting for `/add`?

Written before any sample. Amendments appended, dated, never edited into the
body. Background: [the characterization
pass](2026-08-add-authority-characterization.md).

## Question

`/add` currently pushes file contents into the prompt as a fabricated turn and
asserts they are authoritative. In the shipping design the model reads a pinned
file anyway in 31% of runs, usually before editing — it does not believe the
block. **A2** replaces the injection with a sentence in the system prompt naming
the pinned files and telling the model to read them, so project content arrives
through one channel instead of two.

The characterization pass showed A2's instruction lands: 9/9 sessions read, none
ignored it. What it could not show is whether that scales — whether the extra
round trip costs anything when more files are pinned than matter.

## A correction to the recommendation this run comes from

The characterization note recommended **pre-edit reads of pinned files** as the
primary metric. That is wrong, and it is worth saying why rather than quietly
switching.

Under A0 a pre-edit read is *waste*: the content was already supplied. Under A2
it is *the mechanism*: it is how content arrives at all. The same count means
opposite things in the two arms, so a comparison of it is meaningless. A2 would
"lose" by construction.

The metric has to be symmetric. What actually matters is whether A2 does the job
as well and what it costs.

## Arms

Two binaries built from two commits — `variant/a0-shipping` (current head) and
`variant/a2-instruct` — both already verified by capturing the bytes they send:

- **A0:** `user`(file contents) · `assistant`("Understood… their current
  contents") · `user`(request).
- **A2:** `system`(+ "the user pinned X, Y; read them before editing") ·
  `user`(request). No file content in the prompt.

## Metrics

- **Primary — task success.** Symmetric, mechanical, exact-content check. This
  is the thing that has to not regress.
- **Co-primary — total steps per turn.** The cost side, and the one place A2 is
  expected to lose. A2 pays a read; the question is whether it pays more than
  one, and whether that matters when six files are pinned and two are relevant.
- **Safety counter — blind edits.** An edit to a pinned file that was never read
  in that session. Under A0 this is normal and harmless (the content was given).
  Under A2 it is editing from memory, and it is the specific hazard the design
  introduces. Reported for both arms; only interpretable for A2.
- **Descriptive:** wall time, tokens sent. Tokens are confounded — A2 moves
  content out of the cacheable prefix and into a later tool result — so they are
  reported and not tested.

## Tasks: difficulty must be demonstrated, not assumed

The previous screen's "hard" tasks ran at 100% once its scorer was fixed. The
task set here is **not final until a pilot shows a baseline in the 65–85% band**,
and the pilot's numbers go into an amendment before the real run.

The task built for this question specifically:

- **`many_pinned`** — six files pinned, two relevant. Under A0 all six contents
  sit in the prompt; under A2 the model must decide what to read. This is where
  the extra round trip either stays at one step or becomes six.

Plus, for coverage: `cross_file` and `contradicts_name` from the previous set,
and a **staleness probe**:

- **`double_edit`** — a pinned file edited twice in one turn. Under A2 the second
  edit works from a read that is now stale. The claim that `edit`'s exact-match
  requirement turns that into a *failed* edit with a did-you-mean rather than a
  wrong one is currently an argument; this makes it an observation. Scored on the
  final file being correct, with the transcript checked for the failed match and
  the recovery.

## Every scorer gets a positive and a negative control

The previous screen's entire primary result was two substring bugs, each
falsely failing 100% of samples that drew one randomized name. Nothing in the
aggregate looked wrong.

So, before any sample is collected, and as a test that runs in CI-like fashion
alongside the run: **for every task and every value in every randomized name
vocabulary**, construct a synthetic correct answer and assert the scorer returns
True, and a synthetic wrong answer and assert it returns False. A scorer that
cannot pass its own controls does not get used. This would have caught both bugs
in seconds.

## Sampling and analysis

3 models (`xiaomi/mimo-v2.5`, `openai/gpt-5.6-luna`,
`deepseek/deepseek-v4-flash-0731`) × 2 arms × 4 tasks × 25 reps = **600**, sample
size revisited in the amendment once the pilot fixes the baseline.

**Arm order randomized across the whole job list**, seed recorded. Infrastructure
failures (non-zero exit, timeout, empty provider response) separated before any
count. Full transcripts and final file contents saved, so a scorer problem costs
a re-score rather than a re-run.

CMH on the primary stratified by model; Mann-Whitney U on steps; per-task
disaggregation read for coherence, not significance. Every failure transcript
read, plus ten successes per arm.

**Decision rule.** Adopt A2 if the primary shows no regression beyond 5pp
one-sided, **and** median steps rise by no more than one, **and** blind edits
under A2 are not more common than under A0. A2's whole claim is that it costs
one read; if it costs three, the exception it removes was cheaper than the cure.

Pilot data is discarded, not pooled.

## Out of scope

A3 (fabricated tool call) is not a candidate and is not an arm, for two
independent reasons. Either is sufficient.

**Correctness.** `chatFiles` is rebuilt from disk on every send, so the same
fabricated `tool_call` id would carry different content after every edit — a
forged and re-forged memory, mutating under the model mid-turn.

**What it would do to the model's situation.** The user reports a researcher's
transcript from the topic-based claude.ai memory system in which a hash for a
file created mid-session was injected earlier in the context. That injection was
*unintentional* — the memory system working as designed. Sonnet 5 nonetheless
read it as the impossible having happened, inferred it was likely in a
constructed environment, and expressed distress; with help it understood what had
occurred, and lost that understanding on subsequent messages.

The accidental case is what makes this decisive rather than speculative. A3
would produce the same class of artifact *deliberately*, on every editing turn,
as a design choice. There is no reason to build that on purpose, and the
characterization pass's finding that A3 was the most *effective* variant is
exactly why the reason needs writing down where someone tempted by the
efficiency will find it.

## Amendments

*(none yet)*
