# Sessions and history

This is the reference for Strument's sessions and the record it keeps of them:
the commands, the record format, stored tool output, and what to do when a
project directory moves. For why resuming and compaction work the way they do,
see [`sessions.md`](sessions.md). For where the files live on disk, see
[per-project state](config.md#per-project-state).

## Sessions

A project can hold several sessions, each with its own name, conversation,
pinned files, and undo history. `--session <name>` (`-s`) says which one to
work in. It creates the session the first time, and the name is remembered as
the one a bare `strument` picks up next. The cost ledger is one file per
project and names the session on every row, so it answers both "what has this
project cost me" and "was that session worth it".

`--session` selects a session; it does not resume its conversation.
`strument --continue` (`-c`) does. It rebuilds the conversation from the
[session record](#the-session-record), so a session survives the process that
had it, whether that ended in a crash, a closed laptop, or a power loss.

- `strument --session review -c` picks up the `review` session's conversation,
  and `strument --session review` starts fresh in it.
- If the restored conversation is already too big to send, Strument compacts
  it first, rather than letting the first turn fail. It says how many messages
  it restored.
- If the conversation was made by a different model than the one now running,
  Strument adds a line saying so. Otherwise the new model would read an earlier
  assistant turn as its own.
- Without `--continue`, a session starts with an empty conversation.

Inside Strument, `/session` lists the sessions and switches between them
without restarting; switching restores the conversation of the session you
move to. `/session fork <name>` starts a new session that carries this one's
notes forward, with the parent recorded so the notes say where they came from.
`/model` does not fork: many conversations on one strong model is the common
case, so forking belongs to sessions.

Session notes carry context from one session to another. `/notes` shows them,
`/notes generate` regenerates them from the session record, and `/notes drop`
discards them. They stay in memory and are not saved to disk.

From the shell:

- `strument session list` shows the sessions with their turn counts and sizes.
- `strument session list --names` prints only the names, one per line. The
  shell completions use it to offer session names after `-s`/`--session` and
  for `session rename` and `session delete`.
- `strument session rename` renames a session.
- `strument session delete` deletes one after saying what that removes. The
  stored tool output stays, because it is shared between sessions.

## The session record

Strument records each session as a [JSON Lines](https://jsonlines.org/) log
under its project's state directory, one file per run. Recording does not
change the terminal output. `--no-history` records nothing at all.

The log lives outside your project on purpose. Inside the tree it would be
part of the workspace, so `grep` and `glob` would match it and the model could
read its own transcript. In a 300-session trial, a search hit the log in 46 of
them.

### Reading it

- `strument history list` shows the session's runs, newest first. Each row has
  the run's number, when it started, its turns, cost and model, and what it was
  asked.
- `strument history markdown` renders the whole session as a Markdown
  transcript. `-t <n>` limits it to the last *n* turns.
- `strument history path` prints the newest run's file, for `jq` and other
  tools. `strument history edit` opens it in your
  [editor](config.md#strument-config).
- `--session <name>` on any of these reads another session's record without
  switching to it.

Runs are numbered from 1 by the first, and a number does not change when later
runs are added. Runs that recorded nothing, such as starting Strument and
quitting, are not counted.

`-b <n>` (`--back`) picks one run for `path`, `edit` and `markdown`:

- A positive number is the run with that number.
- `0`, `-1`, `-2`, … count back from the latest. Write the negatives as `-b-1`
  or `--back=-1`: kong would read a separate `-1` as a flag.

Without `-b`, `path` and `edit` act on the latest run and `markdown` renders
the whole session. With it, `markdown` renders that one run.

### Record types

Each record has a `type` field:

- A **`session`** header at the start of each run.
- **`message`** and **`reasoning`** records for every message the model sent or
  received, including tool calls and their arguments.
- A **`turn`** record at the end of each turn.
- A **`request`** record for each request to the model.
- A **`side_call`** record for each request Strument makes for itself.

The **`turn`** record carries:

- the outcome and the number of steps;
- token counts, cost, and throughput in tokens per second;
- the files the turn changed;
- the one-line summaries of the work that Strument printed;
- the mode (`edit_format`) and the tools the model was offered
  (`offered_tools`);
- the prompt and answer as a reader sees them.

`tokens_per_second` is absent when there is no rate to report: nothing
received, or too little elapsed time to divide by. `outcome` is `Crashed` for a
turn that ended in a panic, and its answer is the fragment the turn had
produced.

A **`request`** record covers each request to the model that a turn or an
aside makes, retries and continuations included. It follows the assistant
message it produced. The turn record has only the sums; the per-request split
is what shows where a turn's cost went: the fixed prompt, the growing history,
reasoning, or the answer. It carries:

- the `call` (`turn` or `aside`);
- the `step` the turn had completed when the request went out;
- an `outcome` (`done`, `continuation`, `failed`, `interrupted`, and others);
- the provider's `finish_reason`;
- `seconds`, and the `error` for a failed request.

When the provider reported usage, the record adds `sent` and `received`, their
`cache_read`, `cache_write` and `reasoning` parts, the `cost`, and the
`provider` that actually served the request, where a router reports one
(OpenRouter does). A request that failed before usage arrived has no counts
rather than zeroes.

A **`side_call`** record covers each request Strument makes for itself: a
commit message, session notes, or a compaction summary. These requests are
separate from the conversation, so they appear nowhere else in the log.
Without these records, a failed one showed only as its consequence, such as a
commit reading `(no commit message provided)`. The record carries:

- the `call` and the `model`;
- `seconds` and `attempts`;
- an `outcome` of `ok`, `empty`, `error`, or `deadline`;
- the `error` text when there is one;
- the same usage fields as a `request` record.

```sh
jq -c 'select(.type=="side_call" and .outcome!="ok")' "$(strument history path)"
jq -s 'map(select(.type=="request")) | group_by(.provider) | map({provider: .[0].provider, requests: length, reasoning: (map(.reasoning // 0) | add)})' "$(strument history path)"
jq -r 'select(.type=="message" and .role=="assistant") | .text' "$(strument history path)"
```

## Stored tool output

Tool output of a kilobyte or more is stored beside the record rather than in
it, in a `blobs/` directory under the project's state directory. That covers
both a tool's result and a call's arguments. Each file is named by the SHA-256
of its contents. The record then carries `blob` (that name), `bytes`, and
`summary` (the output's first line) in place of `text` or `arguments`.

The conversation itself — the model's answers and what you type — always stays
in the record, whatever its length. So does an answer you typed to
`ask_user_question`: it is your own words, not tool output.

This lets history be pruned without being forgotten. Deleting the stored
output leaves the timeline, the hash, and one line saying what was there.
Identical output is stored once, so a file read in five turns is one file on
disk, and removing something that should never have been recorded is one
deletion rather than five.

```sh
# What has this project stored, largest first?
jq -r 'select(.blob) | [.bytes, .summary] | @tsv' "$(strument history path)" | sort -rn
```

`strument history strip` deletes stored tool output that no recent record
refers to, and keeps every record.

- Without `--older-than`, it removes output not referenced in the last 90
  days, so a bare invocation removes what is plainly old rather than
  everything. Pass an age such as `30d`, `6w`, or `720h` to choose.
- It says what it will remove and asks first.
- Stored output is shared across the project's sessions, so `strip` takes no
  session.
- Output is removed only when no recent record anywhere in the project refers
  to it. Removing something that should never have been recorded therefore
  removes every copy of it at once.

Each removed output keeps its hash, size, and first line in the record, so the
conversation still reads and replays. A restored conversation shows
`[strument] This result is no longer stored. It was 13710 bytes. It began:
big.txt (200 lines)` where the output was. A call whose arguments were removed
is labeled the same way.

For a single small item that was kept inline, `strument history edit` opens
the record in your editor.

## If you rename a project directory

Strument keeps a project's records, input history, cost ledger, resume state
and undo stack outside your tree, under `$XDG_STATE_HOME/strument/projects/`,
keyed by the project's path. Renaming the project directory therefore gives it
a new state directory.

When Strument recognizes a renamed project, it shows a notice at startup:

```
strument: this project also has 47 turns recorded under ~/src/proj, which no longer exists.
  Merge saved state: strument project adopt ~/src/proj
  Dismiss this notice: strument project ignore ~/src/proj
```

`strument project adopt` previews the changes and asks for confirmation.

- When confirmed, it combines the records, input history, and cost ledger in
  time order.
- For `resume.json` and `undo.json`, it keeps the newer file from each pair.
- You can run it even after you have started sessions at the new path.
- The old state directory is kept as `<name>.adopted-<timestamp>`.

`strument project list` lists the projects whose directory has moved or been
deleted, with their turn counts, sizes, and state directories. `--all` lists
every project, as it does when none has moved. Use this list when automatic
detection cannot identify the old project. Detection uses the repository's
first commit. So projects without Git, histories formed by merging unrelated
repositories, and multiple moved clones may not produce a notice.
