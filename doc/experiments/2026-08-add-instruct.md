# Instructing beats injecting for `/add`

600 samples, three models, two arms. Pre-registration and its amendment:
[`2026-08-add-instruct-preregistration.md`](2026-08-add-instruct-preregistration.md).
Background: [the characterization
pass](2026-08-add-authority-characterization.md).

**Result: adopt A2.** All three pre-registered conditions pass, and the safety
counter goes to zero.

## The arms

- **A0 (shipping).** File contents injected as a fabricated user turn, followed
  by a fabricated assistant reply asserting they are current.
- **A2.** No file content in the prompt. The system prompt names the pinned files
  and says to read them; the model reaches them with real `read` calls.

## Result

| | A0 | A2 | |
| --- | --- | --- | --- |
| **steps** (primary) | median 3, mean 3.55 | median 4, mean 4.65 | +1.09, U z=−8.86, p<0.0001 |
| **task success** (guardrail) | 300/300 | 299/300 | no regression; bound −0.9pp |
| **blind edits** (safety counter) | 383, in 230/300 runs | **0, in 0/300 runs** | eliminated |

Decision rule required: median steps up by no more than one (**exactly one**),
success not down beyond 5pp (**−0.33pp**), and blind edits under A2 no more
common than A0 (**zero**). All pass.

**The single "failure" is not one.** v4-flash emitted a tool call as literal
inline text — `<｜DSML｜tool_calls><｜DSML｜invoke name="read">…` — rather than
through the tool-call API. A model-formatting failure, not a behavioral one, and
worth recording twice over: the previous screen's write-up named exactly this
("a model emitting a tool call as inline text") as indistinguishable from a real
failure in an aggregate, and **my infrastructure filter still missed it**, because
the exit code was 0 and no empty-response marker appeared. Filters catch the
failure modes you have already met.

## What "blind edits" means, and why zero is the finding

A blind edit is a pinned file written with no prior read of it in that session.
Under A0 this is normal and correct — the content was supplied — which is why the
count is 383. Under A2 it would mean editing from memory, and it is the specific
hazard the design introduces.

It never happened. Not once in 300 runs. The instruction to read before editing
is not merely followed on average; it is followed every time.

## The central risk did not materialize, but not for the reason expected

`many_pinned` pins six files of which two are relevant. The worry was that A2's
"it only costs one read" becomes six.

A2 reads **4.24 of 6** on average, against A0's 0.89. So it *does* over-read —
substantially more than the two it needs. The step cost is still only **+1**
(3 → 4), because reads batch into parallel tool calls inside a single step.

That is the honest shape of the result: A2's cost is not "one extra read", it is
"one extra *step* containing several reads". Steps are what the budget and the
turn structure are denominated in, so +1 is the number that matters — but anyone
reasoning about tokens should use 4.24, not 1.

## The staleness claim is now an observation

`double_edit` makes the model edit one pinned file twice in a turn. Under A2 the
second edit works from a read that is by then stale. The claim was that `edit`'s
exact-match requirement turns that into a *failed* edit with a did-you-mean
rather than a wrong one.

| | A0 | A2 |
| --- | --- | --- |
| runs with a failed/inexact edit match | 3/75 | **13/75** |
| runs re-reading the file mid-turn | 21/75 | 37/75 |
| final file correct | 75/75 | **75/75** |

A2 meets stale content roughly four times as often, exactly as predicted. The
matcher catches it every time, the model re-reads and retries, and the final
state is correct in all 75. The argument survives contact.

## Cost

Tokens sent rise 7–33% by task (A2 moves content out of the cacheable prefix into
later tool results, so this was pre-registered as confounded and untested). Total
spend was slightly *lower* for A2 — $0.177 vs $0.186 — which is cache and output
mix, not a saving to rely on. Treat A2 as costing one step and somewhat more
prompt tokens.

## Why this run's design changed twice

Recorded because the amendments are more useful than the result.

**The difficulty pilot failed its own band.** 47 A0-only samples came back 98%,
against a required 65–85%. That is the third ceiling in a row: the synthetic-turn
screen's original tasks at 24/24, its "hard" replacements at 100% once their
scorer was fixed, and `many_pinned` at 12/12. **These models solve small,
well-specified Go editing tasks essentially always**, so task success cannot be a
discriminating primary at this task scale — and the final run confirms it at
600/600 and 599/600.

So primary and guardrail swapped: steps became primary, success became the
guardrail. The fact that makes that an amendment rather than metric shopping is
that **the pilot was A0 only** — no treatment sample existed when the new primary
was chosen — and the direction was pre-committed by the characterization note,
which had already named cost as the question a scaled run would answer.

**The scorers passed 3,840 controls before the run started.** Every task at every
point in the cross product of all five name vocabularies, with a synthetic correct
answer that must score True and a wrong one that must score False. `run2.py`
refuses to start otherwise. The previous screen's entire primary result was two
substring bugs of exactly that shape; this harness would have caught both in
seconds.

## Recommendation

Redesign `/add` along A2's lines: pin the *names*, not the contents, and let the
model read. It removes Strument's one exception to its own norm, eliminates the
fabricated turn and the fabricated reply with it, and drives blind edits to zero,
for one step.

Two things to carry into the implementation, neither settled here:

- **Over-reading is real.** 4.24 of 6 pinned files. Worth watching on a project
  where pinned files are large, where the token cost of reading four unnecessary
  files is not absorbed by batching the way the step cost is.
- **This is single-turn evidence.** Every session here was one `-m` turn. A long
  interactive session re-pins the same files across many turns, where A0's
  re-rendered block stays current for free and A2's reads accumulate as history.
  That is the next question, and it is not answered by this run.
