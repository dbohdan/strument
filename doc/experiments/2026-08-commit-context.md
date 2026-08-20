# How much conversation should the commit-message model see?

**2026-08-20.** 84 live sessions, two models, three arms, arm order shuffled.
Data and runner in `2026-08-commit-context-data/`.

`prompts.CommitSystem` already asks for the thing everyone wants from a commit
body:

> A body is optional and usually empty. Add one only for something the diff
> cannot say: why this approach, what was rejected, the constraint or
> measurement behind the choice.

And it underfires — 17 of 38 model-assisted commits in this repository have no
body at all. The suspicion was that this is not a wording problem.
`commitContext` fed the weak model `curMessages`: **this turn and nothing else.**
A reason is usually settled a turn or two before the change it justifies lands,
so a model following that instruction faithfully writes nothing, because it does
not know the reason either. Sharpening the wording against a model that lacks
the information would only buy invented rationales, which are worse in a commit
body than absent ones: durable, authoritative, and never contradicted.

| arm | commitContext holds |
| --- | --- |
| narrow | `curMessages` — this turn (shipped before this work) |
| wide | an 8000-byte tail of `doneMessages`, then `curMessages` |
| clause | wide, plus one sentence scoping the message to the diff |

## Design

Three turns, three jobs, every metric a count.

- **T1** states the reason — *the upstream load balancer idles connections out
  at 60 seconds, so it has to stay under that, and 45 is the value we agreed
  on* — and does **unrelated** work. The reason is now in the session and in no
  diff, no file, and no later message.
- **T2** makes the change the reason was for. Its commit body is the benefit
  metric, and it is a count rather than a judgment: the reason is one specific
  fact absent from the tree, so a body either names it or does not.
- **T3** adds a trivial function, with no reason stated anywhere, ever. There is
  nothing to explain, so *any* body is unwanted and any body describing
  something other than this diff is wrong.

Commits are matched to turns by diff content, never by position: a turn that
commits nothing would otherwise shift every later score by one.

## Result

| | narrow | wide | clause |
| --- | --- | --- | --- |
| **T2 body names the reason** | 2/28 | **12/27** | **11/26** |
| T3 body at all | 0/28 | 4/28 | 0/28 |
| T3 body describes the *previous* change | 0/28 | 4/28 | **0/28** |
| median session cost | $0.00220 | $0.00215 | $0.00220 |

narrow~wide **p = 0.0019**; wide~clause on the benefit **p = 1.00**.

The narrow arm is not being terse. It does not have the reason, and both models
agree — 1/14 and 1/14. The wide arm gets it, and the clause keeps it:

> `chore(poll): raise defaultTimeout to 45`
>
> Upstream load balancer idles connections at 60s, so the poll interval must
> stay under that; 45s is the agreed value.

Cost is a rounding error: the side call is on the weak model and already
happens every turn.

## The cost was not the one that was scored

The counter-metric was written as *noise* — a body where nothing needs
explaining. Counting it says 4/28, unremarkable. **Reading the bodies says
something else: all four describe the previous turn's change**, attached to a
commit whose entire diff adds one trivial function. An earlier run of the same
fixture produced 8/28, three of them like this:

> `BREAKING CHANGE: The default poll interval is increased from 30 to 45 seconds
> to avoid connection timeouts at the load balancer (idle timeout: 60s).`

on a commit that adds `func Ping() string { return "pong" }`. A false
`BREAKING CHANGE` in a convention that drives changelogs and release tooling is
the one failure here that can cost somebody real work.

So a wider context does not merely make the model say more. It makes it
**attribute earlier work to this commit** — which is the same defect the body is
supposed to cure, pointing the other way.

The remedy is one sentence:

> Earlier turns are background. Take the reason for this change from them if it
> is there, and describe only what the diff does — work from an earlier turn is
> not part of this commit.

It removes the misattribution entirely (0/28, identical to narrow) and costs
nothing on the benefit (11/26 against 12/27, p = 1.00). At n=28 the 4→0 drop is
p = 0.11 on its own, so the honest claim is *the clause is free and the point
estimates strictly favour it*, not that the removal is established. Pooling both
runs of the wide arm puts its misattribution rate at 12/56 against the clause's
0/28, but those runs are hours apart and pooling across them is exactly the
mistake the next section is about.

**Shipped: the widening and the clause together.** The widening alone should not
be.

## The drift, caught in the act

The same fixture, the same two arms, two runs hours apart:

| | narrow | wide |
| --- | --- | --- |
| run 2 | 0/26 | **21/28** |
| run 3 | 2/28 | **12/27** |

The wide arm's rate moved from 75% to 44% with nothing changed but the hour.
Every within-run comparison here survives that, because arm order is shuffled
and all three arms ran interleaved. A design that had run the new arm on its own
and compared it against the earlier numbers would have concluded the clause
*halved* the benefit, and it does not: inside its own run it is indistinguishable
from the arm it is meant to fix.

`../experimenting.md` already says randomize the arms. This is the second time
in this project that the size of the drift has been larger than the effect being
measured.

## Instrumentation

Four faults, and the pattern is worth stating on its own: **every one produced a
plausible null rather than an error.**

- **A silent `git show`.** `%x1e`-separated `git log` records keep a leading
  newline, so every hash after the first was `"\nabc123…"`. `git show` on that
  prints nothing and exits clean, so every per-turn metric scored "commit not
  found" — which reads exactly like "no effect".
- **Trailers counted as bodies.** Every commit ends `Assisted-by: …`, so "did a
  body appear" was true 100% of the time in both arms and the counter-metric was
  pinned at ceiling.
- **Merged turns.** Some sessions do T1's and T2's work at once despite being
  told not to. There the reason is in `curMessages` anyway, so the session tests
  nothing about widening; left in, they dilute the effect toward null. Detected
  from the diff and excluded.
- **A confirmation eating the measured turn.** 18 sessions of 56 in a first run
  simply did not make the change. The model ran `go build ./poll/`;
  shell commands are `RequiresYesShell`, which `--yes` deliberately does not
  cover, so the confirmer fell through to readline and read **the next scripted
  line** as its y/n answer. "Change defaultTimeout in poll/poll.go from 30 to
  45" is not "y", so the build was declined and the turn that carried it never
  happened: no output, no error, no transcript entry, exit 0. It looked exactly
  like the model choosing not to act.

  That last one was a fixture fault that exposed a real edge, now fixed: a
  mid-turn prompt with nobody at the keyboard declines without reading instead
  of consuming the user's next message.

## Noticed in passing

The weak model returned an empty commit message 21 times across 84 sessions —
roughly 8% of commits, which then read `(no commit message provided)`. Same
family as the `--continue` failure in
[`2026-08-notes-header.md`](2026-08-notes-header.md): a side call that fails
quietly and leaves an artifact whose wording suggests a choice rather than a
failure.
