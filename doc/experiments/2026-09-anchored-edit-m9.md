# Anchored edits, M9: does the fuzzy whitespace stack still earn its keep?

Pre-registered in
[`2026-09-anchored-edit-preregistration.md`](2026-09-anchored-edit-preregistration.md);
phase 0 in [`2026-09-anchored-edit-phase0.md`](2026-09-anchored-edit-phase0.md).

Phase 0 removed the token argument for arm C — the indent column — leaving one
question as the whole of its case: **how often does the line matcher place an
edit the model could not place itself?** If the answer is "never", arm C is a
solution to a problem Strument does not have.

Runner: [`m9_run.py`](2026-09-anchored-edit-data/m9_run.py).
Fixtures: [`fixtures/`](2026-09-anchored-edit-data/fixtures).
Results: [`m9-results.jsonl`](2026-09-anchored-edit-data/m9-results.jsonl),
one transcript kept as
[`m9-luna-gonest-0.jsonl`](2026-09-anchored-edit-data/m9-luna-gonest-0.jsonl).

72 runs — 6 models × 3 fixtures × 4 reps, arm A only, job list shuffled with
seed 20260903, 4 concurrent. **$0.1325.**

## Result

| model | runs | exact | **fuzzy** | edit calls | failed | correct | $ |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `deepseek/deepseek-v4-flash-0731` | 12 | 12 | **0** | 12 | 0 | 12/12 | 0.0118 |
| `openai/gpt-5.6-luna` | 12 | 4 | **8** | 12 | 0 | 12/12 | 0.0176 |
| `qwen/qwen3.8-27b` | 12 | 12 | **0** | 12 | 0 | 12/12 | 0.0646 |
| `tencent/hy3` | 12 | 12 | **0** | 12 | 0 | 12/12 | 0.0169 |
| `xiaomi/mimo-v2.5` | 12 | 12 | **0** | 12 | 0 | 12/12 | 0.0134 |
| `z-ai/glm-5.3-flash` | 12 | 12 | **0** | 12 | 0 | 12/12 | 0.0081 |

**M9 = 11.1% of applied edits (8 of 72) — and every one of them is one model.**

By fixture, the split is just as narrow:

| fixture | indent | exact | fuzzy |
| --- | --- | --- | --- |
| `gonest` | tabs | 20 | 4 |
| `goswitch` | tabs | 20 | 4 |
| `pynest` | spaces | 24 | 0 |

Never on space-indented Python. Only on tab-indented Go, only from
`gpt-5.6-luna`.

## The mechanism

From the kept transcript. Luna quotes the block one indent level deeper than
the file has it — uniformly, every line:

```
luna sent      '\t\t\tif time.Now().After(s.budget.Deadline) {'
the file has   '\t\tif time.Now().After(s.budget.Deadline) {'
```

A constant offset, which is exactly what
`replacePartWithMissingLeadingWhitespace` and `matchButForLeadingWhitespace`
were written for. It is not a transcription error in the ordinary sense — the
text is right, the depth is off by one — and it appears not to happen at all
when the indentation is spaces.

## What it means for arm C

**All 72 tasks were completed correctly**, including all 8 fuzzy placements.
First-try edit success (M1) was 100% across the panel.

That 100% is the finding, and it needs its counterfactual said out loud: **it is
100% because the fuzzy tier exists.** Delete the whitespace fallbacks and luna
fails 8 edits in 12 — a 33% first-try rate, and eight extra round trips. The
stack is not vestigial; it is load bearing, for one model in six.

But that is also the case *against* arm C. The indent column would remove this
failure class by construction, because the model would name the indentation
rather than reproduce it. It would be removing a failure class that **currently
costs nothing**: the existing matcher already catches every instance, correctly,
at zero tokens. Arm C would buy the same outcome for +17.7% input on every read
of every file, forever.

**Arm C is not worth building.** The ladder is A → B → D.

## What this does not settle

Eight rescues is a small number of guesses to conclude from, and the risk the
fuzzy tier carries is not the guess that fails — it is the guess that lands on
the wrong lines and reports success. That did not happen here, and 8
observations cannot bound how often it would. What changed is that it is no
longer silent: an edit placed by the line matcher now warns, so a wrong guess is
visible in the same place the diff is.

Nor is 12 runs per model a tight estimate of any single model's rate. The
qualitative split is what carries weight: 0 of 12 for five models, against 8 of
12 for one. The fixtures are three small single-file tasks and cannot say
anything about larger edits, multi-file changes, or code the model has to
reconstruct from a windowed read.

## Rig checks

- **The counter can read non-zero.** `TestFuzzyEditIsCountedAndAnnounced` drives
  the applied-edit path with four-space indentation against a tab-indented file
  and asserts `fuzzy=1, exact=0`; its twin asserts an exact match is not counted
  as a guess. A zero in a trial means nothing without them.
- **The fixtures can contain the phenomenon.** Every task edits inside a nested
  block. An edit to an unindented top-level line cannot produce a whitespace
  rescue, and a fixture like that would have measured nothing — as `pynest`'s
  zero shows, the fixture being *able* to show the effect is not the same as it
  showing one.
- **Correctness scored from the files on disk**, not from the transcript, and
  independently of how the edit matched.
- **Shuffled job list**, seed recorded, so the run is not confounded with the
  wall-clock window it ran in.
- **Every transcript accounted for**: 72 runs, 0 timeouts, 0 non-zero exits, 0
  runs that changed nothing.
