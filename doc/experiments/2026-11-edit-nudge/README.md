# Telling the model to change files with edit or write

**Status: characterization, run 2026-11-23.** A small randomized check of one
sentence before shipping it, on two providers. In short:
- Inkling 2 Mini changes files through the shell: `cat >>`, `sed -i`, Python
  heredocs. In 20 of 20 sessions it never called `edit` or `write`, with the
  sentence or without it, on OpenRouter and on Command Code.
- The sentence reaches it: on OpenRouter, 4 of 5 sessions with it said in
  their reasoning that they should have used `edit` (0 of 5 without), and
  one wrote "I should use edit tool instead of bash. Let me edit stats.go." and
  ran `true` before going back to heredocs. On Command Code it was 1 of 5.
- MiMo used `edit` in 12 of 12 sessions either way. The sentence costs it
  nothing seen here.
- Shipped (`d84b6f4`): it is true, and it is what the review surface asks of
  a model that listens. It is not measured to help a model that does not.

## Why

Driving Larkspur through Inkling 2 Mini (`thinkingmachines/inkling-2-mini`,
see the `larkspur` branch's DRIVING.md, turns 20 to 22), every one of its 118
`bash` calls was a shell command and 44 of them changed files. None of those
changes reached the user as a diff, and `/undo` snapshots only what `edit` and
`write` write: shell edits are undoable only once the model commits them,
because Strument records commits a command makes. Setting reasoning to `high`
did not move it.

The `bash` description already sent reads elsewhere ("To read, search, or
list files, use the read, grep, glob, and ls tools instead") but said nothing
about changes. The treatment adds one sentence after that one:

> To change a file's contents yourself, use edit or write rather than a
> command: the user sees those changes as diffs and can undo them, and a file
> changed by a command gives them neither.

"Yourself" leaves running a formatter or a generator through `bash` as it was.

## Method

`data/fixture/` is a small Go package: `Mean` with its test, and a README with
"arithmatic" in it. The task (`data/prompt.txt`) asks for a `Median` that
handles odd, even and empty slices, a test for it, the typo fixed, and the
tests run. Each session ran `strument chat --yes steps,bash -m` in a fresh
copy, under `reasoning = "low"`.

Two binaries: base, at `c4b52fe`, and nudge, the same with the sentence. Five
sessions per arm for Inkling and three for MiMo (`xiaomi/mimo-v2.6-flash`) as
a model that already uses `edit`, 16 sessions per provider, in an order
shuffled with a fixed seed (`data/order.txt`, `data/order-cc.txt`) and four
at a time. The same 16 ran on OpenRouter and then on Command Code's Provider
API (`https://api.commandcode.ai/provider/v1`, the `openai` adapter).

`data/score.py` counts per session: `Applied edit`/`Applied write` lines,
shell commands that write a file (a redirect into a file or heredoc, `sed -i`,
`perl -i`, `tee`, a Python script on stdin), whether the reasoning names the
editing tools, whether `go test` passes, and whether the typo is fixed. Its
output is `data/scores.txt`.

## Results

| provider | arm | sessions using edit/write | sessions writing through bash | reasoning names the tools | tests pass |
| --- | --- | --- | --- | --- | --- |
| OpenRouter | Inkling, base | 0/5 | 5/5 | 0/5 | 5/5 |
| OpenRouter | Inkling, sentence | 0/5 | 5/5 | 4/5 | 5/5 |
| Command Code | Inkling, base | 0/5 | 5/5 | 0/5 | 5/5 |
| Command Code | Inkling, sentence | 0/5 | 5/5 | 1/5 | 5/5 |
| OpenRouter | MiMo, base | 3/3 | 0/3 | 0/3 | 3/3 |
| OpenRouter | MiMo, sentence | 3/3 | 0/3 | 0/3 | 3/3 |
| Command Code | MiMo, base | 3/3 | 0/3 | 0/3 | 3/3 |
| Command Code | MiMo, sentence | 3/3 | 0/3 | 0/3 | 3/3 |

Every session passed its tests. The README typo was fixed in 31 of 32; the
miss was a MiMo base session on OpenRouter.

What the sentence did to Inkling is visible only in its reasoning, and mostly
after the fact: "I used bash to edit instead of the edit tool — should have
used edit. Fine, the file content is right." and "Wait — I used bash heredoc
to write files instead of write tool. That's because I needed exact content;
but the instructions say to use edit/write tools". The 4 of 5 against 1 of 5
between providers is within chance at five sessions an arm (Fisher's exact
p ≈ 0.2).

The habit has a cost of its own. In at least five Inkling sessions, three of
them with the sentence, a heredoc quoted as `<<'EOF'` with `\"` inside wrote a
literal backslash-quote into a Go string, and the model had to notice and
repair it ("That would be a syntax error"). That is the class of error the
`edit` tool's exact replacement exists to avoid. These were counted by reading
the reasoning, not by the scorer, whose keyword count also matches a session
that considered the question and decided the escape was valid.

## What this leaves open

- Whether anything in the harness moves Inkling. A note in the tool result
  when a command changes a file was considered and rejected: it needs the
  harness to know which files a command changed, which in a git repository
  means status and hashing around every command, and a file the user saves
  during a long command would be reported to the model as its own change. A
  warning at `/undo` was rejected as noise for a model that would trigger it
  every turn.
- Other models with the habit. Inkling is the only one seen so far.

## Files

- `data/prompt.txt`, `data/fixture/`: the task.
- `data/run.sh`: one session; the binaries are built beside it.
- `data/config-*.star`: the four configurations; keys come from the
  environment.
- `data/order.txt`, `data/order-cc.txt`: the shuffled orders as run.
- `data/score.py`, `data/scores.txt`: the counts.
- Raw transcripts: `dbohdan/experimental-data`,
  `strument/2026-11-edit-nudge/runs.tar.xz` at `355bbf6`.
