# Strument port — STATUS

## Current phase
Phase 0 — Scaffold — started 2026-07-16

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

### Phase 0 — Scaffold — in-progress
- Oracle: builds + empty CI green
- Started: 2026-07-16
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

## Deviations
(none yet)

## Pending questions for human
- [ ] (none)
