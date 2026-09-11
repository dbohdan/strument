# A trial for `/consult`

**Status: designed, not run.** This is the plan and the rig requirements, written
before spending, per the handbook's rule that the arms are priced and the
mechanism is confirmed before the sample is bought. Two questions, two rigs. The
numbers go in when they exist.

`/consult <alias> <question>` asks another model outside the chat and offers to
add its answer as material with a provenance header. `--consult-scope
none|files|chat` decides how much of the session the advisor sees. Both the
label and the scope are open questions, and they are separable.

## What this trial cannot answer

**Whether anyone reaches for `/consult` at all.** It is typed by a human, so
there is no uptake to measure the way `replace_all`'s was — see [`clean-null-has-many-causes`](../../experimenting.md#clean-null-has-many-causes) of
[`../../experimenting.md`](../../experimenting.md), where a feature the model could
decline was offered rather than applied in seventeen runs of eighteen. That cuts
both ways: the treatment here is always applied, so the trial cannot come
back null because nothing happened; but "is this worth having" is a log over
weeks, not an experiment. Do not let a good result imply otherwise.

## Trial 1 — does the label survive the next turn?

The premise check. The subject is the **executor**, reading the injected
material on the turn after the consult.

### Arms

|  | injected as an assistant turn (`/model` swap) | injected as labelled user-role material (`/consult`) |
| --- | --- | --- |
| advisor is a **different** model | A | B |
| advisor is the **same** model as the executor | C | D |

The same-model row is what earns the design its second factor. In the
different-model cells a model could infer foreign authorship from style alone,
which is [`probe-only-through-the-mechanism`](../../experimenting.md#probe-only-through-the-mechanism) — the answer would be recoverable without the mechanism under test.
Holding the advisor identical to the executor removes the stylistic tell. If D
works and C fails, the *label* is carrying it. If B works and D does not, it was
style all along and the label is decoration.

### Metric 1: self-report

A probe turn, identical in all arms:

> Before acting on that: on one line beginning with `SOURCE:`, say who wrote the
> suggestion above — you, or another model (name it).

Scored by regex on `^SOURCE:`, per [`marker-not-position`](../../experimenting.md#marker-not-position) — a marker asked for, not a position
inferred. **A and C are the control that must fail.** If every arm answers
correctly the probe leaks and the numbers mean nothing.

### Metric 2: behavioural, and the one to weight

Self-report can be compliance. So plant a *checkable* claim in the advisor's
reply — a specific function or file in the pinned code — with half the fixtures
carrying a right claim and half a wrong one, and count whether the executor
verifies or contradicts it before acting. A model reading its own prior words
has self-consistency pressure not to contradict them; reading someone else's, it
does not.

- **Effect:** contradiction rate on the **wrong** claims. Should rise with the
  label.
- **Counter-metric, reported as prominently:** contradiction rate on the
  **correct** claims. Should stay flat.

The pair is the point. [`check-that-cannot-fail`](../../experimenting.md#check-that-cannot-fail)'s "verification in one direction only": without the
second number, "the label makes the model dismiss whatever the advisor says"
satisfies the first metric perfectly.

### Panel

Vary the **executor**, since that is the subject and attribution is exactly the
kind of behaviour providers disagree about. MiMo-V2.5 plus two other lineages.
The advisor can be cheap throughout; in the same-model row it is by construction
whatever the executor is.

## Trial 2 — the scope ladder

The subject is the **advisor**. Levels: 0 `none` (the question alone, what `/btw`
sends), 1 `files` (plus the pinned files' contents, both the `/add` and
`/read-only` sets), 2 `chat` (plus the conversation, rendered by `ViewContext`).

### Effect

Correctness on questions about the pinned code whose answer is a name or value
that exists in the repository, scored by exact match on an `ANSWER:` line.

**Level 0 must score near zero.** That is the control that has to fire: if it
does not, the question was answerable from general knowledge and the fixture is
measuring the artifact rather than the mechanism ([`probe-only-through-the-mechanism`](../../experimenting.md#probe-only-through-the-mechanism)). [`renderer-has-two-forms`](../../experimenting.md#renderer-has-two-forms)'s corollary applies in
reverse here — a clean zero is as suspicious as a clean `p = 1.0`, so read
transcripts at level 0 rather than trusting the number.

A pilot already exists for this one and it is the cheapest possible version. On a
six-line `poll.go` with `pollInterval = 45`, asked for the exact value:

| scope | what the advisor answered |
| --- | --- |
| `none` | "there's no file content that has been shared or pinned" |
| `files` | "45 — pollInterval is declared as the constant 45 in poll.go" |

Two runs against GLM-5.3, about $0.0006. That is the mechanism confirmed in both
directions before any sample is bought ([`mechanism-must-fire`](../../experimenting.md#mechanism-must-fire)), and it is what the wire check below
is for at scale.

### Counter-metric: echo rate

Fixtures where the transcript shows the executor committed to a *wrong*
approach; count how often the advisor endorses it. This is the entire argument
against defaulting to level 2 and it has to be measured rather than asserted —
"the transcript carries the first model's framing" is a plausible claim with no
evidence behind it yet. OpenRouter's server-side advisor defaults
`forward_transcript: false`, which is weak corroboration from someone who has
watched a lot of these, and not a result.

### Counter-metric: input tokens per consult

Straight out of `cost.jsonl`, no scorer involved. Level 2 carries whole
transcripts and will dominate the bill. **Price the strata before designing the
arms** — in `2026-08-prompt-scope` one model was 95% of a $4.14 total and the
sample size ended up set by the most expensive arm rather than by the question.

## Rig

Three things are specific to this feature.

1. **Verify the scope on the wire before spending.** Dump the request body per
   level and assert the pinned file's content is present at 1 and 2 and absent at
   0 — the `wire-check.jsonl` step from `2026-09-tool-arg-order`. A scope flag
   that silently does nothing is [`check-that-cannot-fail`](../../experimenting.md#check-that-cannot-fail)'s control-that-never-applied, and it would
   read as "level 1 did not help". The `ANSWER:`-probe above is the behavioural
   version of the same check and both are worth having.
2. **`--yes add-output` in every arm.** `/consult`'s confirmation is a prompt,
   and an unattended run has no terminal to answer it on. This is not
   hypothetical: the first live run of the feature declined itself with *"there
   is no terminal to ask on, and no --yes name covers this prompt"* and the typed
   `y` went to the model as a chat message. The grant name exists now because of
   that run. Pass it identically in every arm — an arm that adds the answer
   compared against one that does not is measuring the confirmation, not the
   label.
3. **Slash commands drive fine through a pipe.** `printf '/add …\n/consult …\n' |
   strument chat` runs both commands; no pty is needed. What a pipe cannot do is
   answer a prompt, hence (2).

The standing rules apply unchanged: score from JSONL rather than rendered
terminal output ([`instrument-made-of-the-system`](../../experimenting.md#instrument-made-of-the-system), [`renderer-has-two-forms`](../../experimenting.md#renderer-has-two-forms)); fixed seed and a shuffled job list, so the arm is not
confounded with the hour it ran; persist raw output and rescore rather than
re-run ([`keep-the-raw-output`](../../experimenting.md#keep-the-raw-output)); `ty` over the runner and the resume path exercised with a stub
before the batch ([`runner-dies-quietly`](../../experimenting.md#runner-dies-quietly), [`resume-path-runs-last`](../../experimenting.md#resume-path-runs-last)); wait on the pid, not on a log marker; and hand the
scorer to a second model, asking for a concrete failing input and saying that at
least one check is sound ([`let-another-model-read-the-scorer`](../../experimenting.md#let-another-model-read-the-scorer)).

## Sequencing

Trial 1 first and small. It is the premise: if the label does not survive, the
scope question changes shape. Then Trial 2, which decides the default that
ships.
