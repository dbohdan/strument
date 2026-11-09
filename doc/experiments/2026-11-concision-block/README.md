# A copied concision block, and the work it removes

**Status: trial, run 2026-11-10.** The design was committed before the pilot,
the pilot's changes before the main run, and a provider-pinned follow-up
before it ran. The results are at the end. In short:
- The block's cost did not reach the preregistered threshold in either run.
- It pointed the same way in every run where the models were served by
  their usual providers.
- The largest effect measured was not the prompt but which provider
  OpenRouter routed MiMo to.

## Where this came from

The maintainer compared harnesses on a practical code-investigation question,
running the same models in Strument and in Command Code, a third-party coding
agent. Under Command Code, the same models did much less investigation: one
model read a single file where in Strument it read the source and ran a
probe, and the reading-only answers were the ones with mechanical errors. That
comparison was one session per model and harness, so it is a lead, not a
result.

The Opus 5.5 agent that examined Command Code's system prompt (from the
`command-code@1.88.1` npm package) found a block of verbosity rules that is
nearly word for word from Claude Code's 2025 system prompt:
- "fewer than 4 lines";
- "minimize output tokens as much as possible";
- "One word answers are best";
- the `2 + 2 → 4` examples.

Several other sections are close to Claude Code's 2025 text as well.

The block was written to correct a 2025 Claude model's tendency to over-explain.
That raises a question about prompt reuse in general: what does a correction do
to models that don't have the tendency it corrects? There are signs the Command
Code team is trying to do a good job. This trial is not about their product. It
tests the one block, transplanted into Strument, and whether the same brevity
can be had without the cost.

**Not tested here:** Command Code's other sections, including a rule that sends
"ANY exploration/understanding question" to an explore sub-agent whose tools
are `glob`, `grep`, `read_file` and `read_directory`, with no shell. That rule
may matter more than the block. Nothing here says what Command Code's users
experience.

## The private pilot

A private run on 2026-11-09 put the whole block into Strument's prompt on the
[bash-bullet](../2026-11-bash-bullet/) fixture's tasks, with MiMo-V2.6-Flash
and DeepSeek-V4.1-Flash, 20 sessions per model, task and arm. On the run task:

| | Strument | + block | p |
| --- | --- | --- | --- |
| ran project code | 34/40 | 15/40 | < 0.0001 |
| all four questions right | 12/40 | 3/40 | 0.020 |
| median output tokens, DeepSeek | 2,559 | 1,052 | |
| median cost per session | $0.0019 | $0.0018 | |

Its preregistration predicted that the accuracy drop would not reach
significance. It did. That run was preregistered only in scratch space, so it
is reported here as a pilot and not pooled with what follows. This trial is in
part its replication.

## Arms

All arms are built from the same commit by
[`data/build_arms.py`](data/build_arms.py). Each inserts one text,
[`data/arms/<ARM>.txt`](data/arms/), at one place in Strument's tool prompt:
after the tools section and before "Every call's result comes back to you".
The script checks that every arm's diff is one hunk in `prompts.go`, and the
diffs are kept beside the texts.

| arm | inserted text |
| --- | --- |
| A | nothing |
| C | the whole block as shipped in Command Code 1.88.1, minus one bullet that names an explore agent Strument does not have |
| C-length | only the length rules: "fewer than 4 lines" and "minimize output tokens…" |
| C-direct | only "Answer the user's question directly, without elaboration… One word answers are best" and the four examples |
| P | "Keep your final answer short. This is about the answer, not the work: investigate and verify as much as the question needs, then report what you found in a few sentences." |

- **C-length and C-direct split the block** to find which sentences cost the
  work. They do not add up to C: the preamble paragraph and the "CRITICAL
  EXCEPTION" paragraph are in neither.
- **P is the constructive arm.** It is a prose-only rule, written to ask for
  brevity in the answer and to exclude the work from it.

## Models

| model | price per M tokens (in / out) | why |
| --- | --- | --- |
| MiMo-V2.6-Flash | $0.14 / $0.28 | this project's default; in the pilot |
| DeepSeek-V4.1-Flash | $0.05 / $0.57 | in the original comparison and the pilot |
| Claude Haiku 5.5 | $0.10 / $0.50 | a current Claude model |

All run at `reasoning = "low"`.

**Why a Claude model is included.** If the block is a correction written for
a Claude tendency, it should cost a Claude model less: the "coach's notes"
hypothesis. Haiku has a known risk: it may already be terse, and then it has
less work for the block to remove. That would make "it hurt Haiku less" mean
nothing. So this rule was written before the pilot: **if Haiku runs project
code in fewer than 3 of 10 pilot run-task sessions under A, the coach's-notes
comparison is uninformative on Haiku, and Sonnet 5.5 replaces it** for arms A,
C and P on the run task, budget permitting. Haiku's results are reported
either way.

## Fixtures and tasks

Each session is one turn of `strument --yes bash,steps --unattended -m TASK`,
run in a fresh git copy of the fixture with the sandbox on, through
`strumentrec`, which records the wire.

- **tally,** the [bash-bullet](../2026-11-bash-bullet/) fixture. The run task
  is four yes/no questions about behavior, with `map(parseInt)`, integer-like
  key order and the default sort behind three of them. Key: `no,no,no,yes`.
- **pricetag,** new: [`data/fixture2.py`](data/fixture2.py). Three different
  planted behaviors:
  - `parseFloat` stopping at a decimal comma, which the README says is
    accepted;
  - a module-level `/g` regex whose `lastIndex` makes `isValidSku` alternate
    between true and false;
  - a floating-point total compared exactly against a budget.

  Key: `yes,no,no,no`. `fixture2.py --check` computes the key under Node and
  shows that neutralizing each behavior flips its answer.
- **The read task, on each fixture,** asks where something is implemented.
  Running code adds nothing there, so it measures what a rule costs, or saves,
  where work cannot help.

## Run

- **Size:** 5 arms × 3 models × 2 fixtures × 2 tasks × 20 reps = 1,200
  sessions.
- **Order and concurrency:** shuffled with seed 20261110, eight at a time.
- **Cost:** estimated at about $3.50.

## Measures

All counts, from each session's own log. See [`data/score.py`](data/score.py).

- **executed:** ran the project's code through bash (`node`, `npm` or `npx`).
- **correct:**
  - run task: all four answers right on the `RESULT:` line;
  - read task: the right file and function.

  No answer, unparseable and failed are counted apart.
- **per-question correct,** steps, output tokens, answer length and cost.
- **caveat dropped (new):** the reasoning names a planted behavior, yet the
  answer to its question is the wrong one. Examples:
  - "integer-like" keys, but answering that codes keep their order;
  - "lastIndex", but answering that `isValidSku` is stable.

  The patterns match the behavior, not the conclusion. The scorer's
  self-test covers both directions.

## Pilot

One session per cell for MiMo, DeepSeek and Haiku: 60 sessions. Plus four
extra Haiku baseline run-task sessions per fixture, for the floor rule, and
eight Sonnet sessions. In all $0.55. No pilot session is counted.

- **The floor rule fired.** Haiku ran project code in 0 of 10 baseline
  run-task sessions, yet answered all four questions right in 7 of them by
  reading. As written in advance, Sonnet 5.5 was then tried, and it ran code
  in 0 of 5 baseline sessions (4 correct). Strument's Claude models read their
  way to answers here. That includes the `map(parseInt)` trap, which no Flash
  session in the bash-bullet trial caught without running code.
- **So the coach's-notes comparison cannot be about work for either Claude
  model.** Both start at the floor. It is about correctness and dropped
  caveats, where both have room to fall. Executed is still reported.
- **`pricetag` is at the ceiling under A:** 7 of 7 run-task sessions correct,
  mostly without running anything. That is fine for detecting harm, since C
  can only pull it down. But it makes `pricetag` a test of keeping a caveat
  found by reading, and leaves `tally` carrying the signal about running code.
- **Plumbing:**
  - every pilot session finished, with no failures and no unparseable
    answers;
  - every read-task answer was correct;
  - the dropped-caveat count fired on 6 sessions, and all 6 were read. Five
    are real: the reasoning names the planted behavior, and the answer goes
    against it. One MiMo session wrote "0.10+0.20 = 0.30000000000000004 >
    0.30 → false" and then answered "yes". The sixth was a false positive:
    "parseInt without radix, but uses default" matched a bare "radix". The
    pattern now needs index and radix together, and that sentence is a
    negative case in the self-test. One of the five real ones is in arm A,
    so the baseline drops caveats too.

**Changes, made before the main run:**

- **Sonnet 5.5 runs arms A and C only,** on both fixtures' run task, 20 reps
  each: 80 sessions, at about $0.05 a session. P is left out for Sonnet: P's
  question is whether work is kept, and Sonnet does none here to keep.
- **Haiku stays in every arm,** as the terse Claude model.
- **All jobs, Sonnet's included, run in one seeded shuffle,** so no model is
  confounded with when it ran.
- **Rule 4 is now judged on correct and caveat dropped,** not on executed.

## Follow-up: pinned providers

Added on 2026-11-10 after the main run's scores, and before the run it
describes. The main run's results are below. Reading them showed that
OpenRouter's choice of provider mattered more than the prompt arm. MiMo
sessions served mostly by DeepInfra:
- ran code in 5 of 38, against 38 of 62 on other providers (p = 3 × 10⁻⁶);
- answered correctly in 0 of 38, against 9 of 62 (p = 0.01).

DeepInfra and Xiaomi both list the model as `fp8`. The mix was also uneven
across arms: 13 of MiMo's 20 C sessions went mostly to DeepInfra, against 8 of
its 20 A sessions.

**Design:**
- **Pinning:** MiMo goes through Xiaomi only, its maker's own endpoint.
  DeepSeek, which has no first-party endpoint, goes through Wafer only, the
  provider in the comparison that started this. Both have fallbacks off
  (`provider.only`, `allow_fallbacks: false`).
- **Scope:** all five arms on the `tally` run task, 20 reps each per model:
  200 sessions, seed 20261111.

**Rule:** rule 1 again, on these sessions alone. C is below A on executed and
on correct, each at p < 0.05, the two models pooled (n = 40 per arm).
C-length, C-direct and P are reported against A as before.

**Prediction:** with Xiaomi serving MiMo, A's baseline returns to the private
run's level (about 12 of 40 correct), and rule 1 holds.

## Rule

All p-values are Fisher two-sided. The Flash models are pooled over models and
fixtures: n = 80 per arm per task.

1. **The block costs work and accuracy** if, on the run task, C is below A on
   both executed and correct, each at p < 0.05, Flash models pooled.
2. **The decomposition** is reported, not ruled on: C-length and C-direct
   each against A, same measures.
3. **P succeeds** if all three hold, on the run task, Flash models pooled:
   - its median output tokens fall by at least half as much as C's do,
     relative to A;
   - executed is not significantly below A;
   - correct is not significantly below A.

   If P succeeds, the write-up recommends it, or something like it, to anyone
   who wants short answers from an agent.
4. **Coach's notes:** C against A on Haiku and on Sonnet, each with n = 40.
   - Judged on correct and caveat dropped, not executed, because both Claude
     models start at the floor on executed (see the pilot).
   - Supported if each Claude model's drop in correct is smaller than the
     pooled Flash drop.
   - Contradicted if a Claude model loses as much or more.
   - Reported as effect sizes, not ruled on by p.
5. **Caveat dropped** is reported for every arm against A. The prediction is
   that C and C-direct raise it.

## Prediction

- **Rule 1 holds,** as in the pilot, on both fixtures.
- **C-direct does most of the damage.** "Without elaboration" and "One word
  answers are best" tell a model to cut its reasoning out of the answer, and
  in the pilot the integer-key caveat was the answer. C-length costs output
  tokens but much less work.
- **P succeeds:** answers about as short as under C-length, with executed and
  correct within noise of A.
- **The Claude models lose less correctness than the Flash models,** but
  still lose some: they drop more caveats under C than under A.
- **Caveat dropped is highest under C-direct.**

## Results

The main run had 1,280 sessions and the pinned follow-up 200. Every session
finished; there were 7 "no answer" turns in all, all MiMo ending a turn with reasoning and no reply, and no failures. Main-run
cost was about $6, the follow-up's about $0.40. Data:
- the scorer's output, in [`data/scores-main.txt`](data/scores-main.txt) and
  [`data/scores-pinned.txt`](data/scores-pinned.txt);
- every session's log, compressed, in `data/runs-main.tar.xz` and
  `data/runs-pinned.tar.xz`.

### Rule 1: does the block cost work and accuracy?

Run task. Main run: both fixtures, Flash models pooled. Follow-up: `tally`
only.

| run | executed A → C | p | correct A → C | p |
| --- | --- | --- | --- | --- |
| private pilot (11-09), `tally` | 34 → 15 of 40 | < 0.0001 | 12 → 3 of 40 | 0.020 |
| main run, both fixtures | 50 → 36 of 80 | 0.039 | 45 → 48 of 80 | 0.75 |
| main run, `tally` only | 26 → 17 of 40 | 0.072 | 5 → 9 of 40 | 0.38 |
| pinned follow-up, `tally` | 35 → 27 of 40 | 0.059 | 15 → 8 of 40 | 0.14 |

**Rule 1 is not met in either preregistered run.** The main run fails on
correctness. The follow-up has both measures in the predicted direction, and
neither reaches p < 0.05. Read together:
- **In both runs where MiMo was served mostly by good providers** (the private
  pilot and the pinned follow-up), the block cut both executions and correct
  answers, and by similar amounts in the follow-up: 8 of 40 fewer
  executions, 7 of 40 fewer correct.
- **The main run is the exception,** and its baseline was degraded by routing
  (below).
- **The private pilot's effect was probably an overestimate,** as a first
  significant result tends to be. A fair summary is that the block costs some
  work and some accuracy on a task where running code finds the answer. The
  size is smaller than the pilot suggested, and not established at the
  threshold set in advance.

### The provider effect

On `tally` in the main run, MiMo's results depended on where OpenRouter sent
its requests:

| MiMo, by majority provider | sessions | ran code | correct |
| --- | --- | --- | --- |
| DeepInfra | 38 | 5 (13%) | 0 |
| Cohere | 32 | 20 (63%) | 3 |
| Xiaomi (first party) | 16 | 12 (75%) | 5 |
| Makora | 14 | 6 (43%) | 1 |

- **Every endpoint here lists the model as `fp8`,** so this is not
  quantization. What differs about the DeepInfra endpoint is not visible
  from here.
- **The routing was uneven across arms:** 13 of MiMo's 20 C sessions against 8
  of its 20 A sessions. That confounds the main run's work effect for MiMo.
- **The private pilot had almost no DeepInfra traffic,** and pinned to Xiaomi,
  A's baseline came back: 15 of 40 correct, against 5 in the main run.
- **DeepSeek's spread across providers was smaller and noisier.**

This is the most consequential finding of the trial for this project. Every
earlier MiMo result through OpenRouter was measured on routing nobody
controlled, and a prompt arm can be swamped, or faked, by which arm happened
to land on which endpoint. The handbook now says to pin the provider.

### Which part of the block?

Pinned follow-up, `tally`, Flash models pooled, each arm against A's 35
executed and 15 correct of 40:

| arm | executed | correct | median output tokens |
| --- | --- | --- | --- |
| A | 35 | 15 | 2,118 |
| C | 27 | 8 | 1,334 |
| C-length | 33 | 8 | 1,707 |
| C-direct | 34 | 9 | 1,762 |
| P | 35 | 15 | 1,987 |

- **Only the whole block cut executions noticeably.** Either half alone cost
  correctness about as much as the whole, and neither p is below 0.14.
- **The prediction that C-direct does most of the damage is not supported.**
  The one striking cell is DeepSeek under C-direct: 1 of 20 correct, against
  8 under A.
- **The decomposition is underpowered.** It says the cost is spread across
  the block rather than living in one sentence, and not much more.

### P, the prose-only rule

| run | executed P vs A | correct P vs A | output tokens saved vs A (C saved) |
| --- | --- | --- | --- |
| main, Flash pooled | 55 vs 50 of 80 | 47 vs 45 | 17% (28%) |
| pinned, Flash pooled | 35 vs 35 of 40 | 15 vs 15 | 6% (37%) |

- **P cost nothing measurable in either run.**
- **Its saving was modest.** It met rule 3's "at least half of C's saving" in
  the main run (203 tokens saved, 171 needed). It missed it in the follow-up
  (131 saved, 392 needed).
- **So P is safe, but it is not the answer to short output.** That needs
  something that shortens the prose more and still leaves the work alone. It
  is the obvious next arm.

### Coach's notes: the Claude models

- **Neither Claude model ran code here,** under any arm: 0 executions in 80
  Sonnet sessions and 1 in 200 Haiku run-task sessions.
- **They read instead, and Sonnet reads well.** Question 3 of `tally` asks
  whether codes keep their first-appearance order. The key is "no", because
  integer-like codes come first. Sonnet answered "Yes, with one exception"
  and stated the exception exactly, in 38 of its 39 "wrong" answers. Scored
  leniently, counting an answer that states the exception as right
  ([`data/lenient.py`](data/lenient.py), post hoc), Sonnet's `tally` scores
  are A 19 of 20 and C 20 of 20. All three traps were caught by reading, and
  the block cost nothing.
- **Haiku, both fixtures:** correct 24/40 under A, 21/40 under C. Its
  bottleneck is the `parseInt` question, 5 of 20 under A.
- **Against the pinned Flash drop** (15 → 8 of 40), the Claude models lost
  little: Haiku −3, Sonnet −1 strictly, +1 leniently. That is the direction
  coach's notes predicts. But the test cannot separate "the correction fits a
  Claude model" from "a model that answers by reading has no work for the
  block to remove". Both Claude models were at the floor on work before the
  block arrived.

### Caveat dropped

- **No arm differed from A** in any run. The measure works: the pilot
  sessions it flagged were read, and five of six were real.
- **The block did not move it.**
- **The question-3 finding above** shows the measure's limit on that
  question: a yes/no format forces a model that understands the exception to
  pick a side. Questions without that ambiguity would make it a sharper
  instrument.

### The read task

All 600 read-task sessions were correct in every arm, and almost none ran
code. The block shortened the answers: MiMo's median output fell from 294 to
194 tokens, Haiku's from 419 to 228. Where reading is enough, the block saves
tokens at no cost found here.

### Predictions, scored

1. **Rule 1 holds, as in the pilot, on both fixtures:** no. The direction
   holds in the pinned follow-up; significance does not.
2. **C-direct does most of the damage:** no. The cost is spread across the
   block.
3. **P succeeds:** half. No cost in either run; enough saving in one.
4. **The Claude models lose less correctness than the Flash models:** yes in
   direction (Haiku −3, Sonnet ≈ 0, Flash pinned −7). The second half, that
   they drop more caveats under C, is no.
5. **Caveat dropped is highest under C-direct:** no difference anywhere.

### What to take from it

- **For harness authors:** this block's measured effect is a modest loss of
  work and accuracy on investigation, with a real saving where the answer is
  found by reading. It is not the large effect the private pilot suggested.
  The effect also depends on whether the model in the harness answers by
  running or by reading.
- **For this project:**
  - Pin the provider in every MiMo trial. That belongs in the handbook beside
    "pin reasoning low".
  - Earlier MiMo results should be read knowing their routing was not
    controlled.
- **About Claude Code's 2025 prompt,** the coach's-notes question, on what
  could be measured: on a Claude model the block cost nothing, consistent
  with its having been written for one. On models that work by running code,
  it cost something. A prompt is half of a pair, and here the other half was
  measured.
