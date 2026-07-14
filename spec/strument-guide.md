# Strument: the implementor's guide (skeleton)

*The bootstrap artifact for an autonomous Claude Code run that ports a trimmed aider fork to Go. The four subsystem specs are the settled decisions; this guide is the spine that orders them, defines the oracle, splits autonomous from collaborative work, and states when to stop. Written in the pattern of the starlark-py port (long upfront design, then execution) with one deliberate difference: starlark-py inherited a conformance corpus as its oracle; Strument has to manufacture one.*

## 0. What this is, and the claim

This is the prompt, not the code. The methodological bet — proven once on starlark-py — is that you settle every decision that could be relitigated mid-run **before** any code is written, so the autonomous session never stalls on a design question. Four specs already do that settling:

- `editblock-spec.md` — SEARCH/REPLACE parse + the matching ladder.
- `config-schema.md` — the Starlark config surface + the trust gate.
- `repomap-spec.md` — ranked tag map (tags → graph → PageRank → TreeContext).
- `basecoder-spec.md` — the orchestration spine (assemble → stream → reflect → apply → shell → commit → cost).
- `fixture-harness-spec.md` — fixture capture & replay.

Each was hardened through adversarial review against the pinned aider source. Treat them as frozen. This guide adds only the connective tissue: order, oracle, modes, deviation protocol, stopping conditions.

## 1. The one thing different from starlark-py: a manufactured oracle

starlark-py copied Bazel's `testdata/*.star` verbatim and parameterized a pytest over it — the corpus *was* the oracle, and test-writing time was ~zero. Strument has no runnable corpus. Its oracle is built in three tiers, and the tier a subsystem falls into determines whether its phase is autonomous or collaborative:

| Tier | Oracle | Subsystems | Mode |
|---|---|---|---|
| **Transliteration** | aider's own pytest files, ported to Go table tests | editblock parse/match, repomap tags/graph/PageRank/TreeContext | autonomous |
| **Record/replay** | Python aider @ `5dc9490` driven to emit deterministic fixtures (JSON-Lines) | base-coder chat loop, LLM client dialect | semi-autonomous |
| **Hand-validation** | the human, comparing REPL feel to aider's cast | REPL, streaming renderer texture | collaborative |

The pure-function cores are transliteration tier because aider ships their tests: `tests/basic/test_editblock.py` (29 tests), `tests/basic/test_find_or_blocks.py`, `tests/basic/test_repomap.py` (46 tests). Port these first, as Go table tests, and they become the progress dashboard exactly as the Bazel corpus did for starlark-py. The streaming renderer has its **own** inherited oracle: `thetarnav/streaming-markdown` (smd.js) ships a test suite — port it alongside the renderer.

The base-coder cannot be transliterated (it needs a live model) and cannot be hand-validated cheaply (its correctness lives in guards and side-buffers, per the spec's v4 history). So it is the record/replay tier: make Python aider a deterministic reference by capturing `(coder state, fs/git state, model StreamEvent sequence, confirmation script, command-runner results)` and asserting Strument reproduces the assembled requests, side effects, and final history. The base-coder spec's §11 failure-path list is the fixture manifest.

## 2. Settled decisions & the deviation protocol

The specs hold the decisions. A few standalone ones, not to relitigate: tree-sitter via **gotreesitter** (pure Go, no cgo) using its low-level **`Query`** API, not the `Tagger`; CLI via **Kong**; config in **Starlark** (`go.starlark.net`); a **single OpenAI-compatible client** speaking OpenRouter's dialect (no litellm, no MCP, no function-calling); streaming markdown **hand-rolled by porting smd.js** (no Charm/bubbletea); git by **shelling out** (not go-git); readline via **`github.com/ergochat/readline`**.

**Deviation protocol (verbatim instruction to the implementor):** *If you want to deviate from any decision in a spec or in §2, stop. Write a three-paragraph analysis in `STATUS.md` (what the spec says, why you'd deviate, what it costs), pick a different phase, and continue there until the human responds. Do not deviate silently. Do not relitigate a settled decision because a phase is hard.*

Two decisions are already **fix-and-declare deviations from aider** — implement the fix, not the bug: the compounding `sqrt` inside the repomap definer loop (compute once), and the tag double-append (emit once). Both are in `repomap-spec.md §3.4/§1.1`. Golden fixtures are regenerated from the corrected Go, and asserted on ranking + rendered skeleton, never raw tag-emission order.

**Cross-cutting conventions:** *Silence means follow `reference/`; explicit `[Deferred]` means don't build it.* If a spec is silent on a detail, check the pinned aider source — that's the answer. If a feature is labeled `[Deferred]`, it is not v1 scope; do not implement it "while you're at it." Dependency versions are resolved at phase 0 by querying the module proxy, never asserted from a model's memory (two Opus sessions produced two different wrong version numbers for the same library).

## 3. Directory layout

```
strument/
  cmd/strument/            main.go (Kong CLI)
  internal/
    editblock/             editblock-spec.md
    config/                config-schema.md (Starlark loader + trust gate)
    repomap/               repomap-spec.md
    coder/                 basecoder-spec.md (the spine)
    client/                OpenAI/OpenRouter client
    render/                streaming markdown (smd.js port)
    repl/                  readline, prompt, live render
  reference/               aider @ 5dc9490bb35f9729ef2c95d00a19ccd30c26339c — READ-ONLY, for grep; excluded from build
  testdata/
    fixtures/              record/replay JSON-Lines
    transliterated/        Go ports of aider's pytest cases
  spec/                    the four .md specs, vendored so the run is self-contained
  STATUS.md                the journal — memory across context compactions
```

`reference/` is the aider clone pinned at **`5dc9490bb35f9729ef2c95d00a19ccd30c26339c` (0.86.3.dev)** — pin the SHA, never the v0.86.0 tag; they differ, and several spec facts (the double-append, the compounding `sqrt`) exist only at the dev commit. `STATUS.md` is the phase journal and the deviation log; it is how the run survives context-window compaction, exactly as in starlark-py. Use the template below; update it each phase transition.

**`STATUS.md` template:**
```
# Strument port — STATUS

## Current phase
Phase N — <name> — <started YYYY-MM-DD>

## Phase log

### Phase 0 — Scaffold — [done | in-progress | blocked]
- Oracle: <e.g. builds + empty CI green>
- Started: YYYY-MM-DD
- Finished: YYYY-MM-DD
- Deviations: (bullet list; each links to a Deviations entry below)
- Notes: (running log)

### Phase 1 — editblock — ...
...

## Deviations
### D01: <one-line summary> (Phase N, YYYY-MM-DD)
**What the spec says:** ...
**Why deviate:** ...
**What it costs:** ...
**Resolution:** [pending human | applied unilaterally with justification | reverted]

## Pending questions for human
- [ ] <question>
```

## 4. Build sequence & phase roadmap

Overall order is **no-git script mode → REPL → git mode**, and within that, dependencies first. Each phase names its spec, the `reference/` files to read, its oracle, its mode, and what it unlocks. A phase is done when its oracle is green — not before, not (because it's hard) after.

| # | Phase | Spec / reference | Oracle (gate) | Mode |
|---|---|---|---|---|
| 0 | Scaffold: Go module, pinned deps, `reference/` clone, `STATUS.md`, CI, JSON-Lines fixture format, `--no-git` skeleton | — | builds + empty CI green | auto |
| 1 | **editblock** parse + matching ladder | `editblock-spec.md`; `editblock_coder.py` | transliterate `test_editblock.py` + `test_find_or_blocks.py` | auto |
| 2 | **config**: Starlark loader, builtins, trust gate (multihash) | `config-schema.md` | hand-written table tests per spec | auto |
| 3 | **repomap** pure core: tags (`Query` API), graph, PageRank, TreeContext | `repomap-spec.md`; `repomap.py`, grep_ast | transliterate `test_repomap.py`; assert order + skeleton | auto |
| 4 | **client**: OpenAI/OpenRouter dialect, streaming, usage/cost | `basecoder-spec.md §8`; models.py | recorded fixtures; no live LLM | semi |
| 5 | **base-coder script mode** (`--no-git`): assemble → stream → reflect → apply → shell → cost | `basecoder-spec.md` (all) | record/replay + the §11 failure-path list | semi |
| 6 | **render**: streaming markdown | port `thetarnav/streaming-markdown` | smd.js's own test suite | auto |
| 7 | **REPL**: readline, prompt, `/` commands, live render | `basecoder-spec.md §1.2, §6` | hand-validate vs aider asciinema cast | **collab** |
| 8 | **git mode**: auto-commit (`Assisted-by` trailer, argv), `/undo`, `/diff`, dirty-commit contract | `basecoder-spec.md §7.3–7.4` | fixtures + a scratch-repo integration test | semi |
| 9 | **packaging**: single static binary, `grammar_subset` build tags, zipapp-equivalent distribution | — | binary size + smoke run | auto |

Phase 5 is the hard one and everyone should know it going in: the base-coder is where correctness lives in guards (`if not interrupted`) and side-buffers (`multiResponseContent` reset scope), not in clean transforms. Four review passes on its spec found real bugs at every pass. Budget accordingly, lean hard on the fixtures, and consult `reference/base_coder.py` for every ordering question rather than inferring from the linearized step list.

## 5. Stopping conditions (verbatim to the implementor)

*Stop and write to `STATUS.md`, then switch phases, if: (a) a decision is genuinely Pending — the specs don't cover a case you've hit; (b) a spec claim contradicts what `reference/` actually says at `5dc9490bb35f9729ef2c95d00a19ccd30c26339c` — quote the file:line, do not "fix" the spec silently; (c) an oracle can't be made deterministic without a live LLM — the automated suite must never call one. Do **not** stop because a phase is hard, because a test is tedious to port, or because you'd prefer a different design. Hardness is expected; pendingness is the only blocker.*

## 6. Runtime model standing order

Two models, two roles — don't conflate them. The **implementor** writing Go is Claude Code (this run). The model **Strument-the-tool** calls when exercised manually is **DeepSeek V4 Flash at low reasoning effort** (cheap, capable; a cost-limited expiring OpenRouter key will be provided). The **automated test suite never calls a live LLM** — it replays fixtures. When you need a live call to *record* a fixture, use DeepSeek V4 Flash; strip response-side reasoning before the edit parser sees it (`basecoder-spec.md §5`).

## 7. Deferred — do not build in v1

From the specs' `[Deferred]` labels, so the run doesn't scope-creep: type-aware `GoMapper` (v2 — the uniform tree-sitter mapper covers Go in v1); lint/test reflection (edit-failure + file-mention only); architect/voice/GUI/analytics/cache-warming; images/multimodal (`/paste`); history summarization; udiff/patch/func edit formats (editblock + fenced + whole only); Anthropic-native client path (behind the provider `kind` field). The re-entry points for lint/test are marked in `basecoder-spec.md §9` for when they return.

## 8. Forward links

**`/review` (see `review-note.md`).** Out of scope for v1, but two things this run produces — the `reference/` clone and the record/replay harness — are exactly the substrate `/review` needs. Build the fixture format and the `reference/`-grep path cleanly enough that a later `/review` command can reuse them; don't paint them into `testdata/`-only corners.

**The retrospective.** starlark-py's public page was written *after* the run, mining its `STATUS.md`. Strument's equivalent — the "guild-book" of this AI-assisted porting method — is the same: written from `STATUS.md` once the run lands, not before. This guide is its prospective half; the journal will be its raw material.

---

*The conversations that produced the four specs were the work; this guide and those specs are the artifact. The autonomous run should be, as starlark-py was, comparatively mechanical — provided nothing settled here gets relitigated once code begins.*

## 9. Dependency versions (resolved 2026-07-14)

*Dependency versions are resolved at phase 0 by querying the module proxy, never asserted from a model's memory.*

| Module | Pin |
|---|---|
| `github.com/odvcencio/gotreesitter` | v0.36.0 |
| `github.com/alecthomas/kong` | v1.15.0 |
| `github.com/alecthomas/chroma/v2` | v2.27.0 |
| `go.starlark.net` | v0.0.0-20260708150628-5395d018f003 |
| `github.com/multiformats/go-multihash` | v0.2.3 |
| `github.com/ergochat/readline` | v0.1.3 |
| `github.com/creack/pty` | v1.1.24 |
| `github.com/Netflix/go-expect` | v0.0.0-20220104043353-73e0943537d2 |
| Go toolchain | go1.26.5 |
