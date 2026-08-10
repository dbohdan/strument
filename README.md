# Strument

Strument is an AI pair-programming tool for the terminal.
You describe a change.
The model searches your project with `grep`, `glob`, and `symbol`,
reads what it finds, edits it, runs your test suite, reads the failure, and tries again —
all inside the turn you started.
Every edit scrolls past as a diff.
The whole turn is one `/undo` away,
in a git repository or outside one.

The loop closes inside a turn and stops at its edge.
Strument will read forty files and rewrite six of them without asking permission for any of it.
But it will not begin a turn you didn't begin,
and it will not run a shell command you didn't approve.
Broad authority inside a turn, none between turns.

Strument is one static Go binary.
There is no Python, no cgo, and no model database —
just one configuration file in Starlark and one OpenAI-compatible dialect.
Strument began as a ground-up reimplementation of [aider](https://github.com/Aider-AI/aider)
at commit [`5dc9490`](https://github.com/Aider-AI/aider/tree/5dc9490bb35f9729ef2c95d00a19ccd30c26339c) (0.86.3.dev).
It now follows its own direction — closer to aider in some places, further in others.
[`doc/README.md`](doc/README.md) is the developer overview.

## A session

```
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

One prompt, four sends, one commit, one cost line.
The recessive gray lines are Strument narrating its own mechanics.
The diffs and the sentence at the end are the model's.
The tests ran because `verify_auto` says they run after any turn that changed a file,
not because the model remembered to.
`/undo` at that prompt puts both files back and drops the commit.

## Getting started

Go 1.26 or later, no cgo, no C toolchain:

```sh
go install dbohdan.com/strument/cmd/strument@latest
```

Strument needs a configuration file before it will start.
There is no model database, and nothing is assumed about which models you have.
The smallest config that works is four lines.
Put it in `~/.config/strument/config.star`:

```python
openrouter = provider("openrouter", api_key=env("OPENROUTER_API_KEY"))

models = {"haiku": model(openrouter, "anthropic/claude-haiku-4.5")}
default = "haiku"
```

Then export the key and start it in your project:

```sh
export OPENROUTER_API_KEY=sk-or-...
cd ~/src/myproject
strument
```

That is enough to work with.
It is worth adding `context`, `max_output`, and the costs per model,
so that Strument can warn you before you overrun the window
and report what a turn spent.
`strument model-config <slug>` fetches those numbers and prints a config block to paste.
See [Configuration](#configuration).

Strument spends your money by the turn, and a turn can run twenty-five steps.
Start on a small request against a cheap model,
and watch the cost line before you turn it loose on something large.

## Using it

Type what you want changed.
The model works until it is finished or until it needs you,
and each tool call reports one line as it goes.
At twenty-five steps it stops, summarizes what it has done and what it has spent,
and asks whether to keep going.
That is a checkpoint, not a wall.
Shell commands are the other place it stops.
`bash` always asks first.
Reading, searching, and editing do not.

Slash commands do the things that are yours rather than the model's:

| | |
| --- | --- |
| `/add <file>`, `/drop`, `/ls` | Pin a file's contents into the conversation when you already know what needs changing. Everything else the model finds itself. |
| `/ask <question>` | Ask about the code with the editing tools taken away. `/code` switches back. |
| `/undo` | Put the last turn back — the files, and the commit if there was one. |
| `/squash [n]` | Fold the last n turns' commits into one. |
| `/diff`, `/tokens` | Show what changed, and how full the context window is. |
| `/symbol <name> [reference]` | Find where a name is defined, or used, from the language parser rather than from text. |
| `/run <cmd>`, `/web <url>` | Run a command or fetch a page and offer the output to the model. |
| `/model [alias]`, `/reload` | Switch models mid-session; reload `config.star` without restarting. |

`/help` lists all twenty-one.

For scripts and one-offs, `-m` runs a single turn and exits:

```sh
strument -m 'Add a --version flag to cmd/pollctl.'
strument --dry-run -m 'Fix the race in internal/poll.'   # report the edits, write nothing
strument --yes -m 'Update the changelog for v0.3.0.'     # answer confirmations; still never runs a shell command
```

`--yes-shell` is the flag that lets the model run shell commands unattended.
`--no-git` turns off the git integration inside a repository.
Outside one it is already off, and `/undo` works either way.

## What it does differently

Strument is a *reviewable* loop rather than an autonomous one.
The model works within a turn because that is what tool calls mean:
a reply ending in a tool call is a reply the model has not finished.
Refusing to honor that produced confusion, not safety.
What Strument keeps is the review surface —
the diff on your screen, the undo behind it, the turn boundary in your hands.
Everything below follows from that.

- **One binary, no Python.** Pure Go, tree-sitter included, no cgo.
- **Starlark configuration.**
  A single `config.star` replaces layered YAML, `.env` files, and a model database.
  Project-local `.strument.star` files are inert until you run `strument trust` in the directory.
  It is a direnv-style content-hash gate, and you re-run it after every edit.
- **One model dialect.** OpenAI-compatible chat completions with OpenRouter extensions and native tool calls.
  No litellm, no MCP.
- **Nine tools, in three natures.**
  `read`, `grep`, `glob`, `ls`, and `symbol` look.
  They are free, run without confirmation, and never see a file the project ignores.
  `edit` and `write` change things, and their edits land the moment the call arrives.
  `bash` runs a command behind a confirmation, and `verify` runs a check you configured.
  `symbol` answers "where is this defined" from the language parser rather than from text.
  That is what keeps it from being a second `grep`.
- **Every turn is undoable, with or without git.**
  Strument records each file before the first time it writes to it,
  so `/undo` can restore a whole turn even in a directory that is not a repository —
  a live configuration directory, say, or a checkout under another SCM.
  An edit preserves the file's mode and writes *through* a symlink instead of replacing it.
  A batch that fails partway rolls back whole.
  Where there is a repository, a turn is also one commit,
  described as one piece of work, and `/squash [n]` merges several.
- **Checks the model can't talk its way past.**
  `verify` names your project's commands — tests, a linter, a build — and the model runs them by *name*.
  It never composes a shell command to check its own work.
  `verify_auto` runs the ones you list at the end of any turn that changed a file,
  whether or not the model thought to.
  A passing check prints one line.
  Only a failing one prints its output.
- **Plain-HTTP URL scraping, with an escape hatch.**
  URLs you mention, or `/web <url>`, are fetched with an ordinary HTTP GET — a real `User-Agent`, no headless browser — and converted to markdown.
  A static binary can't embed a browser, so JavaScript-rendered pages come back thin.
  For those, point the `scraper` setting at an external command and Strument shells out to it.
  You bring the renderer; the binary stays one file.

The terminal interface stays deliberately close to aider's:
the same green/blue palette (with `--dark-mode` and `--light-mode`),
a horizontal rule and the in-chat file list before each prompt, an opening banner.
Where it diverges, it does so because the loop is different.
A step's tool calls are narrated in a recessive gray.
Reasoning arrives between `‹thinking›` and `‹/›` rather than under a banner,
because most reasoning is one line and a banner cannot be one.
Syntax highlighting and the code-block background are the intentional omissions.

## Configuration

Strument is configured in [Starlark](https://starlark-lang.org/), a small sandboxed dialect of Python.
A config file is a short program that builds model objects and assigns them to a few names.
[`doc/config.md`](doc/config.md) is the reference for every built-in and setting.
Here is a fuller example than the starter above —
two providers, a factory for a repeated option, and aliases:

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

models["s"] = models["sonnet"]  # One model, two aliases.

default = "sonnet"
```

`cache` (off by default) attaches cache-control breakpoints with a one-hour TTL.
Anthropic models reached through OpenRouter honor them explicitly.
Other providers cache automatically and ignore them,
but a stable prefix helps their implicit caching too,
so it is worth setting on any cache-capable model.

Writing `context`, `max_output`, and the costs by hand for every model is tedious.
Instead, `strument model-config anthropic/claude-haiku-4.5` fetches them from the provider's catalog
and prints a `models` dict to paste,
with the judgment calls (`reasoning`, `reasoning_tag`, `weak_model`) left as commented placeholders.
It keeps no model database.
The catalog is fetched on demand and frozen into your own config.

Three settings live at the top level rather than on a model.
`verify` names the commands that check your project.
`verify_auto` says which of them Strument should run itself at the end of an editing turn.
`reasoning_display` says how much of the model's thinking to show:

```python
verify = {
    "lint": ["golangci-lint", "run"],
    "test": ["go", "test", "./..."],
}
verify_auto = ["lint", "test"]

reasoning_display = 10  # "full" (the default), a line count, or "off".
```

Checks run in the order they are listed and stop at the first failure, so put the fast ones first.
Hiding reasoning is not the same as not buying it.
Reasoning tokens are billed either way,
and `reasoning="off"` on the model is what stops the spending.

On a network that can't reach a provider directly,
a `proxy` on the `provider()` call routes that provider's requests through SOCKS5.
A top-level `proxy` covers every outbound HTTPS action Strument takes.
A project-local `.strument.star` can extend or override any of this,
once you have run `strument trust` in the directory.
[`doc/config.md`](doc/config.md) has the details of all of it.

## What it doesn't do

Strument is pre-1.0 and moving.
Expect settings to change under you, and read the commit log before upgrading.
Beyond that, the limits worth knowing before you install it:

- **It needs a model that calls functions well.**
  Everything — reading a file, searching, editing — is a tool call,
  so a model that fumbles them cannot drive Strument at all.
  The text edit formats that existed for such models (SEARCH/REPLACE, fenced, whole-file) have been removed.
  In a sixteen-model live sweep, fourteen drove every tool cleanly.
  The two that couldn't were the smallest and most reasoning-heavy of the set,
  and they spent their output budget thinking instead of calling.
  Treat about 27B as the working floor.
- **It is tested on Linux.**
  Releases cross-compile for twelve platforms, including macOS, Windows, and the BSDs.
  But CI runs on Linux only, and that is where the live testing happens.
- **No MCP, no subagents, no architect mode, no voice, no GUI, no analytics.**
  One dialect, one model at a time, one terminal.
- **No syntax highlighting** and no background behind code blocks — a deliberate omission, not a missing feature.
- **The scraper is plain HTTP.** JavaScript-rendered pages need the `scraper` escape hatch above.

## Relationship to aider

Strument reimplements aider's ideas in Go rather than wrapping them, and the debt is specific.
The tree-sitter tag queries under `internal/repomap/queries*/` are aider's, copied.
The built-in prompts began as aider's and have since been rewritten.
The repository map — a PageRank-ranked skeleton of the project — is aider's design.
So are `/undo`, `/ask`, the chat-history summarization,
the fuzzy matcher that lands an edit whose whitespace the model reproduced imperfectly,
and the look of the terminal.

Two divergences are deliberate and worth stating outright.
First, the loop closes.
Aider's turn is one send because a SEARCH/REPLACE block is a finished thought.
A tool call is not a finished thought, and Strument follows its semantics instead.
Second, the safety net is a snapshot rather than a commit.
Aider's undo is git's.
Strument's is its own record of every file it wrote,
which is what makes it usable where there is no repository at all.

## Credits and license

Strument is derived from [aider](https://github.com/Aider-AI/aider) by Paul Gauthier and the aider contributors,
licensed under the [Apache License 2.0](LICENSE), and carries the same license.

Three components are vendored, each with a `NOTICE` recording what was taken and what was changed:

- The streaming markdown renderer (`internal/render/`) is ported from
  [streaming-markdown](https://github.com/thetarnav/streaming-markdown) by Damian Tarnawski (MIT).
- The gitignore pattern matcher (`internal/gitignore/`) comes from
  [go-git](https://github.com/go-git/go-git) at `v6.0.0-alpha.5` (Apache 2.0).
- The terminal line editor (`internal/readline/`) is a fork of
  [ergochat/readline](https://github.com/ergochat/readline) v0.1.3 (MIT).
  Its redraw was reworked to be flicker-free using the single-write technique from
  [bestline](https://github.com/jart/bestline) by Justine Tunney (2-clause BSD).

## Building

```sh
go build ./cmd/strument     # full build: every bundled tree-sitter grammar
task build:strument:subset  # release variant: only the grammars Strument uses
task release                # cross-compile the subset build for every platform
```

The subset build compiles in just the 35 grammars the repository map supports,
via gotreesitter's `grammar_subset` build tags.
That comes to about 32 MB instead of 43 MB, statically linked and stripped.
The tag list lives in [`script/grammar-tags.txt`](script/grammar-tags.txt),
and a test keeps it in sync with the supported languages.

Strument builds and tests offline, with no API keys and no extra setup.
To read aider's source alongside it, `task setup:reference` clones aider at commit `5dc9490`
into a gitignored `reference/` directory.
Nothing in the build needs it.
