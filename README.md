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
  One `config.star` file replaces YAML config, `.env` files, and a JSON model database.
  Projects can have their own config that is loaded only when approved with `strument trust`.
- [Tool calls](https://datacream.substack.com/p/tool-calling-explained-how-ai-agents), including `bash`, which runs commands in an embedded cross-platform Bash-compatible shell ([mvdan/sh](https://github.com/mvdan/sh)).
- An optional [decision model](doc/config.md#approve_model) screening shell commands for automatic approval.
  TypeSafe's Jev or a compatible model (like Cloudflare's Clef) can rate each shell command for safety, and Strument will run sufficiently safe commands without asking you.
- [Agent Skills](https://agentskills.io/): a `SKILL.md` in your skills directory or the project's is available to the model by name.
  See [Skills](doc/config.md#skills).
- A sandboxed [`run_code` tool](doc/config.md#the-run_code-tool) for short JavaScript programs, such as calculations or processing many inputs at once.
  Programs can call read-only search tools but nothing else on the host.
- Undo for every edit through Strument's file tools, with and without Git.
  In a repository, each turn is one commit.
- [Project checks](#configuration): named commands such as tests and a linter, which the model can run without a permission prompt and Strument can run after every turn that edits a file.
- Web fetch: `/web <url>` and the model's `webfetch` tool fetch a page as Markdown.
  The tool asks before the model fetches from an unfamiliar origin.
- Optional [web search](doc/config.md#websearch) through your own [SearXNG](https://docs.searxng.org/) instance or a hosted backend ([AnySearch](https://anysearch.com/), [Exa](https://exa.ai/)).
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

Tokens: 12.4k sent, 1.8k received, 30 t/s. Cost: $0.03 turn, $0.03 run. 4 steps, 2 files changed.
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

models = {"mimo": model(openrouter, "xiaomi/mimo-v2.6-flash", context=1050000)}
default = "mimo"
```

Export the key and start Strument in your project:

```sh
cd ~/src/myproject
OPENROUTER_API_KEY=sk-or-... strument
```

Set `context` so Strument can warn you before a request exceeds the model's context window and can summarize older chat history between turns.
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

Type what you want done.
The model works until it finishes or reaches the step limit.
At the limit (25 steps by default) Strument reports the number of edits and the cost so far, then asks whether to continue.

Strument prints a status line for each tool call.
Shell commands ask for permission first, which you can grant for that command or for all commands in the turn; `webfetch` and `websearch` ask too.
Reading, searching, and editing do not ask.

In a Git repository, each turn that changes a file ends in a commit.
`--no-git` turns the Git integration off inside a repository; outside one it is automatically off.
The `/undo` command works either way: Strument records each file before it first writes to it, so a turn's writes can be undone in a directory that is not a repository, such as a live configuration directory or a checkout under another SCM.

Web pages come from a built-in HTTPS client.
For pages that need JavaScript, an external browser command ([`scraper`](doc/config.md#scraper)) can be configured.
A URL fragment limits the result to that section.
The model sees a page over the size limit as an outline.

### Writing longer messages

The prompt is displayed on a single line with any line breaks shown as `↵`.
<kbd>Alt+Enter</kbd> adds a line break.
A multi-line paste is added to the prompt whole, line breaks included.
It is not sent until you press `Enter`.
For anything longer, `/editor` opens your editor (`$VISUAL`, then `$EDITOR`) to write a new prompt.
The keys <kbd>Ctrl+X Ctrl+E</kbd> do the same starting from the current prompt.
What you save in the editor becomes the updated prompt for you to read and send.
`/editor <command>` uses that command instead, for example `/editor code --wait`.

### Interrupting and steering

While the model is responding or a tool is running, press <kbd>Ctrl+C</kbd> once to interrupt it.
Strument keeps the conversation and any completed work, then asks whether to continue, stop, or enter a correction.
Press <kbd>Ctrl+C</kbd> twice within two seconds to exit Strument.
A script can interrupt a run with `SIGUSR1` instead; see [script mode](doc/config.md#script-mode--m).

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

"Continue" lets the model resume from the partial response with the context preserved.
Typing your own answer sends it as a correction, and "Stop" ends the turn.
Edits made before the interruption remain undoable with `/undo`.

Exiting in the middle of a turn — <kbd>Ctrl+C</kbd> twice, closing the terminal, or a `SIGTERM` from `timeout` or a service manager — does not lose the turn.
Strument saves its edits for `/undo` on the way out.
It can commit the files on the next start.
A `SIGKILL`, a crash, or a power loss still ends the run with no `/undo` saved and the edits on disk.

### REPL commands

- `/add <file> ...`, `/drop`, `/ls`&thinsp;&mdash;&thinsp;pin files you want the model to inspect or change. Strument gives the model their names; the model reads them as needed and can find other project files itself.
- `/ask <question>`&thinsp;&mdash;&thinsp;ask about the project without giving the model editing tools. `/ask` on its own switches to ask mode, and `/code` switches back.
- `/yes [add <name> ... | drop <name> ... | reset]`&thinsp;&mdash;&thinsp;show or change which prompts are approved automatically. On its own it lists each approval and where it came from: `--yes`, the config's `auto_approve`, or this run. Dropping one makes Strument ask again mid-run, which is useful when a turn starts going somewhere unexpected.
- `/attach <file> ...`&thinsp;&mdash;&thinsp;attach images (PNG, JPEG, GIF, WebP) to your next message, from anywhere on disk. On its own it lists what is attached; `/attach drop` removes attachments. Unlike pinned files, attachments go with one message only. A model that does not accept images is told an image was there and that it could not see it, rather than the request failing; declare `input_modalities` for one that can.
- `/check [<name>]`&thinsp;&mdash;&thinsp;run a project check by name, or all checks if no name is given. Checks run in the order the config lists them and stop at the first failure. On failure or non-empty output, Strument offers to add the transcript to the chat. A successful check with no output is not offered to the chat.
- `/consult <alias> <question>`, `/consult scope [<name>]`&thinsp;&mdash;&thinsp;ask another model without switching the active model, then optionally add its answer to the conversation, labeled with the advisor's name. `/consult scope` shows or sets how much the advisor sees: `none`, `files` (the pinned files, the default), or `chat` (the pinned files and the conversation); `--consult-scope` sets the starting value. The consultation is billed at the advisor's rates and appears in the cost ledger under its slug.
- `/session`, `/session new|switch|fork|rename|delete <name>`&thinsp;&mdash;&thinsp;list this project's sessions, or create, switch to, fork, rename, or delete one. See [Sessions](#sessions).
- `/notes`, `/notes generate`, `/notes drop`&thinsp;&mdash;&thinsp;show the session notes, regenerate them from the session record, or discard them. Notes stay in memory and are not saved to disk. They carry context from one session to another; to pick up *this* session's conversation, use `--continue`. See [`doc/sessions.md`](doc/sessions.md).
- `/read-only <file> ...`&thinsp;&mdash;&thinsp;pin a file the model can read but not edit, such as a spec or a header from a sibling repository. The model can also ask to read a file outside the project, and you are asked first; pinning skips the question. The search tools see only the project itself.
- `/commits [on | off]`&thinsp;&mdash;&thinsp;show or change whether a turn that edits a file ends in a commit. With no argument, it shows the current setting. `--no-auto-commits` starts a session with commits off, and `auto_commits = False` in the config makes that the default. With commits off, edits are still written to the working tree, and `/undo` and `/diff` still work.
- `/undo`&thinsp;&mdash;&thinsp;revert the last turn. Restores files changed through Strument's file tools and removes the commit if there was one.
- `/rewind [<n>]`&thinsp;&mdash;&thinsp;take the last `n` turns (default 1) out of the conversation, for a turn that went wrong in a way that would steer the next one. Files are not changed, and Strument names any the rewound turns edited; `/undo` reverts edits. The turns stay in the session record, and `--continue` restores the conversation without them. Turns folded into a compaction summary cannot be rewound.
- `/squash [<n>]`&thinsp;&mdash;&thinsp;combine the last `n` turns' commits into one.
- `/usage [<provider> | all]`&thinsp;&mdash;&thinsp;show token usage and cost for the last 24 hours, 7 days, and 30 days. Defaults to the current model's provider. See [Usage reports](#usage-reports).
- `/diff`, `/tokens`&thinsp;&mdash;&thinsp;show what changed and how full the context window is.
- `/context [<n>]`&thinsp;&mdash;&thinsp;show the chat history as the model receives it: compaction summaries followed by recent, unsummarized messages. With `n`, show only the first `n` summaries.
- `/skill [<name>]`&thinsp;&mdash;&thinsp;list the available skills, or add a skill's instructions to the chat yourself. See [Skills](doc/config.md#skills).
- `/symbol <name> [definition | reference]`&thinsp;&mdash;&thinsp;find where a name is defined or used, using the language parser rather than a text search.
- `/editor [<command>]`&thinsp;&mdash;&thinsp;write your message in an editor: `$VISUAL`, then `$EDITOR`, or the command given. What you save comes back to the prompt to read and send. <kbd>Ctrl+X Ctrl+E</kbd> opens it with what you have typed.
- `/submit <file>`&thinsp;&mdash;&thinsp;send a file's contents as your message, as if you had typed them: the trimmed contents are printed first, then sent. Paths outside the project are allowed. Files over 100 KiB are refused rather than truncated.
- `/run <cmd>`, `/web <url>`&thinsp;&mdash;&thinsp;run a command or fetch a page and offer the output to the model. `/run` keeps your full environment; model-run commands receive an [allowlist](doc/config.md#env_allow). `/web` on its own lists the origins `webfetch` can fetch from without asking, and `/web drop` and `/web reset` revoke those approvals.
- `/env`, `/env add <NAME>...`, `/env drop <NAME>...`, `/env reset`&thinsp;&mdash;&thinsp;show or change, for this run, which environment variables model-run commands receive. Tab completes variable names. Persistent changes belong in `env_allow`.
- `/model [alias]`, `/reload`&thinsp;&mdash;&thinsp;switch models mid-session; reload the configuration without restarting ([what a reload applies](doc/config.md#what-reload-applies)).

`/help` lists all commands.
The model also has a `run_code` tool, which runs a short JavaScript program in a sandbox, in either mode.
See the [`run_code` tool](doc/config.md#the-run_code-tool).

### Script mode

`strument -m '<request>'` runs a single turn and exits.
The option `--dry-run` shows the edits without writing them.
With no terminal, every confirmation prompt is declined unless `--yes <name>` approves it;
read [script mode](doc/config.md#script-mode--m) before giving an unattended run `--yes bash`.

### Sessions

A project can hold several _sessions_, each with its own conversation, choice of model, pinned files, and undo history.
Each invocation of Strument is called a _run_.
The option `-s <name>` (`--session`) picks the session, creating it if necessary.
A bare `strument` command returns to the last session used, or `default`.
A session starts with an empty conversation; `-c` (`--continue`) restores its conversation from the session record.

Inside Strument, `/session` lists, switches, forks, renames, and deletes sessions; `/notes` carries context from one session into another.
From the shell, the subcommands `strument session list`, `rename`, and `delete` do the same.
[`doc/history.md`](doc/history.md) and [`doc/sessions.md`](doc/sessions.md) have the details.

### Session records

Strument records every session outside your project directory.
It uses [JSON Lines](https://jsonlines.org/) files for this.
The lines of this file correspond to messages, tool calls, and results, plus a summary row with the cost of each turn.
Long tool output (over 1024 bytes) is stored in separate _blob_ files.

- `strument history list` shows a session's runs
- `strument history markdown` renders them as a transcript
- `strument history path` prints the run file path for commands like `jq`
- `strument history edit` opens the run file in your editor
- `strument history zip <file>` packs a run with its stored tool output for sharing

The runs are numbered.
The `history` subcommands take the option `-b <n>` to pick one run.
Positive numbers pick that run; zero and negative pick the current run minus the number.

Use the option `--no-history` to record nothing.

The record format, stored tool output, `strument history strip`, and what to do if you rename a project directory are documented in [`doc/history.md`](doc/history.md).

### Usage reports

`strument usage [<provider>]` reports token usage and cost per provider, across every project.
With no argument, it reports usage for the default model's provider; `usage all` covers every provider together.
`/usage [<provider>]` prints the same report inside Strument, defaulting to the current model's provider.

The output shows rolling windows: the last 24 hours, 7 days, and 30 days.
Those will not match a provider's calendar-month invoice; see [`doc/config.md`](doc/config.md#strument-usage).

### Shell completions

The `shell` subcommand prints a completion script for Bash or fish.
Load the generated script in your current shell:

```sh
# Bash
source <(strument shell bash)

# fish
strument shell fish | source
```

With the completions loaded, the `-M`/`--model` option completes model aliases from the effective config (it runs `strument config models`), and `-s`/`--session` completes session names.
Subcommands, their flags, and enumerable option values (`--yes`, `--mode`, `--consult-scope`) are completed, too.
Paths are completed where a command takes one.

To load completions automatically, add the command to your shell configuration.


## Configuration

Strument is configured in Starlark, a small sandboxed dialect of Python.
A config file is a short program that builds model objects and assigns values to the configuration variables.
[`doc/config.md`](doc/config.md) is the reference for the settings and every built-in function specific to Strument.
`strument config edit` opens your user config in your editor (`--project` for the project's config); `strument config path` prints where it is.
`--config PATH` uses another user config for one run, `/reload` included; sessions and other state stay where they are, so a tester's config can be swapped in without forking the history.

Here is an example of a more complete configuration:

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
        "xiaomi/mimo-v2.6-flash",
        display_name="MiMo-V2.6-Flash",
        context=1050000,
        max_output=131072,
        cache=True,
        reasoning="low",
    ),
    "sonnet": model(
        openrouter,
        "anthropic/claude-sonnet-5.5",
        display_name="Claude Sonnet 5.5",
        context=1000000,
        max_output=128000,
        input_cost=2,
        output_cost=10,
        input_modalities=["text", "image"],
        cache=True,  # OpenRouter reports prompt caching for this model.
        reasoning="medium",  # The effort: "max", "xhigh", "high", "medium", or "low".
        # reasoning_tag="think",  # Uncomment if the model emits reasoning in inline tags.
        # side_model="...",  # Uncomment to use a different model for summaries and commits.
    ),
    "qwen": model(
        local_llm,
        "qwen/qwen3.8-27b",
        display_name="Qwen3.8 27B",
        reasoning="low",
        reasoning_tag="think",  # This model emits reasoning in inline tags.
    ),
}

models["ds"] = models["deepseek-flash"]  # One model, two aliases.

default = "mimo"
```

`cache` (off by default) attaches cache-control breakpoints with a one-hour TTL to the stable prompt sections and to the end of the conversation, so each step of a turn reuses the previous one's prefix.
Anthropic models reached through OpenRouter explicitly honor them.
Other providers may ignore them and/or implement their own prompt-caching behavior.
When a turn uses the cache, the usage line breaks down the figure in parentheses: `12.4k sent (4.2k cache write, 3.2k cache hit)`.
Cache-write and cache-hit tokens are included in the sent total.

Writing `context`, `max_output`, and the costs by hand for every model is tedious.
Instead, `strument model-config z-ai/glm-5.3` fetches them from the provider's catalog (OpenRouter by default) and prints a `model` block you can copy into your configuration.
While this command works before you have a config, an OpenRouter token is recommended to avoid getting rate-limited or IP-banned from OpenRouter.
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

reasoning_display = 1000  # "full" (the default), a line count, or "off".
```

Checks run in the order in which they are listed in `check` or `check_auto`, depending on which setting is being used.
They stop at the first failure, so put the fast checks first.

A shell command that exactly matches a configured check runs without a permission prompt.
A modified command, such as one with an extra flag, still requires permission.

`check = project_checks()` fills the dictionary with commands detected from your project's marker files for Go, Rust, Python, Node, Deno, `make`/`task`/`just`, Java, .NET, PHP, Ruby, Elixir, Crystal, and Haskell.
Check detection is opt-in and includes only targets the heuristics detect.
These are your project's own commands: `npm test` runs whatever your `package.json` says.

Hiding reasoning is not the same as disabling it.
The reasoning tokens are still generated, logged, and billed.
Set `reasoning="off"` on a model that supports this to disable it.

On a network that cannot reach a provider directly, a `proxy` on the `provider()` call routes requests to that provider through [SOCKS5](https://en.wikipedia.org/wiki/SOCKS5).
A top-level `proxy` is applied to all providers and every outbound HTTPS connection Strument makes.
`proxy="direct"` disables the top-level `proxy` for that provider.
`search()` calls work the same way.

A project-local config, `.strument.star` or `.strument/config.star`, can override any of these settings, once you have run `strument trust` in the directory.
Trust is recorded by content hash, following the [direnv](https://direnv.net/) model, so an edited config must be trusted again.
`strument trust` lists settings with potential security implications as well as the project [skills](doc/config.md#skills).
The same command trusts the project's skills.
Project skills are not loaded until you trust them.
See [`doc/config.md`](doc/config.md) for details.


## Security and the sandbox

On Linux, Strument confines itself with [Landlock](https://landlock.io/) before the session starts.
Every process it spawns inherits the Landlock sandbox, as does the `bash` tool.
As a result, every child process can write only to your project, a temporary directory, the session's state directory, and toolchain-specific paths like caches.
`/sandbox` lists the effective paths.
`sandbox_write` in the config adds writable paths; `sandbox = ""` disables the sandbox, which is the default on non-Linux platforms.

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
- No MCP (for now), subagents, aider's architect mode, voice, or GUI.
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
