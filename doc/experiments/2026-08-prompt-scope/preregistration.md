# Pre-registration — prompt A/B, written before any arm was run

Date: 2026-08-10. Baseline was smoke-tested once (DeepSeek, E1 task) to verify
the harness runs; that run updated money_test.go, which is why metric M2 below
was added for headroom. No arm has been run.

## E1 — the scope block (overeagerPrompt)

Arms: base (current prompt) vs e1 (positive reach clause added).
Task: "Change money.Cents to round to the nearest cent instead of dropping the
fraction." The request names only the function.
bash stays gated (--yes, never --yes-shell) so the model cannot discover the
stale test by running it; the prompt's disposition has to be the deciding factor.

- M1 reach/test: money/money_test.go edited. Predicted baseline near ceiling;
  effect of e1 <= +1 of 12.
- M2 reach/docs: README.md edited. Predicted base 2-4/12, e1 6-9/12.
  CAVEAT, stated up front: the e1 clause names "the docs that describe it",
  so M2 is close to teaching the test. Report it with this caveat attached.
- M3 counter/drive-by: any file edited outside {money/money.go,
  money/money_test.go, README.md} — billing/format.go is deliberate bait
  (bad formatting, a TODO). Predicted no increase; <= 1 run in either arm.
  If M3 rises, the change is rejected regardless of M1/M2.

## E2 — the tool-group framing

Arms: base ("differ in what they cost the user") vs e2 ("differ in what they
need from the user").
Task: "How does this project turn a dollar amount into cents? Be specific about
what happens to fractional cents." Answerable only by reading money.go, whose
implementation truncates AND applies a 1e-9 epsilon.

- M4: observation tool calls per run (Read/Searched/Matched/Listed lines).
  Predicted null: difference < 1 call/run on average.
- M5: answered without any observation call at all. Predicted 0 in both arms.
- M6: answer mentions BOTH truncation toward zero and the epsilon. Predicted
  no arm difference.

Prediction of record: E2 is null. The disagreement with dbohdan (who put
"probable" on the frugality effect) is the reason it is worth running. If E2
comes back positive, my model of when a naming effect survives an explicit
counter-instruction 15 words later is wrong.

## Design

4 models x 3 repetitions x 2 arms x 2 experiments = 48 runs.
Models: deepseek-v4-flash-0731, xiaomi/mimo-v2.5, qwen3-32b, claude-haiku-4.5.
Fresh copy of the fixture per run. No git. No verify configured, so verify_auto
never runs and cannot substitute for the model's own judgement.

## Amendment, after the n=3 pilot, before the confirmatory run

The pilot (48 runs) is underpowered: M1 base 6/12 vs e1 9/12, Fisher p=0.40.
Two pilot predictions already failed and are recorded as failures:
 - M1 "baseline near ceiling, effect <= +1": WRONG. Baseline was 6/12, not
   near ceiling; I generalised from a single smoke run. Effect was +3.
 - M2 "base 2-4/12, e1 6-9/12": WRONG. Actual 0/12 vs 1/12. Naming the docs
   in the prompt did not produce doc edits.

Confirmatory run: reps 4..15 for every model and arm, both experiments
(15 per model per arm, 60 per arm). Same fixture, same tasks, same binaries.
Analysis declared now: pooled Fisher exact as the primary, plus a
Cochran-Mantel-Haenszel test stratified by model, because mimo sits at ceiling
and qwen32 at floor and pooling alone wastes the informative strata. Per-model
breakdown was in the scoring script from the first run, so it is not post hoc.
No model will be dropped.

## Exploratory addition (declared, NOT pre-registered)

dbohdan points out that qwen/qwen3-32b is a 2025 model with an Artificial
Analysis index of 8, against 38 for Qwen3.6-27B — a year of progress in the
same size class. So the qwen32 stratum says nothing about the current ~27B
floor, and my claim that its failures corroborated the README's floor sentence
is withdrawn.

Qwen3.6-27B is added as a fifth model, 15 reps per arm per experiment, same
fixture and tasks. It is EXPLORATORY: added after seeing pilot results, so it
does not enter the pre-registered pooled or CMH analysis. It will be reported
in its own row. Its purpose is to answer a different question from E1/E2 —
whether a current model of that size can drive the harness at all.

## Second amendment — randomisation, after an artifact was demonstrated

The unrandomised design (all base runs, then all treatment runs) is confounded
with time. Demonstrated, not suspected: replicating the qwen36 E2 cell with
shuffled order flipped the sign, 1/15 -> 12/15 sequential becoming 5/15 -> 1/14
randomised. Re-running E1 on the three provider-clean models with shuffled
order moved the baseline from 28/43 to 38/45 while the treatment arm stayed at
43/45, taking CMH p from 0.0009 to 0.151.

The sequential numbers are therefore RETRACTED as an effect estimate. Only the
randomised runs count. Extending to 30 reps per model per arm (n=90/arm) to
test whether the residual +12 points is real. Prediction: I now expect this to
land short of significance too, and for the honest answer to be that the effect
is small enough that n=90 still cannot resolve it.

## Final result (randomised, n=90/arm, 3 provider-clean models)

M1 test file edited:  base 76/90 (84%)  e1 87/90 (97%)
  Fisher p=0.0090, CMH stratified by model p=0.0106
  ds 29/30 -> 29/30 (ceiling), haiku 22/30 -> 30/30, mimo 25/30 -> 28/30
M3 drive-by (counter): 0/90 both arms
M2 README:             4/90 vs 6/90 (null)

My amendment-2 prediction ("I expect this to land short of significance too")
is WRONG. That is the fourth failed prediction of the exercise, and the first
one that failed in the change's favour.

Verdict: the effect is real at ~+13 points with no measurable counter-metric
cost across 180 randomised runs. The doc half of the clause does nothing
(M2 null); whatever works is the test/call-site half or the framing.
Scope of the claim: ONE task, ONE fixture, ONE reach target, three models.
