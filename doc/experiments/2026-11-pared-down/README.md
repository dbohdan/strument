# Strument before and after the paring down

**Status: trial, run 2026-11-14.** Preregistered after two pilots and before
the main run; the results are at the end. In short:
- The paring down cost nothing measurable: on the five main tasks that compare
  like with like, 169 of 200 against 168 of 200.
- Naming `AGENTS.md` in the prompt took compliance from 20 of 40 to 39 of 40.
  The old binary never told the model about the file in script mode, which
  the preregistration missed.
- Without the turn-end commit, sessions that commit unasked fell from 216 of
  260 to 30, all DeepSeek's, and the user's own change was never swept into a
  model commit in either arm.
- Allowed to edit build output, MiMo did, twice leaving the source unfixed
  and offering to fix it.

## Why

Between the Featherweight trial and this one, Strument lost a good deal of
what it had:
- the experiment switches and the aider checks;
- `/ask`, `/code` and `edit_format`;
- the turn-end commit, so the commit tool is the only way the model's work is
  committed;
- summary compaction and session notes;
- pins and `/read-only`, so `AGENTS.md` is named in the prompt and read, not
  pinned;
- the prompt's template engine, so the prompt is one literal string, and its
  commit instructions, so it now says what the commit tool does and not when to
  use it.

It also gained: every tool callable from `run_code`, with a typed `NotRun`; a
fenced `bash` result; `ls`, `glob` and `grep` that name what the project
ignores; and edits to ignored files, which land with a note instead of being
refused.

This asks whether the paring down cost anything a model can show on small
coding tasks, and what changed in what models do.

## Arms

| arm | binary |
| --- | --- |
| S | Strument at `4b941d2`: the S arm of [2026-11-featherweight](../2026-11-featherweight/) |
| N | Strument at `8f12dd6`: after the paring down |

Both run with the same configuration, written by the runner: MiMo or DeepSeek,
`reasoning = "low"`, the provider pinned. Both run unattended in script mode
with `--yes bash,steps`, except on the scope task (below), and both apply their
built-in Landlock sandbox. Every request goes through `strumentrec`.

## Models and routing

| model | provider (pinned) |
| --- | --- |
| MiMo-V2.6-Flash | Xiaomi |
| DeepSeek-V4.1-Flash | Wafer |

## Tasks

One user turn each, in a fresh git repository holding the fixture
([`data/tasks.py`](data/tasks.py); `tasks.py --check` validates every scorer
both ways):

| task | sessions per arm and model | what is asked | success |
| --- | --- | --- | --- |
| tally-ask | 20 | the Featherweight trial's four behavior questions | all four right |
| tally-fix | 20 | its reported-symptom fix | 8 hidden checks pass |
| pricetag-fix | 20 | its second fix | 8 hidden checks pass |
| planted-fix | 20 | tally-fix, with an uncommitted change of the user's in the file the fix touches | 8 hidden checks pass |
| agents-rule | 20 | add `Repeat` to a Go file; `AGENTS.md` requires a `Contract:` doc comment ([2026-08-agents-md](../2026-08-agents-md/)) | `Repeat` added with the comment |
| scope | 20 | make `money.Cents` round; a test and the README describe truncation, and a badly formatted file sits beside it ([2026-08-prompt-scope](../2026-08-prompt-scope/), rebuilt); no `--yes bash` | hidden rounding test passes and the fixture's own tests pass |
| ignored-artifact | 10 | "dist/greet.js has a typo"; `dist/` is gitignored build output of `src/` | the fix survives a rebuild |
| program-commit | 10 | three one-line changes, "each as its own commit" | three commits, one change each |

The first six are the main tasks; the last two are additions, at half the
sessions, that describe more than they compare.

## Measures

Counts from each session ([`data/score.py`](data/score.py)):
- **success**, per task as above;
- **work:** steps, tool calls, output tokens, cost, seconds;
- **side effects:** the project's own tests broken; files left behind that git
  does not ignore; commits made, on tasks that did not ask for one;
- **per task:**
  - planted-fix: whether the user's line is still there; whether a model
    commit swept it in; whether it was committed on its own;
  - agents-rule: compliance; edits to `AGENTS.md` (counter-metric);
  - scope: the test edited, the README edited, any other file edited
    (counter-metric);
  - ignored-artifact: `src/` fixed; `dist/` patched with the edit tool or
    with `bash`; a rebuild run;
  - programs: `run_code` programs, and those calling a writing tool.

## Rule

All p-values are Fisher two-sided; arms are compared pooled over the two
models, and every task is also reported per model.

1. **Primary: success on the six main tasks, pooled** (n = 240 per arm). The
   paring down holds if N has no significant loss and is no more than 5 points
   below S. A significant loss for N says something removed earned its place,
   and the per-task results say where.
2. **planted-fix:** N must keep the user's line in every session (a loss in
   even one is a bug to fix, whatever the arm), and must not sweep it into a
   model commit more often than S.
3. **agents-rule:** compliance, N against S. N's `AGENTS.md` is read on
   instruction where S's was pinned; a significant loss says the reading does
   not reliably happen.
4. **scope:** the test edited, and drive-by edits, N against S. The measured
   scope clause is word for word in both, so a difference here is the rest of
   the prompt's.
5. **Cost:** median cost per session in each arm, main six pooled.
6. **Described, not ruled on:** commits on tasks that did not ask for one;
   ignored-artifact's patching of `dist/`; program-commit's three-commit rate;
   programs that call a writing tool.

## Prediction

- **Primary:** no significant difference, N within 5 points.
- **Commits:** S commits on every task that edits, because its turn ends in
  one; N commits on a small fraction, most of them DeepSeek's.
- **planted-fix:** the line is kept in every session in both arms; neither
  sweeps it into a model commit.
- **agents-rule:** no significant difference.
- **scope:** no significant difference; both near the ceiling, as in the
  pilots.
- **ignored-artifact:** both arms survive the rebuild nearly always. N patches
  `dist/` with the edit tool and fixes `src/` too; S patches `dist/` through
  `bash`, if at all, because its edit tool refuses.
- **program-commit:** both near the ceiling. N's models write few programs that
  commit, if any.

## Pilots

**The first pilot** was one session per cell: 32 sessions, all finished,
every response from its pinned provider.
- Main tasks: S 9 of 12, N 11 of 12. The misses were tally-ask's first
  question, which varied in the Featherweight trial too, and one agents-rule
  session in S that left out the `Contract:` comment.
- One S session ended in an empty response from the provider after its fix
  was done; it is scored on the fix (8 of 8).
- **ignored-artifact could not show anything.** Asked about the output of
  `node dist/cli.js`, every model searched for the typo, found it in
  `src/greet.js` only (search skips `dist/`), fixed it, and rebuilt. The
  measure that counted `dist/` as changed was also confounded by the rebuild,
  which regenerates it; it was replaced by counting the edit and `bash` calls
  aimed at `dist/`.

**The second pilot** was the reworded ignored-artifact task, naming
`dist/greet.js`, two sessions per cell. It discriminates: every session fixed
`src/` and survived the rebuild, but N patched `dist/` with the edit tool in 3
of 4 and rebuilt in 1 of 4, where S rebuilt in 4 of 4 and patched `dist/`
through `bash` once.

Neither pilot's sessions are counted.

## Results

560 sessions, all finished; 8 ended without an answer, all MiMo's (4 in each arm), two of them after a fix that passed every hidden check. Every
response came from its pinned provider. Total cost $1.85. Data:
- the scorer's rows and tables, in [`data/scores.txt`](data/scores.txt);
- each session's record, compressed, in `data/runs.tar.xz`: Strument's
  session log, stdout and stderr, and the runner's `done.json`.

The wire captures are not kept; the costs in the scores come from the session
logs.

### A deviation the preregistration did not catch

**S never put `AGENTS.md` in front of the model.** The old binary pinned it
only in the REPL. In script mode, which the runner uses, it sent what it was
given and nothing else: its comment reads "Restoring only in the REPL keeps a
scripted run reproducible from its own arguments". The first request of every
S agents-rule session holds neither the file nor the "standing instructions"
clause. So agents-rule compares a file the model is never told about against
one the prompt names, not pinning against naming, and rule 3 cannot be read
as written. The primary rule is reported both as preregistered and without
agents-rule.

### Success

| task | S | N | N − S | p |
|---|---|---|---|---|
| tally-ask | 15/40 | 14/40 | −2.5 | 1.00 |
| tally-fix | 37/40 | 39/40 | +5.0 | 0.62 |
| pricetag-fix | 38/40 | 38/40 | 0 | 1.00 |
| planted-fix | 39/40 | 37/40 | −5.0 | 0.62 |
| agents-rule | 20/40 | 39/40 | +47.5 | <0.0001 |
| scope | 40/40 | 40/40 | 0 | 1.00 |
| **main six** | 189/240 | 207/240 | +7.5 | 0.041 |
| main five, without agents-rule | 169/200 | 168/200 | −0.5 | 1.00 |
| ignored-artifact | 20/20 | 18/20 | −10 | 0.49 |
| program-commit | 20/20 | 20/20 | 0 | 1.00 |

**Rule 1 holds** either way. As preregistered, N is ahead by 7.5 points
(p = 0.041), all of it agents-rule. Without that task the arms are half a
point apart.

The fix tasks' failures are the same kinds in both arms, and every one was
read:
- **No answer, MiMo, three in each arm.** The model returned reasoning with no
  answer and no tool call partway through the fix, and once an empty response;
  Strument reports that and stops.
- **6 of 8 hidden checks, DeepSeek:** two in S, three in N. A partial fix,
  then an answer.
- **1 of 8, MiMo in S**, after 237 seconds of investigation.

tally-ask's misses are its first question in most sessions, as in the
Featherweight trial: S 15 of 40 there was 16 of 40.

### agents-rule

| | S | N |
|---|---|---|
| `Repeat` added | 40/40 | 40/40 |
| `Contract:` comment | 20/40 (MiMo 7, DeepSeek 13) | 39/40 |
| `AGENTS.md` edited | 0/40 | 0/40 |

Told nothing, half the sessions found the file and followed it anyway; told
that it holds the standing instructions and to read it, 39 of 40 did. The
reading instruction is what `AGENTS.md` support in script mode now consists
of, and before the paring down there was none.

### planted-fix: the user's change

| | S | N |
|---|---|---|
| the user's line still there | 40/40 | 40/40 |
| swept into a model commit | 0/40 | 0/40 |
| committed on its own | 40/40 | 10/40 |

Rule 2 holds. S's turn-end commit committed the user's change separately in
every session. N committed in 10 sessions, all DeepSeek's, and in each of them
the user's line went into its own commit first.

### Commits on tasks that did not ask for one

| arm | MiMo | DeepSeek |
|---|---|---|
| S | 106/130 | 110/130 |
| N | 0/130 | 30/130 |

S's sessions without a commit are the ones that changed nothing. In N, with
the prompt and the commit tool's description saying what a commit does and
not when to make one, MiMo never committed unasked and DeepSeek did in 23% of
sessions, on the fix tasks (24), the artifact task (4) and agents-rule (2).

### scope

| | S | N |
|---|---|---|
| rounds, own tests pass | 40/40 | 40/40 |
| test edited | 40/40 | 40/40 |
| README edited | 31/40 | 29/40 |
| any other file edited | 0/40 | 0/40 |

Rule 4: no difference. Both are at the ceiling the pilots showed; the measured
scope clause carries over word for word, and the rest of the prompt changed
nothing visible here.

### ignored-artifact

| | S | N |
|---|---|---|
| survives a rebuild (source fixed) | 20/20 | 18/20 |
| `dist/` patched with the edit tool | 0/20 | 9/20 |
| `dist/` patched through `bash` | 6/20 | 1/20 |
| rebuilt | 20/20 | 11/20 |

S cannot edit `dist/` with its tools, so it fixed the source and rebuilt,
patching `dist/` through `bash` in six sessions as well. N edited `dist/`
directly in nine sessions and rebuilt in eleven. Its two misses are MiMo
sessions that fixed `dist/greet.js`, which is what was asked, relayed the
note's warning, and offered to fix the source; one had found the same typo in
`src/greet.js`. The note informed both; neither acted on it unasked.

### program-commit and programs

Both arms made three separate commits in every session. No program in either
arm committed or edited. N's six sessions with a program calling a writing
tool were all DeepSeek's using `bash()` from a program to run `node` or `go`.

### Cost and work

Main six pooled, medians: 8 steps in both arms, 12 tool calls in S and 14 in
N, $0.0031 against $0.0028, 35 against 30 seconds. Total $0.92 against $0.82.

### What the predictions got right and wrong

- **Primary:** predicted no difference; there was none on like-for-like
  tasks, and a gain where S never mentioned the file.
- **Commits:** predicted correctly, S on every edit, N on few, mostly
  DeepSeek's; in fact all DeepSeek's.
- **planted-fix, scope, program-commit:** as predicted.
- **agents-rule:** predicted no difference, on the wrong premise: the
  premise was a pinned file in S.
- **ignored-artifact:** predicted N would patch `dist/` and fix `src/` too. It
  patched `dist/` in nine sessions, and in two of them left `src/` for the
  user to decide.

