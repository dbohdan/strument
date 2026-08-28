# Strument

<img alt="Two crossed hand tools: a network cable crimping tool and a screwdriver/voltage tester with a transparent handle. The screwdriver handle glows on its own." src="doc/logo/1024x1024.png" width=128>

Strument is an AI pair-programming tool for the terminal.
It is designed for a human in the loop, the kind of developer who wants to see and steer technical and UX decisions.
Work with Strument is divided into turns, each beginning with the human's instructions for the model and ending in a commit (or a snapshot for undo, outside Git).

Strument started as an accurate ground-up reimplementation of [aider](https://github.com/Aider-AI/aider) but has since diverged.
See [`doc/`](doc/README.md) for the developer overview.


## Features

- A single binary; no Python runtime like aider.
  Pure Go with no cgo, even for [tree-sitter](https://github.com/odvcencio/gotreesitter).
- [Starlark](https://starlark-lang.org/) configuration.
  One `config.star` file replaces YAML, `.env` files, and a JSON model database.
  Project-local `.strument.star` files are supported.
  They stay inert until authorized with `strument trust` in the directory.
  (This is the [direnv](https://direnv.net/) model of trust based on recorded content hashes.)
- [Tool calls](https://datacream.substack.com/p/tool-calling-explained-how-ai-agents).
  `bash` runs a command using the embedded [mvdan/sh](https://github.com/mvdan/sh) shell, a cross-platform reimplementation of Bash.
- Every turn is undoable, with or without Git.
  Strument records each file before the first time it writes to it.
  `/undo` can restore a whole turn even in a directory that is not a repository,
  like a live configuration directory or a checkout under another SCM.
  In a Git repository, a turn is one commit.
  The command `/squash [n]` merges commits.
- Checks.
  The `check` config setting is a dictionary of named verification commands, like tests, a linter, and a build.
  The model can run them by name without the harness asking permission.
  `project_checks()` detects standard checks for your project type.
  `check_auto` lists which of the `check` commands Strument runs at the end of any turn that changed a file.
- URL scraping.
  URLs you mention, or `/web <url>`, are fetched and converted to Markdown.
  This can use either a built-in HTTPS client or an external browser command (necessary for pages that rely on JavaScript).
  The model fetches with the `webfetch` tool, which asks you before an unfamiliar host; a URL fragment fetches just that section, and an oversized page returns its outline instead.
- Web search, if you want it.
  Configure [`websearch`](doc/config.md#websearch) and the model gets a `websearch` tool.
  Either your own [SearXNG](https://docs.searxng.org/) instance — your engines, no API key, nobody else in the loop — or the hosted [AnySearch](https://anysearch.com/), which needs nothing set up and works with or without a key.
- You can [interrupt and steer](#interrupting-and-steering) a turn.

The terminal interface has stayed deliberately close to aider's with a similar green/blue palette (with `--dark-mode` and `--light-mode`).
Strument diverges where its programming loop is different.
Reasoning is delimited with `‹thinking›` and `‹/›` because there are multiple reasoning blocks per turn and most reasoning is one line.
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
test $ go test ./...
test passed
Commit 6c1e0a4 refactor(poll): rename defaultTimeout to pollInterval

Tokens: 12.4k sent, 1.8k received. Cost: $0.03 turn, $0.03 session. 4 steps, 2 files changed.
```


## Getting started

Go 1.26 or later is required, but not a C toolchain:

```sh
go install dbohdan.com/strument/cmd/strument@latest
```

Strument needs a configuration file before it will start in chat mode.
There is no model database, and nothing is assumed about which models you have.
The following is the minimal config that works.
Put it in `~/.config/strument/config.star`:

```python
openrouter = provider("openrouter", api_key=env("OPENROUTER_API_KEY"))

models = {"mimo": model(openrouter, "xiaomi/mimo-v2.5", context=1050000)}
default = "mimo"
```

Export the key and start Strument in your project:

```sh
export OPENROUTER_API_KEY=sk-or-...
cd ~/src/myproject
strument
```

`context` is in the minimal config because Strument needs it to warn you before a request overruns the context window and to summarize settled chat history.
A long session without `context` grows until the provider refuses the request.

The cost fields are optional: OpenRouter reports the cost of each request in-band, so they can be omitted.
A plain OpenAI-compatible endpoint may not report costs.
In that case, `input_cost` and `output_cost` are what the turn's cost estimate comes from.
The command `strument model-config <slug>` fetches all of this information from OpenRouter's catalog and prints a pastable `model` block.
It works before you have a config.
See [Configuration](#configuration).

Strument counts your money by the turn.
A turn can run up to twenty-five steps by default (or `max_steps`).
Start on a small request against a cheap model and watch the cost line before you turn it loose on something large.


## Using Strument

Type what you want changed.
The model will work until it finishes or runs out of steps.
At twenty-five steps Strument prints the number of edits and the cost so far and asks whether to continue.

Each tool call reports one line in the log.
Shell commands ask for permission first, which you can grant for that command or for all commands in a turn.
Reading, searching, and editing do not ask for permission.

When the model is streaming or running a tool, press `Ctrl-C` once to stop the current send.
Strument keeps the conversation and any completed work, then asks whether to continue, stop, or enter a correction.
Press `Ctrl-C` twice within two seconds to exit Strument.
In script mode (`-m`), an interrupt stops the turn without asking a follow-up question.

### Interrupting and steering

You can stop a long response and redirect the model without starting over:

```none
‹thinking› I’ll inspect the authentication package first...
Reading internal/auth/auth.go
^C
^C again to exit

‹question› You stopped the model. What now?
1. Continue — Carry on from where it was cut off
2. Stop — End the turn here
Answer (1-2, or your own text): Use the existing token helper instead

‹thinking› I’ll continue from the interrupted response using the existing token helper.
...
```

`Continue` lets the model resume from the partial response with its context preserved.
Typing your own answer sends it as a correction, and `Stop` ends the turn.
Edits made before the interruption remain undoable with `/undo`.

| | |
| --- | --- |
| `/add <file> ...`, `/drop`, `/ls` | Pin the files you already know need to be read or changed. Strument names them for the model, which reads them itself; it finds everything else on its own. |
| `/ask <question>` | Ask about the project without giving the model editing tools. `/ask` on its own activates ask mode, and `/code` switches back. |
| `/check [<name>]` | Run a project check by name, or all checks if no name is given. Checks run in the order they are listed in the config and stop at the first failure. On failure or non-empty output, offers to add the transcript to the chat. A successful check with no output is silent. |
| `/notes`, `/notes generate`, `/notes drop` | Show the session notes, regenerate them from the transcript, or discard them. Notes are generated on demand (`--continue` at startup, or `/notes generate` mid-session) and live in memory for the session. They are never persisted to disk. See [`doc/sessions.md`](doc/sessions.md). |
| `/read-only <file> ...` | Pin a file the model can read but not edit. This is a way to show it something outside the project, like a spec or a sibling repository's header. Search tools only see the project itself. |
| `/undo` | Put the last turn back. Restores the files and removes the commit if there was one. |
| `/squash [<n>]` | Fold the last `n` turns' commits into one. |
| `/diff`, `/tokens` | Show what changed and how full the context window is. |
| `/context [<n>]` | Show the folded chat history as the model sees it: the compaction summaries in order, then the live tail. `n` caps the number of summaries shown. |
| `/symbol <name> [definition \| reference]` | Find where a name is defined or used from the language parser rather than from text. |
| `/submit <file>` | Send a file's contents as your message, as if you had typed them: the trimmed contents are printed first, then sent. Outside-project paths are allowed. Files over 100 KiB are refused. (Large files aren't truncated.) |
| `/run <cmd>`, `/web <url>` | Run a command or fetch a page and offer the output to the model. `/run` keeps your full environment; model-run commands see an [allowlist](doc/config.md#env_allow). Bare `/web` shows which origins `webfetch` may reach unasked, and `/web drop`/`/web reset` take those back. |
| `/env`, `/env add <NAME>...`, `/env drop <NAME>...`, `/env reset` | Show or change, for this session, which environment variables model-run commands receive. Tab completes variable names. Persistent changes belong in `env_allow`. |
| `/model [alias]`, `/reload` | Switch models mid-session; reload `config.star` without restarting ([what a reload applies](doc/config.md#what-reload-applies)). |

`/help` lists all commands.

For scripts and one-offs, `-m` runs a single turn and exits:

```sh
strument -m 'Add a --version flag to cmd/pollctl.'
strument --dry-run -m 'Fix the race in internal/poll.'  # Report the edits, write nothing.
strument --yes -m 'Update the changelog for v0.3.0.'  # Answer confirmations; still never runs a shell command.
```

Two inspection commands answer "what does my effective config say?" without editing the file.
`strument config models` prints the keys of `models`, one per line (sorted, so scripts can rely on the order),
and `strument config default` prints the value of `default`.
Both read the merged user + trusted project config for the current project, so the answer matches what a chat session would use.

`--yes-shell` is the flag that lets the model run shell commands unattended.
Combined with `-m`, it gives a model up to 25 (or `max_steps`) unattended steps of arbitrary shell in your project.
This feature is meant for a terminal you are watching rather than for CI or cron, where a prompt-injected message can become remote code execution.
You want a different harness from Strument for long-term autonomy.
`--no-git` turns off the git integration inside a repository.
Outside one it is already off.
`/undo` works either way.

`--jsonl FILE` records the session as [JSON Lines](https://jsonlines.org/) alongside the normal output.
The file is a stream of records with different `type` fields: a `session` header once at the start, then `message` and `reasoning` records for every message the model sent or received (including the tool calls), and a `turn` record once at the end with the outcome, number of steps, token count, and cost.

```sh
strument --jsonl run.jsonl -m 'Which functions call settleEdits?'
jq -r 'select(.type=="message" and .role=="assistant") | .text' run.jsonl
```

JSONL is a second output sink, not a mode; the terminal output is unchanged.
Write the file **outside the project directory**: a log inside the tree is part of the workspace, so `grep` and `glob` will match it and the model can read its own transcript back.
(In a 300-session trial, a search hit the log in 46 of them.)
The JSONL output exists because parsing rendered terminal text was the main source of this project's measurement bugs in live trials.

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

Here is a fuller example than the starter above.
It demonstrates two providers, a factory for a repeated option, and aliases.

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
        "deepseek/deepseek-v4-flash-0731",
        display_name="DeepSeek V4 Flash 0731",
        context=1048576,
        max_output=384000,
        input_cost=0.14,
        output_cost=0.28,
        cache=True,  # OpenRouter reports prompt caching for this model.
        reasoning="high",
    ),
    "gpt": flex(
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
        reasoning="max",
        reasoning_tag="think",  # This one emits reasoning in inline tags.
    ),
}

models["ds"] = models["deepseek-flash"]  # One model, two aliases.

default = "mimo"
```

`cache` (off by default) attaches cache-control breakpoints with a one-hour TTL to stable prompt sections.
Anthropic models reached through OpenRouter explicitly honor them.
Other providers may ignore them or implement their own prompt-caching behavior.
When a turn used the cache, the usage line breaks down the figure in parentheses: `12.4k sent (4.2k cache write, 3.2k cache hit)`.
Those are parts of what was sent, not extra tokens beside it.

Writing `context`, `max_output`, and the costs by hand for every model is tedious.
Instead, `strument model-config z-ai/glm-5.3` fetches them from the provider's catalog and prints a copy-pastable `models` dictionary.
It leaves the judgment calls (`reasoning`, `reasoning_tag`, `side_model`) as commented-out placeholders.
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

Checks run in the order they are listed and stop at the first failure, so put the fast ones first.

Naming a check also quiets the shell prompt for it.
A `bash` command that is one of your checks _verbatim_ runs without asking, because you already approved it by writing it in the config.
Anything that is not an exact match, like an added flag, asks as usual.

`check = project_checks()` fills the dictionary in from your project's marker files, for Go, Rust, Python, Node, Deno, `make`/`task`/`just`, Java, .NET, PHP, Ruby, Elixir, Crystal, and Haskell.
It is opt-in and never runs a target your project doesn't define.
Note that these are your project's own commands, not commands that are inherently safe: `npm test` runs whatever your `package.json` says.

Hiding reasoning is not the same as disabling it.
The reasoning tokens are still generated, logged, and billed.
Set `reasoning="off"` on a model that supports this to disable it.

On a network that can't reach a provider directly, a `proxy` on the `provider()` call routes requests to that provider through [SOCKS5](https://en.wikipedia.org/wiki/SOCKS5).
A top-level `proxy` is applied to all providers and every outbound HTTPS action Strument takes.

A project-local `.strument.star` can extend or override any of this, once you have run `strument trust` in the directory.
See [`doc/config.md`](doc/config.md) for details.


## Security and the sandbox

On Linux, Strument confines itself with [Landlock](https://landlock.io/) before the session starts.
Every process it spawns inherits this, as does the `bash` tool.
It means your checks, and every child process of theirs, can write only to your project, a temporary directory, the session's state directory, and the machine's toolchain caches.
`/sandbox` lists the effective paths.
`sandbox_write` in the config adds to them; `sandbox = ""` turns the sandbox off, which is the default on non-Linux platforms.

The sandbox buys **integrity, not confidentiality**.
While writes are confined, reads are not confined at all.
A mistaken or injected command cannot edit your dotfiles or your other repositories, and it can read them all.
The threat model is mistakes and prompt injection with you watching, not a misaligned agent working over hundreds of turns.
[`doc/security.md`](doc/security.md) says what exactly is and is not confined, and where the policy is deliberately loose.


## Caveats and limitations

Strument is pre-1.0 and its behavior is not stable.
Expect settings to change and read the commit log before upgrading.

Known limits:

- Strument needs a model that calls functions well.
  Everything is a tool call, so a model that fumbles tool calls cannot drive Strument.
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
To read aider's source alongside it, `task setup:reference` clones aider at commit `5dc9490`
into a gitignored `reference/` directory.
Nothing in the build needs it.


## Credits and license

Strument is derived from [aider](https://github.com/Aider-AI/aider) by Paul Gauthier and the aider contributors,
licensed under the [Apache License 2.0](LICENSE), and carries the same license.

Three components are forked and vendored, each with a `NOTICE` recording the changes:

- The streaming markdown renderer (`internal/render/`) is ported from
  [streaming-markdown](https://github.com/thetarnav/streaming-markdown) by Damian Tarnawski (MIT).
- The `gitignore` pattern matcher (`internal/gitignore/`) comes from
  [go-git](https://github.com/go-git/go-git) at `v6.0.0-alpha.5` (Apache 2.0).
- The terminal line editor (`internal/readline/`) is a fork of
  [ergochat/readline](https://github.com/ergochat/readline) v0.1.3 (MIT).
  Its redraw was reworked to be flicker-free using the single-write technique from
  [bestline](https://github.com/jart/bestline) by Justine Tunney (2-clause BSD).
