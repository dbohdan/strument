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
| `check` | dict of string to list of strings | Optional. Named verification commands (argv) the model may run without confirmation. See below. |
| `check_auto` | list of strings | Optional. Names of `check` entries Strument runs itself at the end of a turn that changed files. See below. |
| `reasoning_display` | `"full"`, a number, or `"off"` | Optional. How much of the model's thinking to show. Default `"full"`. See below. |
| `max_steps` | positive integer | Optional. Work-step budget per turn before the "Keep going?" checkpoint. Default 25. See below. |
| `max_error_reflections` | positive integer | Optional. Error-reflection budget per turn. Default 3. See below. |
| `webfetch_allow` | list of strings | Optional. Origins (host, or host:port) the `webfetch` tool may fetch without asking. See below. |
| `loop_detection` | boolean | Optional. Stop a reply that has begun repeating itself. Default `True`. See below. |
| `git_sign` | boolean or string | Optional. Sign auto-commits with `git commit -S`. `True` signs with the default key; a key-id string signs with that key. Default `False`. See below. |
| `env_allow` | list of strings | Optional. Environment variable names passed to model-run commands on top of the built-in allowlist. See below. |
| `sandbox` | `"landlock"` or `""` | Optional. Confinement mechanism. Defaults to `"landlock"` on Linux and `""` (off) elsewhere. See below. |
| `sandbox_write` | list of strings | Optional. Absolute paths the sandbox may write to on top of the derived set. See below. |

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

Both fetchers serve `/web` and the `webfetch` tool, and both honor a URL
fragment: `…/page#section` returns that section rather than the whole page. A `scraper` command is a
subprocess the model can cause, so it runs under the [environment
allowlist](#env_allow) and, when the model is what asked for it, is refused
where a required sandbox is not enforcing. The built-in fetcher spawns nothing
and is not gated that way.

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

### `check`

`check` names the commands that check your project — tests, a linter, a build.
Each value is an argv list, run without a shell.

```python
check = {
    "lint": ["golangci-lint", "run"],
    "test": ["go", "test", "./..."],
}
```

The model reaches these through the `check` tool, and — unlike `bash`, which
always asks — they run **without confirmation**. That is safe because the model
supplies only a *name*: it calls `check("lint")` and never a command, so there
is nothing for it to alter or append. Everything runnable is written by you, in
this file. This is the observation half of the harness running freely while
mutation stays gated.

Declared order matters. `check()` with no name runs every verification command in order and
stops at the first failure, so put the fast ones first. `check("test")` runs
just that one.

A project's `.strument.star` merges into your `check` **per key**: it can
replace one verification command or add its own without restating the rest.

```python
# .strument.star — override just the test command, keep the user's lint.
check = {"test": ["go", "test", "-race", "./..."]}
```

Because the key replaces rather than appends, extend a verification command by
building the dict explicitly:

```python
check = dict(check, lint=["golangci-lint", "run", "--fast"])
```

Unset, no `check` tool is offered and every command goes through `bash` and its
confirmation prompt.

Naming a verification command buys one more thing, and it is a property of
`check` rather than of `bash`: a `bash` command that *is* one of these commands,
**verbatim**, runs without the confirmation prompt. You wrote that command here,
so the prompt would be asking you to re-approve your own decision — and a prompt
that fires on every `go test ./...` is what teaches you to answer the ones that
matter without reading them.

Verbatim is strict. The command must be a single simple command of bare words:
no pipelines, `;`, `&&`, redirections, backgrounding, leading assignments, or
expansions of any kind — and no quoting, so a verification command like
`["pytest", "-k", "not slow"]` can never match and always asks. Anything that is
not an exact match simply gets the ordinary prompt, so the failure direction is
an extra question rather than a command you did not approve. On a match Strument
runs the argv from this file, never the model's string, so what runs is
certainly what was compared.

## Language support

Two features need to know which ecosystems Strument understands, and they need
to agree. `project_checks()` decides what to *run*; the sandbox decides what a
run is allowed to *write*. A project Strument offers to run checks for is a
project whose checks have to work under the sandbox, so the list lives here
once and both features read it.

Two lists that almost match would be worse than either. The gap does not show
up as a bug report — it shows up as one ecosystem failing for one person,
months later, with nothing to connect it back to a decision anyone made.

| Ecosystem | Marker | Checks | Writable paths |
| --- | --- | --- | --- |
| Go | `go.mod` | `go-vet`, `go-test` | `GOCACHE`, `GOMODCACHE`, subdirectories of `GOPATH` |
| Rust | `Cargo.toml` | `cargo-check`, `cargo-test` | subdirectories of `CARGO_HOME`, `RUSTUP_HOME` |
| Python | `pyproject.toml`, `pytest.ini`, `tox.ini`, or `setup.cfg` declaring pytest | `py-test`, plus `py-test-uv` / `py-test-poetry` when `uv.lock` / `poetry.lock` is present | `PIP_CACHE_DIR`, `UV_CACHE_DIR`, `POETRY_CACHE_DIR` |
| Node | `package.json` with a `test` script | `node-test` (`npm`/`pnpm`/`yarn`/`bun` per the lockfile) | `npm_config_cache`, `YARN_CACHE_FOLDER`, subdirectories of `PNPM_HOME`, `BUN_INSTALL`, `~/.yarn` |
| Deno | `deno.json`/`deno.jsonc` | `deno-check`, `deno-test` | `DENO_DIR`, subdirectories of `DENO_INSTALL_ROOT` |
| Java (Maven) | `pom.xml` | `mvn-test` | `~/.m2` |
| Java (Gradle) | `gradlew` | `gradle-test` | `GRADLE_USER_HOME` |
| .NET | `*.sln`/`*.csproj` | `dotnet-build`, `dotnet-test` | `NUGET_PACKAGES`, subdirectories of `DOTNET_CLI_HOME` |
| PHP | `composer.json` with a `test` script | `php-test` | `COMPOSER_HOME`, `COMPOSER_CACHE_DIR` |
| Ruby | `Gemfile` naming rspec, plus `spec/` | `rspec` | subdirectories of `GEM_HOME`, `BUNDLE_USER_HOME` |
| Elixir | `mix.exs` | `mix-compile`, `mix-test` | `MIX_HOME`, `HEX_HOME` |
| Crystal | `shard.yml` plus `spec/` | `crystal-spec` | `SHARDS_CACHE_PATH` |
| Haskell | `*.cabal`/`cabal.project`, or `stack.yaml` | `cabal-build`/`cabal-test`, or `stack-test` | `STACK_ROOT`, subdirectories of `CABAL_DIR` |
| Swift | `Package.swift` | `swift-build`, `swift-test` | `~/.swiftpm`, and `~/Library/Caches/org.swift.swiftpm` on macOS |
| Gleam | `gleam.toml` | `gleam-check`, `gleam-test` | — (uses `XDG_CACHE_HOME`) |
| OCaml | `dune-project` | `dune-build`, `dune-test` | `OPAMROOT` |
| D | `dub.json`/`dub.sdl` | `dub-test` | `DUB_HOME` |
| Dart | `pubspec.yaml` plus `test/` | `dart-test`, or `flutter-test` for a Flutter package | `PUB_CACHE` |
| Racket | `info.rkt` | `raco-test` | `~/.local/share/racket` |
| Clojure | `project.clj` | `lein-test` | `~/.lein`, `~/.m2` |
| Solidity | `foundry.toml` | `forge-build`, `forge-test` | subdirectories of `FOUNDRY_DIR` |
| Lua | `.busted`, a `*.rockspec` with a test section, `.luacheckrc`, `selene.toml` | `busted` or `luarocks-test`; `luacheck`; `selene` | `~/.luarocks` |
| Bats | `*.bats` under `test/` or `tests/` | `bats` | — |
| Emacs Lisp | `Eldev` | `eldev-test` | `~/.eldev` |
| R | `DESCRIPTION` plus `tests/testthat/` | `r-test` | `R_LIBS_USER` |
| Task runners | `Makefile`, `Taskfile.yml`, `justfile`, or a mise config with a `lint`/`check`/`test` target | `make-*`, `task-*`, `just-*`, `mise-*` | — |
| Version managers | — | — | subdirectories of `PYENV_ROOT`, `MISE_DATA_DIR`; `MISE_CACHE_DIR` |

Every path is read from that ecosystem's own environment variable, falling back
to the conventional location, so relocating a cache moves what the sandbox
grants. `XDG_CACHE_HOME` (default `~/.cache`) is granted for everyone, which is
where most of these default anyway — Go's build cache, pip, uv, Deno, Yarn 1,
composer's cache and Crystal's shards all land there, so their variables above
matter only when someone has moved them.

Task runners have no last column because they have no cache of their own. They
run whatever your project told them to, and that command's ecosystem supplies
the paths. mise is in that row for its tasks — `[tasks.test]` or a `test` key
under `[tasks]`, in any of the config files mise reads — and in the row below
for the toolchains it installs.

Version managers are the mirror image: nothing to run, but their toolchains have
to be writable or the first build after an install fails. **A `shims` directory
is never granted**, whether or not it is on `PATH` when Strument starts. Both
pyenv and mise resolve every command through one, so a writable `shims` would
let a model-run command replace the interpreter that every later shell finds.
What is granted is where the toolchains themselves live — `versions/`,
`installs/`, `downloads/`.

**"Subdirectories of"** is not a shorthand. Several toolchains keep a cache and
an executable directory side by side — `~/.cargo` holds `bin/` next to
`registry/`, `~/.bun` holds `bin/` next to `install/`, and pnpm keeps its store
*inside* the directory that is on `PATH`. Granting the root would hand over the
executables, so their contents are granted one subdirectory at a time, minus
anything on `PATH`. `~/go/pkg` is writable; `~/go/bin` is not.

The cost of that is worth knowing: a toolchain that has never run once has no
subdirectories to grant, so its first run inside the sandbox fails. Name the
path you need and it works from then on.

### `project_checks()`

`project_checks()` detects a project's usual checks from its marker files, so
you don't have to write a `check` dict for every repository:

```python
check = project_checks()
```

It is opt-in: nothing happens because a file is on disk, only because you wrote
this. Names are always prefixed with the ecosystem — `go-test`, `node-test`,
`make-test` — so a polyglot repository keeps both suites instead of one quietly
losing to the other. Extend and drop with ordinary dict operations:

```python
check = dict(project_checks(), lint=["golangci-lint", "run"])
```

The root it looks in is the *project's*, not the config file's, so this works in
your user config as well as in a `.strument.star` — write it once and every
project you open gets its own checks.

The ecosystems, their markers, and the paths the sandbox grants each one are
listed under [Language support](#language-support).

Two rules decide what is on that list. Only commands the marker's own toolchain
ships, or ones your project's config names: `go vet` comes with Go, but pytest
does not come with Python, so it appears only where your config declares it. And
where the command runs a target you defined — `make`, `task`, `just`, `npm`,
`composer` — the target has to actually exist, or a detected check would just be
a command that fails for reasons unrelated to your code.

The Python check gets one more gate, in the same spirit: `py-test-uv` and
`py-test-poetry` appear only when `uv.lock` or `poetry.lock` is on disk, the way
the node check reads the lockfile to pick its package manager. `uv run pytest`
on a project that never installed uv would fail for a reason having nothing to
do with your code.

**These are not commands that cannot do harm.** Every one of them runs your
project's own code: `npm test` runs whatever `package.json` says, `make test`
runs your Makefile, `cargo test` compiles and runs your crate. No test runner is
safe in that sense. What they are is commands whose effect is decided by your
project's own committed configuration — which is why this is opt-in, and why it
is worth glancing at what it detected the first time you use it on an unfamiliar
repository. Every check's argv is printed when it runs.

### `check_auto`

`check_auto` lists the verification commands Strument runs *itself*, without being asked, at
the end of a turn that changed files.

```python
check_auto = ["lint", "test"]
```

The names must be keys of `check`; a name that isn't fails at load, so a typo
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
same rule `check` follows: verification commands run in the order they are listed, wherever
they are listed. Nothing runs after a turn that only read files, or under
`--dry-run`, since no edit lands. The `check` tool stays available either way,
so the model can still check something mid-turn.

### `--continue` / `-c`

When starting an interactive session, regenerate the session notes from the
project's existing transcript and load them into context for the new session.
Without it, a session starts clean — no notes in context, no model call, no
cost. The notes live in memory for the session and are never persisted; the next
`--continue` regenerates from a transcript that now includes the full prior
session. `/notes generate` achieves the same thing mid-session at the user's
request. The option does nothing when history is disabled, unavailable, or
empty. Either path prints the same token/cost line a turn ends with, so the
notes call is never an invisible charge.


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

### `max_steps`

The number of work steps the model can take in one turn before Strument pauses
and asks whether to keep going.

```python
max_steps = 25    # the default
max_steps = 50    # for long refactors
max_steps = 10    # for tighter control
```

Each step is the model reading a tool result and carrying on — ordinary
progress. When the limit is reached, Strument prints a summary (steps taken,
files edited, and optionally cost) and prompts **"Keep going?"**. Answering yes
resets the counter and buys another batch; answering no ends the turn with the
work so far already applied and committed.

This is a checkpoint, not a wall. It exists because a long turn should not run
away unnoticed — the user should see what is happening and decide whether to
continue. Setting it high is fine for deliberate long refactors; setting it low
gives more frequent check-ins.

### `max_error_reflections`

The number of times the model can recover from its own errors in one turn
before Strument stops and hands back to the user.

```python
max_error_reflections = 3    # the default
max_error_reflections = 5    # for models that need more retries
```

An error reflection is the model reacting to a tool failure — an `old_string`
that didn't match, a bad shell command — and trying again. It is distinct from
a work step: the model is recovering, not progressing. Keeping the budget small
means a model that is stuck in a fix-break cycle hands back to the human rather
than burning the work-step budget on retries nobody asked for.

### `webfetch_allow`

Origins the `webfetch` tool may fetch without asking you first.

```python
webfetch_allow = [
    "docs.python.org",   # https://docs.python.org/... and http://, nothing else
    "pkg.go.dev",
    "localhost:3000",    # the dev server, and not whatever is on :8080
]
```

**An entry is an origin, not a URL** — no scheme, no path. A URL written here is
refused at load rather than left to silently never match.

Matching is exact, and it includes the **port**. An entry without one covers
only the defaults, 80 and 443; `localhost:3000` covers only that port. This is
the setting's most useful property on localhost, where a dev server and
whatever else happens to be listening have nothing to do with each other, and
it is why `webfetch_allow = ["localhost"]` will not silently admit
`localhost:8080`. Write `example.com:443` to insist on https, since a bare
entry admits the plaintext port too.

Subdomains are not covered. `example.com` does not admit `docs.example.com`,
and there is no wildcard. On `*.github.io`, `*.pages.dev`, and
`*.s3.amazonaws.com` the subdomain is whoever signed up, so a rule that
admitted them would hand an attacker the host you vouched for.

**It says which fetches skip the prompt, not which are reachable.** Strument
will not pretend this is a network boundary: `bash` can `curl` anywhere, and
the sandbox confines the filesystem rather than the network. What the list
buys is fewer questions about the hosts you read from every day. (If Strument
ever gains network confinement, a restricting form of this setting becomes
honest and can be added then.)

A trusted project config replaces the user's list rather than adding to it,
like `env_allow` and for the same reason: this is one decision about which
hosts stop being asked about, and merging two lists could only ever widen it.

### `loop_detection`

Whether to stop a reply that has degenerated into repeating itself.

```python
loop_detection = True     # the default
loop_detection = False    # off
```

Some models — small ones especially — get stuck emitting one sentence over and
over until the context fills. It does not resolve on its own the way a repeated
*tool call* usually does, so Strument watches the streamed text and stops the
reply when a fifty-character window has recurred ten times at close spacing, or
when one word has repeated thirty times running. The answer and the reasoning
are watched separately; in practice it is nearly always the reasoning.

Stopping looks like an interrupt without the Ctrl-C: the partial reply stays in
the chat, Strument tells the model what repeated and to take another approach,
and you are asked whether to stop, let it try again, or steer it with a message
of your own.

Turn it off if your model's ordinary output trips it — generated tables and
fixture data are the plausible cases. Nothing else changes.

### `shell_timeout`

Seconds a single model-caused command may run before Strument stops it.
Defaults to 120. `0` means no limit.

```python
shell_timeout = 600   # a slow integration suite
shell_timeout = 0     # no limit
```

It applies to the `bash` tool and to `check` commands — everything the *model*
can cause to run. **`/run` is exempt**: the user typed that command and may well
have meant the twenty-minute build.

When the deadline stops a command, the tool result says so in those words. That
matters more than it looks: a command killed at the deadline and one that failed
on its own are otherwise indistinguishable to the model, and the obvious next
move after an unexplained failure is to start changing code.

A timeout is not a resource limit. It bounds how long a runaway command wastes,
not what it can do while running.

### `git_sign`

Sign the commits Strument makes with Git's own signing, passed through as
`git commit -S`:

```python
git_sign = True         # sign with the default key: `git commit -S`
git_sign = "ABC123"     # sign with that key: `git commit -SABC123`
```

`False` (the default) and an empty string leave commits unsigned. A string is
always used as the key id, so write the key alone — Strument adds the `-S`.
Everything else about signing is Git's domain: which key `-S` uses is decided by
`user.signingkey`. As with any `git commit`, a failure to sign makes the turn's
commit fail; the edits stay in the working tree, where `/undo` still reaches
them through the turn's snapshot.

### `env_allow`

Commands the model causes to run — the `bash` tool, the `check` tool, the
`scraper` command — do not inherit your whole environment. They get an
allowlist: the variables that make builds and tests work (`PATH`, `HOME`,
`LANG` and the `LC_*` family, `TZ`, `TMPDIR`, the XDG locations, the standard proxy
variables and `SOCKS5_SERVER`, and the non-secret knobs of the common
toolchains — the `GO*` family, `CARGO_HOME`, `JAVA_HOME`, `VIRTUAL_ENV`, and
a handful of others). Everything else is withheld, so a
model-run `env`, or a failing test that prints its environment, cannot carry
`OPENROUTER_API_KEY` — or any other credential — into a tool result, the
transcript, and the model's context.

What decides the filtering is who caused the command, not which command it is.
Commands *you* type inherit your whole environment — `/run`, and `/check`,
which runs the same named checks the model's `check` tool does but under your
own environment because you asked for it.

`env_allow` adds names on top of the default list:

```python
env_allow = ["HF_TOKEN", "MY_SERVICE_ENDPOINT"]
```

Matching is exact: `FOO_` does not admit `FOO_BAR`. The value always comes from
the real environment at run time — a name containing `=` fails the load, so the
config carries names only, never values.

Adding a credential-shaped name is allowed and deliberate. There is no filter
that rejects `HF_TOKEN` on shape: a hard one would just push you toward writing
the token to a file, which is worse than passing it. What the allowlist buys is
that exposure has to be *written down* — one line per variable, visible in the
config and in a `.strument.star` you had to trust.

For ad-hoc, session-scoped changes the REPL has `/env`: `/env` shows the
effective allowlist by origin (defaults, config, session changes), `/env add`
and `/env drop` change it until the session ends (Tab completes variable names:
add offers set variables not yet allowed, drop offers what is), and `/env
reset` returns to the config's list. Nothing is persisted, values are never
displayed, and `/reload` discards session changes — the config is the source
of truth. To make a `/env add` permanent, add the name to `env_allow`.

A project's `.strument.star` **replaces** the user's `env_allow` whole-value,
for the same reason it replaces `check_auto`: a merge of two lists could only
widen, and the project needs to be able to narrow.

Two things are untouched by the allowlist. `/run` keeps the full environment,
because you typed that command yourself. And the API keys Strument itself uses
(`api_key=env(...)` in this file) are read at load time and never re-exposed
through it.


### `env_set`

Sets environment variables for the session:

```starlark
env_set = {"TZ": "Europe/Kyiv"}
```

The classic case is a time zone. Commit in your own zone on a UTC server, or in
UTC on a laptop that is not. `TZ` is the example, but anything works — `GOFLAGS`,
`RUST_BACKTRACE`, a tool's cache directory.

The variables are set on Strument's own process at startup, so everything it
starts inherits them: `git`, `/run`, and the model's commands. **`env_set` does
not widen what the model sees.** A name set here still has to pass
[`env_allow`](#env_allow) to reach a model-run command, so a value written in a
config file is not handed to the model by accident.

Values are strings, and they do not have to be literals. To pass something you
would rather not write down, read it from the environment:

```starlark
env_set = {"GH_TOKEN": env("GITHUB_TOKEN")}
```

That renames a variable without the secret ever appearing in the file. Prefer it
to typing a credential into a config: files get committed, backed up, and read
over shoulders in a way an environment variable does not.

A project's `.strument.star` may set variables too, and unlike `env_allow` the
two configs merge per entry — a project naming `TZ` does not drop your
`GOFLAGS`. A project config only takes effect once you have run `strument
trust`, which is the same gate its `check` commands sit behind.

#### `TZ`, and what it does reach

Setting `TZ` is not by itself enough to move Strument's own clock, and for
different reasons on different systems: Go reads `TZ` once on Unix, the first
time anything formats a time, and never reads it at all on Windows. Strument
therefore sets its zone directly rather than signalling it, so a `TZ` in
`env_set` governs the date in the prompt and the timestamps on transcript turns
on every platform. Files it writes for itself — the resume, undo, and cost
records — stay in UTC, which is what they were always written in.

Give a zone database name, like `Europe/Kyiv` or `UTC`. A POSIX string carrying
its own rules (`EST5EDT4,M3.2.0/2`) is not one, and neither is a typo; either
way Strument says so at startup instead of quietly using the wrong zone. The
database is compiled into the binary, so a name means the same thing on a
machine that has no zone files of its own.

`env_set` changes need a restart; `/reload` does not re-apply them.

### `sandbox`

Which confinement mechanism to apply to the session. `"landlock"` or `""`:

```python
sandbox = "landlock"   # the default on Linux
sandbox = ""           # off; the default everywhere else
```

There is no boolean and no `"auto"`, because "sandboxed" is not one thing —
naming the mechanism keeps the setting honest when there is a second one.
Anything other than those two values fails the load rather than falling back,
since the whole value of this setting is knowing whether you are confined.

The default is the rule you would write yourself, and you can write it: the
`platform` value is available, so `sandbox = "landlock" if platform.system ==
"Linux" else ""` is exactly the built-in behavior spelled out.

With it on, Strument applies a [Landlock](https://landlock.io/) ruleset to its
own process at startup. Reading and executing are unrestricted anywhere on the
filesystem; writing is permitted only under a derived set of paths:

- the project root, including `.git`, and the real git directory when `.git` is
  a file (a worktree or a submodule)
- the session's state directory under `$XDG_STATE_HOME/strument`
- `TMPDIR`, and `/tmp` besides
- this machine's toolchain caches — the last column of
  [Language support](#language-support) above
- `/dev/null` and the other harmless devices, plus your terminal
- everything in `sandbox_write`

Landlock is inherited across `fork`/`exec` and cannot be undone, so the `bash`
tool, `check` commands, the `scraper` command, and every process they start are
confined by the same policy. **So is `/run`**, even though you typed it — there
is no call that removes a ruleset. That is the accepted cost of confining the
process instead of each command.

`/sandbox` in the REPL prints the effective writable set for the session. It
cannot be changed mid-session; edit the config and restart.

**On a kernel without Landlock**, `sandbox = "landlock"` does not proceed
unsandboxed. Strument starts, and reading, editing and committing work, but
everything the model can cause to execute refuses with one line naming this
setting. `/run` still works. Off Linux, the setting must be `""` — there is no
mechanism to fall back to.

What this buys is integrity, not confidentiality: writes are confined, reads
are not. [`doc/security.md`](security.md) is the full account, including the
places the policy is deliberately loose.

### `sandbox_write`

Extra absolute paths the sandbox may write to:

```python
sandbox_write = [
    "/srv/scratch",
    env("HOME", "") + "/.local/share/my-tool",
]
```

Paths must be absolute — a relative one would depend on where Strument was
started, which is not a property a security decision should have. A path that
does not exist is skipped rather than failing the load, so a list can name a
cache directory that is not there yet.

Reach for this when a denial names a path outside the derived set. The two
common cases are a toolchain that has never run on this machine, so its cache
directory does not exist yet and there was nothing to grant, and a project that
writes somewhere genuinely outside itself.

Granting a directory grants everything under it, so grant the narrowest thing
that works. In particular, do not grant a directory on your `PATH`: the derived
set excludes those on purpose, since a writable `bin/` is a program that runs
as you the next time you type its name.

A project's `.strument.star` **replaces** the user's `sandbox_write`
whole-value, like `env_allow` and for the same reason: merging could only
widen, and a project needs to be able to narrow.

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

### `model(provider, slug, *, display_name=None, edit_format="tool", side_model=None, reasoning=None, reasoning_tag=None, temperature=None, repo_map=True, cache=False, context=None, max_output=None, input_cost=None, output_cost=None, extra_params={})`

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
- **`side_model`** — the model for Strument's own writing: commit messages,
  session notes, and compaction summaries. An alias string or an inline
  `model()`; unset means the main model does its own. These are *side requests*
  — prose about the session rather than work on your code — which is where the
  name comes from, and why a cheaper model usually belongs here.

  It was called `weak_model` before, after aider. The name made a claim about
  capability that stopped being true: the model most often put in this seat now
  is a near-peer of a frontier one. A config still using `weak_model` gets an
  error naming the new key.
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
  inside `max(context/8, 2048)` tokens. Without it a long session
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

## Built-in values

### `platform`

Read-only facts about the machine, for a config that has to branch on the OS:

```python
sandbox = "landlock" if platform.system == "Linux" else ""
```

It imitates CPython's `platform` module rather than Go's vocabulary, because a
Starlark config is a Python dialect and the shape you reach for is the Python
one. So the values are the ones CPython would give you, not the ones Go uses
internally:

| attribute | example | source |
| --- | --- | --- |
| `platform.system` | `"Linux"`, `"Darwin"`, `"Windows"` | capitalized, never Go's `"linux"` |
| `platform.machine` | `"x86_64"`, `"aarch64"` | `uname` on Unix, never Go's `"amd64"` |
| `platform.bits` | `"64bit"` | pointer width |
| `platform.node` | `"houdini"` | hostname, `""` if unreadable |
| `platform.release` | `"6.18.5"` | `uname` release; `""` without `uname` |
| `platform.version` | `"#1 SMP ..."` | `uname` version; `""` without `uname` |

Writing `platform.system == "linux"` and having it silently never match is the
mistake the capitalization exists to prevent.

CPython's `platform()` and `processor()` are **absent** rather than
approximated. The first is a composite string assembled differently on every OS
with no faithful translation; the second is empty even in CPython on Linux and
comes from places Go cannot portably reach. Referring to either is an error you
see when you edit the config, which is better than a plausible wrong value you
ship.

The attributes are read-only, and `platform` is not a function — these are facts
about the host, not a computation.

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

## `strument config`

Two subcommands read the *effective* config — the merge of your user config and
a trusted project `.strument.star`, resolved for the current project exactly as
a chat session resolves it:

```sh
strument config models    # the keys of `models`, one per line, sorted
strument config default   # the value of `default`
```

The output is plain text on stdout, one line per model alias, so it composes
with scripts and pipelines. `models` is sorted alphabetically, not in config
declaration order, so a script can rely on the order across edits.

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
        # side_model="...",  # Uncomment to use a cheaper model for summaries and commits.
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
