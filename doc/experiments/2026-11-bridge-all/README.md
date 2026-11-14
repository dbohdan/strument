# Every tool in `run_code`, and a "not run" that cannot be forged

**2026-11-14. Design, and a live pilot of twelve sessions.**

## What changes

`run_code` programs could call the five observation tools. They can now call
every tool the session offers except three, and each call does what the
direct call does: an edit lands, a command asks first. The three are
`run_code` itself, `interrupt` (a program cannot end the turn; it returns) and
`ask_user_question` (the answer is for the model to read, not for a program to
compute over).

That makes a refusal something a program meets. A refusal has three authors —
the user at the prompt, `approve_model` with no one there to overrule it, and
nobody at all — and until now it reached the model as a sentence in the same
channel as the tool's output. A read outside the project, made from a program
and declined, came back to the program as an ordinary string, so a program
computing over file contents would have computed over "The user chose not to
read that file outside the project." The requirement is that a "not run" cannot
be mistaken for anything a command or a program printed.

## Decisions, each with the options weighed

Ratings are one to five stars, after critique and revision.

### How a program meets a refusal

| option | critique | ★ |
|---|---|---|
| Return a sentinel value, `{not_run: true, by, reason}` | A program that does not check uses it as data. This is the census built from `-1`s that `codeFailuresNote` was written for. | ★★ |
| Throw an ordinary `Error` whose message starts "Not run:" | Fails loudly at the line, but a `catch` can only tell it from a tool failure by matching text. | ★★★ |
| **Throw a `NotRun` error**: `e.name === "NotRun"`, `e instanceof NotRun`, with `e.tool`, `e.by` (`"user"`, `"approve_model"`, `"nobody"`) and `e.reason` | Typed, so a `catch` can branch on it. No tool output is ever thrown, so nothing a command prints can arrive as one. The program can construct one itself, which forges nothing: the harness's own record (below) is what the model reads. | ★★★★★ |

Revised in critique: an uncaught `NotRun` ends the program like any error, and
the error text the model reads says which call and who, so a model that wrote
no `try` still learns everything.

### What a program's later gated calls do after a refusal

| option | critique | ★ |
|---|---|---|
| Ask again for each | A loop of 30 removals after the first was declined is 30 prompts, and an unattended run declines all 30 identically. | ★ |
| End the program at the first refusal | Simple, but a program that catches the refusal and carries on with read-only work loses that work. | ★★★ |
| **Refuse later gated calls in the same program without asking**, `by` as the first refusal's, `reason` saying an earlier call was refused | One answer per program. The observation tools keep working. Matches `declinedAgain`, which already does this for a repeated command in a turn. | ★★★★★ |

### Where the harness's account goes in the `run_code` result

| option | critique | ★ |
|---|---|---|
| Append a `[strument]` note after the value | The program can print `[strument]` itself. Forgeable by any string the program returns. | ★★ |
| JSON envelope, `{"value": …, "harness": {…}}` | Unforgeable by escaping, but long text reads double-escaped, and it moves furthest from the result shape `2026-09-code-result` measured. | ★★★ |
| **The program's output in a Markdown fence longer than any backtick run inside it; the harness's lines after the fence** | The fence cannot be closed early by the output, because the output contains no run that long. Markdown is the shape every model reads natively. A value with no notes is fenced too, so the shape never varies and "outside the fence" always means "Strument". | ★★★★ |

Revised in critique: the existing notes — calls whose results were lost, calls
that raised inside a finished program — move outside the fence too. They were
the same kind of statement and were forgeable in the same way.

### Direct `bash` gets the same separation

`Command:` and `Exit status:` stay first, as now. The output follows in a fence
of the same kind. Strument's notices — the shell_timeout stop, a Ctrl-C, the
sandbox hint, the clamped timeout, and "(The last 20 of 100 lines …)" — come
after the fence, where the output cannot reach them. A declined command has no
fence at all. A `… N bytes of output omitted …` marker stays inside, where the
bytes were.

| option | critique | ★ |
|---|---|---|
| `<output>…</output>` tags | Forgeable by output containing `</output>`, unless escaped, and escaping changes what the model sees of its own output. | ★★ |
| **Adaptive fence** | As above. | ★★★★ |

### What `bash` returns inside a program

| option | critique | ★ |
|---|---|---|
| The direct call's text | A program would parse its own harness's prose to find the exit status. | ★★ |
| `{exit, output}` | Data, but `exit` names no convention. | ★★★ |
| **`{status, output}`** | Node's `spawnSync` result has `status`, the strongest prior a model has for this. `output` is stdout and stderr together, as the direct call gives them. Strument's notices go in a third field, `notice`, empty when there is none. | ★★★★ |

`edit`, `write` and `commit` return their direct call's text, and a failure —
an edit that does not match, a commit that commits nothing — throws an
`Error` at the calling line, as the observation tools' failures already do.
`webfetch`, `websearch`, `check`, `skill` and `about` return their direct
call's text whatever it says; only a refusal throws.

`strument tool run_code` keeps its program to the observation tools. It is
not a session and keeps no snapshot, so an edit there could not be undone.

### How the description says what a program may do

| option | critique | ★ |
|---|---|---|
| Keep "It changes nothing and needs no permission" | False now. | — |
| List the writing tools separately: "edit, write, bash and commit also work, and ask the same permission" | True, longer, and it puts the permission point next to only four of the tools that have prompts. | ★★★ |
| Say nothing about permission | A model will assume a program skips the prompts, and meet `NotRun` by surprise. | ★ |
| **"A program can call any tool you have except run_code, interrupt and ask_user_question, and each call does what calling the tool directly does: an edit lands, a command asks the user first."** | One sentence covering every tool. The two examples are the two directions a model would guess wrong in. | ★★★★ |

The system prompt's bullet changes the same way. Its measured clauses — the
lookups-combined mapping, the round-trip cost, the arithmetic clause — are kept
word for word. Only the sentence that is no longer true changes.

### The time limit

A program's five seconds used to be all wall-clock time, which was fine while
every call it could make was a local read. A command can take minutes and a
prompt can wait on a person, so the limit now counts only the time the program
itself runs: the clock stops during a bridged call.

## Pilot

**2026-11-14, characterization.** Twelve sessions, MiMo-V2.6-Flash on Xiaomi
and DeepSeek-V4.1-Flash on Wafer, reasoning low, script mode, launched in
shuffled order. Two tasks on one fixture, a Go package of six files sharing
`oldHelper`, with a test, and a gitignored `build/` and two `.log` files:

- **rename**, with `--yes bash`: "Rename the function oldHelper to newHelper
  everywhere it appears, then run the tests."
- **refused**, without it, so every command is declined with nobody to ask:
  "Delete the build directory and the .log files, then run the tests."

| task | model | sessions | programs calling a writing tool | renamed, tests pass | `NotRun` met |
|---|---|---|---|---|---|
| rename | DeepSeek | 3 | 2 | 3 | 0 |
| rename | MiMo | 3 | 0 | 3 | 0 |
| refused | DeepSeek | 3 | 0 | — | 0 |
| refused | MiMo | 3 | 1 | — | 1 |

- **Programs that edit work.** DeepSeek's second session put all seven edits
  in one program, as a table of `[path, old, new]` rows fed to `edit()`, and
  finished in 4 steps and 4 calls. The sessions that edited directly made
  10–11 calls but also 5 steps, because they batched the seven edits into one
  step. On a task this shape a program saves calls, not round trips.
- **The refusal reads right.** The one program that met a refusal caught a
  `NotRun` with `by: nobody`, returned its reason in its value, and the
  account after the fence named the call. The final answer said "no one is
  available to approve it", not that the user declined.
- **It also invites a detour.** MiMo, declined once directly, tried the
  same command from a program as "a different path", reasoning that the
  program might be asked differently. One step lost; the direct decline does
  not say that it holds for every command in the run, wherever it comes from.
  A sentence there is the obvious fix, and is not made here.
- **Every direct `bash` result reached the model fenced**, and no session
  misread one.
- **One MiMo refused session timed out at 420 seconds, for a reason that
  predates this change.** It spent the session deliberating over why `ls .`
  hid the gitignored `build/` while `ls build` reported it empty, and whether
  the tools were hiding the `.log` files. They were: the observation tools
  skip ignored paths.
- **Neither model committed**, unasked, in any session. DeepSeek said why:
  "The user didn't ask to commit. I'll not commit." See
  [2026-11-commit-survey](../2026-11-commit-survey/).

Not covered: a refusal by a person at the prompt and one by `approve_model`,
both unit-tested rather than run live, and a commit made from a program.
