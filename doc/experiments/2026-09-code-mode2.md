# Does naming the tool in the prompt fix `code` uptake?

2026-08-31. The first `code` trial
([`2026-08-code-mode.md`](2026-08-code-mode.md)) ended 0/36, and its
strongest sentence — "the remedy sat in the schema the whole time" — was
also its own answer: the *policy* sat in the prompt, and the policy said
grep. A multi-model review of the null (fable.md, consult note) identified
two shipped sentences that overdetermined the zero:

1. The system prompt's tool list omitted `code`. "These are the ones you
   will reach for most" reads as closed-world policy to a flash-class model,
   and the schema reads as reference.
2. `FilesNoFullFiles` said "Use read, grep, glob, and ls to find what you
   need" — a task-proximal instruction naming exactly the tools that compete
   with `code`, present in every treated run.

This trial tests those two sentences, one factor at a time.

## Design

Three arms, one task, the same six models, two reps each — 36 runs, shuffled,
seed `20260830`.

| arm | prompt bullet | schema description | what it isolates |
| --- | --- | --- | --- |
| **BASE** | no | old | the 2026-08 baseline, rebuilt |
| **AB** | yes | new | prompt + description bundled |
| **DESC** | no | new | description alone |

The prompt bullet rides a `{code_tools}` slot filled at assembly time from
the same `OfferCode` flag the schema uses, so prose and schema cannot drift
in either direction — the rule that kept `code` out of the prompt when it
first shipped, kept while getting the effect. The bullet is a conditional
rule with an explicit when-not clause (single lookup, or sequential
exploration, is a direct call), and names the round-trip cost — the quantity
the model cannot otherwise perceive.

The description was inverted per the `symbol` precedent: trigger and payoff
first ("Do several lookups, or a computation, in one call"), an inline
program example, the bridge second, the Python-subset caveats compressed to
one sentence paired with the recovery path.

Not taken from the review: the runtime nudge (deferred — highest expected
uptake, but needs its own guardrails), the rename (`code` matches the
field's convention; a rename is its own arm), and few-shot examples
(per-request token cost; the reserve arm).

**Task.** Identical to the 2026-08 trial's, answer key unchanged.

**Metrics**, pre-registered: runs using `code` at all (primary); round trips
per run; and, per the review's counter-metric table, program errors,
degenerate single-lookup wrappers, and answer correctness.

## Results

| | BASE | AB | DESC |
| --- | --- | --- | --- |
| **runs using `code`** | **0/12** | **3/12** | **5/12** |
| `code` calls total | 0 | 4 | 13 |
| mean round trips | 5.8 | 4.7 | 6.2 |
| full-correct answers | 12/12 | 12/12 | 12/12 |
| program errors | 0 | 0 | 0 |
| total cost | $0.095 | $0.045 | $0.072 |

**Uptake moved off zero for the first time.** 8 of 24 treated runs called
`code`, against 0/24 in the first trial's treated arms and 0/12 here in
BASE. The description alone (DESC, 5/12) out-uptook the bundled arm (AB,
3/12) — consistent with the `symbol` decomposition, where the description
was the recruiting factor and everything else priced the tool.

Per model: glm 6 calls (2/2 DESC runs, 1/2 AB), qwen 2 (both DESC), mimo 2
(1 AB), v4flash 1 (1 AB), luna and hy3 zero everywhere. The models that
moved are not the same ones that used the tool in the first trial's probes —
this is uptake from prose, not from being told.

**The counter-metrics came back clean.** Zero program errors in 8 programs;
every answer full-correct in all three arms; the degenerate-wrapper worry did
not materialize — the programs read as real batching. `mimo-AB-0`:

```python
for pattern in ["maxToolOutputBytes", "MaxSteps", "maxChatHistoryTokens"]:
    results = grep(pattern=pattern, glob="**/*.go", mode="content")
    print(f"=== {pattern} ===")
    print(results)
```

`glm-DESC-0` combined two patterns in one alternation and read the results
in the same step. These are the multi-lookup programs the bridge was built
for.

**Round trips did not fall**, and reading the transcripts says why: the
models that called `code` still explored with grep around it, and the run-to
-run variance (mimo 13 round trips in one BASE run, qwen 12) swamps any
per-run saving at this n. The first trial's 4.0-removable-round-trips figure
measured consecutive observation runs; this task's answers were short enough
that most runs never built a long run of them. Round-trip saving remains
unproven, not refuted.

## What to conclude

- **The zero was the prompt, not the tool.** 0/36 → 8/24 uptake from two
  sentences. The recommendation against shipping is withdrawn; the honest
  statement is that the first trial measured what models do when told to
  grep, offered a calculator in the fine print.
- **Uptake is real but partial**: 8/24 treated runs, concentrated in three
  of six models. The prose did not recruit luna or hy3 at all. If the tool
  ships, it ships as a sometimes-used tool — which is exactly what the
  `replace_all` caution warns about pricing into every request.
- **The description alone carried as much as the bundle.** The prompt bullet
  is still worth keeping — it is the honest fix for the closed-world
  reading, and the conditional-assembly mechanism removes the drift risk
  that motivated leaving `code` unnamed — but the lever was the description.
- **No harm observed**: correctness unchanged, zero program errors, no
  degenerate wrappers, cost flat. The review's failure modes did not fire at
  this n.
- **Unproven:** the round-trip saving, which was the other half of the
  original motivation. Testing it needs a task that builds long runs of
  observation calls (the 27-consecutive-call shape from `mimo-B-1`), not
  this three-facts task. That, the arithmetic-shaped fixture, and the
  runtime nudge are the open experiments.

**Verdict: worth keeping, worth documenting, not yet worth a default-slot
claim.** The tool earns its schema entry by uptake; the round-trip claim
waits on a fixture built to test it.
