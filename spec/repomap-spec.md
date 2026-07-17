# Spec: Strument repo map (ranked tag map) — v2

Source of truth: `aider/repomap.py` at **commit `5dc9490bb35f9729ef2c95d00a19ccd30c26339c` (0.86.3.dev)** and `grep_ast/grep_ast.py` (TreeContext, grep_ast ~0.9). Pin the SHA, not the release tag — the tagged v0.86.0 differs (it lacks the double-append and the compounding-`sqrt` quirks addressed below).
Query assets: `aider/queries/tree-sitter-language-pack/<lang>-tags.scm` — **31 files** at `5dc9490`, plus a legacy `tree-sitter-languages/` dir. **Errata (corrected 2026-07-17):** `get_scm_fname` (`reference/aider/repomap.py:805-829`) gates only the *pack lookup* on `USING_TSL_PACK`; the legacy fallback is **unconditional and per-language**, so aider's effective coverage is the **union** — the original "selected only when `USING_TSL_PACK` is false; ignore it" was wrong. Copy both verbatim; try pack first, then legacy (aider's order). **Coverage bound:** a language yields tags only if a `-tags.scm` exists *and* gotreesitter has a compatible grammar — julia and zig ship a legacy query but gotreesitter's grammars have diverged (unknown node types), so they fall back to bare entries. See STATUS.md for the implemented set.
Runtime substrate: gotreesitter (pure Go, no cgo).

Four stages: **extract tags → build reference graph → personalized PageRank → fit to budget and render.**

Standing decisions: **uniform tree-sitter mapper for every language including Go in v1** (the type-aware `GoMapper` is deferred; see §1.4); **faithful port** except for two declared deviations (§3.4, §1.1); **no cross-call cache**, but process-lifetime compiled queries and invocation-local parse/render memoization are required (§0.2).

---

## 0. Data types, interface, cache tiers

### 0.1 Types — tagged union, not heterogeneous tuples

Aider's `ranked_tags` mixes 5-field `Tag` namedtuples and bare `(fname,)` 1-tuples; Python's element-wise tuple comparison sorts the short one first. Go has no such comparison, so model it explicitly:

```go
type Kind int
const ( Def Kind = iota; Ref )

type Tag struct {
    RelFname string // repo-root-relative, forward-slashed (§2.1)
    Fname    string // absolute
    Line     int    // 0-based start row; -1 only for chroma-backfilled refs (§1.3)
    Name     string // display identifier text
    Kind     Kind
}

// A ranked entry is either a full tag or a bare file marker.
type MapItem struct {
    RelFname string
    Tag      *Tag // nil => bare file (rendered as a filename line, no body)
}
```

One comparator governs ordering (§6 enumerates the rest):

```go
// Python tuple order: bare (len 1) sorts before Tag (len >=2) at equal RelFname.
func lessMapItem(a, b MapItem) bool {
    if a.RelFname != b.RelFname { return a.RelFname < b.RelFname }
    if (a.Tag == nil) != (b.Tag == nil) { return a.Tag == nil } // bare first
    if a.Tag == nil { return false }
    if a.Tag.Line != b.Tag.Line { return a.Tag.Line < b.Tag.Line }
    if a.Tag.Name != b.Tag.Name { return a.Tag.Name < b.Tag.Name }
    return a.Tag.Kind < b.Tag.Kind
}
```

### 0.2 Mapper interface (v1 = tree-sitter only)

```go
type Mapper interface { Tags(absPath, relPath string, src []byte) ([]Tag, error) }
```

`TreeSitterMapper` is the sole v1 implementation, used for **all** languages (§1). The interface exists so a batch-oriented `GoMapper` can be added in v2 (§1.4) without touching the ranker.

### 0.3 Cache tiers (replaces the blanket "no cache")

- **No cross-call persistent or mtime cache.** Every `RepoMap` call re-tags and re-ranks. (Dropped: `TAGS_CACHE`/diskcache, `map_cache`, `refresh` modes, `map_processing_time`.)
- **Process-lifetime, immutable:** one compiled `Query` per language, reused for the process's life.
- **Invocation-local, required:** within a single `RepoMap` call, memoize each file's parse tree and its built `TreeContext` keyed by `relFname`. The binary search (§4.2) renders the same files across ~log(n) probes; without this it reparses each probe. Discard when the call returns.

## 1. Tag extraction (`TreeSitterMapper.Tags`)

### 1.1 Query execution — low-level `Query`, not `Tagger`

**Critical.** Aider's `.scm` files use the *dotted* capture convention — the identifier node is captured as `@name.definition.function` and the enclosing construct as `@definition.function`. gotreesitter's `Tagger` expects the *paired* convention (bare `@name` + `@definition.*`) and would derive the name from the definition node (the whole declaration). So do **not** use `NewTagger` with aider's queries.

Instead, replicate aider's `get_tags_raw` over gotreesitter's `Query`/`QueryCursor`:

1. `grammars.DetectLanguage(fname)` -> language; nil => return nil (file becomes an unranked bare entry, §3.6).
2. Compile the embedded `<lang>-tags.scm` once (process-lifetime); parse `src`; `cursor := q.Exec(root, lang, src)`.
3. For each match, for each capture, read the capture's **name string**:
   - starts with `name.definition.` -> emit `Tag{Kind: Def, Name: node.Text, Line: node.StartRow}`
   - starts with `name.reference.` -> emit `Tag{Kind: Ref, ...}`
   - anything else (`definition.*`, `reference.*`, `doc`, bare `name` in the few files that use it) -> skip.

Emit **each qualifying capture once**. Do **not** replicate the upstream double-append (lines 307-310 of the pinned source append the last node of each tag a second time — a latent bug that inflates that ref's count by one; declared deviation).

### 1.2 The `.scm` queries

Strument's v1 tags languages are exactly those with a shipped query. Of the 31, the ones we care about are **Go, Python, Rust, Lua, Clojure** — all present and verified. **Crystal is deferred to v2** (no `crystal-tags.scm` in aider; would require vendoring one upstream). There are no config-language tags queries (no starlark/tcl/dhall/pkl/nickel), and none are needed — a def/ref graph of `.strument.star` has no value. Config and unsupported files appear as **bare entries** per §3.6, which is correct behavior. Adding a language in v2 = vendoring a `-tags.scm`, not flipping a flag.

### 1.3 Reference backfill (definitions-only languages)

Track seen kinds. If any `Ref` was emitted, done. If no `Def` was seen, done. **Defs but zero refs** (e.g. historically C++): backfill via chroma. Lex the file; for each token whose type is a name, emit `Tag{Kind: Ref, Line: -1}`. The pygments check is hierarchical (`token[0] in Token.Name` matches `Name.Function` etc.), so the chroma predicate must be hierarchical too:

```go
func isNameToken(t chroma.TokenType) bool {
    return t == chroma.Name || strings.HasPrefix(t.String(), "Name.")
}
```

`Line = -1` marks a backfilled ref; the renderer must never treat `-1` as a real line, and a `Def` must never carry `-1`.

### 1.4 GoMapper — deferred to v2 (rationale recorded)

Not shipped in v1. Two blocking reasons: `go/packages` is package-oriented and costs seconds (module load + type-check), incompatible with the per-file `Mapper` interface and the no-cache budget; and the ranker joins tags by `Name` string, so type resolution is **discarded at the join** — a type-aware extractor followed by a name-only join is still collision-prone, so the promised "no name-collision noise" does not hold without further work. A faithful v2 `GoMapper` needs: a batch/package API (not per-file), an `Overlay` for in-memory `src`, per-module memoization keyed by `go.sum`/dir mtime, a `Symbol` field on `Tag` carrying canonical `(pkgpath, object)` identity used as the **graph key** while `Name` stays display text, and a capture policy matching aider's Go query rather than emitting all of `TypesInfo.Defs`/`Uses` (locals, params, imports -> a much noisier graph). Until then, Go files go through `TreeSitterMapper` like everything else.

## 2. Inputs, canonicalization, personalization

### 2.1 Path canonicalization (centralize)

One function produces every `relFname`, applied to `chatFiles`, `otherFiles`, `mentionedFnames`, mapper outputs, important-file checks, and graph keys:

```go
func canonicalRel(root, abs string) string // relpath(root, abs), forward-slashed
```

Cross-mount / cross-drive (Windows `C:` vs `D:`) -> return the absolute `fname` (matches aider's `ValueError` fallback). Decide and document handling of `..`-outside-root, symlinks, and case-insensitive filesystems; dedupe identical canonical paths.

### 2.2 Personalization scoring

Base `personalize = 100 / len(allFiles)` where `allFiles = chatFiles union otherFiles`. Per file, accumulate `pers`:

- in `chatFiles` -> `pers += personalize`
- `relFname in mentionedFnames` -> `pers = max(pers, personalize)` (max, not add)
- any path component (each path part, plus basename with and without extension) in `mentionedIdents` -> `pers += personalize` **once**
- record `personalization[relFname] = pers` only if `pers > 0`.

In script mode with no prior turns, `mentioned*` are empty; personalization rests on `chatFiles`. Fine.

## 3. Graph construction and ranking (`get_ranked_tags`)

### 3.1 Collect (deterministic)

Iterate `sorted(chatFiles union otherFiles)`. For each existing file, extract tags and accumulate:

- `defines: name -> set(relFname)`
- `definitions: (relFname, name) -> set(Tag)` (the def tags, for rendering)
- `references: name -> []relFname` (**with repetition**; multiplicity feeds §3.4)

Missing files (deleted-but-tracked): warn once, skip.

### 3.2 Degenerate fallback

If `references` is empty after the scan, set `references = {name: list(definers) for name, definers in defines}`.

### 3.3 Identifier set

`idents = keys(defines) intersect keys(references)`.

### 3.4 Build the weighted MultiDiGraph

Nodes are `relFname`s. **A file becomes a graph node only when an edge touches it.** Files with no defs/refs/self-edge are absent from PageRank and are appended later as bare entries (§3.6) — faithful; do not pre-seed all files as nodes.

Retain edge identity: keep weight per `(src, dst, ident)` for rank distribution (§3.6), and separately sum over `ident` to get the `(src, dst)` transition weight PageRank consumes (§3.5). Do not collapse to `weights[src][dst]` — that loses the ident needed downstream.

**Self-edges.** For each `ident in defines` but `not in references`: for each definer, add self-edge `definer->definer`, weight `0.1`, `ident`.

**Cross edges.** For each `ident in idents`, multiplier `mul` (start 1.0):

- `ident in mentionedIdents` -> `x10`
- (`isSnake or isKebab or isCamel`) and `len(ident) >= 8` -> `x10`
  - snake: has `_` and any alpha; kebab: has `-` and any alpha; camel: has an upper and a lower
- `ident` startswith `_` -> `x0.1`
- `len(defines[ident]) > 5` -> `x0.1`

Then for each `(referencer, count)` in `Counter(references[ident])`:

- `w = sqrt(count)` — **computed once per referencer** (declared deviation: the pinned source recomputes `sqrt` inside the definer loop, compounding it per definer and making weights nondeterministic over the definer *set*; we fix it)
- for each `definer in definers`: `useMul = mul`; if `referencer in chatRelFnames` -> `useMul x= 50`; add edge `referencer->definer`, weight `useMul * w`, `ident`.

### 3.5 Personalized PageRank (no graph library — ~40 lines)

Power iteration matching networkx defaults:

- `alpha = 0.85`, `maxIter = 100`, uniform initial vector over nodes.
- **Convergence: L1 error `< nodeCount * 1e-6`** (networkx compares against `N*tol`, not `tol`).
- `personalization` and `dangling` vectors independently L1-normalized to sum 1; a node absent from `personalization` gets weight 0 (not 1/N). Pass the personalization map as **both** vectors when non-empty; otherwise omit both.
- Dangling nodes (no out-edges) redistribute rank by the dangling vector.
- Parallel edges between a pair sum for the transition matrix (§3.4).

Failure taxonomy — do **not** model as "retry all failures unpersonalized":

- empty graph -> empty rank map (map disabled this turn).
- zero/invalid personalization -> retry once unpersonalized; still failing -> empty.
- non-convergence after `maxIter` -> use the last iterate (documented) rather than throwing.

### 3.6 Rank distribution -> ranked list

Distribute each node's rank across its out-edges by weight fraction: `rankedDefs[(dst, ident)] += rank[src] * w(src,dst,ident) / totalOutWeight(src)`.

1. Sort `rankedDefs` by `(rank desc, (fname,ident) desc)` — the tuple tiebreak is **reverse**, matching `sorted(..., reverse=True, key=lambda x:(x[1],x[0]))`.
2. For each `(fname, ident)` in order: **skip if `fname in chatRelFnames`**; else append its def tags as `MapItem{Tag:...}`.
3. Bare-node pass — iterate **all** graph nodes (chat files included) sorted by `(rank desc, node desc)`; for each node not already emitted, append `MapItem{RelFname: node}` (bare). Chat-file bares are added here and skipped later in `to_tree`; they still affect `num_tags` and thus the truncation index, so include them. Then append any `otherFiles` still absent, bare.

## 4. Budget fitting (`get_ranked_tags_map_uncached`)

### 4.0 Guards

- `if len(otherFiles) == 0: return ""` (no map).
- No-chat widening only when the context window is known: if `maxMapTokens > 0 and maxContextWindow > 0`, `target = min(maxMapTokens * mapMulNoFiles, maxContextWindow - 4096)`; if `chatFiles` empty and `target > 0`, set `maxMapTokens = target`. If `maxContextWindow <= 0`, skip widening entirely. Defaults: `mapTokens = 1024`, `mapMulNoFiles = 8`. Ensure `maxContextWindow - 4096` can't yield a usable negative.

### 4.1 Important-files prepend

`special_fnames` = important files among `otherFiles` not already in `ranked_tags`, prepended as bare `MapItem`s so they survive truncation. **Matching rule (from `special.py`):** exact match of the normalized root-relative path against a fixed set of 154 names (`.gitignore`, `README*`, `LICENSE*`, `CHANGELOG*`, `CONTRIBUTING*`, `SECURITY*`, `CODEOWNERS`, `requirements.txt`, `Pipfile`, `pyproject.toml`, `package.json`, `Cargo.toml`, `go.mod`, `Makefile`, `Dockerfile`, `.github/dependabot.yml`, ...), **plus** the single glob special-case: any file in `.github/workflows/` ending `.yml`. Not general globs. Port the list verbatim. Note: important files are prepended **bare**, so their contents aren't shown, and on a small `mapTokens` budget they can consume the prefix before any ranked defs appear — faithful, worth knowing.

### 4.2 Binary search over prefix length

- `lower=0`, `upper=len(items)`, `middle = min(maxMapTokens/25, num)`.
- while `lower <= upper`: render `to_tree(items[:middle], chatRelFnames)` (§5), count tokens (§4.3); `pctErr = |tok - max| / max`, `okErr = 0.15`; if `(tok <= max and tok > best) or pctErr < 0.15` -> record best; if `pctErr < 0.15` **break**; if `tok < max` -> `lower = middle+1` else `upper = middle-1`; `middle = (lower+upper)/2`.
- return best.

**Budget is soft, and say so:** the early exit can accept up to ~15% over target, and the framing prefix is added afterward (uncounted). `maxMapTokens` is a target, not a hard cap. For Strument's default (1024 in a >=128k window) the overage is negligible. If a hard cap is ever wanted, restrict the "record best" branch to `tok + prefixTokens <= maxMapTokens` and count the prefix inside the loop.

### 4.3 Token estimate

Match aider's cheap estimator. Since Strument uses estimated counts anyway, `estimate(text) = runeCount(text) / 4` is adequate — count **runes (code points)**, not bytes, since aider's `len(str)` is code points and a UTF-8 byte count would over-estimate non-ASCII source. The search self-corrects, so exact fidelity isn't required; golden tests assert on rendered *structure* (which files/tags appear), not token counts.

## 5. Rendering (`to_tree` + TreeContext)

### 5.1 `to_tree(items, chatRelFnames)`

Sort `items` with `lessMapItem` (§0.1); append a sentinel to flush the last file. Walk, grouping consecutive items by `relFname`:

- skip any item whose `relFname in chatRelFnames`.
- on file change: emit the previous file — `"\n" + relFname + ":\n" + renderTree(absFname, relFname, lois)` when it had tag lines; a file that only appeared bare emits `"\n" + relFname + "\n"` (no body).
- accumulate each `Tag.Line` into `lois`.

Finally truncate every output line to **100 runes** (not bytes — guards minified files without splitting a codepoint); ensure trailing newline.

### 5.2 `renderTree` config + source normalization

Read the file; **if it doesn't end in `\n`, append one** (the scope/`splitlines` math depends on it). Decode UTF-8 with replacement, or skip non-UTF-8 files (match aider's implicit UTF-8). Build `TreeContext` with exactly:

```
color=false, line_number=false, child_context=false, last_line=false,
margin=0, mark_lois=false, loi_pad=0, parent_context=true(default), show_top_of_file_parent_scope=false
```

Set `linesOfInterest = lois`, call `addContext()`, then `format()`. Memoize the built context per file for the call (§0.3).

### 5.3 TreeContext algorithm (the one careful reimplementation)

Shows only lines of interest plus the **headers of the scopes containing them**, gaps elided by the vertical-ellipsis marker. Port these grep_ast methods: `walkTree`, header finalization, `addContext`, `addParentScopes`, `closeSmallGaps`, `getLastLineOfScope`, `format`. **Skip** `grep`, `addChildContext`, `findAllChildren` (repo map disables them) — that's the difference between grep_ast's ~220 lines and the ~150 you actually port.

**walkTree — iterative, not recursive.** Go has no recoverable stack overflow; a deeply nested AST (generated code) would panic where aider catches `RecursionError` and disables the map. Use an explicit stack. Per node with `start..end`:

- append node to `nodes[start]`
- if `size = end - start > 0`, append `(size, start, end)` to `header[start]`
- for `i in [start, end]`, add `start` to `scopes[i]`.

**Header finalization (verified rule).** For each line `i`, sort `header[i]`; then:

- **if `len(header[i]) > 1`**: take `header[i][0]` (smallest size) as `(size, hs, he)`; if `size > headerMax` (**headerMax = 10**), `he = hs + headerMax`.
- **else** (zero or exactly one candidate): `hs = i`, `he = i + 1` (single line).

Store `header[i] = (hs, he)`. The `> 1` is not "has entries" — a line with a single multi-line node starting on it uses the single-line header. (A stray commented-out `header_max=30` in the source is not active; the default is 10.)

**addContext.** `showLines = set(lois)`. With the repo-map config: `loi_pad=0` (skip padding), `last_line=false` (skip bottom-line and its recursion — note `addParentScopes` contains a `last_line`-gated recursive descent into scope-end lines that stays **disabled**), `parent_context=true` -> for each loi call `addParentScopes(i)`, `child_context=false` (skip), `margin=0` (skip). Then `closeSmallGaps()`.

**addParentScopes(i)** (guarded by a `done` set): for each scope start `line_num in scopes[i]`, show `header[line_num]`'s range **only if `head_start > 0`** (since `show_top_of_file_parent_scope=false`, the file's line-0 outermost header is not forced in). The `last_line` recursion is skipped.

**closeSmallGaps.** (1) if lines `i` and `i+2` show but `i+1` doesn't, add `i+1`; (2) if a shown line has content and the next line is blank, show the blank too. This is what makes the skeleton read as blocks rather than shrapnel.

**format.** Iterate source lines; runs of hidden lines collapse to one vertical-ellipsis marker (a leading marker appears only when line 0 is hidden; the `dots` flag prevents consecutive markers). Shown lines get a vertical-bar prefix (repo map uses `mark_lois=false`, so no block marker). No line numbers.

## 6. Determinism (enumerate every ordering)

Go maps and Python sets both randomize iteration; pin all of it:

- collection scan: `sorted(chatFiles union otherFiles)`.
- `definitions[(fname,ident)]` tags: sort `(RelFname, Line, Name, Kind)` before rendering.
- definers of an ident, referencers after counting, graph nodes, a node's out-edges: iterate sorted wherever iteration affects accumulation or output.
- ranked defs: `(rank desc, (fname,ident) desc)`. bare nodes: `(rank desc, node desc)`. leftover `otherFiles`: sorted asc.
- lines of interest and `showLines`: operate on sorted ints.
- final `MapItem` list: `lessMapItem`.

## 7. Mapper / file error policy

- Query compile failure for a configured supported language -> fatal at startup-test.
- Per-file read/parse/tag failure -> warn once, continue with the file as a bare entry.
- chroma backfill lexer failure -> keep defs, continue without backfilled refs.
- Non-UTF-8 file -> skip (or replacement-decode), warn once.
- Mapper selection is deterministic by extension (v1: always `TreeSitterMapper`).

## 8. Port checklist

1. Tagged-union `MapItem` + `lessMapItem`; `TreeSitterMapper` over gotreesitter's **`Query`** API, capture-prefix inspection, single emission (§1.1). Embed the `.scm` files; startup-compile them.
2. chroma backfill with hierarchical `Name.*` predicate (§1.3).
3. Collection + graph with the exact multipliers; `sqrt` once; per-`(src,dst,ident)` edge identity (§3.4).
4. Power-iteration PageRank, `L1 < N*1e-6`, failure taxonomy (§3.5).
5. Rank distribution + bare-node pass over all nodes incl chat (§3.6).
6. `special.py` exact-match list + `.github/workflows/*.yml` (§4.1); soft binary-search budget with prefix counted (§4.2); rune-based token estimate (§4.3).
7. `TreeContext` reimplemented **iteratively**, header rule `len>1`, `parent_context` on, `last_line` recursion off (§5.3).
8. Determinism section satisfied everywhere (§6); centralized path canonicalization (§2.1).
9. Cache tiers: no cross-call cache, process-lifetime queries, invocation-local parse/render memoization (§0.3).

## 9. Testing (layered, not one broad oracle)

1. **Query-compat:** compile the exact embedded aider queries; assert extracted def/ref identifier text and 0-based line per supported language.
2. **Ranker (injected tags, no parsing):** multiplicity, long-name boost, mentioned boost, `_private` penalty, over-defined penalty, chat x50, dangling behavior, deterministic ties.
3. **PageRank parity:** small hand-built graphs vs networkx 3.x fixtures generated once; assert order and approximate scores.
4. **TreeContext golden:** zero/one/multi header-candidate cases, nested defs, multiline signatures, leading omission, blank-line closure, line-0 parent suppression.
5. **Budget:** exact/under/soft-overrun, empty prefix, many important files, framing-token behavior.
6. **End-to-end:** small multilingual fixture repo; stable rendered map; chat files excluded; unsupported files appear bare.
7. **Performance (cold):** record files/bytes/parse/graph/PageRank/render timings separately; no persistent cache by design.

Golden fixtures are **regenerated from the corrected Go implementation**; they differ from upstream's in raw tag-emission order (double-append, `sqrt` placement) but must match on final ranking and rendered skeleton. Assert on the latter.

## 10. Dropped vs. aider

All caches and `refresh` modes; `tqdm`/`Spinner` callbacks; the 0.23/0.24 `QueryCursor` shim; the capture double-append; the compounding `sqrt`; networkx/scipy (-> power iteration); `GoMapper` (-> v2, §1.4).
