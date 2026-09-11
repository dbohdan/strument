# Handbook review

**2026-09-08.** Editorial review of [`doc/experimenting.md`](../../experimenting.md),
the live-experiment handbook. This is a documentation review, not a live
experiment. The purpose was to make the handbook more useful to a new
contributor or model preparing a run under time pressure without flattening its
incident-reporting voice.

## Independent judgements

### Assistant review

The handbook is clear as a body of hard-earned knowledge and less clear as a
quick reference. I rated it roughly **8/10 for an experienced experimenter**,
**5/10 for a new contributor**, and **4/10 as a pre-run checklist**. The main
problem was structural: the reader had to extract the operational checklist from
hundreds of lines of case studies, and had to assemble the distinctions between
model, provider, scorer, fixture, and runner failures from separate sections.

I recommended preserving the evidence-led prose and incident narratives while
adding an orientation layer at the front: a pre-run checklist, a small failure
taxonomy, clearer navigation, and a practical diagnostic for provider failures
versus model output in the wrong format.

### GLM-5.3-Flash review

GLM independently rated the handbook **8/10 as an essay** and **4/10 as a
pre-run reference**. It identified the same central problem: the most useful
pre-run questions were buried at the end, and the handbook does not tell a new
reader which sections are mandatory before launch and which are supporting
history.

It separately called out the unnumbered block beneath [`check-that-cannot-fail`](../../experimenting.md#check-that-cannot-fail), the scattered
failure taxonomy, the missing raw-output diagnostic in [`no-answer-vs-wrong-answer`](../../experimenting.md#no-answer-vs-wrong-answer), and the mild ambiguity
between PID-watching and log-marker watching. Its proposed priorities were to
move the checklist to the front, make the [`check-that-cannot-fail`](../../experimenting.md#check-that-cannot-fail) addition scannable, add a failure
index and model/provider diagnostic, and clarify the runner guidance. The source
review is [`glm-review.md`](glm-review.md); the consultation prompt is
[`glm-prompt.md`](glm-prompt.md).

The two reviews reached these conclusions independently. They were treated as
separate judgements rather than as a vote or a claim that either reviewer is an
experimental authority.

## Patch made

The handbook now has:

- a short “how to use this handbook” instruction after the introduction;
- a front-matter **pre-run checklist** covering mechanism activation, treatment
  uptake, fixture validity, scorer directions, arm identity, sabotage controls,
  runner completion, resume behavior, and transcript inspection;
- a **failure types and where to look** table routing common symptoms to the
  relevant sections;
- a practical [`no-answer-vs-wrong-answer`](../../experimenting.md#no-answer-vs-wrong-answer) diagnostic that says to inspect raw responses and process
  status rather than classify provider failures or inline tool-call output from
  summary rows;
- an explicit `17a` heading for the additional false-success check shapes;
- runner guidance that makes waiting on the specific process primary and log
  matching only a secondary fallback.

The patch deliberately leaves the numbered incident narratives, examples, and
transitions in place.

## What the patch got wrong, and the follow-up

A later review of the patch itself found three defects worth recording, since
the failure mode is the interesting part.

**Three editorial notes shipped inside the document.** The patch left
`> **Editorial note:** …` blocks in [`mechanism-must-fire`](../../experimenting.md#mechanism-must-fire), [`check-that-cannot-fail`](../../experimenting.md#check-that-cannot-fail) and [`resume-path`](../../experimenting.md#resume-path), each asking a reader to
verify a claim the patch had removed or blurred. A note addressed to the author
is not a note to the reader. All three were answerable: [`mechanism-must-fire`](../../experimenting.md#mechanism-must-fire)'s from the
code-result write-up's own limitation section, [`resume-path`](../../experimenting.md#resume-path)'s from this archive
(`2026-09-code-mode2/data/run.py` is the runner the shell-parallelism trial
adapted), and [`check-that-cannot-fail`](../../experimenting.md#check-that-cannot-fail)'s by running the experiment — `go test` does **not** hand back
a stale cached `ok` after a build error, but a cached `ok` for a *different*
package prints above the error, which is the real hazard and now what [`check-that-cannot-fail`](../../experimenting.md#check-that-cannot-fail) says.

**Rewriting the introduction removed the argument the introduction was
making.** "Everything here was paid for. Each item names the run that taught
it, because a rule with the evidence attached survives a reader who disagrees
with it" became a general statement that the examples explain the
recommendations — and then several rules lost their numbers, the conclusion
included. The restored opening carries an operational form of the same rule: if
you shorten a section, keep the number.

**A fourth note reached `doc/messages.md` as prose**, observing that
`‹question›` precedes harness-authored text and asking for the provenance rule
to be verified. It was right that the rule as stated had an exception and wrong
to leave the reader holding it: the marker announces the `ask_user_question`
protocol, and `Coder.afterInterrupt` deliberately reuses that protocol. The
style guide now states the exception and the reason.

**`*Tell:*` was relabelled `*Warning sign:*` at all thirteen sites, then put
back at eleven.** The concern behind the rename was reasonable — a reader whose
English is second-hand may know *tell* only as a verb. But the two words mean
opposite things about who put the sign there. A warning sign is posted
deliberately, by someone who wanted you to see it; a tell is involuntary, and
the handbook is about faults that are *not* announcing themselves. The fix is a
gloss rather than a substitution: the front matter now explains all three
margin labels in a sentence each, which serves that reader better than a
blander word would have, and the marker keeps its meaning.

Two of the thirteen were imperatives rather than symptoms and never should have
worn the label. They are `*Check:*` now, joining [`clean-null`](../../experimenting.md#clean-null)'s, and a third site the
patch added is a plain `**Do:**`.

The rest of the patch stands, and the navigation layer is the improvement it
was meant to be.

## What was preserved

The review agreed that the handbook's strongest material should remain intact:

- [`instrument-is-the-system`](../../experimenting.md#instrument-is-the-system)'s evidence-led structure, especially the ANSI example and the line that
  cosmetic output was load-bearing for measurement;
- the repeated pattern of symptom followed by a mechanical test, and its
  `*Tell:*` marker (see below);
- the distinction between honest loss and confabulation in [`loss-vs-confabulation`](../../experimenting.md#loss-vs-confabulation);
- the [`clean-null`](../../experimenting.md#clean-null) → [`runner-dies-quietly`](../../experimenting.md#runner-dies-quietly) → [`resume-path`](../../experimenting.md#resume-path) transitions, which give the failure taxonomy its clearest
  existing spine;
- the evidence attached to each rule rather than unsupported general advice;
- the final conclusion that resolving to be more careful is not a substitute for
  a control that can fail.

One important qualification was retained in the patch: a provider failure does
not have one universal textual signature. The new guidance tells the reader to
inspect raw response and process data, and to keep provider failures and
model-produced protocol mistakes as separate scorer categories, rather than
promising a brittle detection rule.

The review also found no reason to turn the handbook into a generic academic
methods guide. Its Strument-specific archaeology is part of why the rules are
credible; the change is to put the route through that material in front of the
reader, not to remove it.
