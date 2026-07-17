# Strument port — STATUS

## Current phase
Phase 8 — git mode — started 2026-07-17

### Phase 7 — REPL — done (code + automated slice); **hand-validation by the human pending**
- Oracle: hand-validate vs aider's feel (collab). The automatable slice is
  green: scripted pipe sessions, both double-Ctrl-C chords, and a real-pty
  round trip (prompt → /ls → rendered turn → ^C^C exit).
- Started: 2026-07-16
- Finished (code): 2026-07-17
- Deviations: none. One planned-tooling note: the plan named creack/pty +
  go-expect for automation; creack/pty alone sufficed (the pty test answers
  readline's cursor-position query ESC[6n itself), so go-expect was
  dropped by `go mod tidy` rather than kept unused.
- Notes:
  - `internal/repl`: ergochat/readline v0.1.3 behind seams (Stdin/Stdout,
    IsTerminal, MakeRaw/ExitRaw, Notify, Exit, Now) so tests can drive it
    over pipes and a pty. `strument` (no --message) now starts the REPL;
    `--no-color` and NO_COLOR are honored; input history lands next to the
    trust store ($XDG_STATE_HOME/strument/history).
  - §1.2 chords: one shared 2s window like aider's last_keyboard_interrupt.
    At the prompt, readline clears the line, we print "^C again to exit",
    a second ^C within 2s returns cleanly. During a turn, SIGINT cancels
    the send context (the coder's §2.11 interrupt shape takes over) and a
    second within 2s exits 130 via the injected Exit.
  - §1.4 dispatch: slash commands run before preproc and return either a
    message to send or ""; the table: /add /read-only /drop /ls /clear
    /reset /tokens /model /map /run /help /exit /quit, plus /undo /diff
    stubs that point at phase 8. /run reuses the §6.3 result shape and
    appends {user,result}+{assistant,"Ok"} on confirm, like §6.2 output.
    Tab completion covers commands, addable files, chat files, and model
    aliases.
  - Live render: coder.Out streams answer deltas through
    render.Parser+ANSI (phase 6); reasoning deltas print dim and unparsed
    under a "· thinking ·" header. Confirms route through readline
    (ReadLineWithConfig on a cloned config) so prompt and confirm input
    share one reader; aider's defaults kept (Enter=yes, except
    explicit-yes prompts).
  - New REPL-facing coder surface in `internal/coder/session.go`:
    ChatFiles/ReadOnlyFiles/DropFile/DropAll/ClearHistory/AppendExchange/
    SetModel/LastCommitHash/RepoMapNow/TokensReport.
  - **For the human to hand-validate** (guide §5 collab): REPL feel vs
    aider — prompt texture, streaming render pacing, ^C behavior, confirm
    wording. `go build ./cmd/strument && ./strument` in a repo with a
    config.star. Proceeding to phase 8 per the plan while this waits.

### Phase 6 — render (smd.js port) — done
- Oracle: smd.js's own test suite, ported — green (435 cases × 2 modes)
- Started: 2026-07-16
- Finished: 2026-07-16
- Deviations: none.
- Notes:
  - Upstream cloned to `attic/smd` (thetarnav/streaming-markdown @ HEAD,
    MIT; credited in README). The 435 `test_single_write` invocations in
    `smd_test.js` were extracted verbatim to
    `testdata/transliterated/render/smd-cases.json` by a node shim
    (`attic/smd/dump_cases.mjs`) that stubs the setup module and collects
    `(title, markdown, expected_children)`; the expected trees serialize as
    `{type, attrs?, children}` with smd.js's numeric Token/Attr values.
  - `internal/render/parser.go` is a line-faithful transliteration of the
    smd.js character state machine (`Parser.Write`/`End`, `Renderer`
    interface = add_token/end_token/add_text/set_attr). The full ported
    suite passed on the first run, in both single-write and char-by-char
    modes (the harness was mutation-checked: it detects type and text
    mismatches).
  - JS-semantics traps handled explicitly: iteration is by code point
    (`for _, r := range`), `.length` checks compare byte length only where
    content is provably ASCII, and the two checks that can see arbitrary
    text (CodeFence/CodeInline close) use `jsLen` (UTF-16 units) so astral
    characters can't false-close a fence; out-of-range indexing uses a
    `charAtIs` helper mirroring JS's undefined semantics. The
    Uint32Array(24) token stack became a growable slice (JS silently
    drops writes past the cap; nesting >23 deep is broken upstream anyway).
  - `internal/render/ansi.go` is the terminal Renderer for the phase 7
    live render (smd.js's default renderer is DOM-only, so the texture
    here is ours to hand-validate): styled headings/emphasis/code, "│ "
    blockquote prefixes, bullet/ordinal lists with hanging indent,
    " │ "-joined table cells with an underlined header row, links as
    "label (url)". Tests pin plain-mode layout, chunk-granularity
    invariance (byte-identical output whole-string vs char-by-char), and
    color output stripping back to the plain output exactly.

### Phase 5 — base-coder script mode — done
- Oracle: record/replay + the §11 failure matrix — green; live smoke passed
- Started: 2026-07-16
- Finished: 2026-07-16
- Deviations: none beyond those the spec declares. Notable notes:
  - **The assembled request byte-matches Python aider's captured request**
    (TestReplayEditSuccess compares every message's role+content against the
    live capture with PlatformInfo pinned) — system prompt, examples, reset
    pair, chat_files, and the reminder-in-user-message all identical.
  - §11 matrix covered as inline fixture scenarios + targeted tests:
    continuation stitch/replace/cap, retry-discards-partial (first sleep
    0.25s), Failed-after-partial bytes, empty stream, context-exhausted
    empty/with-partial, checkTokens-declined, H1 stale-accumulator
    regression, interrupt shape [Exact] + interrupt-then-mention no-reflect,
    dup shell blocks, suggest-shell-off gating, shell-from-failed-attempt
    cross-reflection, reflection cap 4 sends/3 follow-ups, no-edit
    lifecycle, dry-run, path traversal/absolute rejection, declined-mention
    memory, mention reflect message, reminder sys/user/skip-on-assistant/
    unknown-max, fence escalation + quad reminder, unreadable-chat-file
    drop, cache breakpoints (≤3, never done/cur, no history mutation),
    native+inline reasoning separation, reasoning-tag metachars.
  - Interpretations settled from reference reading (documented here, not
    deviations): usage from length-terminated attempts IS accumulated (aider
    loses it — spec §8 wins); repeated Finish events (OpenRouter sends
    "stop" twice) are idempotent; `edited` includes allowed-but-failed paths
    (aider parity); apply-report reflection returns AFTER history rotation.
  - Small declared choices pending human review (see Pending questions):
    TokenCounter is runes/4 for all models in v1 (no tiktoken dep pinned;
    counts are advisory §10); URL scraping is a minimal GET+strip
    (`SimpleScraper`) vs aider's browser-based scraper; `ReminderPlacement`/
    `PrefillSupported`/`ExamplesAsSysMsg` are Coder options with aider's
    defaults ("user", true, false) since config-schema doesn't model them.
  - Edit formats: diff (editblock), diff-fenced (prompt swap), whole
    (ParseWholeFile ported from wholefile_coder.py) all wired.
  - CLI script mode wired end-to-end; **live smoke**: `strument --yes -m
    'add a hello() function…' main.go` against OpenRouter DeepSeek V4 Flash
    edited the file and reported $0.00043 in-band cost.

### Phase 4 — client — done
- Oracle: replay fixtures, no live LLM in the suite — green
- Started: 2026-07-16
- Finished: 2026-07-16
- Deviations: none
- Notes:
  - `internal/client`: request building per adapter dialect (OpenRouter
    `reasoning:{effort}` vs OpenAI `reasoning_effort`; extra_params written
    beneath the owned transport keys; `stream_options.include_usage` always
    sent — harmless on OpenRouter, required on OpenAI), SSE parsing to
    StreamEvents, HTTP error classification onto the §2.1 classes. Tests
    use a RoundTripper stub; no sockets.
  - `client.ParseSSE` is the single source of dialect truth: the new
    `strumentrec -distill` mode uses it to turn raw captures into fixture
    rows, and the smoke fixture's stream row was regenerated through it.
  - Wire canon fact: OpenRouter repeats `finish_reason:"stop"` on both the
    finish chunk and the trailing usage chunk, so a stream carries TWO
    Finish events; the parser reflects the wire and the coder must treat
    repeated Finish as idempotent (phase-5 note).
  - Captured raw fixtures committed under `testdata/fixtures/raw/`:
    edit-success, edit-multifile, no-edit-conversational, edit-plus-shell,
    repo-map-present. The repo-map capture is a 2-request run that also
    exercises the real **file-mention flow** (aider auto-added `bye.go` and
    re-sent) and shows the reminder concatenated into the final user
    message. `openrouter-usage-cost` is covered by every capture;
    `reflection-search-not-found` and `reasoning-model-inline-think` will
    be authored/mutated in phase 5 (capture can't reliably force them —
    fixture-harness §0 corollary 2).

### Phase 3 — repomap — done
- Oracle: transliterated `test_repomap.py` + sample-code-base golden — green
- Started: 2026-07-16
- Finished: 2026-07-16
- Deviations: only the two pre-declared ones (sqrt-once, single tag
  emission). Notably, **the sample-code-base golden matches aider's own
  golden byte-for-byte** — for that corpus the declared deviations don't
  perturb the final map, so we kept upstream's golden file unmodified.
- Notes:
  - **Spec-vs-reference finding (documented, not acted on):**
    `repomap-spec.md` §1.2/§Query-assets says the legacy
    `tree-sitter-languages/` query dir "is selected only when
    `USING_TSL_PACK` is false; ignore it." The pinned source disagrees:
    `reference/aider/repomap.py:805-829` (`get_scm_fname`) falls back to the
    legacy dir **per language** whenever the pack lacks a `<lang>-tags.scm`,
    even with USING_TSL_PACK true. That is how aider's language tests for
    haskell/kotlin/php/typescript/tsx/zig/scala/hcl pass. The spec's
    *decision* (v1 = the 31 pack queries only; adding a language = vendoring
    a query) is unambiguous, so we followed it; those legacy-fallback
    languages are out of v1 scope and their aider tests were not
    transliterated. Flagged under "Pending questions".
  - **gotreesitter engine limitation + workaround:** the anchored
    doc-comment pattern `((comment)* @doc . (X))` matches only once per
    parent in gotreesitter v0.36.0 (upstream tree-sitter matches at every
    position). Minimal repro: 3 commented JS functions -> upstream yields
    3 defs, gotreesitter yields 1. Since `(comment)*` matches zero comments
    upstream, the prefix never constrains which definitions match; the
    mapper never reads `@doc`. So `preprocessQuery` strips the
    `(comment)*@doc`+anchor prefix and all `@doc` directives
    (`#strip!`/`#select-adjacent!`) at load. It also strips
    `(#set-adjacent! ...)` (go-tags.scm), a tags-crate directive
    py-tree-sitter ignores but gotreesitter rejects at compile.
  - Coverage: 28 of the 31 pack languages have gotreesitter grammars
    (missing: ocaml_interface, pony, udev — those files fall back to bare
    entries per §3.6); `csharp` maps to registry name `c_sharp`.
  - PageRank parity pinned against networkx 3.x (personalized incl.
    dangling redistribution + unpersonalized); TreeContext goldens pinned
    against grep_ast (nested headers via enclosing block, multiline
    signature via parameters-node header, blank-line pickup, line-0
    suppression); ranker multiplier tests via a `TagsOverride` test seam.
  - Float determinism: PageRank adjacency and all accumulations iterate in
    sorted order (map-order float summation would flip near-ties).

### Phase 2 — config — done
- Oracle: hand-written table tests per config-schema §9 — green
- Started: 2026-07-16
- Finished: 2026-07-16
- Deviations: none
- Notes:
  - `internal/config`: `provider`/`model`/`env` builtins over
    `go.starlark.net` (Set literals + top-level control on; while/recursion
    off), whole-key merge, post-merge weak_model resolution (None→self
    permanent, cross-file string refs), validation.
  - Trust gate: JSONL store at `$XDG_STATE_HOME/strument/trust`, hex
    multihash records verified under **their own** recorded algorithm
    (sha2-512 round-trip test simulates a default-hash migration).
  - Interpretation settled: `models`/`default` are optional **per file**
    and required on the **merged** result, so a project config may
    override only `default` or only aliases.
  - Reserved extra_params keys: model, messages, stream, stream_options,
    usage (the OpenRouter usage/cost control).
  - `strument trust` CLI subcommand wired.

## Standing notes
- Git features are **on by default** when cwd is inside a git repository; `--no-git`
  opts out. Confirmed with the human 2026-07-16 (guide commit `ef7c8d3` is
  authoritative; the "`--no-git` default" wording surviving in
  `basecoder-spec.md` §Parity/§7.4 is stale). Build order is unchanged:
  script mode without git first, git mode in phase 8.
- `reference/` (aider @ `5dc9490`) and any other reference clones are
  **gitignored**, never committed. Committed aider content is limited to the
  31 `*-tags.scm` query files and verbatim prompt strings, covered by the
  Apache-2.0 LICENSE + README credit.
- Verified 2026-07-16: aider upstream HEAD == pinned SHA `5dc9490…`; go1.26.5
  toolchain auto-downloads in this container; proxy.golang.org and
  openrouter.ai reachable; Python 3.11.15 available for fixture capture.

## Phase log

### Phase 0 — Scaffold — done
- Oracle: builds + empty CI green — `go build/vet/test ./...` green locally
- Started: 2026-07-16
- Finished: 2026-07-16
- Deviations: none
- Notes:
  - `reference/` cloned at `5dc9490bb35f9729ef2c95d00a19ccd30c26339c`.
    A stub `reference/go.mod` carve-out (uncommitted, inside the gitignored
    clone) stops `go build ./...` from walking aider's Go test fixtures —
    recreate it if the clone is redone.
  - LICENSE copied from aider (Apache-2.0). README credits aider + smd.js.
  - Recorder design: `strumentrec` captures the wire **raw** (verbatim request
    body, verbatim SSE response, secret-stripped headers). Distillation into
    the fixture schema's `StreamEvent` rows happens in phase 4 using the real
    client's SSE parser, so the dialect has a single source of truth. The
    harness spec's schema (§2) is the *scenario* format, which the loader and
    replay stubs implement now.
  - Python aider 0.86.3.dev53+g5dc9490bb installs and runs from the reference
    clone (`attic/venv`). The phase-0 smoke fixture
    `testdata/fixtures/basecoder/edit-success.jsonl` was **captured live**
    (aider `--edit-format diff --no-git` → strumentrec → OpenRouter,
    `deepseek/deepseek-v4-flash`) and distilled from the raw wire log.
    Dialect facts learned: OpenRouter emits native reasoning on
    `delta.reasoning` (124 chunks in this capture); the final chunk carries a
    `usage` object including in-band `cost` **without** `stream_options`
    being requested; litellm strips the `openai/` routing prefix so the wire
    model is the bare slug; aider sends `temperature: 0` by default.

### Phase 1 — editblock — done
- Oracle: transliterated `test_editblock.py` + `test_find_or_blocks.py` — green
- Started: 2026-07-16
- Finished: 2026-07-16
- Deviations: none
- Notes:
  - The `test_find_or_blocks.py` golden oracle (4 MB `chat-history.md` →
    988 KB gold output) passes **byte-for-byte** on the ported parser.
    Corpus copied to `testdata/transliterated/editblock/`.
  - difflib.SequenceMatcher ported generically (autojunk included, junk
    lists unused) with ratios pinned against CPython 3.11, including a
    discriminating autojunk case at the ≥200-element threshold.
  - `ApplyEdits` is a pure planner over a `FileReader` + overlay: dry-run
    and real apply share one code path, and stacked edits against one file
    compose (aider's real-run behavior; its dry_run path reads stale
    content — we don't copy that artifact). Failure report strings are
    byte-shaped per §5.
  - Python-fidelity details preserved: negative-slice semantics in
    `match_but_for_leading_whitespace`, `str.splitlines` boundary set,
    `strip("`")`/`strip("*")` both-sided strip order, `get_close_matches`
    tie-break toward the lexicographically larger string.
  - Prompt strings extracted **mechanically** from the installed aider
    classes into `internal/prompts` (editblock, editblock_fenced,
    wholefile + base fields), pinned by sha256 in tests. Upstream wart
    carried verbatim: `editblock_fenced_prompts.py` contains a leaked
    `<<<<<<< HEAD` merge-conflict marker inside example[1]; kept for
    [Exact] parity ("diff-fenced" is not the default format). Flag for
    the human: we may want to declare a deviation and drop that line.
  - Deferred to phase 5: the three coder-level tests (`test_full_edit`,
    `test_full_edit_dry_run`, `test_create_new_file_with_other_file_in_chat`)
    — ApplyEdits-level analogs are in place now.

## Deviations
(none yet)

## Pending questions for human
- [ ] `repomap-spec.md` §1.2 mis-states `get_scm_fname`
  (`reference/aider/repomap.py:805-829` falls back to the legacy query dir
  per-language even when USING_TSL_PACK is true). We followed the spec's
  decision (31 pack queries only). If parity with aider's *effective*
  language coverage is wanted later, vendoring legacy queries
  (haskell/kotlin/php/typescript/tsx/zig/scala/hcl, subject to gotreesitter
  grammar availability) is the v2 path. OK?
- [ ] `editblock_fenced_prompts.py` @ 5dc9490 contains a leaked
  `<<<<<<< HEAD` merge-conflict marker inside example[1]; we carry it
  verbatim per [Exact] parity ("diff-fenced" is not the default format).
  Declare a deviation and drop that line instead?
- [ ] basecoder-spec §10 says "tiktoken port for OpenAI-family"; the pinned
  dependency list has no tiktoken, so v1 uses runes/4 for all models
  (advisory-conservative consumers only). OK, or should we add
  a tiktoken-go dependency?
- [ ] URL scraping (§1.4 checkForUrls) is a minimal HTTP GET + tag strip in
  v1 (`coder.SimpleScraper`), not aider's browser-based scraper. OK?
