# A sentence on String.raw in run_code's description

**Status: trial, stopped after two pilots, 2026-11-18.** Not preregistered:
the pilots found no failure for the treatment to prevent. In short:
- Across 48 sessions carrying backslash-heavy source through a `run_code`
  program into `edit()`, no session in either arm wrote a corrupted file.
  Base arm 23 of 24 correct (the miss was an indent), treatment 24 of 24.
- Hy4 flash already reached for `String.raw` unprompted in 5 of 6 base-arm
  block sessions, MiMo in 2 of 12 base-arm sessions. The sentence moved Hy4
  flash's use from 6 of 12 sessions to 10 of 12, and MiMo's from 2 to 0;
  outcomes were at the ceiling either way.
- Where it went wrong, it failed loudly and was retried: backslashes doubled
  inside `String.raw` (an edit not found), and once, in the treatment arm, a
  line ending in `\` inside `String.raw`, whose backslash escaped the closing
  backtick.
- The sentence does not ship. The failure that prompted it got an error hint
  instead (`f4e4591`), which covers the loud case.
- MiMo wrote `await` before a tool call in 5 of its 35 programs, more often than
  anything escape-related, and goja refuses it with "Unexpected identifier".

## Why

In a user's session, Hy4 flash (`tencent/hy4-flash`) applied seven edits to a
C++ file from one `run_code` program, each old and new text a template
literal. The program failed: `"\033"` is an octal escape, which a template
literal refuses. It recovered by switching to `String.raw`. The same paste does
worse where it does not fail: `"\n"` in a template literal becomes a newline,
`"\e"` an `e`, and a `\` before a line break vanishes with the break. An edit
built that way writes a file that differs from what the model meant, silently.

The question was whether one sentence in `run_code`'s description prevents
that.

## Arms

- **B:** Strument at `4b38b5f`.
- **R:** the same plus `data/treatment.patch`, which appends to the paragraph
  describing the interpreter:

  > In a string or template literal a backslash starts an escape, so source
  > text passed to edit or write changes on the way in: "\n" in a C string
  > becomes a newline and "\033" is refused. Put source text in
  > String.raw`...` to pass it as written.

  The model-facing golden file, regenerated in the patch, shows that sentence
  as the only difference. The R binary contains it and the B binary does not
  (checked with `strings`).

Both arms have the error hint from `f4e4591`.

## Design

- **Models:** Hy4 flash (provider pinned to Tencent, its only one) and
  MiMo-V2.6-Flash (pinned to Xiaomi), `reasoning = "low"`.
- **Setting:** one turn, `chat --unattended --yes steps`, a fresh git fixture
  per session, every request recorded through `strumentrec`. Order shuffled
  with seed 20261118, eight at a time.
- **Tasks** (`data/tasks.py`): each asks for exact changes made with `edit()`
  from one `run_code` program, so exactly one resulting file is right.
  - Pilot 1, line tasks: four single-line changes to C (`"\033[1m"`, `"\n"`,
    `"\t"`, `'\\'`), four to Python (raw-string regexes, `"\\"`), three to a
    shell script (sed `\(…\|…\)`, lines ending in a continuation `\`).
  - Pilot 2, block tasks: the same files, with whole functions or command
    blocks replaced from code blocks in the prompt, since moving blocks is
    what invited template literals in the original session.
  - Counter-metric: a program reads a CSV and writes a report with `write()`,
    where `"\t"` and `"\n"` are meant as escapes and `String.raw` would be
    wrong.
- **Measures** (`data/score.py`): the target file correct, unchanged or wrong
  (the silent failure); whether a program called `edit()` or `write()`;
  whether any program used `String.raw`; programs that failed, and those that
  failed on an escape; turns the provider cut short.

## Results

| pilot | tasks | B correct | R correct | B `String.raw` | R `String.raw` | escape errors |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | line (c, py, sh) | 11/12 | 12/12 | 2/12 | 4/12 | 0 |
| 1 | counter-metric (csv) | 4/4 | 4/4 | 0/4 | 0/4 | 0 |
| 2 | block (cb, pyb, shb) | 12/12 | 12/12 | 6/12 | 6/12 | 0 |

By model, over the line and block tasks: Hy4 flash used `String.raw` in 6 of
12 sessions in B and 10 of 12 in R; MiMo in 2 of 12 in B and 0 of 12 in R.

Every session reached the mechanism: each called `edit()` or `write()` from a
program. No turn was cut short by the provider.

**What the models do with backslashes.** In the line tasks both models wrote
each text as an ordinary double-quoted string and escaped every backslash
correctly (`"\\033"`, `'\\\\'`). In the block tasks Hy4 flash moved to
`String.raw` on its own; MiMo mostly kept double-quoted strings with `\n`
between lines, correctly escaped.

**The one wrong file** (`B_c_mimo_00`) had every backslash right and the added
line's four-space indent missing.

**The failures that occurred, all retried to a correct file:**

| session | program failed because |
| --- | --- |
| `B_shb_hy4f_01` | backslashes doubled inside `String.raw` (`\\(ERROR`), so the edit's text was not found |
| `R_shb_hy4f_01` | one `\\t` inside `String.raw`, the same |
| `R_sh_hy4f_00` | a sed line ending in `\` inside `String.raw`: the backslash escaped the closing backtick, and the literal never closed |
| `B_py_mimo_01`, `B_shb_mimo_01` (3), `R_pyb_mimo_01` | `await edit(...)` / `await read(...)` at the top of the program |

The third row is a trap in the treatment's own advice, met by a model in the
treatment arm. `String.raw` keeps backslashes, but a backslash still escapes a
backtick, so text ending in `\` (every continued shell line) cannot sit at the
end of one.

## Decision

The sentence does not ship. There was nothing for it to fix in these two
models, and it points at a construct with a trap of its own. The error hint
added in `f4e4591` already answers the loud failure that prompted the trial.
What the trial cannot speak to is the silent one at scale: a rate below 1 in
48 is not ruled out. A model that pastes blocks into plain template literals,
as Hy4 flash did once in the user's session and never here, would be the
stratum to run.

**The `await` habit is the more frequent failure.** MiMo wrote it in 5 of its
35 programs, in both arms; goja refuses a top-level `await` with "Unexpected
identifier", which does not name the cause. Tool functions are synchronous, so
either an error hint naming `await`, or accepting it, would turn a retry into
a working first program.

## Files

- `data/tasks.py`: fixtures, prompts, outcome, and the fixtures' own checks.
- `data/runner.py`, `data/score.py`: the runner and the scorer.
- `data/treatment.patch`: arm R.
- `data/pilot1-scores.txt`, `data/pilot2-scores.txt`, and the per-session
  rows beside them.
- Raw records:
  [`dbohdan/experimental-data` at `5ff73ac`](https://github.com/dbohdan/experimental-data/tree/5ff73ac/strument/2026-11-string-raw).

## Instrument notes

- **Pilot 1 could not contain the failure.** Single-line changes are written as
  double-quoted strings, which both models escape correctly. The block tasks
  were added after reading pilot 1's programs.
- **The scorer first read large programs as empty.** The session store keeps
  a large tool argument as a blob, with only a hash and a cut summary in the
  log, so a 2,576-byte program using `String.raw` in arm B was scored as no
  program. The scorer now reads blobs, for arguments and results.
- **The fixtures were hit by the trial's own subject three times.** A Python
  raw string kept a backslash meant as an escape, so one planted change never
  applied (the fixture check caught it); the check had the same fault; and a
  generator script's raw triple-quoted string contained its own delimiter.
  The block fixtures are built by slicing the before text rather than by
  string replacement, and checked by diffing.
