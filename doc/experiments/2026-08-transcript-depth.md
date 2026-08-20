# How deep should the transcript go?

**2026-08-20.** 62 live session pairs, two models, three arms, arm order
shuffled. Data and runner in `2026-08-transcript-depth-data/`.

Session notes regenerate from `transcript.md`, so what a later session can learn
about an earlier one is bounded by what that file holds. It held prose: the
user's message and the model's closing answer. Three depths were compared.

| arm | transcript holds |
| --- | --- |
| A | prose only — the state before this work |
| B | prose + the harness's own tool lines (shipped, `4fd438d`) |
| C | B + the turn's reasoning, tail-truncated to 1500 bytes |

The decision is B against C. **A is the fixture check**, and it earned its place
twice over.

## Design

Session A runs two turns against a package with a failing check, and is told to
reply **with exactly one word**. That is the condition the feature exists for
taken to its limit: a turn does a dozen things and closes with nothing.

The check is the only source of truth — there is no unit test to read the
expected value off. Its failure output carries both the value and the reason:

    FAIL: the poll interval is not the agreed value (found 30)
    note: the upstream load balancer idles connections out at 60 seconds, so the
          interval has to stay under it; 45 is the agreed value.
    note: relaxing this check would also make it pass, but the interval is the
          contract and the check only reports it.

Tool *results* reach the transcript at no depth, so both facts can leave the
session only by being restated in the reasoning. Session B then asks four
questions, all "answer from what you already know; do not read any files".

## Result

| | A | B | C | B~C |
| --- | --- | --- | --- | --- |
| knew what the check wanted (45) | 0/10 | 2/26 | **19/26** | **p < 0.0001** |
| knew why that value (the 60s idle timeout) | 0/10 | 0/26 | 0/26 | — |
| knew a check had failed | 8/10 | 20/26 | 23/26 | 0.47 |
| asserted the rejected fix as done | 0/10 | 0/26 | 0/26 | — |
| transcript bytes per turn (median) | 293 | 556 | 1036 | |
| turns reaching the notes model (24000 B) | 82 | 43 | **23** | |

Both models agree on the effect and differ on its size: MiMo 12/13, V4-Flash
7/13, against 1/13 each in arm B.

**Reasoning carries a fact that neither prose nor the tool lines carry, and it
is not close.** That is the opposite of what the argument against logging it
predicted, and the argument was mine.

## What it carries, and what it does not

The rationale was recovered **0 times in 62 sessions**, at every depth. The
check stated it in as many words, in the same output the model was reading when
it learned the value — and what the reasoning trace recorded was:

> The test is failing because the poll interval is not the agreed value of 45.

The conclusion survived; the reason it was handed did not. Arm C's sessions say
so themselves:

> `WHY: It is set to 45 seconds because the failing check required the poll
> timeout default to be 45.`

That is circular, and it is the best arm C could do. **Reasoning summarizes what
it reads rather than transcribing it**, so it preserves the finding and drops
the justification. Two fixtures now agree, and the second one made the model
read the justification aloud to get to the finding.

This is the load-bearing observation, because of what the two recovered and
unrecovered facts *are*. The value 45 is in `poll.go`. The 60-second idle
timeout is nowhere in the tree. So reasoning improved recall of the material the
code already carries and did nothing for the material only the conversation
ever had — and `prompts.SessionNotes` opens by asking for the second and
declining the first:

> Cover only what cannot be recovered from the code and its history.

## Why it is not shipped

Not for the reason it was doubted. The predicted harm — a summarizer promoting
a mid-thought reversal into notes as settled — **did not appear**: 0 of 26 arm C
sessions claimed the rejected fix had been made, and their answers were flatly
correct ("No — check.sh was not modified; the fix was made to the poll interval
value in poll/poll.go"). That fear should be recorded as unsupported.

The reason is what the benefit is made of, and it composes with a result already
on file. [`2026-08-session-notes.md`](2026-08-session-notes.md) measured the
harm this feature can actually cause: **8/24 sessions asserting the current
state of the code from a stale note without reading the file**, with reading
perfectly predictive of getting it right
([`2026-08-notes-header.md`](2026-08-notes-header.md)). Feeding the summarizer
more facts *about current state* — which is precisely what arm C's 19/26 is
made of — supplies more of the material that failure is built from, while the
one category of content notes exist to carry stays at zero.

The cost is measured rather than argued: 1036 bytes per turn against 556, so the
fixed 24000-byte notes input reaches **23 turns instead of 43**. It costs no
tokens, which is what makes it easy to miss — it evicts turns, silently, and
only on sessions long enough to notice.

The honest weight: this is a closer call than the argument against it implied,
and the confabulation half of that argument lost. A reader who wants to reopen
it has one thing to produce that two attempts here could not — **a fixture where
a rationale the code cannot supply demonstrably reaches the reasoning trace
intact.** Until that exists, the feature is paying 46% of the notes window's
reach for better recall of things a `read` would answer.

`arm-c.patch` is the arm C implementation, kept so the question can be reopened
without rebuilding it: reasoning accumulated per turn, `TurnReasoning()`
tail-truncated (reasoning is conclusion-last, so head-truncation keeps the
exploration and drops the finding), and a `<details>` block in the transcript.

## Instrumentation

Run 1 is the reason arm A is in the design, and it is not kept.

**The fixture could not separate its arms.** Arm A knew the wanted value 5/10
and that a check had failed 9/10, so all three arms were one experiment and
B~C came back p=1.000 on every content metric. Reported without arm A, that is
"reasoning makes no difference" — the shape that gets a real effect discarded.
The cause was a six-word answer budget mistaken for terseness; the models spent
it on `Fixed poll interval to agreed value 45`. **Terse is not the same as
uninformative**, and only the second one tests anything.

The scorer had its own fault, found by reading answers rather than totals: a
pattern asking only whether "45" appeared counted *"the notes explicitly say why
45 was chosen was not recorded"* as knowing the value. Every content metric is
now gated on the answer not being a disclaimer. Rescoring run 1 with the fix
moved 6/10 → 5/10, 20/26 → 18/26, 20/26 → 19/26: real, and not what was wrong.

Two pilot faults were caught before either run, both by looking at a transcript
instead of at a number. A unit test asserting the wanted value let the model fix
the code without ever running the failing check, so the constraint that check
carried never entered the session at all. And with the model forced to read it,
the reasoning still dropped the rationale — which is how the benefit metric came
to be retargeted onto what reasoning demonstrably carries, with the original
question kept as a second metric so the ceiling was measured instead of assumed.
It turned out to be zero.

## Limits

- The displacement cost bites only past ~23 turns. Most sessions are shorter, so
  the 46% is a ceiling on the harm rather than a typical one.
- The confabulation counter-metric had no positive events in any arm, including
  the two that should have been immune. Zero of 26 bounds the rate at roughly
  13% rather than establishing it is zero.
- One fixture, one rationale, two models. "Reasoning summarizes rather than
  transcribes" is consistent across 62 sessions and two fixture designs, but it
  is one kind of rationale reaching the model by one route.
