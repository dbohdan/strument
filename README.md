# Strument

Strument is an AI pair-programming tool for the terminal: a ground-up Go port
of [aider](https://github.com/Aider-AI/aider), trimmed to the improved
essentials. It talks to LLMs through a single OpenAI-compatible client
(OpenRouter dialect), applies the model's edits to your files through native
tool calls, builds a ranked repository map with tree-sitter, and — in a git
repository — auto-commits each change so every edit is one `git undo` away.

Strument began as a close reverse-engineering of aider at commit [`5dc9490`](https://github.com/Aider-AI/aider/tree/5dc9490bb35f9729ef2c95d00a19ccd30c26339c) (0.86.3.dev), reimplemented in Go. It now follows its own direction — closer to aider in some places, further in others. [`doc/README.md`](doc/README.md) is the developer overview.

## What's different from aider

- **One binary, no Python.** Pure Go, including tree-sitter (no cgo).
- **Starlark configuration.**
  A single `config.star` replaces layered YAML/`.env`/model-database
  [configuration](#configuration); project-local `.strument.star` files are inert until
  explicitly trusted (direnv-style content-hash gate).
- **One model dialect.** OpenAI-compatible chat completions with OpenRouter
  extensions and native tool calls; no litellm, no MCP.
- **Essentials only.** Tool-call edits by default (with SEARCH/REPLACE,
  fenced, and whole-file as fallbacks), repo map, reflection on failed edits,
  shell-command suggestions, git auto-commit with `/undo`, and `/ask` mode
  for questions that should not touch files. Architect mode, voice, GUI,
  analytics, summarization, and the other long-tail features are out of scope
  for v1.
- **Plain-HTTP URL scraping.** URLs you mention are fetched with a plain
  HTTP GET and reduced to text — no headless browser (a static binary can't
  embed one), so JavaScript-rendered pages, which is most modern docs
  sites, come back empty. aider ships a Playwright-based scraper; Strument
  trades that reach for the single-binary distribution.

The terminal interface, on the other hand, deliberately stays close to
aider's: the same green/blue palette (with `--dark-mode` and `--light-mode`),
a horizontal rule and the in-chat file list before each prompt, and an
opening banner — so a returning aider user feels no seam. Syntax highlighting
and the code-block background are the intentional omissions.

## Building

Go 1.26 or later; no cgo, no C toolchain.

```sh
go install dbohdan.com/strument/cmd/strument@latest  # install the latest release
go build ./cmd/strument            # full build: every bundled tree-sitter grammar
task build:strument:subset         # release variant: only the grammars strument uses
```

The subset build (about 32 MB instead of 43 MB, statically linked and
stripped) compiles in just the 35 grammars the repo map supports, via
gotreesitter's `grammar_subset` build tags; the tag list lives in
[`script/grammar-tags.txt`](script/grammar-tags.txt) and a test keeps it in
sync with the supported-language set. `task release` cross-compiles subset
binaries for the release platforms.

Strument builds and tests without any extra setup. To consult the original
aider source, `task setup:reference` (or `sh script/setup-reference.sh`)
clones aider at commit `5dc9490` into a gitignored `reference/` directory;
it is a read-only grep target, never committed.

## Configuration

Strument is configured using [Starlark](https://starlark-lang.org/), a sandboxed dialect of Python.
It reads model and provider definitions from a `config.star` file (typically `~/.config/strument/config.star`).
Here is an example configuration demonstrating providers, reusable model factories, and aliases:

```python
openrouter = provider("openrouter", api_key=env("OPENROUTER_API_KEY"))
local_llm = provider(
    "openai",
    name="local",
    base_url="http://localhost:8000/v1",
)

def deepseek(variant):
    return model(
        openrouter,
        "deepseek/deepseek-v4-%s:nitro" % variant.lower(),
        context=128 * 1024,
        display_name="DeepSeek V4 %s" % variant.title(),
        edit_format="tool",  # The default; "diff"/"diff-fenced"/"whole" fall back for weaker tool calling.
        max_output=8192,
        reasoning="low",
        reasoning_tag="think",  # Strip reasoning from the response body before it is processed.
        temperature=None,
        weak_model="deepseek-flash",
    )

def flex(m):
    return m.with_extra_params(service_tier="flex")

models = {
    "deepseek-flash": deepseek("flash"),
    "deepseek-pro": deepseek("pro"),
    "ds": None,  # A placeholder for the alias below.
    "gemini-flash": flex(
        model(openrouter, "google/gemini-3.5-flash", display_name="Gemini 3.5 Flash")
    ),
    "gpt": flex(model(openrouter, "openai/gpt-5.4", display_name="GPT-5.4")),
    "haiku": model(
        openrouter, "anthropic/claude-haiku-4.5", display_name="Claude Haiku 4.5"
    ),
    "k3": flex(model(openrouter, "moonshotai/kimi-k3", display_name="Kimi K3")),
    "sonnet": model(
        openrouter,
        "anthropic/claude-sonnet-5",
        display_name="Claude Sonnet 5",
        cache=True,  # Cache the prompt prefix (Anthropic honors this); freezes the repo map.
    ),
    "qwen": model(
        local_llm,
        "qwen/qwen3.6-27b",
        display_name="Qwen3.6 27B",  # Handles tool calls, so it uses the default "tool" format.
        reasoning="max",
        reasoning_tag="think",
        weak_model="qwen",  # Self-as-weak; only strings express this.
    ),
}

models["ds"] = models["deepseek-pro"]  # One struct under both keys.

default = "deepseek-pro"
```

The `cache` option (default off) turns on prompt caching for a model: Strument
attaches cache-control breakpoints with a one-hour TTL and freezes the repo map
so the cached prefix stays byte-stable across turns. Explicit breakpoints are
honored by Anthropic models (reached through OpenRouter); other providers cache
automatically and ignore them, but the frozen prefix still helps their implicit
caching, so it is worth setting on any cache-capable model. There is no
automatic default — set it per model where you know it pays off. Freezing the
map is the tradeoff caching accepts: the map refreshes when you add or drop
files, not on every message, so mid-conversation file mentions no longer re-rank
it while caching is on.

Writing out `context`, costs, and `cache` by hand for every model is tedious, so
`strument model-config` fetches them for you and prints copy-pastable `model()`
blocks:

```sh
strument model-config anthropic/claude-haiku-4.5
```

```python
model(
    openrouter,
    "anthropic/claude-haiku-4.5",
    display_name="Claude Haiku 4.5",
    context=200000,
    max_output=64000,
    input_cost=0.000001,
    output_cost=0.000005,
    cache=True,  # OpenRouter reports prompt caching for this model.
    # reasoning="low",  # Uncomment and set the effort: "low", "medium", or "high".
    # reasoning_tag="think",  # Uncomment if the model emits reasoning in inline tags.
    # weak_model="...",  # Uncomment to use a cheaper model for summaries and commits.
),
```

It fills the objective fields from OpenRouter's catalog (context size, max
output, per-token costs, and `cache=True` when the model supports caching) and
leaves the judgment calls — `reasoning`, `reasoning_tag`, `weak_model` — as
commented placeholders for you to fill in. Pass exact slugs; `--provider-name`
sets the provider variable emitted in the call (default `openrouter`); output
goes to stdout, so redirect or pipe it wherever you like. It maintains no model
database — the catalog is fetched on demand and frozen into your own config.
Because `config.star` is almost Python, you can tidy the pasted blocks with a
Python formatter such as `ruff format` or `black`.

Project-local `.strument.star` files can override or extend this, but
require explicit trust (`/trust`) before they take effect.

## Credits and license

Strument is derived from [aider](https://github.com/Aider-AI/aider) by Paul
Gauthier and the aider contributors, licensed under the
[Apache License 2.0](LICENSE). Strument carries the same license. The
tree-sitter tag queries under `internal/repomap/queries/` and
`internal/repomap/queries-legacy/` are copied from aider, and the built-in
prompts began as aider's; the rest is an independent reimplementation.

The streaming markdown renderer (`internal/render/`) is a port of
[streaming-markdown](https://github.com/thetarnav/streaming-markdown) by
Damian Tarnawski, MIT licensed.

The terminal line editor (`internal/readline/`) is a vendored fork of
[ergochat/readline](https://github.com/ergochat/readline) (MIT), taken at
v0.1.3. Its redraw was reworked to be flicker-free using the in-place,
single-write technique from [bestline](https://github.com/jart/bestline) by
Justine Tunney (2-clause BSD), and Ctrl+arrow now moves by word. See
[`internal/readline/NOTICE`](internal/readline/NOTICE) for details.
