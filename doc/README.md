# Strument — developer docs

This is the high-level map for people hacking on Strument. For user-facing
docs (install, configuration, what differs from aider) see the top-level
[`README.md`](../README.md).

Strument started life as a close reverse-engineering of
[aider](https://github.com/Aider-AI/aider) at commit `5dc9490`, driven by a
set of written specifications. Those port specs and their journal have since
been retired — the code is now the source of truth, and Strument follows its
own direction: closer to aider in some places, further in others. A read-only
clone of aider is still handy when you want to compare (`task setup:reference`
drops it in a gitignored `reference/`).

## Relationship to aider

- **Scope.** Essentials only: SEARCH/REPLACE (plus fenced and whole-file)
  edits, repo map, reflection, shell suggestions, git auto-commit with
  `/undo`, `/ask`. Architect mode, voice, GUI, analytics, and summarization
  are out of scope for v1.
- **One dialect.** A single OpenAI-compatible client with OpenRouter
  extensions replaces litellm; Starlark `config.star` replaces layered
  YAML/`.env`/model-database configuration.
- **Where we differ.** Some behavior is deliberately not aider's — atomic
  batch writes with rollback, a single pure apply planner shared by dry and
  real runs, usage accounting that survives an aborted turn, an in-chat-file
  exemption on path containment. When you diverge, say why in the code
  comment and the commit message.
- **Borrowed material.** The tree-sitter tag queries under
  `internal/repomap/queries*/` are copied from aider. The built-in prompts in
  `internal/prompts/` began as aider's and are now ours to change; a test
  pins their hashes so a change is always deliberate, not accidental.

## Codebase structure

- `cmd/`
  - `strument/` — the CLI: kong command wiring, config load, REPL/script
    dispatch.
  - `strumentrec/` — dev-only fixture recorder: a reverse proxy that logs
    both directions of an OpenAI-compatible exchange verbatim.
- `internal/`
  - `coder/` — the orchestration spine: assemble → stream → reflect →
    apply → shell → commit → cost. Its seams with the outside world are
    interfaces in `ports.go` (see below).
  - `editblock/` — the pure edit-format engine: SEARCH/REPLACE parsing,
    Python-`difflib` sequence matching, whole-file parsing, and a pure
    apply planner (`ApplyEdits`) shared by dry runs and real runs.
  - `llm/` — wire-neutral chat types (messages, stream events, usage,
    money) shared by the client, the coder, and the fixture harness.
  - `client/` — the one HTTP client: OpenAI-compatible chat completions,
    OpenRouter dialect, SSE parsing. Tests stub `Transport`; the suite
    never opens a socket.
  - `config/` — the Starlark configuration surface (`provider()`,
    `model()`, `env()`) and the direnv-style trust gate for project
    configs.
  - `repomap/` — ranked repo map: tree-sitter tag extraction (pure-Go
    grammars via gotreesitter), personalized PageRank, token-budgeted
    rendering. `queries/` and `queries-legacy/` hold the aider `.scm`
    files.
  - `prompts/` — the built-in prompt sets.
  - `render/` — streaming markdown renderer (a Go port of
    thetarnav/streaming-markdown) plus the ANSI terminal renderer and the
    color `Theme`.
  - `repl/` — the interactive layer: ergochat/readline, slash commands,
    double-Ctrl-C chords, live streaming render, chat/input history
    wiring.
  - `gitrepo/` — the git port; always argv, never a shell string.
  - `history/` — per-project markdown chat transcripts under
    `$XDG_STATE_HOME/strument`.
  - `fixture/` — the record/replay harness: JSON-Lines scenarios and
    replay stubs for the coder's ports.
- `script/` — release build, the grammar build-tag list, and
  `setup-reference.sh`.
- `testdata/` — distilled scenario fixtures and tests transliterated from
  aider's suite.
- `reference/` — a gitignored clone of aider at commit `5dc9490`; a
  read-only grep target for comparing against upstream
  (`task setup:reference`).
- `attic/` — local capture artifacts; gitignored, never committed.

## The coder's ports

The coder talks to the world only through small interfaces in
`internal/coder/ports.go`; every one has a production implementation and a
test stub. If you need new outside behavior, extend a port rather than
reaching around it.

| Port | Purpose | Production / test |
|---|---|---|
| `llm.ModelClient` | one streaming send | `client.Client` / `fixture.StreamStub` |
| `Output` | user-facing printing + live stream | `repl.termOutput`, `StdOutput` / test buffers |
| `Confirmer` | y/n/don't-ask questions | readline confirmer wrapped in `AutoConfirmer` |
| `CommandRunner` | `/run` and suggested shell commands | `PipeRunner` / replay stub |
| `Repo` | git operations | `gitrepo.Repo` / nil (no-git mode) |
| `TokenCounter` | advisory token estimates | `RuneCounter` (runes/4, measured) |
| `Clock` | retry backoff sleeps | `RealClock` / instant fake |

The REPL has the same philosophy: `repl.Options` exposes seams
(`Stdin/Stdout`, `IsTerminal`, `MakeRaw/ExitRaw`, `Notify`, `Exit`, `Now`,
`GetSize`) so the whole interactive loop runs under tests over pipes and a
real pty.

## Configuration

A single user `config.star` (`$XDG_CONFIG_HOME/strument/config.star`)
declares providers and models; a project-local `.strument.star` can extend
it but is **inert until trusted** (`strument trust`, content-hash gated,
direnv-style). The `README.md` has a worked example covering providers,
model factories, `with_extra_params`, and aliases.

## Testing

- `go test ./...` runs everything without network, sockets, or API keys.
- **Fixtures**: model behavior is replayed from JSON-Lines scenarios
  (`testdata/fixtures/`), recorded through `strumentrec` and distilled by
  hand. The fixture loader fails loudly on schema-version mismatches.
- **Transliterated tests**: aider's own unit tests, ported file-by-file
  (`testdata/transliterated/`, `internal/editblock/editblock_test.go`).
- **REPL tests**: scripted sessions over pipes in readline's
  non-interactive mode, plus a real-pty round trip that answers the
  cursor-position query itself.
- **Pinning tests**: prompt hashes, embedded-query compilation, the
  grammar-tag list, and the safety-critical behaviors (`rollback_test.go`,
  `unsafepath_test.go`, `usage_test.go`) have tests that fail if the
  invariant drifts.

## Common tasks

### Adding a slash command

1. Add a `command` row to the table in `internal/repl/commands.go` (name,
   argument placeholder, help text, `run` function). The table drives
   `/help` and tab completion automatically.
2. `run` returns the message to send to the model, or `""` for
   commands that only mutate state; use `r.out` for output so colors and
   `--no-color` behave.
3. Extend the scripted-session test in `internal/repl/repl_test.go`.

### Adding a repo-map language

1. Drop the aider `.scm` into `internal/repomap/queries/` (pack) or
   `queries-legacy/` (legacy fallback).
2. Map extensions in `extToLang` (`internal/repomap/lang.go`); check the
   gotreesitter registry name.
3. `TestAllEmbeddedQueriesCompile` must pass — gotreesitter rejects some
   query constructs the upstream `.scm` files use, so extend
   `preprocessQuery` only with care.
4. Regenerate `script/grammar-tags.txt` (a test keeps it in sync) and add
   a fixture row to the language matrix test.

### Adding a `model()` or `provider()` parameter

1. Extend the struct in `internal/config/types.go` and the builtin in
   `builtins.go` (`starlark.UnpackArgs`).
2. Update the config example in `README.md`.
3. Add a parse test in `internal/config/`.

## Verification workflow

1. `go build ./cmd/strument` — full build, every bundled grammar; no cgo.
2. `go test ./...` and `go vet ./...`.
3. `task lint` (`golangci-lint run`) at zero issues; `task format` before
   committing.
4. For repo-map or release work: `task build:strument:subset` builds the
   release variant against `script/grammar-tags.txt`.
5. To compare against upstream: `task setup:reference` clones aider at
   commit `5dc9490` into `reference/`.
