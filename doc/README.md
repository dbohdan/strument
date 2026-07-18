# Strument — developer docs

This is the high-level map for people hacking on Strument. It stays
deliberately short: the authoritative material lives in the frozen
specifications under [`spec/`](../spec/) and the running journal
[`STATUS.md`](../STATUS.md). When this document and a spec disagree, the
precedence rule from [`spec/strument-guide.md`](../spec/strument-guide.md)
applies (guide over subsystem spec); file conflicts as numbered STATUS
entries rather than silently picking a side.

## Compatibility with aider

Strument is a ground-up Go port of [aider](https://github.com/Aider-AI/aider)
at pinned commit `5dc9490`, reverse-engineered into the specs and then
implemented against them. Where behavior diverges, it is on purpose and on
the record:

- **Scope.** Essentials only: SEARCH/REPLACE (plus fenced and whole-file)
  edits, repo map, reflection, shell suggestions, git auto-commit with
  `/undo`, `/ask`. Architect mode, voice, GUI, analytics, and summarization
  are out of scope for v1 (guide §2).
- **One dialect.** A single OpenAI-compatible client with OpenRouter
  extensions replaces litellm; Starlark `config.star` replaces layered
  YAML/`.env`/model-database configuration.
- **Declared deviations.** Behavioral divergences (atomic batch writes,
  dry-run semantics, usage accounting, path containment, and so on) are
  numbered D1, D2, … in the `## Deviations` register in `STATUS.md`. Read it
  before assuming aider parity; extend it when you diverge.
- **Parity surfaces.** Prompt strings and tree-sitter tag queries are
  `[Exact]`: copied verbatim from aider and hash-pinned in tests. Do not
  hand-edit them.

## Codebase structure

- `cmd/`
  - `strument/` — the CLI: kong command wiring, config load, REPL/script
    dispatch.
  - `strumentrec/` — dev-only fixture recorder: a reverse proxy that logs
    both directions of an OpenAI-compatible exchange verbatim
    (fixture-harness-spec §1).
- `internal/`
  - `coder/` — the orchestration spine: assemble → stream → reflect →
    apply → shell → commit → cost (basecoder-spec). Its seams with the
    outside world are interfaces in `ports.go` (see below).
  - `editblock/` — the pure edit-format engine: SEARCH/REPLACE parsing,
    Python-`difflib` sequence matching, whole-file parsing, and a pure
    apply planner (`ApplyEdits`) shared by dry runs and real runs
    (editblock-spec).
  - `llm/` — wire-neutral chat types (messages, stream events, usage,
    money) shared by the client, the coder, and the fixture harness.
  - `client/` — the one HTTP client: OpenAI-compatible chat completions,
    OpenRouter dialect, SSE parsing. Tests stub `Transport`; the suite
    never opens a socket.
  - `config/` — the Starlark configuration surface (`provider()`,
    `model()`, `env()`) and the direnv-style trust gate for project
    configs (config-schema).
  - `repomap/` — ranked repo map: tree-sitter tag extraction (pure-Go
    grammars via gotreesitter), personalized PageRank, token-budgeted
    rendering (repomap-spec). `queries/` and `queries-legacy/` hold the
    verbatim aider `.scm` files.
  - `prompts/` — aider prompt sets, mechanically extracted and
    hash-pinned.
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
    replay stubs for the coder's ports (fixture-harness-spec).
- `spec/` — the frozen specifications. `strument-guide.md` is the plan and
  the precedence root.
- `script/` — release build, the grammar build-tag list, and
  `setup-reference.sh`.
- `testdata/` — distilled scenario fixtures and tests transliterated from
  aider's suite.
- `reference/` — a gitignored clone of aider at the pinned commit; a
  read-only grep target for spec verification (`task setup:reference`).
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
direnv-style). The full surface — three builtins, value semantics, the
trust store, `extra_params` passthrough — is specified in
[`spec/config-schema.md`](../spec/config-schema.md).

## Testing

- `go test ./...` runs everything without network, sockets, or API keys.
- **Fixtures**: model behavior is replayed from JSON-Lines scenarios
  (`testdata/fixtures/`), recorded through `strumentrec` and distilled by
  hand. The fixture loader fails loudly on schema-version mismatches.
- **Transliterated tests**: aider's own unit tests, ported file-by-file
  (`testdata/transliterated/`, `internal/editblock/editblock_test.go`),
  keep the parity surfaces honest.
- **REPL tests**: scripted sessions over pipes in readline's
  non-interactive mode, plus a real-pty round trip that answers the
  cursor-position query itself. The REPL's final oracle is
  hand-validation against aider's feel (guide §5); automate what is
  automatable, note the rest in STATUS.
- **Pinning tests**: prompt hashes, embedded-query compilation, the
  grammar-tag list, and the safety-critical deviations (`rollback_test.go`,
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

1. Drop the verbatim aider `.scm` into `internal/repomap/queries/` (pack)
   or `queries-legacy/` (legacy fallback).
2. Map extensions in `extToLang` (`internal/repomap/lang.go`); check the
   gotreesitter registry name.
3. `TestAllEmbeddedQueriesCompile` must pass — gotreesitter rejects some
   upstream query constructs; extend `preprocessQuery` only with care
   (see Deviation D2).
4. Regenerate `script/grammar-tags.txt` (a test keeps it in sync) and add
   a fixture row to the language matrix test.

### Adding a `model()` or `provider()` parameter

1. Extend the struct in `internal/config/types.go` and the builtin in
   `builtins.go` (`starlark.UnpackArgs`).
2. Document it in `spec/config-schema.md`.
3. Add a parse test in `internal/config/`.

## Verification workflow

1. `go build ./cmd/strument` — full build, every bundled grammar; no cgo.
2. `go test ./...` and `go vet ./...`.
3. `task lint` (`golangci-lint run`) at zero issues; `task format` before
   committing.
4. For repo-map or release work: `task build:strument:subset` builds the
   release variant against `script/grammar-tags.txt`.
5. For spec verification against upstream: `task setup:reference` clones
   aider at the pinned commit into `reference/`.
6. Record what changed — including any new deviation — in `STATUS.md` as
   part of the same commit series.
