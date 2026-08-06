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
- **Plain-HTTP URL scraping, with an escape hatch.** URLs you mention, or
  `/web <url>`, are fetched with a plain HTTP GET — a real `User-Agent`, no
  headless browser — and converted to markdown. A static binary can't embed a
  browser, so JavaScript-rendered pages (most modern docs sites) come back thin;
  aider ships a Playwright scraper. For those pages, point the `scraper` setting
  at an external command (e.g. a headless Chromium): Strument shells out to it
  rather than vendoring a browser, so the single binary stays a single binary and
  you bring your own renderer. See [Configuration](#configuration).

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
[`doc/config.md`](doc/config.md) is the reference for every built-in and parameter; here is an example
configuration demonstrating providers, reusable model factories, and aliases:

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
        cache=True,  # OpenRouter reports prompt caching for this model.
        reasoning="high",
    ),
    "ds": None,  # A placeholder for the alias below.
    "gemini-flash": flex(
        model(
            openrouter,
            "google/gemini-3.6-flash",
            display_name="Gemini 3.6 Flash",
            context=1048576,
            max_output=65536,
            cache=True,
        ),
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
        cache=True,  # Cache the prompt prefix (Anthropic honors this); freezes the repo map.
        reasoning="medium",
        weak_model="mimo",
    ),
    "qwen": model(
        local_llm,
        "qwen/qwen3.6-27b",
        display_name="Qwen3.6 27B",
        # Handles tool calls, so it uses the default "tool" format.
        reasoning="max",
        reasoning_tag="think",
        weak_model="qwen",  # Self-as-weak, the default; only strings express this.
    ),
}

models["ds"] = models["deepseek-flash"]  # One struct under both keys.

default = "ds"
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

On a network that can't reach a provider directly, set a SOCKS5 `proxy` on the
`provider()` call (`socks5://`, or `socks5h://` to resolve DNS at the proxy):

```python
openrouter = provider(
    "openrouter",
    api_key=env("OPENROUTER_API_KEY"),
    proxy="socks5://127.0.0.1:1080",
)
```

A top-level `proxy` is the default for every provider that doesn't set its own,
and it also covers `strument model-config` and URL scraping — every outbound
HTTPS action:

```python
proxy = "socks5://127.0.0.1:1080"
```

A provider bypasses that global proxy and connects directly with
`proxy="direct"` — the case for a LAN-local model server when the proxy is only
for external traffic. Credentials go in the URL
(`socks5://user:pass@host:1080`); keep them out of the file with
`proxy=env("STRUMENT_PROXY")`, exactly as with `api_key`. Only `socks5://` and
`socks5h://` are supported.

The built-in scraper is a plain HTTP GET, so JavaScript-rendered pages come back
thin. For those, set a `scraper` command — an argv list with `%s` marking the
URL — and Strument runs it instead of the built-in fetcher, treating its stdout
as HTML and converting that to markdown the same way. A headless browser dumping
the rendered DOM is the usual choice:

```python
scraper = ["chromium", "--headless=new", "--dump-dom", "%s"]
```

The command runs without a shell, so a hostile URL can't inject arguments; if no
element contains `%s`, the URL is appended as the last argument. Leaving
`scraper` unset keeps the built-in fetcher (the default). The global `proxy` does
not apply to a `scraper` command — it does its own networking.

Writing out `context`, costs, and `cache` by hand for every model is tedious, so
`strument model-config` fetches them for you and prints a copy-pastable
`models` dict:

```sh
strument model-config anthropic/claude-haiku-4.5
```

```python
models = {
    "claude-haiku-4.5": model(
        openrouter,
        "anthropic/claude-haiku-4.5",
        display_name="Claude Haiku 4.5",
        context=200000,
        max_output=64000,
        input_cost=1,
        output_cost=5,
        cache=True,  # OpenRouter reports prompt caching for this model.
        # reasoning="low",  # Uncomment and set the effort: "low", "medium", or "high".
        # reasoning_tag="think",  # Uncomment if the model emits reasoning in inline tags.
        # weak_model="...",  # Uncomment to use a cheaper model for summaries and commits.
    ),
}
```

Each key is the slug's core — rename it to the short alias you'll actually type.
The output is a whole `models` dict, so it drops in as a new config; to add to
one that already defines `models`, merge the two dicts (don't paste a second
`models =`). It fills the objective fields from OpenRouter's catalog (context
size, max output, costs in US dollars per million tokens, and `cache=True` when
the model supports caching) and leaves the judgment calls — `reasoning`,
`reasoning_tag`, `weak_model` — as commented placeholders for you to fill in.
Pass exact slugs; `--provider-name`
sets the provider variable emitted in the call (default `openrouter`); output
goes to stdout, so redirect or pipe it wherever you like. It authenticates with
your OpenRouter key — taken from your config, or the `OPENROUTER_API_KEY`
environment variable — because anonymous catalog requests are rate-limited and
can get your IP blocked; and it caches each fetched model for a day under your
cache directory, so repeated runs don't refetch. When the catalog fetch itself
must go through a proxy, pass `--proxy socks5://…`; otherwise it uses your
config's global `proxy`. It maintains no model database — the catalog is fetched
on demand and frozen into your own config.
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
