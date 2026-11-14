# Strument before and after the paring down

**Status: trial. Preregistered 2026-11-14, after two pilots and before the main
run; results follow at the end.**

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
