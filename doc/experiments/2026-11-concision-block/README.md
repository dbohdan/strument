# A copied concision block, and the work it removes

**Status: trial, preregistered.** This design was committed before the pilot.
What the pilot changes, if anything, goes in a pilot section committed before
the main run. Results will follow below.

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
4. **Coach's notes:** C against A on Haiku (or Sonnet), the same measures.
   - It is supported if the Claude model's drop in executed and correct is
     smaller than the pooled Flash drop.
   - It is contradicted if the Claude model loses as much or more.
   - With n = 40 it is reported as an effect size, not ruled on by p.
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
- **Haiku loses less than the Flash models,** but still loses something.
- **Caveat dropped is highest under C-direct.**
