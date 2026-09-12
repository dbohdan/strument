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
  Trust is recorded by content hash, following the [direnv](https://direnv.net/) model.
- [Tool calls](https://datacream.substack.com/p/tool-calling-explained-how-ai-agents).
  `bash` runs a command using the embedded [mvdan/sh](https://github.com/mvdan/sh) shell, a cross-platform reimplementation of Bash.
- [Agent Skills](https://agentskills.io/).
  Drop a `SKILL.md` under `~/.local/share/strument/skills/foo/` or the project's `.strument/skills/foo/`, and the model can ask for it by name.
  Project skills also require `strument trust`.
  A skill's `allowed-tools` field does not grant tool permissions.
- A sandboxed `run_code` tool.
  The model can run short Python programs for calculations, formatting, or processing several inputs at once in a restricted Wasm interpreter.
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
- Project checks.
  The `check` config setting is a dictionary of named verification commands, like tests, a linter, and a build.
  The model can run them by name without a permission prompt.
  `project_checks()` detects standard checks for your project type.
  `check_auto` lists which of the `check` commands Strument runs at the end of any turn that changed a file.
- URL scraping.
  URLs that you add to the context with `/web <url>` are fetched and converted to Markdown.
  This can use either a built-in HTTPS client or an external browser command (necessary for pages that rely on JavaScript).
  The model can use the tool `webfetch`, which asks you permission before accessing an unfamiliar origin.
  A URL fragment limits the result to that section.
  If a page exceeds the size limit, the tool returns an outline instead.
- Web search, if you enable it.
  Configure [`websearch`](doc/config.md#websearch) and the model gets a `websearch` tool.
  Use your own [SearXNG](https://docs.searxng.org/) instance, with your choice of engines and no API key, or hosted [AnySearch](https://anysearch.com/), which requires no setup and works with or without a key.
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
Searched for defaultTimeout — 3 matches in 2 files
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

Applied the edit to internal/poll/poll.go
Applied the edit to internal/poll/poll.go
Applied the edit to internal/poll/watch.go
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
Shell commands ask for permission first, which you can grant for that command or for all commands in a turn.
Reading, searching, and editing do not ask for permission.

### Interrupting and steering

While the model is responding or a tool is running, press `Ctrl-C` once to interrupt it.
Strument keeps the conversation and any completed work, then asks whether to continue, stop, or enter a correction.
Press `Ctrl-C` twice within two seconds to exit Strument.
In script mode (`-m`), an interrupt stops the turn without asking a follow-up question.

`SIGUSR1` interrupts the current send the same way a single `Ctrl-C` does.
This interruption doesn't count toward the double-`Ctrl-C` exit shortcut.
It does nothing when sent between turns.
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
| `/ask <question>` | Ask about the project without giving the model editing tools. `/ask` on its own activates ask mode, and `/code` switches back. |
| `/check [<name>]` | Run a project check by name, or all checks if no name is given. Checks run in the order they are listed in the config and stop at the first failure. On failure or non-empty output, Strument offers to add the transcript to the chat. A successful check with no output is silent. |
| `/consult <alias> <question>` | Ask another model without switching the active model, then optionally add its answer to the conversation, identified by the advisor's name. `--consult-scope` sets how much the advisor sees: `none`, `files` (the pinned files, by default), or `chat` (the pinned files and the conversation). The consultation is billed at the advisor's own rates and appears in the cost ledger under its slug. |
| `/notes`, `/notes generate`, `/notes drop` | Show the session notes, regenerate them from the transcript, or discard them. Generate notes with `--continue` at startup or `/notes generate` during a session. Notes remain in memory and are not saved to disk. See [`doc/sessions.md`](doc/sessions.md). |
| `/read-only <file> ...` | Pin a file the model can read but not edit. This is a way to show it something outside the project, like a spec or a sibling repository's header. Search tools only see the project itself. |
| `/undo` | Revert the last turn. Restores files changed through Strument's file tools and removes the commit if there was one. |
| `/squash [<n>]` | Fold the last `n` turns' commits into one. |
| `/diff`, `/tokens` | Show what changed and how full the context window is. |
| `/context [<n>]` | Show the chat history as the model sees it: compaction summaries followed by recent, unsummarized messages. `n` limits the number of summaries shown. |
| `/skill [<name>]` | Show the skills this session found, or add one's instructions to the chat yourself. See [Skills](doc/config.md#skills). |
| `/symbol <name> [definition \| reference]` | Find where a name is defined or used from the language parser rather than from text. |
| `/submit <file>` | Send a file's contents as your message, as if you had typed them: the trimmed contents are printed first, then sent. Paths outside the project are allowed. Files over 100 KiB are refused. (Large files aren't truncated.) |
| `/run <cmd>`, `/web <url>` | Run a command or fetch a page and offer the output to the model. `/run` keeps your full environment; model-run commands see an [allowlist](doc/config.md#env_allow). Bare `/web` shows which origins `webfetch` can access without prompting, and `/web drop`/`/web reset` revoke those permissions. |
| `/env`, `/env add <NAME>...`, `/env drop <NAME>...`, `/env reset` | Show or change, for this session, which environment variables model-run commands receive. Tab completes variable names. Persistent changes belong in `env_allow`. |
| `/model [alias]`, `/reload` | Switch models mid-session; reload `config.star` without restarting ([what a reload applies](doc/config.md#what-reload-applies)). |

`/help` lists all commands.
The model also has a `run_code` tool — a short sandboxed Python program, callable from any mode.
See the [`run_code` tool](doc/config.md#the-run_code-tool).

For scripts and one-offs, `-m` runs a single turn and exits:

```sh
strument -m 'Add a --version flag to cmd/pollctl.'
strument --dry-run -m 'Fix the race in internal/poll.'  # Report the edits, write nothing.
strument --yes steps -m 'Update the changelog for v0.3.0.'  # Do not stop to ask at the step budget; grants no tool.
strument --yes bash,steps -m 'Run the tests and fix what fails.'  # Also run shell commands unattended.
```

The process exits with a nonzero status if the request produces no answer — for example, because authentication fails, the endpoint remains unreachable after retries, or the model returns an empty reply.
A nonempty answer, even if truncated, exits with status 0.

`strument config models` prints the keys of `models`, one per line (sorted, so scripts can rely on the order),
and `strument config default` prints the default model (the value of `default`).
Both commands answer the question "what does my effective config say?".
Both read the merged user + trusted project config for the current project, so the answer matches what a chat session would use.

The option `--yes <name>` removes confirmation from the named prompt: `bash`, `webfetch`, `websearch`, `steps`, `context`, `add-output`, or `all`.
The option can be repeated and accepts comma-separated lists, so `--yes bash --yes webfetch,websearch` and `--yes bash,webfetch,websearch` mean the same thing.
An unknown prompt name causes a startup error.

`--yes bash` lets the model run shell commands unattended.
Combined with `-m`, it gives a model up to `max_steps` unattended model steps, including arbitrary shell commands (25 steps by default).
`--yes steps` removes that bound, since the budget resets each time the prompt is answered.
Strument is not designed for long-running autonomous use.
This feature is meant for a terminal you are watching rather than for CI or cron, where prompt injection could cause unintended shell commands to run.
`--no-git` turns off the git integration inside a repository.
Outside one it is already off.
`/undo` works either way.

`--jsonl <file>` records the session as a [JSON Lines](https://jsonlines.org/) log alongside the normal output.
The log consists of records.
Each has a `type` field: a `session` header once at the start, then `message` and `reasoning` records for every message the model sent or received (including the tool calls), and a `turn` record once at the end with the outcome, number of steps, token count, and cost.

```sh
strument --jsonl run.jsonl -m 'Which functions call settleEdits?'
jq -r 'select(.type=="message" and .role=="assistant") | .text' run.jsonl
```

JSONL logging does not change the terminal output.
Write the file **outside the project directory**: a log inside the tree is part of the workspace, so `grep` and `glob` will match it and the model can read its own transcript back.
(In a 300-session trial, a search hit the log in 46 of them.)
JSONL logging was added for model-assisted debugging.

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

You can run the `project adopt` command even after starting sessions at the new path.
The old state directory is retained as `<name>.adopted-<timestamp>`.

`strument project list` shows all recorded projects, with orphaned entries first.
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
Settings that require your choice (`reasoning`, `reasoning_tag`, `side_model`) appear as commented-out placeholders.
The catalog is fetched on demand with caching.

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
Check detection is opt-in and only includes targets defined by the project.
Note that these are your project's own commands: `npm test` runs whatever your `package.json` says.

Hiding reasoning is not the same as disabling it.
The reasoning tokens are still generated, logged, and billed.
Set `reasoning="off"` on a model that supports this to disable it.

On a network that can't reach a provider directly, a `proxy` on the `provider()` call routes requests to that provider through [SOCKS5](https://en.wikipedia.org/wiki/SOCKS5).
A top-level `proxy` is applied to all providers and every outbound HTTPS connection Strument makes.
`proxy="direct"` disables the top-level `proxy` for that provider.
`search()` calls work the same way.

A project-local config can override any of these settings, once you have run `strument trust` in the directory.
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
- The `run_code` Python sandbox (`internal/monty/`) is a fork of
  [monty-go](https://github.com/fugue-labs/monty-go) at `v0.2.0` (MIT),
  which provides pure-Go wazero bindings for [Pydantic's Monty](https://github.com/pydantic/monty).
  The fork is maintained in-tree, including the Rust shim used to build `monty.wasm`, in `internal/monty/shim/`.
