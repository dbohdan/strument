# Strument

Strument is an AI pair-programming tool for the terminal.
It is designed for making precise code changes under human oversight.
It implements a reviewable, diff-centric development loop rather than an autonomous one.

Strument began as an accurate ground-up reimplementation of [aider](https://github.com/Aider-AI/aider) [0.86.3.dev](https://github.com/Aider-AI/aider/tree/5dc9490bb35f9729ef2c95d00a19ccd30c26339c) but has since diverged.
See [`doc/README.md`](doc/README.md) for the developer overview.


## Features

- One binary, no Python dependency like aider.
  Pure Go with cgo, even for [tree-sitter](https://github.com/odvcencio/gotreesitter).
- [Starlark](https://starlark-lang.org/) configuration.
  A single `config.star` replaces YAML, `.env` files, and a JSON model database.
  Project-local `.strument.star` files are supported.
  They stay inert until authorized with `strument trust` in the directory.
  This is the [direnv](https://direnv.net/) model of trust based on recorded content hashes.
- Nine [tool calls](https://towardsdatascience.com/tool-calling-explained-how-ai-agents-decide-what-to-do-next/) in three groups.
  Models get access to `read`, `grep`, `glob`, `ls`, and `symbol` for lookup.
  Those tools are freely available and require no use confirmation.
  They never see a file the project ignores.
  `symbol` answers "where is this defined" using the language parser (tree-sitter and `go/parser`) rather than text.
  `edit` and `write` change text.
  Their edits are written immediately after the call.
  `bash` runs a command behind a confirmation using the embedded pure-Go
  [mvdan/sh](https://github.com/mvdan/sh) shell, and `verify` runs a check you pre-configured.
- Every turn is undoable, with or without Git.
  Strument records each file before the first time it writes to it.
  `/undo` can restore a whole turn even in a directory that is not a repository,
  like a live configuration directory or a checkout under another SCM.
  An edit preserves the file's mode and writes through a symlink instead of replacing it.
  A tool-call batch that fails partway is rolled back.
  Where there is a repository, a turn is also one commit described as one piece of work in the message.
  The command `/squash [n]` merges several commits.
- Configured checks.
  The `verify` dictionary names your project's verification commands, like tests, a linter, and a build, that the model can run by name.
  `verify_auto` runs a list of `verify` commands at the end of any turn that changed a file.
  Only failing checks print their output.
- URL scraping.
  URLs you mention, or `/web <url>`, are fetched and converted to Markdown using either a built-in HTTPS client or an external browser command.
  For pages that rely on JavaScript, the `scraper` setting starts an external command to download them.

The terminal interface has stayed deliberately close to aider's with a similar green/blue palette (with `--dark-mode` and `--light-mode`).
Strument diverges where its programming loop is different.
Reasoning is delimited with `‹thinking›` and `‹/›` rather than a banner because there are multiple reasoning blocks per turn and most reasoning is one line.
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

Go 1.26 or later is required, but no C toolchain:

```sh
go install dbohdan.com/strument/cmd/strument@latest
```

Strument needs a configuration file before it will start.
There is no model database, and nothing is assumed about which models you have.
The following is the minimal config that works.
Put it in `~/.config/strument/config.star`:

```python
openrouter = provider("openrouter", api_key=env("OPENROUTER_API_KEY"))

models = {"mimo": model(openrouter, "xiaomi/mimo-v2.5")}
default = "mimo"
```

Export the key and start Strument in your project:

```sh
export OPENROUTER_API_KEY=sk-or-...
cd ~/src/myproject
strument
```

It is worth adding `context`, `max_output` per model.
Strument can warn you before you overrun the context window and report spending for the turn.
You can optionally add cost, although OpenRouter automatically returns it.
The command `strument model-config <slug>` fetches model configuration and prints a pastable `model` config block.
See [Configuration](#configuration).

Strument counts your money by the turn.
A turn can run up to twenty-five steps.
Start on a small request against a cheap model and watch the cost line before you turn it loose on something large.


## Using Strument

Type what you want changed.
The model will work until it finishes or runs out of steps.
At twenty-five steps Strument prints the number of edits and the cost so far and asks whether to continue.

Each tool call reports one line in the log.
Shell commands ask for permission first, which you can give for the command or all commands in a turn.
Reading, searching, and editing do not ask for permission.

Slash commands perform actions initiated by you rather than the model:

| | |
| --- | --- |
| `/add <file>`, `/drop`, `/ls` | Pin a file's contents into the conversation when you already know what needs changing. The model finds everything else on its own. |
| `/ask <question>` | Ask about the project without giving the model editing tools. `/code` switches back. |
| `/read-only <file>` | Pin reference material the model can read but never edit. This is a way to show the model a file outside the project, like a spec or a sibling repository's header. Search tools only see the project itself. |
| `/undo` | Put the last turn back. Restores the files and removes the commit if there was one. |
| `/squash [n]` | Fold the last n turns' commits into one. |
| `/diff`, `/tokens` | Show what changed and how full the context window is. |
| `/symbol <name> [reference]` | Find where a name is defined, or used, from the language parser rather than from text. |
| `/run <cmd>`, `/web <url>` | Run a command or fetch a page and offer the output to the model. |
| `/model [alias]`, `/reload` | Switch models mid-session; reload `config.star` without restarting. |

`/help` lists all commands.

For scripts and one-offs, `-m` runs a single turn and exits:

```sh
strument -m 'Add a --version flag to cmd/pollctl.'
strument --dry-run -m 'Fix the race in internal/poll.'  # Report the edits, write nothing.
strument --yes -m 'Update the changelog for v0.3.0.'  # Answer confirmations; still never runs a shell command.
```

`--yes-shell` is the flag that lets the model run shell commands unattended.
`--no-git` turns off the git integration inside a repository.
Outside one it is already off, and `/undo` works either way.


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
        weak_model="mimo",  # A cheaper model for commit messages and summaries.
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
Anthropic models reached through OpenRouter honor them explicitly.
Other providers may ignore them or provide their own prompt-caching behavior.

Writing `context`, `max_output`, and the costs by hand for every model is tedious.
Instead, `strument model-config anthropic/claude-haiku-4.5` fetches them from the provider's catalog and prints a copy-pastable `models` dictionary.
It leaves the judgment calls (`reasoning`, `reasoning_tag`, `weak_model`) as commented-out placeholders.
The catalog is fetched on demand with caching.

Some settings live at the top level rather than on a model:

- `verify` names the commands that check your project.
- `verify_auto` says which of them Strument should run itself at the end of an editing turn.
- `reasoning_display` says how much of the model's thinking to show:

```python
verify = {
    "lint": ["golangci-lint", "run"],
    "test": ["go", "test", "./..."],
}
verify_auto = ["lint", "test"]

reasoning_display = 10  # "full" (the default), a line count, or "off".
```

Checks run in the order they are listed and stop at the first failure, so put the fast ones first.
Hiding reasoning is not the same as disabling it.
The reasoning tokens are still generated, logged, and billed.
Set `reasoning="off"` on the model to disable it.

On a network that can't reach a provider directly, a `proxy` on the `provider()` call routes requests to that provider through [SOCKS5](https://en.wikipedia.org/wiki/SOCKS5).
A top-level `proxy` is applied to all providers and every outbound HTTPS action Strument takes.
A project-local `.strument.star` can extend or override any of this, once you have run `strument trust` in the directory.
See [`doc/config.md`](doc/config.md) for details.


## Caveats and limitations

Strument is very pre-1.0 and not stable.
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
