# The WASM shim

`../monty.wasm` — the Python interpreter blob the Go wrapper in this
directory embeds and runs through wazero — is built from the Rust shim in
this directory (`crates/monty-wasm`). The shim is a hard fork of the one
fugue-labs/monty-go shipped (frozen at commit `c5fede1`); it is maintained
here now, and upstream will not be pulled from again.

It wraps pydantic/monty's Rust crate (a git dependency, currently pinned to
tag `v0.0.21`) and exposes its pause/resume API as the C-ABI WASM exports
the Go wrapper in `../wasm.go` drives: `wasm_alloc`, `monty_compile`,
`monty_start`, `monty_resume`, `monty_resume_error`, `monty_resume_futures`,
`monty_result_len`, `monty_result_read`, `monty_free_runner`,
`monty_free_snapshot`.

`monty_resume_error` is this fork's addition: it resumes a snapshot by
raising a `{"type", "message"}` exception at the call site instead of
returning a value, so a host-side tool failure re-enters the interpreter and
the traceback names the program line that made the call. Without it, the Go
wrapper had to drop the snapshot on handler error and tool-call failures
reached the model as a flat one-liner with no attribution.

## Building

```sh
rustup target add wasm32-wasip1   # once
make -C internal/monty/shim check
```

`check` builds the blob, installs it as `internal/monty/monty.wasm`, refuses
a blob that embeds local paths, and runs the Go tests that exercise the
blob. `build` skips the verification. The Makefile passes
`--remap-path-prefix` for the build directory and the cargo home (Rust's
`-trimpath`) and strips DWARF; `CARGO_HOME` is resolved explicitly because
it is often unset in the environment and rustc then defaults to
`$(HOME)/.cargo` — without the remap, dependency paths survive in the blob.

The build is reproducible on a fixed toolchain: three cold builds into
separate `CARGO_TARGET_DIR`s have produced the identical hash. The gate is
the rustc version and the resolved dependency versions — the shim builds
against git dependencies, so two machines agree only with the same
`Cargo.lock` and the same rustc. The path part is fully handled by the
remap.

## The shim's local changes (the v0.0.11 → v0.0.21 adaptation)

These are in the tree. They are recorded as *changes to make*, keyed by the
compile error that motivates each, so a future upgrade to a newer
pydantic/monty tag can be re-derived when the code has drifted from this
description — match by intent, not by line text, and skip any change whose
symptom no longer appears.

### Types moved to `monty_types`

**Symptom:** `unresolved import` for names like `DictPairs`, `ExcType`,
`ExtFunctionResult`, `MontyException`, `MontyObject`, `NameLookupResult`,
`PrintWriter`, `PrintWriterCallback`, `ResourceLimits`.

Split the `use monty::{…}` block in two: interpreter-facing types
(`FunctionCall`, `MontyRun`, `OsCall`, `ResolveFutures`, `RunProgress` —
the things that describe a run's *progress*) stay on `monty`; value and
resource types move to `use monty_types::{…}`. When in doubt, check which
crate the target tag exports a name from. Add `monty-types` from the same
git repository and tag to `crates/monty-wasm/Cargo.toml`; skip if the
target tag still exports these from `monty` itself.

### Generic tracker parameter removed

**Symptom:** `struct takes 0 generic arguments but 1 generic argument was
supplied` on `RunProgress<T>`, `FunctionCall<T>`, `OsCall<T>`,
`ResolveFutures<T>`.

pydantic/monty removed the `<LimitedTracker>`-style generic from its public
progress types. Delete the generic parameter everywhere it appears in the
shim: the `SnapshotState` enum variants, the `progress_to_result`
signature, and the `drive_progress` signature.

### `LimitedTracker` replaced by `ResourceTracker`

**Symptom:** `cannot find LimitedTracker` or `no function associated item
named new` on `LimitedTracker::new`.

Replace `LimitedTracker::new(limits)` with `ResourceTracker::new(limits)`
from `monty_types`. Same role — wraps a `ResourceLimits` for `start()` —
different name.

### `MontyRun::new` takes a `CompileOptions`

**Symptom:** `this function takes 4 arguments but 3 arguments were
supplied` on `MontyRun::new`.

Append `CompileOptions::default()` as the last argument at *both* call
sites (the `monty_check` feasibility probe and `monty_compile`). Import
`CompileOptions` from `monty_types`.

### `OsCall` carries a typed `OsFunctionCall`

**Symptom:** `no field function on OsCall`, or `args`/`kwargs` fields gone.

The OS call stopped being a flat `(function, args, kwargs)` triple. It now
has `function_call: OsFunctionCall` (a typed enum with one variant per OS
operation) and `call_id`. In `progress_to_result`'s `OsCall` arm, recover
the flat view with `let (args, kwargs) = call.function_call.clone()
.to_args();` — the public projection method — and serialize those as
before. The wire format to Go (`os_function` string, `args` array,
`kwargs` object) must not change; `to_string()` on the enum yields the
same `"Path.read_text"`-style names the old `.function` field held.

### `max_allocations` no longer enforced (behavioral, not a compile fix)

**Symptom:** `struct ResourceLimits has no field named max_allocations`.

Allocation accounting moved out of the tracker into the `monty-alloc`
global allocator, which this shim does not arm. Drop the field from the
`ResourceLimits` construction in `monty_start` — but keep parsing
`max_allocations` in `LimitsInput` (mark it `#[allow(dead_code)]`): the Go
side still sends it, and the wire format must stay stable. Default
`max_recursion_depth` to `monty_types::DEFAULT_MAX_RECURSION_DEPTH` when
the wire value is absent, since the field lost its `Option` wrapper and is
now mandatory. `max_duration` and `max_recursion_depth` are still
enforced.

### Dependency pin: `get-size2`

**Symptom:** `CompactString: GetSize is not satisfied` inside
`ruff_python_ast`.

get-size2 0.10.2+ resolved against `compact_str` 0.10 while
`ruff_python_ast` uses `compact_str` 0.9, and the two cannot coexist. The
pin `get-size2 = "=0.10.1"` in `crates/monty-wasm/Cargo.toml` fixes it.
When upgrading the pydantic/monty tag, first try *without* the pin —
upstream's own `Cargo.lock` at the tag is the authority on which versions
agree — and re-add it only if the tree resolves into the same conflict.

## Wire-format invariant

The C-ABI exports and the JSON `ProgressResult` shape are the contract with
the Go wrapper in `../wasm.go`. Changes here must preserve both. If a new
pydantic/monty release cannot be adapted without changing them, that is a
Go-wrapper change too — stop and make it deliberately, not as a side
effect of a rebuild.

## Known drift candidates (watched, not yet hit)

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
