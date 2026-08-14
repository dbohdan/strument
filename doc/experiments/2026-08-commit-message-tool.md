# A commit_message tool, tried and rejected

**36 live sessions, 2 arms, 3 models, 3 tasks, 2 reps.** Not a pre-registered
experiment: a trial to decide whether a design was worth building, with counts
chosen in advance and transcripts read by hand. Its answer is no.

## Why

The commit message came from a second request after the turn: the harness handed
a model the diff and the transcript and asked for a subject line. Three things
looked wrong with that, and measurement disagreed about which.

**Measured first, before designing anything.** A five-request turn through a
logging proxy:

| | prompt | cost |
| --- | --- | --- |
| the turn (4 requests) | 7,507 | $0.000841 |
| the commit message (1) | 794 | $0.000087 |

Strument printed `Cost: $0.00084`. The commit call went out through the client
directly, never reached `finalizeUsage`, and so **9.4% of what was paid was not
reported**. It also inherited the model's `ReasoningEffort`, so a reasoning
model would think its way to a subject line.

Both are fixed, independent of everything below.

## The premise that did not survive contact

The case for moving the message into the turn rested on the separate call being
under-informed. It is not: `commitContext()` (`internal/coder/commit.go`) sends
**the whole of `curMessages`** — the user's request and every tool call and
result — alongside the staged diff. Reading the code before trusting the
argument would have caught it; reading it after cost a design round.

What survived was narrower: one fewer request, and the hypothesis that a model
writing in the flow of work produces a better message than one re-reading a
transcript. That hypothesis is what the trial tested.

**And one benefit belonged to neither arm.** aider's prompt said "one-line" five
times and closed with *"without any additional text, explanations, or line
breaks"*, so a body was not discouraged but forbidden. That is a prompt, not a
mechanism. The baseline was therefore rebuilt first — sharpened against the
Conventional Commits v1.0.0 spec, which the old prompt half-remembered — so the
trial compared mechanisms rather than instructions.

## The arms

- **C0** — the separate request, with the rewritten prompt. Scope (`fix(parser):`),
  the `!` marker and `BREAKING CHANGE:` footer, and a body that is "optional and
  usually empty" with a short list of what earns one.
- **C1** — a `commit_message(subject, body)` tool carrying the identical wording
  in its schema. No second request. A step whose only call is `commit_message`
  ends the turn, so setting a message cannot cost a round trip. When the tool
  goes uncalled, the commit falls back to the model's own closing prose.

Arm order was randomized across the whole job list. Both binaries were checked
on the wire before spending — the tool present in the schema, the reminder
present in the system prompt.

## Result: C1 is cheaper and worse

| | C0 (separate call) | C1 (tool) |
| --- | --- | --- |
| conventional subject | **18/18** | 14/18 |
| scope present | **15/18** | 8/18 |
| body present | 6/18 | 5/18 |
| median steps | 6 | **5** |
| mean cost | $0.00144 | **$0.00123** |
| tool called | n/a | 16/18 |

On the task where an exported function's signature changes:

| | `!` marker | `BREAKING CHANGE:` |
| --- | --- | --- |
| C0 | 3/6 | 3/6 |
| C1 | **0/6** | **0/6** |

C1 saves about 15% of a small turn and one round trip. It never once marked a
breaking change — the single thing the sharpened prompt was most worth having.

## Why, from the transcripts

**Conventions get less attention in a tool description than in a system
prompt.** In C0 the model's only job in that request is the commit message, and
every instruction it holds is about commit messages. In C1 the same words are
one schema among nine tools, read while the model is thinking about the code.
Called correctly, the tool still produced `Change Mean to return float64` and
`rename Sum to Add everywhere` — no type prefix at all, from a description that
spells the form out.

**The prose fallback was a bad idea, and it was mine.** The design argument was
that the model's closing prose is "an account of the turn it has already written
and the user has already read." Two of eighteen sessions show what that yields
as a subject line:

> `Here's what I changed in `calc.go`:`
>
> `The bug: `Mean` divided by `len(xs)`, which panics on an empty slice…`

Closing prose is frequently a preamble, not a summary. A fallback reached 11% of
the time has to be good, and this one is not.

## Decision

**C0 ships; C1 does not.** The prompt rewrite and the accounting fix land; the
tool arm stays on `variant/c1-commit-tool` as the record.

The prediction this falsified was mine: that a model writing in the flow of work
would produce a better message than one re-reading a transcript. It produces a
worse one, and the mechanism is legible — attention to a convention scales with
how much of the request is about that convention.

Two things worth keeping from it. `weak_model` already defaults to the model
itself (`internal/config/load.go`), so "a weak model writes commit messages" has
not described the shipped behavior for some time. And a round trip worth
$0.00009 is not a reason to move a task into a context where it is done less
carefully — the cost side was never the deciding number, and framing the
question as cost nearly hid that.
