# Does the harness's fabricated acknowledgement earn its place?

600 samples, three models, two arms. Pre-registration and its dated amendments:
[`2026-08-synthetic-turns-preregistration.md`](2026-08-synthetic-turns-preregistration.md).

## The question

Every request Strument assembles carries a fabricated pair: a user message
holding the chat files, and an assistant message replying *"Understood. Any
changes I propose will be to those files, and I'll treat this message as their
current contents."* Neither turn happened. The user message at least has a
referent — the user did pin those files with `/add` — but the reply is the
harness writing the model's lines.

The instruction it carries is not unique to it: `filesContentPrefix`, in the
user message that stays, already says *"Trust this message as the true contents
of these files."* So the reply is a **restatement in the model's own voice**, and
the question is whether hearing it in that voice does anything — whether a
fabricated acknowledgement works as a commitment device.

Arm A is current Strument. Arm B removes the fabricated assistant replies and
changes nothing else. Two binaries from two commits, so no experimental
scaffolding entered production code and the arms provably differ by the diff.

## Result

**Primary (task success): no difference, and the measurement is weak.**

| | A | B | diff |
| --- | --- | --- | --- |
| pooled | 299/300 (99.7%) | 298/300 (99.3%) | −0.33pp |

CMH stratified by model: χ² = 0.000, p ≈ 1.0. 95% CI on the difference
[−1.5pp, +1.5pp]. That interval is tight, and it means very little: at 99.5%
both arms are at the ceiling, so the tasks cannot discriminate. The honest claim
is "not worse on work these models find trivial", which is not the claim anyone
wanted.

**Counter-metric (reads of files already in the chat): B is worse.**

| | A | B |
| --- | --- | --- |
| runs with ≥1 redundant read | 93/300 (31.0%) | 118/300 (39.3%) |
| mean redundant reads | 0.76 | 0.99 |

z = 2.15, p = 0.032 on the proportion; a 20,000-shuffle permutation test on the
mean count gives p = 0.034. The direction replicated across both runs.

The mechanistic pattern is what makes this more than a p-value. The effect is
present in every task where files are in the chat and absent in the control
where they are not:

| task | A | B | ratio |
| --- | --- | --- | --- |
| `contradicts_name` | 0.61 | 0.81 | 1.33× |
| `cross_file` | 0.91 | 1.25 | 1.38× |
| `many_call_sites` | 1.52 | 1.87 | 1.23× |
| `search_required` (control) | 0.01 | 0.03 | — |

Without its own acknowledgement, the model re-fetches what it was already given.
That is exactly the behavior the removed sentence claims to prevent, appearing
exactly where the file block is load-bearing.

**Cost.** B spent 9% more ($0.213 vs $0.196) at identical median tokens and step
counts — the extra spend is the extra reads.

## Decision

The pre-registered rule was: adopt B if the CMH estimate shows no regression
beyond 5pp **and** the counter-metric shows no increase in redundant reads. The
first condition passes; the second fails. **B is not adopted.** The fabricated
assistant reply stays.

Honesty about how strong that is: p = 0.032 is one look at one metric, the
absolute effect is about one extra read every four turns, and the between-run
instability below means the magnitude is not pinned down. This is enough to stop
a change motivated by aesthetics. It is not enough to call the mechanism
established.

## What the aggregate hid

**The first 600-sample run measured the scorer, not the harness.** Two
independent substring bugs, each falsely failing 100% of the samples that drew
one particular randomized name:

- `contradicts_name` asks for a Go doc comment, which by convention *begins with
  the function's name*. The check looked for `"sum"` anywhere in that text to
  detect name-pattern-matching. When the randomizer drew `Sum`, every correct
  answer contained it. **0/41.**
- `many_call_sites` renames `<OLD>` to `<MAX>Total`, and the leftover-reference
  check was `"Total(" in src`. `"Total("` is a substring of `"MaxTotal("`, so
  every correct rename looked incomplete. **0/28.**

The arm difference was entirely this. Poisoned-name draws were A 17 / B 24 and
A 15 / B 13; observed failures were A 18 / B 24 and A 15 / B 13. `many_call_sites`
had **zero** genuine failures. The −8pp gap that looked like the commitment
device mattering was an unequal draw from a broken instrument.

Three things are worth carrying forward.

**A randomized surface parameter crossed with a substring test is a silent,
name-dependent scorer failure.** Nothing about the aggregate looked wrong. The
failure rate was plausible, the direction matched the hypothesis, and the
per-model breakdown was unremarkable. Randomizing surface names is good practice
— it is what stops a result being about one literal prompt — and it is precisely
what turned a sloppy `in` check into a systematic bias.

**Two metrics agreeing is when you stop checking.** The counter-metric told a
clean, mechanistically coherent story (1.80×, p = 0.003, absent in the control),
and the primary's −8pp read as corroboration. Corroboration is what made the
bug invisible. It surfaced only because the pre-registration *required* reading
failure transcripts, and the first sample line was
`// Sum returns the product of all the values in xs.` — a correct answer marked
wrong.

**The difficulty calibration was built on the bug too.** The pilot's 67% pass
rates, which justified promoting two "hard" tasks into the final set, were
exactly the 25% poisoned-name rate. With a working scorer those tasks run at
100%. So the ceiling problem the pilot was supposed to have solved was never
solved — it was disguised. Any future run needs tasks these models actually find
hard, and the way to know is to check that failures have *reasons*, not just
rates.

## Also worth recording

- **The counter-metric is noisy across runs.** Arm A's own total moved 145 → 229
  (1.58×) between the two runs, a swing comparable to the within-run arm effect
  (1.30×). The within-run comparison stays internally valid — arms are
  interleaved under one shuffle — but the magnitude is not stable, and a future
  run should not expect to reproduce 1.30× closely.
- **Infrastructure failures must be separated before counting.** The first
  pilot's single "failure" was a 300-second timeout, my own sentinel. Timeouts
  are now recorded by return code and excluded explicitly; the corrected run had
  zero infrastructure failures out of 600.
- **The runner now saves final file contents.** A future scorer bug costs a
  re-score, not a re-run.
- Total spend across pilots and both runs: **$0.82**.

## Scope

This tested only "the harness speaks as the model", and only for the file-context
reply. The other direction — the harness speaking as the user — is untested, and
its remaining sites each have some real referent to point at. The rare synthetic
turns (`AppendExchange`, `NoteUndo`'s assistant half, the interrupt pair, the
summary padding) fire too seldom to sample at any budget and remain design
decisions rather than empirical ones.

One of them was decided on inspection during this work and needed no experiment:
a context-limit cutoff used to be reported as an *assistant* message, making the
model appear to report its own transport error. It is a system message now.
