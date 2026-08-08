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
| `history_file` | string | Optional. Overrides the chat-history path (absolute, or relative to the project root). |
| `proxy` | string | Optional. A global SOCKS5 proxy URL — the fallback for providers that set none, and the proxy for `strument model-config` and URL scraping. |
| `scraper` | list of strings | Optional. An external command (argv) run to fetch pages instead of the built-in HTTP scraper — the opt-in path for JavaScript-rendered pages. See below. |
| `verify` | dict of string to list of strings | Optional. Named verification commands (argv) the model may run without confirmation. See below. |
| `verify_auto` | list of strings | Optional. Names of `verify` checks Strument runs itself at the end of a turn that changed files. See below. |

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
- **`repo_map`** — include the tree-sitter repository map (default `True`).
- **`cache`** — enable prompt caching: cache-control breakpoints with a one-hour
  TTL, and the repo map frozen so the cached prefix stays byte-stable (default
  `False`).
- **`context`** — the input window in tokens. `0`/unset means unknown, which
  disables the context-limit warning.
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

### `env(name, default=None, required=True)`

Reads an environment variable at load time — the one impure built-in.

- **`name`** — the variable to read.
- **`required`** — when `True` (the default) and the variable is unset, loading
  fails with an error. Set `required=False` to allow a fallback.
- **`default`** — returned when the variable is unset **and** `required=False`
  (otherwise the result is `None`).

The gotcha worth stating outright: `default` only takes effect when
`required=False`. `env("X", default="y")` on its own still errors if `X` is
unset, because `required` is `True` by default — you need both:

```python
api_key = env("OPENROUTER_API_KEY")                        # required; errors if unset
base = env("STRUMENT_BASE_URL", required=False)            # None if unset
proxy = env("STRUMENT_PROXY", default="", required=False)  # "" if unset
```

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

## Project-local config

A `.strument.star` in the project root can add or override `models`, `default`,
`history_file`, and `proxy`, with the project file winning. It is **inert until
trusted**: run `strument trust` in the directory (a direnv-style content-hash
gate), and re-run it after every edit.
