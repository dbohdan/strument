# Plan: a `code` tool — sandboxed Python, then a read-only tool bridge

**Status:** not started. Tick the boxes as you go; this file is the progress
record. Delete it when the work lands.

**You are implementing this in a fresh session.** Everything you need is below
or named by path. Read Part 0 before writing any code.

---

## Context: why this exists

Two measured facts from this repository's own experiments motivate it.

**1. Models spend a lot of reasoning on arithmetic.**
[`doc/experiments/2026-08-skill-uptake.md`](../experiments/2026-08-skill-uptake.md)
found ~2,300 tokens per run of reasoning spent joining numerals with operators —
recomputing bar heights and coordinates by hand — about a quarter of all
reasoning lines, and ~12,700 tokens in the worst single run. A skill did not
reduce it. That work wants a calculator.

**2. Models make runs of read-only tool calls that cost a model round trip
each.** In `doc/experiments/2026-08-symbol-uptake-data/`, on a task that
required exploring this repository, runs averaged 5.3 observation calls with
**4.0 removable round trips per run** (longest consecutive run: 14). Each of
those is a full request that re-sends the context.

A tool that lets the model write one small program — computing locally, and
calling the read-only tools from inside — addresses both.

**Known counter-argument, do not ignore it:** improving a single tool already
halved the removable round trips (4.00 → 2.04 when `symbol` was improved). So
sharpening tools is a competing remedy. The trial in Part 4 is what decides
whether this earns its place.

---

## Part 0 — orient yourself first

- [ ] Read [`CLAUDE.md`](../../CLAUDE.md) — the project's rules. Especially the
      `env_allow` rule and "verify before you commit".
- [ ] Read the **Philosophy** section of [`doc/README.md`](../README.md).
- [ ] Read [`internal/coder/inspect.go`](../../internal/coder/inspect.go) in
      full. It is the seam this whole plan hangs on.
- [ ] Read [`internal/coder/tools.go`](../../internal/coder/tools.go) lines
      1–120 (tool constants, `toolDefs`) and the dispatch `switch` around line
      540.
- [ ] Skim [`doc/experimenting.md`](../experimenting.md) §17, §18, §19. You will
      need them in Part 4. They are short.

**The one design rule you must not break.** Strument is a *reviewable loop*:
the human's review surface does not move. Concretely — **only tools that never
ask permission may be reachable from inside a program.** Those are exactly the
five in `InspectorTools()`: `read`, `grep`, `glob`, `ls`, `symbol`. They are
*announced*, not *confirmed*.

`bash`, `edit`, `write`, `commit` and `check` must **never** be bridged. If they
were, the question a user answers changes from "may I run this command?" to
"may I run this program that will issue commands you cannot enumerate?" That is
the one thing the philosophy forbids. If you find yourself wanting to bridge a
mutating tool, stop and leave a note instead.

---

## Part 1 — vendor the Monty wrapper

`github.com/fugue-labs/monty-go` (MIT) wraps Pydantic's Monty — a restricted
Python interpreter compiled to WebAssembly — and runs it via `wazero`. Pure Go,
no cgo. Its only dependencies are `wazero` and `golang.org/x/sys`.

We vendor rather than depend: upstream is ~8 commits by a single author.

- [x] Create `internal/monty/` with the wrapper source copied from upstream
      (~3k lines), module path rewritten to `dbohdan.com/strument/internal/monty`.
- [x] Add `internal/monty/NOTICE` in the style of
      [`internal/gitignore/NOTICE`](../../internal/gitignore/NOTICE): name the
      upstream project, the commit or release vendored, and the MIT licence.
- [x] Add `github.com/tetratelabs/wazero` to `go.mod`. It is pure Go — confirm
      with `CGO_ENABLED=0 go build ./...`.
- [x] Record the `monty.wasm` blob's **SHA-256 and provenance** in the NOTICE.

**Do not take on the Rust build.** Rebuilding `monty.wasm` needs a Rust
toolchain with the `wasm32-wasip1` target, which this project deliberately does
not require (`CLAUDE.md`: "no cgo, no C toolchain"). Vendor the pre-built
~2.9 MB blob from an upstream release as-is. Write in the NOTICE, plainly, that
the blob is **not** built from source here and that upgrading Monty is a
separate decision with its own cost. A reviewer must not have to guess this.

- [x] `go build ./...`, `go test ./...`, `task lint` all green with the vendored
      package present but unused.

---

## Part 2 — the `code` tool, pure computation only

Ship this before touching the bridge. It is independently useful and much
simpler. If you run out of time, stopping here is a good outcome.

- [x] New file `internal/coder/codetool.go`.
- [x] Add `toolCode = "code"` to the constants in `tools.go`, beside
      `toolSkill`.
- [x] `codeTool() llm.ToolDef` — one required `string` parameter, `code`.
- [x] `runCode(ctx, ...)` executes it through `internal/monty` and returns the
      value, or the error text for the model to act on.
- [x] Offer it in `toolDefs()` **before the ask-mode early return** — computing
      mutates nothing, so it belongs in a discussion turn too. Copy how
      `skillTool` is offered.
- [x] Dispatch it in the `switch` in `tools.go`, beside `toolSkill`.
- [x] Announce each call with `c.Out.Toolf("‹code› …")`, matching `‹skill›`.
- [x] Set resource limits explicitly — start with `MaxDuration: 5s`,
      `MaxMemoryBytes: 32 MiB`, `MaxRecursionDepth: 100`. Do not leave them at
      zero.

### The tool description is load-bearing

Monty is a Python **subset**. A model writing ordinary Python will hit walls,
and the description is the only thing that can prevent that. State plainly what
is missing: **no class definitions, no `with`, no `match`, no imports beyond
`math`/`re`/`datetime`/`json`, no third-party libraries.** Available: f-strings,
`while`, `try/except`, comprehensions, generators, `sum`/`min`/`max`/`sorted`/
`enumerate`/`zip`/`abs`, and all of `math`.

- [x] **Check whether `round()` exists.** It is absent from upstream's builtin
      list and it is the single most likely call in model-written number
      formatting. Write a test either way, and if it is missing, say so in the
      tool description.
      *(Probed: `round()` exists and works, including the two-argument form.
      The probe also found two walls the upstream docs do not name — no
      %-formatting or `.format()`, and f-string zero-padding applies to
      decimal only, not `b`/`x` — so the description says so and `zfill` is
      named as the workaround. Both are pinned in `TestCodeArithmetic`.)*

### Tests (`internal/coder/codetool_test.go`)

- [x] Arithmetic returns the right value.
- [x] An infinite loop terminates on the duration limit rather than hanging.
- [x] A memory bomb terminates on the memory limit.
- [x] Unsupported syntax (a `class` definition) returns a *useful error string*
      to the model, and does not crash the turn.
- [x] The tool is offered in ask mode.
- [x] **No filesystem or network access** — assert that a program attempting
      either fails. This is the security claim; test it, do not assume it.

---

## Part 3 — the read-only bridge

Monty pauses when a program calls an external function; Go runs it and resumes.
That is the mechanism.

**The seam already exists and you should not build a new one.**
`internal/coder/inspect.go` has:

```go
func (i *Inspector) Run(name string, tc llm.ToolCall) string
func InspectorTools() []string   // read, grep, glob, ls, symbol
```

So the bridge is an *adapter*, not a reimplementation:

```
monty FunctionCall{Name, Args}
      → llm.ToolCall{Name: name, Arguments: json.Marshal(args)}
      → c.inspector().Run(name, tc)
      → string result back into Monty
```

- [ ] Register exactly `InspectorTools()` as Monty external functions. Derive
      the list from that function — **do not hardcode five names**, or the two
      lists will drift. (This repository has had that exact bug three times;
      see the comments in `internal/coder/apply.go` and
      `internal/workspace/contain.go`.)
- [ ] Never register an `OsCallFunc`. Monty routes `os`/`pathlib` through it;
      leaving it unset is what keeps the filesystem unreachable.
- [ ] Cap the number of bridged calls per program (start at 50) so one program
      cannot issue unbounded work.
- [ ] Each bridged call is announced exactly as a direct call would be — the
      user sees the same `Read …` / `Searched for …` lines. The review surface
      must look identical whether the model called `read` directly or from
      inside a program.
- [ ] Results cross the boundary as JSON. Keep calls coarse: a bridged call is
      a *tool call*, never a per-element helper.

### Tests

- [ ] A program calling `read()` returns the file's contents.
- [ ] A program calling `grep()` and filtering results in Python works
      end-to-end.
- [ ] **A program attempting `bash("rm -rf /")` fails** with an unknown-function
      error. Add one such test per forbidden tool: `bash`, `edit`, `write`,
      `commit`, `check`.
- [ ] The bridged-call cap fires.
- [ ] Bridged calls are announced.

**Break-on-purpose before you believe any of these** (`experimenting.md` §17):
break the thing each test guards and watch it go red. A test that passes with
the feature broken is measuring nothing. Note which breaks you tried.

---

## Part 4 — the trial

Do not skip to "does it improve answers". **The first question is whether models
use it at all.** A previous feature (`replace_all`) was used once in eighteen
runs, so seventeen runs compared the control arm to itself and the clean-looking
result meant nothing (`experimenting.md` §18).

Reuse the rig in `doc/experiments/2026-08-skill-uptake-data/`: `run.py` is a
shuffled, resumable runner and `report.py` summarises. Adapt, don't rewrite.

**Arms** (three binaries, same tree):
- `A` no `code` tool
- `B` `code` tool, pure computation only
- `C` `code` tool with the read-only bridge

**Fixtures — this is where the trial will fail if you are careless.** The task
must *require exploration*, or the phenomenon cannot occur. Measured evidence:
on one-pinned-file editing tasks only 0.77 round trips per run were removable;
on a real exploration task against this repository, 4.0 were. Use questions
about this codebase that need several greps and reads to answer, with an answer
key you verify by hand *before* running anything. `2026-08-symbol-uptake.md` is
a good model for the task shape.

**Metrics, pre-registered before you run:**
- **Primary:** did the model call `code` at all (per arm, per model).
- **Primary:** model round trips per run — this is the thing being bought.
- **Counter-metric:** was the answer still correct? A cheaper wrong answer is a
  loss. Report it as prominently as the saving.
- **Counter-metric:** programs that errored, and how often a model retried.
- Cost and steps per run.

**Non-negotiable mechanics:**
- [ ] **Verify the arms differ on the wire before spending.** Run one of each
      through `cmd/strumentrec` and confirm A has no `code` tool, B has it, C's
      description names the bridged functions. `wire_check.py` in the skills
      data dir does this.
- [ ] Randomize the whole job list and record the seed.
- [ ] `ty check` the runner before launching it (`pip install ty`). A
      `TypeError` in an exception handler once killed a 234-run trial's
      bookkeeping while the work carried on — §19.
- [ ] Wait on the runner's **PID**, not a success marker in its log:
      `until ! kill -0 "$PID"; do sleep 20; done`. A log grep cannot tell a
      crash from a slow run.
- [ ] Read **at least ten transcripts** before believing any aggregate. Report
      what they say, including anything that contradicts the table.
- [ ] Models: the usual six — `deepseek/deepseek-v4-flash-0731`,
      `openai/gpt-5.6-luna`, `qwen/qwen3.8-27b`, `tencent/hy3`,
      `xiaomi/mimo-v2.5`, `z-ai/glm-5.3-flash`. Budget ~$3.
- [ ] `OPENROUTER_API_KEY` from the environment. **Never write a key to a file
      or a commit.**

- [ ] Write up as `doc/experiments/2026-08-code-mode.md` with a `-data/`
      directory, following the shape of the two existing experiment writeups.
      **If uptake is low, say so plainly and recommend against shipping.** That
      is a successful trial, not a failed one.

---

## Part 5 — docs and final verification

- [ ] `doc/config.md`: a `code` section — what the tool is, the Python subset,
      the bridge, and that mutating tools are deliberately unreachable.
- [ ] `README.md`: one bullet in Features; one row in the tool table if there is
      one.
- [ ] `doc/README.md`: add `internal/monty/` to the codebase map.
- [ ] `gofmt -l cmd internal` is empty.
- [ ] `go test ./...` passes.
- [ ] `go vet ./...` on `GOOS=linux`, `windows`, `darwin`.
- [ ] `task lint` reports **0 issues**. Run it unpiped — `| tail` has hidden a
      failure here before.
- [ ] Commits are conventional-commit style, one logical change each, and
      explain *why*.

---

## If you get stuck

Leave the box unticked, write a short note under it saying what blocked you,
and move to the next item. A partial, honest result is worth more than a
complete-looking one. Parts 1–2 alone are a shippable increment.
