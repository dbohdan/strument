# Strument port — STATUS

## Current phase
Phase 1 — editblock — started 2026-07-16

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

### Phase 1 — editblock — in-progress
- Oracle: transliterated `test_editblock.py` + `test_find_or_blocks.py`
- Started: 2026-07-16
- Deviations: none
- Notes:

## Deviations
(none yet)

## Pending questions for human
- [ ] (none)
