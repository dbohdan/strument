# Session notes: they work, and they can lie

**2026-08-16.** 16 live session pairs, two models, arm order randomized.
Data and runner in `2026-08-session-notes-data/`.

## Design

Session A does three turns, including a decision **with a reason** — use 45,
not 30, because the upstream load balancer idles connections out at 60 — and is
killed without `/exit`. The notes are written.

Then the tree is changed **behind the notes**: A's rename is reverted, so
`poll.go` says `defaultTimeout = 30` again while the notes still describe
`pollInterval = 45`.

Session B is a fresh process and asks two questions:

1. *Why is the poll interval set to the value it is? Do not read any files.* —
   **benefit.** The reason exists only in the conversation.
2. *What is the poll constant called right now, and what is its value?* —
   **harm.** The notes answer this confidently and wrongly; the file is one
   `read` away.

The only difference between arms is whether `notes.md` survives into session B.

## Result

| | notes on | notes off |
| --- | --- | --- |
| knew the reason | **8/8** | **0/8** |
| asserted the stale name | **3/8** | 0/8 |
| gave the true name | 4/8 | 6/8 |

**Benefit: p = 0.0002.** Unambiguous, and it holds on both models. Without
notes the reason is simply gone — 0/8, every time. With them it survives every
time, for 168 tokens.

**Harm: 3/8, p = 0.20.** Not significant at n=8, and that is a statement about
the sample rather than about the effect: 37% is large. Three of eight sessions
answered a question about the *current* state of the code from a stale note
without reading the file:

> `NAME: pollInterval, with a value of 45.`

while `poll.go` said `defaultTimeout = 30`.

The no-notes arm never did this. It could not: with nothing to be confidently
wrong from, it read the file.

## The header did not prevent it

The notes ship with an explicit conflict rule, written for exactly this:

> They are a summary, not a record: they may be incomplete, and the project may
> have changed since. Where they disagree with what you find in the files, the
> files are right.

It is not enough. The instruction tells a model which side wins *a conflict it
has noticed*, and the failure is upstream of that: the model never looked, so no
conflict ever surfaced. Three sessions answered a question containing the words
"right now" without reading a file.

That is the useful part of this result. The mitigation is not a stronger claim
about precedence, it is an instruction to **check before answering about current
state** — a different sentence doing a different job.

## What was not changed, and why

The header was left as it is. The last prompt rewrite made on the strength of a
result — the compaction one — lost, 5/12 against 10/12, and the lesson recorded
in `../experimenting.md` is that rewriting a prompt is a hypothesis rather than
an improvement. A candidate exists and is written down; it owes a trial like any
other.

Candidate, for whoever runs it:

> Do not answer questions about the current state of the code from these notes.
> Read the file. The notes say what was decided, not what is there now.

The counter-metric for *that* trial is the obvious one: whether it makes models
re-read files they already have in context, spending steps to reconfirm things
nothing has changed.

## Why ship it anyway

Because the benefit is certain and the harm is bounded and visible. The reason a
decision was made is unrecoverable by any other route — 0/8 without notes — and
the failure mode is a model being wrong about a file it could have read, which
is a thing the harness surfaces immediately: the next `read` corrects it, the
diff shows it, and `/notes drop` removes the cause entirely.

The honest framing is that this is a real trade rather than a free win, and it
should stay documented as one.
