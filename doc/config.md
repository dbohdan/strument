# Configuring Strument

Strument reads its configuration from a [Starlark] file, `config.star` (by
default `$XDG_CONFIG_HOME/strument/config.star`, i.e.
`~/.config/strument/config.star`). Starlark is a small, sandboxed dialect of
Python — no imports, no I/O, deterministic — so a config file is a short program
that builds provider and model objects and assigns them to a few top-level
names. If you read Python you can read it already; for the specifics see the
[language spec][Starlark] and Laurent Le Brun's [overview of Starlark][overview].

[Starlark]: https://starlark-lang.org/
[overview]: https://laurent.le-brun.eu/blog/an-overview-of-starlark

A complete, worked example — providers, model factories, aliases,
`with_extra_params` — lives in the [README](../README.md#configuration). This
document is the reference for the built-ins that example uses.

## Top-level names

The loader reads these module-level variables after running your file:

| name | type | meaning |
| --- | --- | --- |
| `models` | dict | Maps an **alias** (string) to a `model()`. Required, non-empty. |
| `default` | string | The alias used when none is given on the command line. Required; must be a key of `models`. |
| `history_file` | string | Optional. Overrides the chat-history path (absolute, or relative to the project root). See below. |
| `proxy` | string | Optional. A global SOCKS5 proxy URL — the fallback for providers that set none, and the proxy for `strument model-config` and URL scraping. |
| `scraper` | list of strings | Optional. An external command (argv) run to fetch pages instead of the built-in HTTP scraper — the opt-in path for JavaScript-rendered pages. See below. |
| `verify` | dict of string to list of strings | Optional. Named verification commands (argv) the model may run without confirmation. See below. |
| `verify_auto` | list of strings | Optional. Names of `verify` checks Strument runs itself at the end of a turn that changed files. See below. |
| `reasoning_display` | `"full"`, a number, or `"off"` | Optional. How much of the model's thinking to show. Default `"full"`. See below. |

Anything else at the top level (helper `def`s, intermediate variables) is
ignored by the loader, so factor freely.

### `scraper`

When set, `scraper` is an argv list whose command replaces the built-in HTTP
fetcher; `%s` in any element is substituted with the URL (if no element has
`%s`, the URL is appended). Strument runs the command without a shell — so a
hostile URL can't inject arguments — treats its stdout as HTML, and converts
that to markdown exactly as it does a fetched page. It is the way to read
JavaScript-rendered pages without bundling a browser: point it at a headless one.

```python
scraper = ["chromium", "--headless=new", "--dump-dom", "%s"]
```

Unset, the built-in HTTP scraper is used (the default). The global `proxy` does
**not** apply to a `scraper` command; the command handles its own networking.

### `history_file`

Strument keeps one directory per project under
`$XDG_STATE_HOME/strument/projects/<basename>-<hash>/`, where the hash is the
first 8 hex characters of the SHA-256 of the project root's absolute path:

```
projects/myproj-9428ba2d/
    root            the absolute path this directory belongs to
    transcript.md   the chat transcript
    input.txt       the REPL's input history (owner-only, like ~/.bash_history)
    resume.json     pinned files and the model alias, so a restart costs no retyping
    cost.jsonl      one line per turn: tokens, cost, steps, files changed
```

`strument history` prints the transcript's path, which is the point of the
command — XDG makes it hard to guess. The `root` file answers the other
direction: which project a directory belongs to, without recomputing hashes.
The directory is `0700` and its files `0600`, because a transcript records
whatever the model read out of the project, and Strument is meant to be usable
on a live configuration directory.

The project, for this purpose, is the **git worktree root** wherever there is
one, and the working directory otherwise. That holds from any subdirectory, and
it does not change under `--no-git`: that flag says how a turn is committed, not
which project you are in, so one repository keeps one directory however you
launch Strument in it.

`history_file` overrides the transcript path. An absolute value is used as
given; a relative one resolves against that same project root. It does not move
the input history, which has no override.

```python
history_file = "notes/strument.md"
```

### `proxy`

A SOCKS5 proxy URL for networks that can't reach a provider directly —
`socks5://host:1080`, or `socks5h://` to resolve DNS at the proxy. Only those two
schemes are supported.

```python
proxy = "socks5://127.0.0.1:1080"
```

At the top level it is the default for every provider that doesn't set its own,
and it also covers the two egress paths that belong to no provider:
`strument model-config`'s catalog fetch and URL scraping. In other words, a
top-level `proxy` covers every outbound HTTPS action Strument takes.

A `proxy` on a `provider()` call overrides it for that provider, and
`proxy="direct"` opts a provider out entirely — the case for a LAN-local model
server when the proxy exists only for external traffic:

```python
openrouter = provider(
    "openrouter",
    api_key=env("OPENROUTER_API_KEY"),
    proxy="socks5://127.0.0.1:1080",
)
local_llm = provider("openai", base_url="http://localhost:8000/v1", proxy="direct")
```

Credentials go in the URL (`socks5://user:pass@host:1080`); keep them out of the
file with `proxy=env("STRUMENT_PROXY")`, exactly as with `api_key`. The URL is
resolved and validated at load, so a malformed one fails at startup rather than
on the first request.

A `scraper` command is the one exception: it does its own networking, and the
global `proxy` does not reach it.

### `verify`

`verify` names the commands that check your project — tests, a linter, a build.
Each value is an argv list, run without a shell.

```python
verify = {
    "lint": ["golangci-lint", "run"],
    "test": ["go", "test", "./..."],
}
```

The model reaches these through the `verify` tool, and — unlike `bash`, which
always asks — they run **without confirmation**. That is safe because the model
supplies only a *name*: it calls `verify("lint")` and never a command, so there
is nothing for it to alter or append. Everything runnable is written by you, in
this file. This is the observation half of the harness running freely while
mutation stays gated.

Declared order matters. `verify()` with no name runs every check in order and
stops at the first failure, so put the fast ones first. `verify("test")` runs
just that one.

A project's `.strument.star` merges into your `verify` **per key**: it can
replace one check or add its own without restating the rest.

```python
# .strument.star — override just the test command, keep the user's lint.
verify = {"test": ["go", "test", "-race", "./..."]}
```

Because the key replaces rather than appends, extend a check by building the
dict explicitly:

```python
verify = dict(verify, lint=["golangci-lint", "run", "--fast"])
```

Unset, no `verify` tool is offered and every command goes through `bash` and its
confirmation prompt.

Naming a check buys one more thing, and it is a property of `verify` rather than
of `bash`: a `bash` command that *is* one of these checks, **verbatim**, runs
without the confirmation prompt. You wrote that command here, so the prompt
would be asking you to re-approve your own decision — and a prompt that fires on
every `go test ./...` is what teaches you to answer the ones that matter without
reading them.

Verbatim is strict. The command must be a single simple command of bare words:
no pipelines, `;`, `&&`, redirections, backgrounding, leading assignments, or
expansions of any kind — and no quoting, so a check like
`["pytest", "-k", "not slow"]` can never match and always asks. Anything that is
not an exact match simply gets the ordinary prompt, so the failure direction is
an extra question rather than a command you did not approve. On a match Strument
runs the argv from this file, never the model's string, so what runs is
certainly what was compared.

### `project_checks()`

`project_checks()` detects a project's usual checks from its marker files, so
you don't have to write a `verify` dict for every repository:

```python
verify = project_checks()
```

It is opt-in: nothing happens because a file is on disk, only because you wrote
this. Names are always prefixed with the ecosystem — `go-test`, `node-test`,
`make-test` — so a polyglot repository keeps both suites instead of one quietly
losing to the other. Extend and drop with ordinary dict operations:

```python
verify = dict(project_checks(), lint=["golangci-lint", "run"])
```

The root it looks in is the *project's*, not the config file's, so this works in
your user config as well as in a `.strument.star` — write it once and every
project you open gets its own checks.

| Marker | Checks |
| --- | --- |
| `go.mod` | `go-vet`, `go-test` |
| `Cargo.toml` | `cargo-check`, `cargo-test` |
| `pyproject.toml`, `pytest.ini`, `tox.ini`, or `setup.cfg` declaring pytest | `py-test` |
| `package.json` with a `test` script | `node-test` (`npm`/`pnpm`/`yarn`/`bun` per the lockfile) |
| `deno.json`/`deno.jsonc` | `deno-check`, `deno-test` |
| `Makefile` with a `lint`, `check`, or `test` rule | `make-lint`, `make-check`, `make-test` |
| `Taskfile.yml` with a `lint` or `test` task | `task-lint`, `task-test` |
| `justfile` with a `lint` or `test` recipe | `just-lint`, `just-test` |
| `pom.xml` | `mvn-test` |
| `gradlew` | `gradle-test` |
| `*.sln`/`*.csproj` | `dotnet-build`, `dotnet-test` |
| `composer.json` with a `test` script | `php-test` |
| `Gemfile` naming rspec, plus `spec/` | `rspec` |
| `mix.exs` | `mix-compile`, `mix-test` |
| `shard.yml` plus `spec/` | `crystal-spec` |
| `*.cabal`/`cabal.project`, or `stack.yaml` | `cabal-build`/`cabal-test`, or `stack-test` |

Two rules decide what is on that list. Only commands the marker's own toolchain
ships, or ones your project's config names: `go vet` comes with Go, but pytest
does not come with Python, so it appears only where your config declares it. And
where the command runs a target you defined — `make`, `task`, `just`, `npm`,
`composer` — the target has to actually exist, or a detected check would just be
a command that fails for reasons unrelated to your code.

**These are not commands that cannot do harm.** Every one of them runs your
project's own code: `npm test` runs whatever `package.json` says, `make test`
runs your Makefile, `cargo test` compiles and runs your crate. No test runner is
safe in that sense. What they are is commands whose effect is decided by your
project's own committed configuration — which is why this is opt-in, and why it
is worth glancing at what it detected the first time you use it on an unfamiliar
repository. Every check's argv is printed when it runs.

### `verify_auto`

`verify_auto` lists the checks Strument runs *itself*, without being asked, at
the end of a turn that changed files.

```python
verify_auto = ["lint", "test"]
```

The names must be keys of `verify`; a name that isn't fails at load, so a typo
can't leave you believing the project is checked when nothing runs.

This exists because the model deciding *whether* and *which* to check is the
part that goes wrong. A model can finish a change, run the tests, see them pass,
and report success while a linter would have caught what it just wrote. Listing
the checks here takes that judgement away from it.

When a check fails, the output goes back to the model and it keeps working in
the same turn. That repeats at most three times before Strument stops and hands
back to you — a model that can't get the checks green should not spend your
budget trying.

Order is the order you write here, stopping at the first failure, which is the
same rule `verify` follows: checks run in the order they are listed, wherever
they are listed. Nothing runs after a turn that only read files, or under
`--dry-run`, since no edit lands. The `verify` tool stays available either way,
so the model can still check something mid-turn.

### `reasoning_display`

How much of the model's thinking to show, in both the interactive REPL and
`--message` script mode:

```python
reasoning_display = "full"   # the default: all of it
reasoning_display = 10       # the first 10 lines, then "… N more lines of thinking …"
reasoning_display = "off"    # none of it
reasoning_display = 0        # the same as "off"
```

`"full"` is the default because a terminal transcript has no way to unfold what
it hid. Showing less makes the transcript incomplete, which is a thing to
choose rather than to inherit.

A number keeps the **first** lines. That is the useful half, not merely the one
that streams: a thinking block usually ends by restating its conclusion, and the
answer then says the same thing, while the opening — the approach weighed, the
option rejected — appears nowhere else.

The number is not sensitive, so do not agonize over it. Block lengths are
bimodal in practice: most are a single sentence restating the tool call that
follows, and the occasional one runs to dozens of lines. Anything from about 3
to 15 behaves the same on both — one-liners untouched, the long one cut. Ten is
a fine starting point.

**`"off"` hides the thinking; it does not stop the model producing it.**
Reasoning tokens are billed whether or not they are shown. To stop paying for
them, set `reasoning="off"` on the model instead — that changes the request,
where this changes only the screen. Keeping them apart matters: otherwise a
project's `.strument.star` could change what a turn costs by way of a display
preference.

## Built-in functions

Three functions are predeclared. Keyword-only parameters follow the `*`, as in
Python.

### `provider(adapter, *, base_url=None, api_key=None, name=None, proxy=None, extra_params={})`

Describes one API endpoint and dialect. Returns a provider value to pass to
`model()`.

- **`adapter`** — `"openai"` or `"openrouter"`. Selects the request dialect
  (e.g. how reasoning effort is serialized) and the default base URL.
  `"anthropic"` is reserved and not yet supported.
- **`base_url`** — endpoint override. Unset uses the adapter default
  (`https://api.openai.com/v1` or `https://openrouter.ai/api/v1`).
- **`api_key`** — the bearer token. Keep it out of the file with `env()`
  (below): `api_key=env("OPENROUTER_API_KEY")`.
- **`name`** — a label for the provider. It appears in the provider-qualified
  slug Strument prints (`local/qwen/...`) and defaults to the adapter when unset,
  so name a provider when you run two of the same adapter.
- **`proxy`** — a SOCKS5 proxy URL (`socks5://host:1080` or `socks5h://…`) for
  this provider's requests. `"direct"` forces a direct connection even when a
  global `proxy` is set; unset inherits the global one. Credentials may be inline
  (`socks5://user:pass@host:1080`) or from `env()`.
- **`extra_params`** — a dict of extra request fields, merged into the JSON body
  beneath the keys Strument owns (`model`, `messages`, `stream`, … — those are
  rejected). Values must be JSON-serializable.

### `model(provider, slug, *, display_name=None, edit_format="tool", weak_model=None, reasoning=None, reasoning_tag=None, temperature=None, repo_map=True, cache=False, context=None, max_output=None, input_cost=None, output_cost=None, extra_params={})`

Describes one usable model. Returns a model value to place in the `models` dict.

- **`provider`** — a value from `provider()`.
- **`slug`** — the model id sent to the API, e.g. `"anthropic/claude-haiku-4.5"`.
- **`display_name`** — a human label (used in git commit trailers). Defaults to
  the slug reduced to its core (provider prefix and `:variant` suffix stripped).
- **`edit_format`** — `"tool"`, which is the default and the only value. The
  text fallbacks (`"diff"`, `"diff-fenced"`, `"whole"`) have been removed: they
  existed for models that could not call functions reliably, and such a model
  can no longer drive Strument at all, because finding, reading, and searching
  files are tool calls too. A config still naming one gets an error saying so;
  the fix is to drop the setting.
- **`weak_model`** — a cheaper model for summaries and commit messages: an alias
  string or an inline `model()`. Unset means the model is its own weak model.
- **`reasoning`** — reasoning effort: `"low"`, `"medium"`, or `"high"` (other
  values pass through). `"off"` disables reasoning where the provider allows it;
  `""` or `"default"` leaves it to the model.
- **`reasoning_tag`** — the name of an inline tag (e.g. `"think"`) the model
  wraps its reasoning in; its contents are stripped from the answer body.
- **`temperature`** — a float, or `None` to omit the field.
- **`repo_map`** — build the tree-sitter parse layer (default `True`). It is what
  the `symbol` tool, the `/symbol` command, and the after-an-edit parse check are
  read from. Nothing derived from it is sent with your requests; the model finds
  code with `grep`, `glob`, and `read`.
- **`cache`** — add prompt-cache breakpoints with a one-hour TTL (default
  `False`). Strument marks the last message in the examples-or-system,
  read-only-files, and chat-files sections; it does not mark the completed or
  current conversation. This adds provider-facing metadata only: whether a
  cache is used depends on the provider and model.
- **`context`** — the input window in tokens. `0`/unset means unknown, and two
  things that depend on knowing it stop working: the warning before a request
  overruns the window, and the summarization that keeps the settled chat history
  inside `min(max(context/16, 1024), 8192)` tokens. Without it a long session
  grows its history until the provider refuses the request. Both are silent when
  it is unset — there is nothing to warn about a limit you have not stated — so
  set it on every model you use for real work.
- **`max_output`** — the maximum output tokens.
- **`input_cost`, `output_cost`** — price in **US dollars per million tokens**
  (e.g. `input_cost=3`), used as a *fallback estimate*. The per-turn cost line
  prefers the cost the provider reports for each request and falls back to these
  only when none is reported. OpenRouter reports it in-band — so any live
  discount is reflected and these go unused — while a plain OpenAI-compatible
  endpoint may not. Treat them as an approximate snapshot, not the source of
  truth. `strument model-config` fills them in; unset means no cost is shown when
  the provider reports none.
- **`extra_params`** — as on `provider()`, but per model; on a key clash the
  model's value wins over the provider's.

### `env(name, default)`

Reads an environment variable at load time — the one impure built-in.

- **`name`** — the variable to read.
- **`default`** — returned when the variable is unset. Giving one is what makes
  the variable optional; omit it and an unset variable fails the load.

It behaves like Starlark's own dictionary access, which is the shape you already
know: `env("X")` is `d["x"]` and raises when the key is absent, while
`env("X", default=v)` is `d.get("x", v)` and does not.

```python
api_key = env("OPENROUTER_API_KEY")                # required; errors if unset
api_key = env("SOME_OPTIONAL_KEY", default="")     # "" if unset
base = env("STRUMENT_BASE_URL", default=None)      # None if unset
proxy = env("STRUMENT_PROXY", "")                  # positional, like get()
```

What counts is whether you passed `default`, not what you passed: `default=None`
makes the variable optional and yields `None`, which omitting the keyword does
not. Note that `provider()` wants a string for `api_key`, so an optional key
wants `default=""` rather than `default=None`.

## Model methods

### `model.with_extra_params(**overrides)`

Returns a copy of the model with `overrides` merged into its `extra_params`
(overrides win); the original is unchanged. Handy for a one-off tweak on top of a
factory:

```python
def flex(m):
    return m.with_extra_params(service_tier="flex")
```

## Aliases

An alias is just a `models` key, so the same model can appear under several:

```python
models["ds"] = models["deepseek-pro"]  # one model, two aliases
```

## `strument model-config`

Writing `context`, `max_output`, and the costs by hand for every model is
tedious, so `strument model-config` fetches them and prints a copy-pastable
block:

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

It fills in the objective fields from OpenRouter's catalog — context size, max
output, costs in US dollars per million tokens, and `cache=True` where the model
supports caching — and leaves the judgment calls commented out for you. Each key
is the slug's core; rename it to the short alias you will actually type. The
output is a whole `models` dict, so it drops in as a new config; to add to one
that already defines `models`, merge the two dicts rather than pasting a second
`models =`.

Pass exact slugs. `--provider-name` sets the provider variable emitted in the
call (default `openrouter`), and output goes to stdout, so redirect or pipe it
wherever you like. Because `config.star` is almost Python, a Python formatter
such as `ruff format` or `black` will tidy the pasted blocks.

It authenticates with your OpenRouter key — from your config, or from
`OPENROUTER_API_KEY` — because anonymous catalog requests are rate-limited and
can get your IP blocked, and it caches each fetched model for a day under your
cache directory so repeated runs don't refetch. Pass `--proxy socks5://…` when
the catalog fetch itself must go through a proxy; otherwise it uses the global
[`proxy`](#proxy).

Strument maintains no model database. The catalog is fetched on demand and
frozen into your own config, which is why nothing goes stale behind your back.

## Project-local config

A `.strument.star` in the project root can add or override `models`, `default`,
`history_file`, and `proxy`, with the project file winning. It is **inert until
trusted**: run `strument trust` in the directory (a direnv-style content-hash
gate), and re-run it after every edit.
