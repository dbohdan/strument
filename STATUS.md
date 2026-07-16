# Strument port — STATUS

## Current phase
Phase 3 — repomap — started 2026-07-16

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
- [ ] (none)
