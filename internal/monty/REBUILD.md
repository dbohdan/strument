# Rebuilding monty.wasm — the shim changes

`internal/monty/monty.wasm` is built from the Rust shim in
[fugue-labs/monty-go](https://github.com/fugue-labs/monty-go)
(`crates/monty-wasm`), not from pydantic/monty directly — upstream replaced
its C-ABI shim with a WIT component model that wazero cannot consume this
way. The shim in the vendored blob carries changes to track the pydantic/monty
API; this file records them as *changes to make*, written so that a model or
a human can re-derive them when upstream has drifted. Match each change by
its *intent*, not by exact line text — names and file layout may differ
slightly across upstream versions. The SHA-256 of the blob these instructions
last produced is in `NOTICE`.

## Procedure

1. Clone fugue-labs/monty-go; check out the commit recorded in `NOTICE`.
2. `rustup target add wasm32-wasip1`.
3. Apply the changes below (the first section is a plain version bump; the
   rest adapt the shim to API changes).
4. `make build` — the Makefile handles `--remap-path-prefix` (Rust's
   `-trimpath`) and DWARF stripping; see its comments. The blob must contain
   no `/home`, `/tmp`, or `/root` paths:
   `strings -n 8 monty.wasm | grep -cE '/home|/tmp/|/root'` must print 0.
5. Copy `monty.wasm` into `internal/monty/`, update the SHA-256 in `NOTICE`,
   and run `go test ./internal/coder/...` — codetool_test.go covers the
   behavior Strument depends on.

## Dependency changes (crates/monty-wasm/Cargo.toml)

- Bump the `monty` git dependency's `tag` to the target pydantic/monty
  release. If the API changes below do not compile, the tag is too new for
  these instructions — either fix forward (preferred; send the resulting
  edits back into this file) or pick the newest tag that does compile.
- Add `monty-types` from the same git repository and tag. pydantic/monty
  split its public value/exception/resource types out of the `monty` crate;
  the shim needs both. Skip this if, at the target tag, the types the shim
  imports are still exported from `monty` itself.
- Pin `get-size2 = "=0.10.1"` **only if** the build fails with
  `CompactString: GetSize is not satisfied` inside `ruff_python_ast`: that
  means get-size2 0.10.2+ resolved against `compact_str` 0.10 while
  `ruff_python_ast` uses `compact_str` 0.9, and the two cannot coexist. Pin
  to whatever get-size2 version the failing build's dependency tree agrees
  on — upstream pydantic/monty's own `Cargo.lock` at the same tag is the
  authority. Remove the pin if the tree resolves cleanly without it.

## Code changes (crates/monty-wasm/src/lib.rs)

Each change below is keyed by the compile error or behavior change that
motivates it. If an error of the given shape does not appear at the target
tag, the corresponding change is probably already unnecessary — skip it
rather than applying it mechanically.

### Types moved to `monty_types`

**Symptom:** `unresolved import` / `use of unresolved crate` for names like
`DictPairs`, `ExcType`, `ExtFunctionResult`, `MontyException`, `MontyObject`,
`NameLookupResult`, `PrintWriter`, `PrintWriterCallback`, `ResourceLimits`.

Split the `use monty::{…}` block in two: interpreter-facing types
(`FunctionCall`, `MontyRun`, `OsCall`, `ResolveFutures`, `RunProgress` — the
things that describe a run's *progress*) stay on `monty`; value and resource
types move to `use monty_types::{…}`. When in doubt, check which crate the
target tag exports a name from (`cargo doc -p monty --open` or grep its
`src/lib.rs`).

### Generic tracker parameter removed

**Symptom:** `struct takes 0 generic arguments but 1 generic argument was
supplied` on `RunProgress<T>`, `FunctionCall<T>`, `OsCall<T>`,
`ResolveFutures<T>`.

pydantic/monty removed the `<LimitedTracker>`-style generic from its public
progress types. Delete the generic parameter everywhere it appears in the
shim: the `SnapshotState` enum variants, the `progress_to_result` signature,
and the `drive_progress` signature.

### `LimitedTracker` replaced by `ResourceTracker`

**Symptom:** `cannot find LimitedTracker` or `no function associated item
named new` on `LimitedTracker::new`.

Replace `LimitedTracker::new(limits)` with `ResourceTracker::new(limits)`
from `monty_types`. Same role — wraps a `ResourceLimits` for `start()` —
different name.

### `MontyRun::new` takes a `CompileOptions`

**Symptom:** `this function takes 4 arguments but 3 arguments were supplied`
on `MontyRun::new`.

Append `CompileOptions::default()` as the last argument at *both* call sites
(the `monty_check` feasibility probe and `monty_compile`). Import
`CompileOptions` from `monty_types`.

### `OsCall` carries a typed `OsFunctionCall`

**Symptom:** `no field function on OsCall`, or `args`/`kwargs` fields gone.

The OS call stopped being a flat `(function, args, kwargs)` triple. It now
has `function_call: OsFunctionCall` (a typed enum with one variant per OS
operation) and `call_id`. In `progress_to_result`'s `OsCall` arm, recover
the flat view with `let (args, kwargs) = call.function_call.clone()
.to_args();` — the public projection method — and serialize those as before.
The wire format to Go (`os_function` string, `args` array, `kwargs` object)
must not change; `to_string()` on the enum yields the same
`"Path.read_text"`-style names the old `.function` field held. Verify by
running a program that does `open()` and checking the Go side still sees a
recognized OS function name.

### `max_allocations` no longer enforced (behavioral, not a compile fix)

**Symptom:** `struct ResourceLimits has no field named max_allocations`.

Allocation accounting moved out of the tracker into the `monty-alloc` global
allocator, which this shim does not arm. Drop the field from the
`ResourceLimits` construction in `monty_start` — but keep parsing
`max_allocations` in `LimitsInput` (mark it `#[allow(dead_code)]`): the Go
side still sends it, and the wire format must stay stable. Default
`max_recursion_depth` to `monty_types::DEFAULT_MAX_RECURSION_DEPTH` when the
wire value is absent, since the field lost its `Option` wrapper and is now
mandatory. Document in `NOTICE` that `max_allocations` is not enforced —
`max_duration` and `max_recursion_depth` are.

## Wire-format invariant

The C-ABI exports (`wasm_alloc`, `monty_compile`, `monty_start`,
`monty_resume`, `monty_resume_futures`, `monty_result_len`,
`monty_result_read`, `monty_free_runner`, `monty_free_snapshot`) and the JSON
`ProgressResult` shape are the contract with the Go wrapper in
`internal/monty/wasm.go`. Changes above must preserve both. If a new
pydantic/monty release cannot be adapted without changing them, that is a
Go-wrapper change too — stop and make it deliberately, not as a side effect
of a rebuild.

## Known upstream drift candidates (not yet needed)

Watched but not yet hit; likely shapes of future changes:

- New `RunProgress` variants (beyond `Complete`, `FunctionCall`, `OsCall`,
  `NameLookup`, `ResolveFutures`): `drive_progress`'s loop must handle or
  explicitly reject each one. The `NameLookup` auto-resolution loop against
  `PARAM_REGISTRY` is load-bearing — external functions only work because
  unresolved names resolve to `MontyObject::Function` for registered names
  and to `Undefined` (→ `NameError`) otherwise.
- `MontyObject` gaining variants (datetime types, `Cycle`, `Repr` arrived
  this way): `monty_to_json`'s fallback arm turns unknown variants into a
  debug-string, which is lossy but safe. Decide per variant whether to
  serialize properly — a variant a model-written program can *return*
  deserves real serialization.
- `ExcType` growing: error strings reach the model verbatim, so new
  exception types need no shim work.
