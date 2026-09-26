# Would a helper library make `run_code` programs shorter and more correct?

**Status: designed, not run.** Written down for later. The measurements under
*What prompted it* were taken on 2026-09-26. Nothing else has been run.

## What prompted it

In a real session, MiMo-V2.6-Flash wrote one `run_code` program that answered
five questions about the approve-model corpus. It returned a single object: the
disputed rows, a category, the id range, and counts by category. That is the
round-trip saving the code-mode trials set out to find, in unprompted use. It
also needed a line like this:

```js
Object.entries(ask.reduce((m,r)=>(m[r.why]=(m[r.why]||0)+1,m),{}))
```

In Lodash 4 that line is `_.countBy(ask, 'why')`. In Python it would be
`Counter(r["why"] for r in ask)`. The engine has no standard library to
reach for, so the model hand-rolls the helper.

**What goja has, probed with `strument tool run_code`** (goja
`v0.0.0-20260917113740-793a2a65c13b`):

- **Present:** `toSorted`, `findLast`, `at`, `flat`, `replaceAll`,
  `Object.fromEntries`, optional chaining and `??`, and rest destructuring.
- **Missing:** `Object.groupBy`, `Map.groupBy`, `Intl` and `structuredClone`.

**What models do about it, from the committed trial transcripts**
(`doc/experiments/**/*.jsonl`):

- 32 occurrences of `.reduce(`, and 11 of `new Set(`.
- **No attempt** at `Object.groupBy` or `Map.groupBy`, no `_.` call, and no
  `require('lodash')`.

So models do not fail by reaching for a helper that isn't there. They
silently write the long version. The cost to measure is therefore program
length, and wrong answers from hand-rolled reduces. Missing-name errors are
not the cost.

## Arms

| arm | change | why |
| --- | --- | --- |
| A | none | the baseline |
| B | polyfill `Object.groupBy` and `Map.groupBy` | standard since ES2024, so a model may reach for them unprompted; no description change |
| C | Lodash 4 as `_`, named in one line of `run_code`'s description | the whole kit, in the version models know best |

B is the minimal change. If B captures most of C's effect, the harness
carries 20 lines rather than 70 KB and a description line. B also tests
whether models reach for a standard name unprompted, which the transcripts
never gave them a reason to do.

**Build notes for C:**

- Compile the library once with `goja.Compile` and run the compiled `Program`
  in each fresh runtime. Evaluating 70 KB of source per call adds milliseconds
  on every program.
- Pin the version and record it here.
- Every arm must keep "the callable functions are exactly: …" true. That
  sentence is the subject of
  [`2026-09-tool-disclosure`](../2026-09-tool-disclosure/), so C's
  description line must not read as extending the tool list. Settle that
  entry's H2 first, or word C's line after its result.

## Fixture and tasks

The saving only shows where the reduce idiom appears, so the tasks need
counting, grouping and joining. A plain lookup shows nothing. Candidates, all
over files in the project:

- **Count by key:** records per category in a JSON file, with the answer a
  table.
- **Group and pick:** for each group, the item with the largest field.
- **Join two files:** rows of one file whose key is missing from the other.
- **Distinct values across files:** a set union over several JSON files.
- **A control:** a single lookup that needs no helper. It is there so that
  C cannot win by breaking the plain case.

Each answer should be computed from planted data and checked by running the
planted data once, as the handbook requires. A plant has to change the value
that comes out, not just a line on the path to it.

## Metrics

- **Primary: program tokens.** The summed length of the `run_code` programs
  in a session, on the helper tasks.
- **Counter-metric: correctness.** A silently wrong answer from a hand-rolled
  reduce is the failure the baseline is suspected of. A wrong answer from a
  misremembered Lodash call is the failure C risks. Both count.
- **Reported:**
  - **Lodash 3 names in C:** calls such as `_.pluck`, `_.where` and
    `_.contains`, which v4 removed. Each fails with the missing name, then
    usually gets a retry.
  - **Uptake:** `_.` calls in C, and `groupBy` calls in B.
  - **`run_code` calls per session in each arm.** The description change in
    C may move how often the tool is reached for at all.
    [`2026-09-code-mode2`](../2026-09-code-mode2/) showed that wording moves
    uptake.
  - Round trips, cost, and the first program's failure rate.

## Rule, to be fixed in the preregistration

C ships if it lowers program tokens on the helper tasks at p < 0.05 without
lowering correctness at p < 0.1. B ships under the same test. If both pass, B
ships, because it is smaller, unless C beats it on the same test.

## What this cannot answer

- **Whether helpers change what models attempt.** A model that knows `_` is
  there might take on a bigger program in one step. Tokens per program could
  rise while round trips fall. Report both, and don't read a rise in tokens
  as a loss without looking at round trips.
- **Other languages' idioms.** Python's `collections` is not on offer. This is
  about closing the gap inside JavaScript.
