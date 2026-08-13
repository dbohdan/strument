# Does a pinned file carry authority? A characterization pass

**This is not an experiment.** 36 live sessions, unscored, read rather than
counted. n=3 per cell. Nothing here is an effect size, and the counts below are
descriptive aids to reading, not results. Its output is one recommendation about
what deserves a pre-registered run.

## Why

The [synthetic-turn screen](2026-08-synthetic-turns.md) produced a number nobody
was looking for: in the shipping design the model reads a file it was already
handed in **93/300 runs (31%)**, and most of those reads happen *before* the
first edit. That is not verifying its own work. `/add` is also the one place
Strument breaks its own norm — everything else the model learns arrives through
a tool call.

Four shapes for `/add`, built as separate binaries from separate branches:

| | shape on the wire |
| --- | --- |
| **A0** shipping | `user`(files) · `assistant`("Understood… I'll treat this as their current contents") · `user`(request) |
| **A1** honest injection | `user`(files, honestly worded) · `user`(request) |
| **A2** instruct | `system`(+ "the user pinned X; read it before editing") · `user`(request) — **no file content** |
| **A3** fabricated read | `assistant`(tool_call `read`) · `tool`(result) · `user`(request) |

All four verified by capturing the bytes each binary actually sends, not by
reading the diffs.

## What the sessions show

3 models (mimo, luna, v4-flash) × 4 variants × 3 tasks. All 36 succeeded; $0.02.

| variant | pre-edit reads | runs with any read | median steps |
| --- | --- | --- | --- |
| A0 | 4 / 7 | 3/9 | 3 |
| A1 | 6 / 8 | 5/9 | 4 |
| A2 | 12 / 16 | 9/9 | 4 |
| A3 | **0** / 6 | 4/9 | 3 |

**A0 reproduces the 31%** (3/9 runs). The baseline is real and not an artifact
of the screen's task set.

**A2's instruction lands cleanly, with no confusion.** The model treats it as an
ordinary instruction: *"First, I should read the pinned files numbers.go and
main_support.go to see their current contents."* All 9 runs read; the failure
mode the plan warned about — ignoring the instruction and never touching the
files — did not occur once. It costs a step (median 4 vs 3), and that step is
the read we were already paying for a third of the time.

**A3 confers the most authority of any variant, by a distance.** Zero pre-edit
reads across all 9 runs: the model edits straight from the fabricated result. Five
runs read afterwards, verifying their own work rather than the input.

## I was wrong about A3, in the way that was cheapest to be wrong

I predicted a fabricated tool call would read as adversarial more than the
current design — precise action, ID never emitted, the channel post-training
calibrates hardest, structurally the shape of prompt injection.

**No vigilance appeared in any of the 9 A3 sessions.** No model noticed it had
not made that call. Zero mentions of a prior or absent action. The reasoning
reads completely ordinary, and one run adopts the fabrication as its own memory
outright:

> *"The file is empty-ish but I read it. Let me add a doc comment."*
> — v4-flash, A3

"I read it." It did not.

Expressed doubt about currency is roughly flat across all four variants (3–5 of
9), so no shape eliminates it and A0's acknowledgement does not prevent it.

## But the test did not probe the dangerous case, and there is a code-level reason

A3 looked fine because **the fabrication was true**. The risk I described was
conditional on a discrepancy between the fabricated result and reality, and no
run produced one.

Strument would produce them constantly. `chunks.chatFiles` is rebuilt on every
send from `absFnamesContent()`, which re-reads from disk. Under A3 that means
**the same fabricated `tool_call` id carries different content after every
edit** — the model's own apparent memory of what it read silently mutates
mid-turn. A user message that changes is a user restating something; an
assistant tool result that changes is a forged and re-forged memory. Five of
nine A3 runs did read a file after editing, which is where they would meet it.

So the vigilance question is **not** settled by this pass. It is only settled for
the case where the harness's lie happens to be accurate. That is not a property
worth depending on, and it is an argument against A3 that needs no experiment.

## Recommendation

**Take A2 to a pre-registered run.** It is the only variant that removes the
exception rather than relocating it: one norm for how project content arrives,
no fabricated turn, and the instruction demonstrably lands. Its cost is one step,
partly already paid.

The metric should be **pre-edit reads of pinned files**, not task success — the
screen showed success saturates at ~99% on anything these models find tractable,
so it cannot discriminate. A2 should drive the metric to zero by construction;
the real question a scaled run answers is the **counter-metric: does the extra
round trip cost steps or wall time on work larger than these fixtures**, where a
model might read three pinned files it did not need.

Two things to fix in the design first. The tasks must be genuinely hard — the
screen's "hard" tasks ran at 100% once its scorer was fixed, so difficulty has
to be demonstrated, not assumed. And staleness needs its own probe: A2's tool
result freezes at read time, and the claim that `edit`'s exact-match requirement
turns a stale read into a *failed* edit rather than a wrong one is currently an
argument, not an observation.

**A3 is not a candidate**, on the mutation argument rather than on vigilance.
**A1 is not worth pursuing** — it keeps the exception and buys nothing measurable
over A0.
