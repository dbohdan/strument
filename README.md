# Strument

<img alt="Two crossed hand tools: a network cable crimping tool and a screwdriver/voltage tester with a transparent handle. The screwdriver handle glows on its own." src="doc/logo/1024x1024.png" width=128>

Strument is an AI pair-programming tool for the terminal.
It works with a human in the loop and is designed for the developer who wants to steer technical and UX decisions.
Strument operates in turns started by instructions, each of which ends in a commit or snapshot (in-memory undo).

Strument began as an accurate ground-up reimplementation of [aider](https://github.com/Aider-AI/aider) but has since diverged.
See [`doc/README.md`](doc/README.md) for the developer overview.


## Features

- One binary, no Python dependency like aider.
  Pure Go with no cgo, even for [tree-sitter](https://github.com/odvcencio/gotreesitter).
- [Starlark](https://starlark-lang.org/) configuration.
  A single `config.star` replaces YAML, `.env` files, and a JSON model database.
  Project-local `.strument.star` files are supported.
  They stay inert until authorized with `strument trust` in the directory.
  (This is the [direnv](https://direnv.net/) model of trust based on recorded content hashes.)
- [Tool calls](https://towardsdatascience.com/tool-calling-explained-how-ai-agents-decide-what-to-do-next/).
  `bash` runs a command using the embedded [mvdan/sh](https://github.com/mvdan/sh) shell, a cross-platform reimplementation of Bash.
- Every turn is undoable, with or without Git.
  Strument records each file before the first time it writes to it.
  `/undo` can restore a whole turn even in a directory that is not a repository,
  like a live configuration directory or a checkout under another SCM.
  In a Git repository, a turn is one commit.
  The command `/squash [n]` merges commits.
- Configured checks.
  The `verify` config setting is a dictionary of named verification commands, like tests, a linter, and a build.
  The model can run them by name.
  `project_checks()` detects standard checks for your project type.
  `verify_auto` runs a list of `verify` commands at the end of any turn that changed a file.
- URL scraping.
  URLs you mention, or `/web <url>`, are fetched and converted to Markdown.
  This can use either a built-in HTTPS client or an external browser command (necessary for pages that rely on JavaScript).

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

models = {"mimo": model(openrouter, "xiaomi/mimo-v2.5", context=1050000)}
default = "mimo"
```

Export the key and start Strument in your project:

```sh
export OPENROUTER_API_KEY=sk-or-...
cd ~/src/myproject
strument
```

`context` is in the minimal config because two things quietly stop working without it.
Strument will not warn you before a request overruns the window, and it will not summarize the settled chat history to keep it in budget, so a long session grows until the provider refuses the request.
Neither is announced: there is no limit to enforce when you have not said what it is.

`max_output` is worth adding next.
Costs are optional, because OpenRouter reports the cost of each request in-band; a plain OpenAI-compatible endpoint may not, and then `input_cost` and `output_cost` are what the turn line is estimated from.
The command `strument model-config <slug>` fetches all of these from OpenRouter's catalog and prints a pastable `model` block.
It works before you have a config, which is when you need it.
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
When the model cannot proceed without your decision, it asks a structured multiple-choice question (`ask_user_question`) instead of guessing; you pick a numbered option or type your own answer, and `--yes` never answers it for you.

Slash commands perform actions initiated by you rather than the model:

| | |
| --- | --- |
| `/add <file>`, `/drop`, `/ls` | Pin the files you already know need changing. Strument names them for the model, which reads them itself; it finds everything else on its own. |
| `/ask <question>` | Ask about the project without giving the model editing tools. `/code` switches back. |
| `/notes`, `/notes generate`, `/notes drop` | Show the session notes, regenerate them from the transcript, or discard them. Notes are generated on demand (`--continue` at startup, or `/notes generate` mid-session) and live in memory for the session — they are never persisted to disk. See [`doc/sessions.md`](doc/sessions.md). |
| `/read-only <file>` | Pin a file the model can read but never edit. This is a way to show it something outside the project, like a spec or a sibling repository's header. Search tools only see the project itself. |
| `/undo` | Put the last turn back. Restores the files and removes the commit if there was one. |
| `/squash [n]` | Fold the last n turns' commits into one. |
| `/diff`, `/tokens` | Show what changed and how full the context window is. |
| `/context [n]` | Show the folded chat history as the model sees it: the compaction summaries in order, then the live tail. `n` caps the number of summaries shown. |
| `/symbol <name> [reference]` | Find where a name is defined, or used, from the language parser rather than from text. |
| `/submit <file>` | Send a file's contents as your message, as if you had typed them. Outside-project paths are allowed. Files over 100 KiB are refused rather than truncated. |
| `/run <cmd>`, `/web <url>` | Run a command or fetch a page and offer the output to the model. `/run` keeps your full environment; model-run commands see an [allowlist](doc/config.md#env_allow). |
| `/env`, `/env add <NAME>...`, `/env drop <NAME>...`, `/env reset` | Show or change, for this session, which environment variables model-run commands receive. Tab completes variable names. Persistent changes belong in `env_allow`. |
| `/model [alias]`, `/reload` | Switch models mid-session; reload `config.star` without restarting. |

`/help` lists all commands.

For scripts and one-offs, `-m` runs a single turn and exits:

```sh
strument -m 'Add a --version flag to cmd/pollctl.'
strument --dry-run -m 'Fix the race in internal/poll.'  # Report the edits, write nothing.
strument --yes -m 'Update the changelog for v0.3.0.'  # Answer confirmations; still never runs a shell command.
```

`--yes-shell` is the flag that lets the model run shell commands unattended.
Combined with `-m`, it gives a model up to 25 unattended steps of arbitrary shell in your project.
That is the point of it, and it is meant for a terminal you are watching rather than for CI or cron, where a prompt-injected message becomes remote code execution.
`--no-git` turns off the git integration inside a repository.
Outside one it is already off, and `/undo` works either way.

`--jsonl FILE` records the session as JSONL alongside the normal output, one JSON object per line: a `session` header, then a `message` per conversation turn — with roles, tool calls, their arguments verbatim, and their results — a `reasoning` record wherever the model thought, and a `turn` record with the outcome and the token and cost accounting.

```sh
strument --jsonl run.jsonl -m 'Which functions call settleEdits?'
jq -r 'select(.type=="message" and .role=="assistant") | .text' run.jsonl
```

It is a second sink rather than a mode, so the terminal output is unchanged.
It exists because scoring a session by parsing rendered terminal text is how most of this project's measurement bugs happened: tool results never appear there at all, and an escape sequence or a reasoning delimiter in the wrong place silently moves the boundary a script is looking for.

### Shell completions

The `shell` subcommand prints a completion script for Bash, fish, or Zsh.
Load the generated script in your current shell:

```sh
# Bash
source <(strument shell bash)

# fish
strument shell fish | source

# Zsh
source <(strument shell zsh)
```

To load completions automatically, save the output in the location used by your shell's completion setup and source it from there. For example, Zsh can load a saved script with `source ~/.zsh/completions/_strument` after adding that directory to `fpath`.


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
Anthropic models reached through OpenRouter honor them explicitly.
Other providers may ignore them or provide their own prompt-caching behavior.
When a turn used the cache, the usage line breaks the figure down in parentheses — `12.4k sent (4.2k cache write, 3.2k cache hit)`.
Those are parts of what was sent, not extra tokens beside it.

Writing `context`, `max_output`, and the costs by hand for every model is tedious.
Instead, `strument model-config anthropic/claude-haiku-4.5` fetches them from the provider's catalog and prints a copy-pastable `models` dictionary.
It leaves the judgment calls (`reasoning`, `reasoning_tag`, `side_model`) as commented-out placeholders.
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

Naming a check also quiets the shell prompt for it.
A `bash` command that is one of your checks *verbatim* runs without asking, because you already approved it by writing it here.
Anything that is not an exact match — an added flag, a `&&`, a redirection — asks as usual.

`verify = project_checks()` fills the dictionary in from your project's marker files, for Go, Rust, Python, Node, Deno, `make`/`task`/`just`, Java, .NET, PHP, Ruby, Elixir, Crystal, and Haskell.
It is opt-in and never runs a target your project doesn't define.
Note that these are your project's own commands, not commands that are inherently safe: `npm test` runs whatever your `package.json` says.
Hiding reasoning is not the same as disabling it.
The reasoning tokens are still generated, logged, and billed.
Set `reasoning="off"` on the model to disable it.

On a network that can't reach a provider directly, a `proxy` on the `provider()` call routes requests to that provider through [SOCKS5](https://en.wikipedia.org/wiki/SOCKS5).
A top-level `proxy` is applied to all providers and every outbound HTTPS action Strument takes.
A project-local `.strument.star` can extend or override any of this, once you have run `strument trust` in the directory.
See [`doc/config.md`](doc/config.md) for details.


## Security and the sandbox

On Linux, Strument confines itself with [Landlock](https://landlock.io/) before the session starts, and everything it spawns inherits that: the `bash` tool, your checks, and every child of theirs can write only to your project, a temporary directory, the session's state directory, and this machine's toolchain caches.
`/sandbox` lists the effective paths; `sandbox_write` adds to them; `sandbox = ""` turns it off, which is the default on other platforms.

What that buys is **integrity, not confidentiality**.
Writes are confined, reads are not confined at all, so a mistaken or injected command cannot touch your dotfiles or your other repositories — and it can read every one of them.
The threat model is mistakes and prompt injection with you watching, not a misaligned agent working over hundreds of turns.
[`doc/security.md`](doc/security.md) says exactly what is and is not confined, and where the policy is deliberately loose.


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
