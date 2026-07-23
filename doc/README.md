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

## Philosophy

The positions below are the ones worth knowing before you change anything
substantive. They are the project's own, arrived at deliberately — not
inherited from aider.

- **A propose/direct-apply tool, not an agent.** Strument is in the aider
  lineage: the model responds, the harness acts, the human drives the next
  turn. There is no self-continuing loop where the model calls tools, reads
  results, and keeps going on its own. Concretely: **file edits apply
  directly** (with git auto-commit and `/undo` as the safety net); **shell
  commands are suggested** and only run after the user confirms; **files are
  added on request**, not reached for. Keep that shape. Adding an autonomous
  loop would make it a different program.

- **The code is the source of truth.** The written port specs and their
  journal are retired. Don't re-introduce a parallel spec that the code must
  "conform to"; document decisions in the code, the commit, and these docs.
  Where we differ from aider, that is on purpose — say why in the comment.

- **Tool calls are the default edit path.** Every model in scope has solid
  function calling, so edits, shell suggestions, and file requests go through
  native tool calls; text SEARCH/REPLACE and whole-file are the fallback for
  weaker models. Beyond reliability, tool calls remove the SEARCH/REPLACE
  delimiter-collision problem — a file that itself contains `<<<<<<< SEARCH`
  is just data — which is what makes the harness usable on its own source,
  including its prompt strings. The user still sees code scroll by, rendered
  as red-green Git-style diffs.

- **Prompts you'd hand a competent colleague.** Calm, specific, one clear
  statement per rule; explain the *why* where a mid-size model benefits and a
  frontier model doesn't mind. No shouting, no pre-escalation, no manufactured
  stakes. For this model class (floor ~27B, up to frontier) the
  welfare-respecting register and the performance-maximizing one are the same
  register; a single prompt set serves all of them.

- **Small, honest, self-contained.** One static binary, no cgo. One
  OpenAI-compatible dialect (OpenRouter). Starlark configuration behind a
  direnv-style trust gate. Never fabricate cost or token counts; mark
  estimates as estimates. Git is the safety model: auto-commit each edit,
  `/undo`, atomic batch writes that roll back on failure, path containment.
  Never commit secrets.

## Relationship to aider

- **Scope.** Essentials only: tool-call edits by default (SEARCH/REPLACE,
  fenced, and whole-file as fallbacks), repo map, reflection, shell
  suggestions, git auto-commit with `/undo`, `/ask`. Architect mode, voice,
  GUI, analytics, and summarization are out of scope for v1.
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
  - `repl/` — the interactive layer: line editing (via `readline/`), slash
    commands, double-Ctrl-C chords, live streaming render, chat/input
    history wiring.
  - `readline/` — the terminal line editor: a vendored fork of
    ergochat/readline (MIT, taken at v0.1.3) with a flicker-free single-write
    redraw adapted from jart/bestline and Ctrl+arrow word motion. Kept in
    upstream style and excluded from Strument's linters; see its `NOTICE`.
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

## Tool calls (the default edit format)

The default `edit_format` is `"tool"`: the model edits, suggests commands,
and asks for files through native function calls instead of text blocks. The
API schema enforces the format, so the whole class of format-parse failures
disappears, the prompts shrink (the schema carries the rules), and a file
that contains `<<<<<<< SEARCH` is just data — which is what lets the harness
edit its own prompt strings. `diff`, `diff-fenced`, and `whole` remain
selectable per model as the fallback for a model with weaker function
calling.

Four tools, in two shapes that match the harness's nature:

- **`replace_in_file(path, search, replace)`** and **`create_file(path,
  content)`** — *direct*. Exactly like a SEARCH/REPLACE block: the edit
  applies and auto-commits the moment the call arrives, with `/undo` as the
  safety net. Not a proposal. `create_file` writes the whole file — creating
  it, or fully overwriting an existing one (the outcome line and tool result
  say which) — so a total rewrite doesn't need a hunk diff.
- **`suggest_command(command, purpose)`** — a *proposal*. Runs only after the
  user confirms (the existing run-shell gate); its output returns as the tool
  result.
- **`request_files(paths, reason)`** — a *request*. The user confirms each
  file before it joins the chat.

The tools live in `internal/coder/tools.go`; `applyToolCalls` dispatches a
captured turn. Every tool call gets a `tool` result message, always, so the
next request stays well-formed. Edits reuse the same replace primitive,
atomic-write, and auto-commit machinery as the SEARCH/REPLACE path — the tool
format changes how edits *arrive*, not how they apply.

**Reflection is a tool error, not a synthetic user turn.** When a `search`
doesn't match, its call's tool result carries the failure (with a
did-you-mean) and the turn re-sends on those results — no injected "please
fix" user message. `runOne` is outcome-driven so a text-free tool reflection
still loops, bounded by `maxReflections`.

**"Code scrolls by" with tool calls.** Providers stream a call's arguments as
JSON-escaped string fragments, so raw rendering would show escaped JSON.
`internal/render/toolargs.go` decodes them live: `ArgScanner` is a streaming
JSON string-field extractor (escape- and UTF-8-boundary-safe) and `ToolDiff`
turns the decoded `search`/`replace`/`content`/`command` fields into a
red-green Git-style diff as they arrive. `ToolDiffSet` fans a turn's calls
out by index. There are two decoders by design: `ArgScanner` for streaming
*display* (best-effort) and `json.Unmarshal` on the complete arguments for
the authoritative *apply*.

### Cross-provider streaming quirks

Tool-call *edits* work across the current model field — but the way providers
*stream* a call's arguments diverges, so the clean, uniform UX is the
renderer's doing, not the wire's. A 16-model live sweep (via OpenRouter, one
`replace_in_file` edit + one `suggest_command` + one `create_file` per model)
made this concrete. Fourteen models drove all three tools cleanly and rendered
byte-identical canonical diffs. The two that stumbled did so only on the *edit*
— they still handled `suggest_command` and `create_file` — and both were the
smallest, most reasoning-heavy models (gpt-oss-120b, qwen3-14b), which spent a
modest output budget thinking and never emitted the call. The ~27B floor holds.

Underneath that uniform surface, the wire order of an edit's JSON fields is
all over the map:

| Wire order of `replace_in_file` fields | Models |
| --- | --- |
| `path, search, replace` (schema order) | Claude Haiku 4.5 / Opus 4.8 / Sonnet 5, Cohere North-mini-code, Kimi K3, Laguna-S-2.1, Step-3.7-flash, MiMo v2.5 |
| `path, replace, search` (replace first) | Gemma-4-31b, MiniMax-M3, Kimi K2.6, GPT-5.6-sol, Inkling |
| `replace, search, path` (path last *and* replace first) | Gemini 3.5 Flash |

Only eight of the fourteen keep schema order. Gemini streams `path` **last**,
so nothing in the arguments even names the file until the end. The renderer
absorbs all of it — every row above renders header-first, removed lines above
added — because it never assumes field order. Three display-only
normalizations, each with a regression test in `toolargs_test.go`:

- **`replace` before `search`.** Held added (`+`) lines wait until the removed
  (`-`) lines are known, so a diff always reads in canonical git order. (Seen
  first with GLM 5.2; confirmed on six models in the sweep, Gemini included.)
- **`path` not first (Gemini, Qwen3.6).** An edit's diff lines are buffered
  until the `path`/header resolves, then the header leads.
- **Interleaved calls (DeepSeek).** With two calls in one turn, fragments
  arrive interleaved; `ToolDiffSet` streams the first call live and buffers
  later ones, appending each whole in first-seen order. (Single-call sweep
  turns didn't re-trigger this; the regression test stands.)

The authoritative `json.Unmarshal` parse is order-independent, so none of this
touches correctness — only display. The lesson worth keeping: **field order
and fragment contiguity are provider-specific and not guaranteed; a streaming
renderer must assume neither.** The per-hunk `replace_in_file` shape itself
needed no change — every capable model produced well-formed single-hunk calls
(some with more surrounding context than others), so batching stays
unnecessary.

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
