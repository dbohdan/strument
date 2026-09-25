# Strument

<img alt="Two crossed hand tools: a network cable crimping tool and a screwdriver/voltage tester with a transparent handle. The screwdriver handle glows on its own." src="doc/logo/1024x1024.png" width=128>

Strument is an AI pair-programming tool for the terminal.
It is designed for developers who want to review and guide technical and UX decisions as the model works.
Working with Strument is organized into turns.
Each turn starts with your instructions to the model; changes are recorded in a commit or, outside Git, an undo snapshot.

Strument began as a ground-up reimplementation of [aider](https://github.com/Aider-AI/aider) but has since diverged.
See [`doc/`](doc/README.md) for the developer overview.


## Features

- A single binary.
  No Python-runtime dependency.
  Pure Go without cgo, even for [tree-sitter](https://github.com/odvcencio/gotreesitter).
- [Starlark](https://starlark-lang.org/) configuration.
  One `config.star` file replaces YAML, `.env` files, and a JSON model database.
  Project-local config is supported as either `.strument.star` or `.strument/config.star`.
  They are loaded only after you authorize them by running `strument trust` in the project directory.
  That command prints what the config would be allowed to do — which hosts, which commands, which variables — and asks before recording anything.
  Trust is recorded by content hash, following the [direnv](https://direnv.net/) model.
- [Tool calls](https://datacream.substack.com/p/tool-calling-explained-how-ai-agents).
  `bash` runs a command using the embedded [mvdan/sh](https://github.com/mvdan/sh) shell, a cross-platform reimplementation of Bash.
- [Agent Skills](https://agentskills.io/).
  Drop a `SKILL.md` under `~/.local/share/strument/skills/foo/` or the project's `.strument/skills/foo/`, and the model can ask for it by name.
  Project skills also require `strument trust`.
  A skill's `allowed-tools` field does not grant tool permissions.
- A sandboxed `run_code` tool.
  The model can run short JavaScript programs for calculations, formatting, or processing several inputs at once, in an embedded interpreter with no access to the host.
  Programs have no direct filesystem or network access.
  They can inspect project data through the five exposed read-only search tools.
  Tools that modify files or run shell commands are not exposed.
  See the [`run_code` tool](doc/config.md#the-run_code-tool).
- Every file Strument edits through its file tools is undoable, with or without Git.
  Strument records each file before the first time it writes to it.
  `/undo` can restore those file changes for a whole turn even in a directory that is not a repository,
  like a live configuration directory or a checkout under another SCM.
  In a Git repository, a turn is one commit.
  The command `/squash [n]` merges commits.
  Files that the model's commands created, rather than its edits (a compiled binary, a scratch script), are not committed.
  Strument lists those that git neither tracks nor ignores at the end of the turn, and tells the model about them once, when it thinks it is done, so it can remove any by-product it did not mean to leave.
- Project checks.
  The `check` config setting is a dictionary of named verification commands, like tests, a linter, and a build.
  The model can run them by name without a permission prompt.
  `project_checks()` detects standard checks for your project type.
  `check_auto` lists which of the `check` commands Strument runs at the end of any turn that changed a file.
- Web pages.
  `/web <url>` fetches a page, converts it to Markdown, and offers it to the model.
  Fetching uses either a built-in HTTPS client or an external browser command (necessary for pages that rely on JavaScript).
  The model has a `webfetch` tool, which asks your permission before fetching from an unfamiliar origin; the built-in client does not follow a redirect to one either.
  A URL fragment limits the result to that section.
  If a page exceeds the size limit, the tool returns an outline instead.
- Web search, if you enable it.
  Configure [`websearch`](doc/config.md#websearch) and the model gets a `websearch` tool.
  Use your own [SearXNG](https://docs.searxng.org/) instance, with your choice of engines and no API key, or a hosted backend that needs no setup: [AnySearch](https://anysearch.com/), which works with or without a key, or [Exa](https://exa.ai/), which searches its own index and returns page text instead of snippets (key required).
- You can [interrupt and steer](#interrupting-and-steering) a turn.

The terminal interface has stayed deliberately close to aider's, including the green/blue palette (with `--dark-mode` and `--light-mode`).
Strument diverges where its programming loop is different.
Model reasoning is marked with `‹thinking›`.
Multiline blocks end with `‹/›`; single-line blocks need no closing marker.
Syntax highlighting and the inverted code-block background are omitted.


## Example session

```none
> Rename defaultTimeout to pollInterval and update the callers.

‹thinking› Let me find where that constant is defined.
Searched for defaultTimeout: 3 matches in 2 files
Read internal/poll/poll.go (118 lines)

‹thinking›
The declaration is in poll.go and there is one more use there and one in
watch.go. I'll rename the declaration, then each use.
‹/›

internal/poll/poll.go
   const (
  -	defaultTimeout = 30 * time.Second
  +	pollInterval   = 30 * time.Second
   	maxRetries     = 3
   )

internal/poll/poll.go
  -	t := time.NewTicker(defaultTimeout)
  +	t := time.NewTicker(pollInterval)

internal/poll/watch.go
  -	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
  +	ctx, cancel := context.WithTimeout(ctx, pollInterval)

Applied edit to internal/poll/poll.go
Applied edit to internal/poll/watch.go

Renamed the constant and updated its two uses.

Running the automatic checks.
‹check› test
$ go test ./...
passed
Commit 6c1e0a4 refactor(poll): rename defaultTimeout to pollInterval

Tokens: 12.4k sent, 1.8k received, 30 t/s. Cost: $0.03 turn, $0.03 session. 4 steps, 2 files changed.
```


## Getting started

Go 1.26 or later is required, but not a C toolchain:

```sh
go install dbohdan.com/strument/cmd/strument@latest
```

Strument requires a configuration file before starting in chat mode.
It has no bundled model database, so you must configure the models you want to use.

Put this minimal configuration in `~/.config/strument/config.star`:

```python
openrouter = provider("openrouter", api_key=env("OPENROUTER_API_KEY"))

models = {"mimo": model(openrouter, "xiaomi/mimo-v2.5", context=1050000)}
default = "mimo"
```

Export the key and start Strument in your project:

```sh
cd ~/src/myproject
OPENROUTER_API_KEY=sk-or-... strument
```

Set `context` so Strument can warn you before a request exceeds the model's context window and summarize older chat history when needed.
Without it, a long session can exceed the provider's limit and have its requests rejected.

Cost fields are optional.
OpenRouter includes each request's cost in its response.
A plain OpenAI-compatible endpoint may not report costs.
For those endpoints, Strument estimates the turn's cost using `input_cost` and `output_cost`.
`strument model-config <slug>` fetches all of this information from a provider's catalog; see [Configuration](#configuration).

Strument reports costs per turn.
Each turn is limited to 25 steps by default; set `max_steps` to change the limit.
Try a small request with an inexpensive model and check the reported cost before attempting a larger task.


## Using Strument

Type what you want changed.
The model works until it finishes or reaches the step limit.
At the limit (25 steps by default) Strument reports the number of edits and the cost so far, then asks whether to continue.

Strument prints a status line for each tool call.
Shell commands ask for permission first, which you can grant for that command or for all commands in the turn; `webfetch` and `websearch` ask too.
Reading, searching, and editing do not ask.

In a Git repository, each turn that changes a file ends in a commit.
`--no-git` turns the Git integration off inside a repository; outside one it is already off.
`/undo` works either way.

### Interrupting and steering

While the model is responding or a tool is running, press `Ctrl-C` once to interrupt it.
Strument keeps the conversation and any completed work, then asks whether to continue, stop, or enter a correction.
Press `Ctrl-C` twice within two seconds to exit Strument.
In script mode (`-m`), an interrupt stops the turn without asking a follow-up question.

`SIGUSR1` interrupts the current send the same way a single `Ctrl-C` does.
It does not count toward the double-`Ctrl-C` exit, and it does nothing between turns.
This is how a script or a remote assistant can stop a run whose keyboard it cannot reach:

```sh
pkill -USR1 strument
```

You can stop a long response and redirect the model without starting over:

```none
‹thinking› I'll inspect the authentication package first...
Reading internal/auth/auth.go
^C
Press Ctrl-C again to exit

‹question› You stopped the model. What now?
1. Continue — Carry on from where it was cut off
2. Stop — End the turn here
Answer (1-2, or your own text): Use the existing token helper instead

‹thinking› I'll continue from the interrupted response using the existing token helper.
...
```

`Continue` lets the model resume from the partial response with its context preserved.
Typing your own answer sends it as a correction, and `Stop` ends the turn.
Edits made before the interruption remain undoable with `/undo`.

### REPL commands

| | |
| --- | --- |
| `/add <file> ...`, `/drop`, `/ls` | Pin files you want the model to inspect or change. Strument gives the model their names; the model reads them as needed and can find other project files itself. |
| `/ask <question>` | Ask about the project without giving the model editing tools. `/ask` on its own switches to ask mode, and `/code` switches back. |
| `/yes [add <name> ... \| drop <name> ... \| reset]` | Show or change which prompts are approved automatically. On its own it lists each approval and where it came from: `--yes`, the config's `auto_approve`, or this session. Dropping one makes Strument ask again mid-session, which is useful when a turn starts going somewhere unexpected. |
| `/attach <file> ...` | Attach images (PNG, JPEG, GIF, WebP) to your next message, from anywhere on disk. On its own it lists what is attached; `/attach drop` removes attachments. Unlike pinned files, attachments go with one message only. A model that does not accept images is told an image was there and that it could not see it, rather than the request failing; declare `input_modalities` for one that can. |
| `/check [<name>]` | Run a project check by name, or all checks if no name is given. Checks run in the order the config lists them and stop at the first failure. On failure or non-empty output, Strument offers to add the transcript to the chat. A successful check with no output is not offered to the chat. |
| `/consult <alias> <question>`, `/consult scope [<name>]` | Ask another model without switching the active model, then optionally add its answer to the conversation, labeled with the advisor's name. `/consult scope` shows or sets how much the advisor sees: `none`, `files` (the pinned files, the default), or `chat` (the pinned files and the conversation); `--consult-scope` sets the starting value. The consultation is billed at the advisor's rates and appears in the cost ledger under its slug. |
| `/session`, `/session new\|switch\|fork\|rename\|delete <name>` | List this project's sessions, or create, switch to, fork, rename, or delete one. See [Sessions](#sessions). |
| `/notes`, `/notes generate`, `/notes drop` | Show the session notes, regenerate them from the session record, or discard them. Notes stay in memory and are not saved to disk. They carry context from one session to another; to pick up *this* session's conversation, use `--continue`. See [`doc/sessions.md`](doc/sessions.md). |
| `/read-only <file> ...` | Pin a file the model can read but not edit, such as a spec or a header from a sibling repository. This is the way to show the model something outside the project; the search tools see only the project itself. |
| `/commits [on \| off]` | Show or change whether a turn that edits a file ends in a commit. With no argument, it shows the current setting. `--no-auto-commits` starts a session with commits off. With commits off, edits are still written to the working tree, and `/undo` and `/diff` still work. |
| `/undo` | Revert the last turn. Restores files changed through Strument's file tools and removes the commit if there was one. |
| `/squash [<n>]` | Combine the last `n` turns' commits into one. |
| `/usage [<provider> \| all]` | Show token usage and cost for the last 24 hours, 7 days, and 30 days. Defaults to the current model's provider. See [Usage reports](#usage-reports). |
| `/diff`, `/tokens` | Show what changed and how full the context window is. |
| `/context [<n>]` | Show the chat history as the model receives it: compaction summaries followed by recent, unsummarized messages. With `n`, show only the first `n` summaries. |
| `/skill [<name>]` | List the available skills, or add a skill's instructions to the chat yourself. See [Skills](doc/config.md#skills). |
| `/symbol <name> [definition \| reference]` | Find where a name is defined or used, using the language parser rather than a text search. |
| `/submit <file>` | Send a file's contents as your message, as if you had typed them: the trimmed contents are printed first, then sent. Paths outside the project are allowed. Files over 100 KiB are refused rather than truncated. |
| `/run <cmd>`, `/web <url>` | Run a command or fetch a page and offer the output to the model. `/run` keeps your full environment; model-run commands receive an [allowlist](doc/config.md#env_allow). `/web` on its own lists the origins `webfetch` can fetch from without asking, and `/web drop` and `/web reset` revoke those approvals. |
| `/env`, `/env add <NAME>...`, `/env drop <NAME>...`, `/env reset` | Show or change, for this session, which environment variables model-run commands receive. Tab completes variable names. Persistent changes belong in `env_allow`. |
| `/model [alias]`, `/reload` | Switch models mid-session; reload the configuration without restarting ([what a reload applies](doc/config.md#what-reload-applies)). |

`/help` lists all commands.
The model also has a `run_code` tool, which runs a short JavaScript program in a sandbox, in either mode.
See the [`run_code` tool](doc/config.md#the-run_code-tool).

### Script mode

For scripts and one-offs, `-m` runs a single turn and exits:

```sh
strument -m 'Add a --version flag to cmd/pollctl.'
strument --dry-run -m 'Fix the race in internal/poll.'  # Show the edits; write nothing.
strument --yes steps -m 'Update the changelog for v0.3.0.'  # Do not stop at the step limit; grants no tools.
strument --yes bash,steps -m 'Run the tests and fix what fails.'  # Also run shell commands unattended.
```

The process exits with a nonzero status if the request produces no answer: for example, because authentication fails, the endpoint remains unreachable after retries, the model returns an empty reply, or the request was too large and sending it anyway was declined.
A nonempty answer, even if truncated, exits with status 0.
Without a terminal on standard input, every prompt is declined unless `--yes` approves it, and Strument names the `--yes` value that would.

The option `--yes <name>` approves the named prompt automatically: `bash`, `webfetch`, `websearch`, `steps`, `context`, `add-output`, or `all`.
It can be repeated and accepts comma-separated lists, so `--yes bash --yes webfetch,websearch` and `--yes bash,webfetch,websearch` mean the same thing.
An unknown name is a startup error.

`--yes bash` lets the model run shell commands unattended.
Combined with `-m`, it gives the model up to `max_steps` unattended steps (25 by default), including arbitrary shell commands.
`--yes steps` removes that bound, since the step count resets each time the prompt is answered.
Strument is not designed for long-running autonomous use.
These options are meant for a terminal you are watching, not for CI or cron, where prompt injection could cause unintended shell commands to run.

### Sessions

A project can hold several sessions, each with its own name, conversation, pinned files, and undo history.
`--session <name>` (`-s`) says which one to work in, creating it the first time, and the name is remembered as the one a bare `strument` picks up next.
The cost ledger is one file per project and names the session on every row, so it answers both "what has this project cost me" and "was that session worth it".

`--session` selects a session; it does not resume its conversation.
`strument --continue` (`-c`) does: it rebuilds the conversation from the [session record](#session-records), so a session survives the process that had it, whether that was a crash, a closed laptop, or a power loss.
`strument --session review -c` picks up the `review` session's conversation, and `strument --session review` starts fresh in it.
If the restored conversation is already too big to send, Strument compacts it first, rather than letting the first turn fail, and it says how many messages it restored.
If the conversation was made by a different model than the one now running, Strument adds a line saying so, since the next model would otherwise read an earlier assistant turn as its own.
Without `--continue`, a session starts with an empty conversation.

Inside Strument, `/session` lists the sessions and switches between them without restarting; switching restores the conversation of the session you move to.
`/session fork <name>` starts a new session that carries this one's notes forward, with the parent recorded so the notes say where they came from.
`/model` does not fork: many conversations on one strong model is the common case, so forking belongs to sessions.

From the shell, `strument session list` shows the sessions with their turn counts and sizes, `strument session rename` renames one, and `strument session delete` deletes one after saying what that removes.
Deleting a session keeps the stored tool output, which is shared between sessions.
`strument history --session <name>` reads another session's record without switching to it, for `path`, `edit`, and `markdown`.

### Session records

Strument records each session as a [JSON Lines](https://jsonlines.org/) log under its project's state directory, one file per run.
`strument history path` prints the newest one, and `strument history markdown` renders the whole session as a Markdown transcript (`-t <n>` limits it to the last *n* turns).
`-b <n>` (`--back`) picks an earlier run for `path`, `edit` and `markdown`: `0` is the latest, `1` the one before, and so on; `-1` means the same as `1`, written `-b-1` or `--back=-1`.
Runs that recorded nothing, such as starting Strument and quitting, are not counted, and with `-b` `markdown` renders that one run.
Each record has a `type` field: a `session` header at the start, then `message` and `reasoning` records for every message the model sent or received (including tool calls), and a `turn` record at the end of each turn.
A `turn` record carries the outcome, the number of steps, token counts, cost, throughput in tokens per second, the files the turn changed, the untracked files its commands created (`created_files`), the one-line summaries of the work that Strument printed, the mode (`edit_format`) and the tools the model was offered (`offered_tools`), and the prompt and answer as a reader sees them.
`tokens_per_second` is absent when there is no rate to report: nothing received, or too little elapsed time to divide by.
`outcome` is `Crashed` for a turn that ended in a panic, and its answer is the fragment the turn had produced.

A `side_call` record covers each request Strument makes for itself: a commit message, session notes, or a compaction summary.
These requests are separate from the conversation, so they appear nowhere else in the log; without these records, a failed one showed only as its consequence, such as a commit reading `(no commit message provided)`.
The record names the `call` and the `model`, and carries `seconds`, `attempts`, an `outcome` of `ok`, `empty`, `error`, or `deadline`, and the `error` text when there is one.

A `request` record covers each request to the model a turn or an aside makes, retries and continuations included; it follows the assistant message it produced.
The turn record has only the sums, and the per-request split is what shows where a turn's cost went: the fixed prompt, the growing history, reasoning or the answer.
It carries the `call` (`turn` or `aside`), the `step` the turn had completed when the request went out, an `outcome` (`done`, `continuation`, `failed`, `interrupted`, and others), the provider's `finish_reason`, `seconds`, and the `error` for a failed request.
When the provider reported usage, it adds `sent` and `received`, their `cache_read`, `cache_write` and `reasoning` parts, the `cost`, and the `provider` that actually served it where a router reports one (OpenRouter does).
A request that failed before usage arrived has no counts rather than zeroes.
`side_call` records carry the same usage fields.

```sh
jq -c 'select(.type=="side_call" and .outcome!="ok")' "$(strument history path)"
jq -s 'map(select(.type=="request")) | group_by(.provider) | map({provider: .[0].provider, requests: length, reasoning: (map(.reasoning // 0) | add)})' "$(strument history path)"
jq -r 'select(.type=="message" and .role=="assistant") | .text' "$(strument history path)"
```

Tool output of a kilobyte or more (a result, or a call's arguments) is stored beside the record rather than in it, in a `blobs/` directory under the project's state directory, named by the SHA-256 of its contents.
The record then carries `blob` (that name), `bytes`, and `summary` (the output's first line) in place of `text` or `arguments`.
The conversation itself — the model's answers and what you type — always stays in the record, whatever its length.
This lets history be pruned without being forgotten: deleting the stored output leaves the timeline, the hash, and one line saying what was there.
Identical output is stored once, so a file read in five turns is one file on disk, and removing something that should never have been recorded is one deletion rather than five.
An answer you typed to `ask_user_question` is always kept in the record; it is your own words, not tool output.

```sh
# What has this project stored, largest first?
jq -r 'select(.blob) | [.bytes, .summary] | @tsv' "$(strument history path)" | sort -rn
```

`strument history strip` deletes stored tool output that no recent record refers to, and keeps every record.
Without `--older-than`, it removes output not referenced in the last 90 days, so a bare invocation removes what is plainly old rather than everything; pass an age such as `30d`, `6w`, or `720h` to choose.
It says what it will remove and asks first.
Stored output is shared across the project's sessions, so `strip` takes no session.

Each removed output keeps its hash, size, and first line in the record, so the conversation still reads and replays.
A restored conversation shows `[strument] This result is no longer stored. It was 13710 bytes. It began: big.txt (200 lines)` where the output was, and a call whose arguments were removed is labeled the same way.
Output is removed only when no recent record anywhere in the project refers to it, and removing something that should never have been recorded removes every copy of it at once.
For a single small item that was kept inline, `strument history edit` opens the record in your editor.

Recording does not change the terminal output.
The log lives outside your project on purpose: inside the tree it would be part of the workspace, so `grep` and `glob` would match it and the model could read its own transcript.
(In a 300-session trial, a search hit the log in 46 of them.)
`--no-history` records nothing at all.

### Usage reports

`strument usage [<provider>]` reports token usage and cost per provider, across every project: `usage all` covers every provider, and with no argument it reports the default model's provider.
`/usage [<provider>]` prints the same report inside Strument, defaulting to the current model's provider.
It shows rolling windows (the last 24 hours, 7 days, and 30 days), which will not match a provider's calendar-month invoice; see [`doc/config.md`](doc/config.md#strument-usage).

### Config and history files

`strument config models` prints the keys of `models`, one per line and sorted, so scripts can rely on the order, and `strument config default` prints the default model alias.
Both read the merged user and trusted project config for the current project, so the answer matches what a session would use.

`strument config path` prints where a config file is, whether or not it exists yet, and `strument config edit` opens it.
They take `--user` (the default) or `--project`; without an existing project config, `--project` picks `.strument/config.star` in a project that already has a `.strument/` directory and `.strument.star` otherwise.
Editing a project config untrusts it, so `config --project edit` says when to run `strument trust` again.
`strument history path` and `strument history edit` do the same for the current session's record.

The `edit` commands open the file with `$VISUAL`, then `$EDITOR`, then a platform default: `vi` on Unix, and on Windows the first of `edit` (Microsoft Edit) and `notepad` that is installed.
The variable holds a command rather than a program name, so `EDITOR="code --wait"` works, and a path with spaces can be quoted.
Windows has no editor that every installation includes, so set `EDITOR` if you reach a Windows machine over SSH and it has no `edit`: `notepad` would open a window you cannot see.

### If you rename a project directory

Strument keeps a project's transcript, input history, cost ledger, resume state and undo stack outside your tree, under `$XDG_STATE_HOME/strument/projects/`, keyed by the project's path.
Renaming the project directory therefore gives it a new state directory.

When Strument recognizes a renamed project, it shows a notice at startup:

```
strument: this project also has 47 turns recorded under ~/src/proj, which no longer exists.
  Merge saved state: strument project adopt ~/src/proj
  Dismiss this notice: strument project ignore ~/src/proj
```

`strument project adopt` previews the changes and asks for confirmation.
When confirmed, it combines the transcript, input history, and cost ledger in time order.
For `resume.json` and `undo.json`, it keeps the newer file from each pair.

You can run `project adopt` even after you have started sessions at the new path.
The old state directory is retained as `<name>.adopted-<timestamp>`.

`strument project list` lists projects whose directory has moved or been deleted, and the rest with `--all` or when none has moved.
Each entry includes the turn count, size, and state directory.

Use this list when automatic detection cannot identify the old project.
Detection uses the repository's first commit; projects without Git, histories formed by merging unrelated repositories, and multiple moved clones may not produce a notice.

### Shell completions

The `shell` subcommand prints a completion script for Bash or fish.
Load the generated script in your current shell:

```sh
# Bash
source <(strument shell bash)

# fish
strument shell fish | source
```

The `-M`/`--model` option completes model aliases from the effective config by running `strument config models`.
Subcommands, their flags, and enumerable option values (`--yes`, `--mode`, `--consult-scope`) complete too, and paths complete where a command takes one.
To load completions automatically, add the command to your shell configuration.


## Configuration

Strument is configured in Starlark, a small sandboxed dialect of Python.
A config file is a short program that builds model objects and assigns values to the configuration variables.
[`doc/config.md`](doc/config.md) is the reference for the settings and every built-in function specific to Strument.

A more complete configuration:

```python
openrouter = provider("openrouter", api_key=env("OPENROUTER_API_KEY"))
local_llm = provider(
    "openai",
    name="local",
    base_url="http://localhost:8000/v1",
)


def flex(m):
    return m.with_extra_params(service_tier="flex")


models = {
    "deepseek-flash": model(
        openrouter,
        "deepseek/deepseek-v4.1-flash",
        display_name="DeepSeek V4.1 Flash",
        context=1048576,
        max_output=384000,
        input_cost=0.15,
        output_cost=0.6,
        cache=True,  # OpenRouter reports prompt caching for this model.
        # reasoning="high",  # Uncomment and set the effort: "low", "medium", or "high".
    ),
    "gpt-luna": flex(
        model(
            openrouter,
            "openai/gpt-5.6-luna",
            display_name="GPT-5.6 Luna",
            context=1050000,
            max_output=128000,
            cache=True,
            reasoning="high",
        ),
    ),
    "mimo": model(
        openrouter,
        "xiaomi/mimo-v2.5",
        display_name="MiMo-V2.5",
        context=1050000,
        max_output=131072,
        cache=True,
    ),
    "sonnet": model(
        openrouter,
        "anthropic/claude-sonnet-5",
        display_name="Claude Sonnet 5",
        context=1000000,
        max_output=128000,
        input_cost=2,
        output_cost=10,
        cache=True,  # Cache the prompt prefix (Anthropic honors this).
        reasoning="medium",
        side_model="mimo",  # A cheaper model for commit messages and summaries.
    ),
    "qwen": model(
        local_llm,
        "qwen/qwen3.6-27b",
        display_name="Qwen3.6 27B",
        reasoning="high",
        reasoning_tag="think",  # This model emits reasoning in inline tags.
    ),
}

models["ds"] = models["deepseek-flash"]  # One model, two aliases.

default = "mimo"
```

`cache` (off by default) attaches cache-control breakpoints with a one-hour TTL to stable prompt sections.
Anthropic models reached through OpenRouter explicitly honor them.
Other providers may ignore them or implement their own prompt-caching behavior.
When a turn used the cache, the usage line breaks down the figure in parentheses: `12.4k sent (4.2k cache write, 3.2k cache hit)`.
Cache-write and cache-hit tokens are included in the sent total.

Writing `context`, `max_output`, and the costs by hand for every model is tedious.
Instead, `strument model-config z-ai/glm-5.3` fetches them from the provider's catalog and prints a `model` block you can copy into your configuration.
It works before you have a config.
Settings that are your choice (`reasoning`, `reasoning_tag`, `side_model`) appear as commented-out placeholders.
The catalog is fetched on demand and cached.

Some settings live at the top level rather than on a model:

- `check` names the commands that check your project.
- `check_auto` lists which checks should run automatically at the end of an editing turn.
- `reasoning_display` says how much of the model's thinking to show.

```python
check = {
    "lint": ["golangci-lint", "run"],
    "test": ["go", "test", "./..."],
}
check_auto = ["lint", "test"]

reasoning_display = 10  # "full" (the default), a line count, or "off".
```

Checks run in the order in which they are listed in `check` or `check_auto`, depending on which setting is being used.
They stop at the first failure, so put the fast checks first.

A shell command that exactly matches a configured check runs without a permission prompt.
A modified command, such as one with an extra flag, still requires permission.

`check = project_checks()` fills the dictionary with commands detected from your project's marker files for Go, Rust, Python, Node, Deno, `make`/`task`/`just`, Java, .NET, PHP, Ruby, Elixir, Crystal, and Haskell.
Check detection is opt-in and includes only targets the project defines.
These are your project's own commands: `npm test` runs whatever your `package.json` says.

Hiding reasoning is not the same as disabling it.
The reasoning tokens are still generated, logged, and billed.
Set `reasoning="off"` on a model that supports this to disable it.

On a network that cannot reach a provider directly, a `proxy` on the `provider()` call routes requests to that provider through [SOCKS5](https://en.wikipedia.org/wiki/SOCKS5).
A top-level `proxy` is applied to all providers and every outbound HTTPS connection Strument makes.
`proxy="direct"` disables the top-level `proxy` for that provider.
`search()` calls work the same way.

A project-local config can override any of these settings, once you have run `strument trust` in the directory.
`strument trust` shows what the config grants and which skills it found, then asks; `--yes` skips the question for scripts, and without a terminal it refuses rather than trusting silently.
The same command trusts the project's skills.
Project skills are not loaded until you trust them.
See [`doc/config.md`](doc/config.md) for details.


## Security and the sandbox

On Linux, Strument confines itself with [Landlock](https://landlock.io/) before the session starts.
Every process it spawns inherits the Landlock sandbox, as does the `bash` tool.
As a result, your checks and every child process they start can write only to your project, a temporary directory, the session's state directory, and the machine's toolchain caches.
`/sandbox` lists the effective paths.
`sandbox_write` in the config adds writable paths; `sandbox = ""` turns the sandbox off, which is the default on non-Linux platforms.

The sandbox protects **integrity, not confidentiality**.
While writes are confined, reads are not.
A mistaken or injected command cannot edit your dotfiles or your other repositories, but it can read them.
The sandbox is intended to limit damage from mistakes and prompt injection during supervised use, not to contain a deliberately malicious agent over a long session.
[`doc/security.md`](doc/security.md) describes the restrictions, exceptions, and remaining risks.


## Caveats and limitations

Strument is pre-1.0 and its behavior is not stable.
Expect settings to change and read the commit log before upgrading.

Known limits:

- Strument needs a model that calls functions well.
  The model uses tool calls to inspect and edit the project, so reliable tool-calling is required.
  Aider's text-edit formats that existed for such models (`SEARCH`/`REPLACE`, fenced, whole-file) have been removed.
- Strument is developed on Linux.
  It is tested on macOS and Windows in CI.
- No MCP, subagents, aider's architect mode, voice, or GUI.
- No syntax highlighting.


## Building

```sh
go build ./cmd/strument     # A full build with every bundled tree-sitter grammar.
task build:strument:subset  # Release variant: only the grammars Strument uses.
task release                # Cross-compile the subset build for every platform.
```

The subset build compiles in just the 35 grammars the parse layer supports,
via gotreesitter's `grammar_subset` build tags.
The tag list lives in [`script/grammar-tags.txt`](script/grammar-tags.txt).
A test keeps it in sync with the supported languages.

Strument builds and tests offline, with no API keys or extra setup.
To read aider's source alongside it, run `task setup:reference`, which clones aider at commit `5dc9490`
into a gitignored `reference/` directory.
Nothing in the build needs it.


## Credits and license

Strument is derived from [aider](https://github.com/Aider-AI/aider) by Paul Gauthier and the aider contributors,
licensed under the [Apache License 2.0](LICENSE), and carries the same license.

Four components are forked and vendored; three carry a `NOTICE` recording the changes:

- The streaming Markdown renderer (`internal/render/`) is ported from
  [streaming-markdown](https://github.com/thetarnav/streaming-markdown) by Damian Tarnawski (MIT).
- The `.gitignore` pattern matcher (`internal/gitignore/`) comes from
  [go-git](https://github.com/go-git/go-git) at `v6.0.0-alpha.5` (Apache 2.0).
- The terminal line editor (`internal/readline/`) is a fork of
  [ergochat/readline](https://github.com/ergochat/readline) v0.1.3 (MIT).
  Its redraw algorithm is a non-destructive single-write repaint after
  [bestline](https://github.com/jart/bestline) by jart (2-clause BSD):
  cells are overwritten in place and only the leftovers erased.
