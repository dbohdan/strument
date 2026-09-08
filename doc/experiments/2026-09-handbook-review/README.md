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

It separately called out the unnumbered block beneath §17, the scattered
failure taxonomy, the missing raw-output diagnostic in §3, and the mild ambiguity
between PID-watching and log-marker watching. Its proposed priorities were to
move the checklist to the front, make the §17 addition scannable, add a failure
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
- a practical §3 diagnostic that says to inspect raw responses and process
  status rather than classify provider failures or inline tool-call output from
  summary rows;
- an explicit `17a` heading for the additional false-success check shapes;
- runner guidance that makes waiting on the specific process primary and log
  matching only a secondary fallback.

The patch deliberately leaves the numbered incident narratives, examples,
transitions, and conclusions in place.

## What was preserved

The review agreed that the handbook's strongest material should remain intact:

- §1's evidence-led structure, especially the ANSI example and the line that
  cosmetic output was load-bearing for measurement;
- the repeated `*Tell:*` pattern of symptom followed by a mechanical test;
- the distinction between honest loss and confabulation in §9;
- the §18 → §19 → §20 transitions, which give the failure taxonomy its clearest
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
