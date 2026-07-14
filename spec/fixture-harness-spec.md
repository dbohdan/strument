# Spec: fixture capture & replay harness

Fills the gap that blocks phases 4, 5, and 8: `basecoder-spec.md §11` says what a fixture *contains*; nothing said how one is *produced*. This does.

Reference: aider @ `5dc9490bb35f9729ef2c95d00a19ccd30c26339c`. Verified plumbing: `main.py:620` maps `--openai-api-base` onto `OPENAI_API_BASE`; `args.py:639` `--message`; `args.py:760` `--yes-always`. So aider is scriptable and redirectable **without patching it**.

## 0. Core decision: capture the wire, author the scenario

Do **not** monkey-patch aider by default. Split the problem:

| Fixture part | Origin | Why |
|---|---|---|
| Assembled provider request | **captured** (aider → recorder) | This is the thing `basecoder-spec §3` must reproduce. Only the real client emits it. |
| Model response (SSE stream, usage, cost) | **captured** (OpenRouter → recorder) | Dialect fidelity for phase 4; realistic content for phase 5. |
| fs/git starting state | **authored** | You construct the scenario; capturing it is circular. |
| Confirmation answers | **authored** | You drive aider with `--yes-always` or a scripted stdin; you already know them. |
| Shell command results | **authored** | `CommandRunner` is an injected port; tests stub it. |
| Expected effects (fs, history, usage) | **authored**, from the spec | These are the assertions. Capturing them would make aider the spec, not the spec the spec. |

Corollary that removes most of the difficulty: **determinism at capture time is not required.** You capture *one* run; whatever the model said is frozen into the fixture. `temperature=0` is a convenience for regeneration, not a correctness requirement. Only *replay* must be deterministic, and replay is a file read.

Second corollary: **most `§11` failure paths cannot be captured at all** — you can't reliably make a provider drop mid-stream, return `finish_reason:"length"` on cue, or emit an empty response. Those fixtures are **authored or mutated** from a captured one (truncate the event list; flip a `finish_reason`; delete the usage row). Capture buys realism for the happy paths and the wire dialect; hand-authoring covers the failure matrix, which is where the bugs live.

## 1. The recorder

A dev-only tool in-repo: `cmd/strumentrec` (Go, ~150 lines). An HTTP reverse proxy, **not** mitmproxy — no TLS interception, no CA trust.

```
aider  --http-->  strumentrec (localhost:8djd)  --https-->  openrouter.ai
                        |
                        +--> testdata/fixtures/<subsystem>/<scenario>.jsonl
```

Capture invocation:

```
strumentrec record --out testdata/fixtures/basecoder/edit-success.jsonl \
                   --upstream https://openrouter.ai/api/v1 &

python -m aider --no-git --yes-always \
                --openai-api-base http://localhost:8djd/v1 \
                --model openai/deepseek/deepseek-v4-flash \
                --message 'add a hello function to main.go' main.go
```

The recorder logs both directions verbatim, re-emitting the upstream SSE unchanged so aider behaves normally. Use the `openai/` model prefix so litellm routes through `OPENAI_API_BASE` (the verified redirect); the recorder forwards to OpenRouter and the OpenRouter dialect still appears on the wire.

**Secret hygiene (mandatory):** the recorder strips `Authorization`, `api-key`, `x-api-key`, and cookies from every captured request before writing. Fixtures are committed; keys are not. A test asserts no fixture contains a bearer token.

Replay needs no server: Go tests stub the `ModelClient` port (`basecoder-spec §0`) with a reader over the JSONL. No network in the test suite, per the guide's standing order.

## 2. Schema — JSON Lines, one scenario per file

Heterogeneous rows tagged by `kind`, in order. Versioned; `v` is checked on load and a mismatch fails loudly.

```jsonc
{"v":1,"kind":"meta","scenario":"edit-success","source":"captured|authored|mutated",
 "recorded":"2026-07-16","aider_sha":"5dc9490bb35f9729ef2c95d00a19ccd30c26339c",
 "model":"deepseek/deepseek-v4-flash","notes":"mutated from edit-success: truncated stream"}

// --- Given ---
{"kind":"fs","path":"main.go","content":"package main\n..."}
{"kind":"git","mode":"none"}                      // none | clean | dirty | commits:<n>
{"kind":"config","models":{"m":{...}},"default":"m"}
{"kind":"chat","editable":["main.go"],"readonly":[]}
{"kind":"user","text":"add a hello function"}
{"kind":"confirm","prompt":"Run shell command?","answer":"y"}     // consumed in order by the Confirmer stub
{"kind":"command","block":"go test ./...","exit":0,"output":"ok\n"} // consumed by the CommandRunner stub

// --- The model ---
{"kind":"request","body":{"model":"...","messages":[...],"stream":true}}   // captured; asserted (§3)
{"kind":"stream","events":[
   {"kind":"Reasoning","text":"..."},
   {"kind":"Answer","text":"main.go\n<<<<<<< SEARCH\n..."},
   {"kind":"Finish","finish_reason":"stop"},
   {"kind":"Usage","usage":{"prompt_tokens":812,"completion_tokens":96,"cost":0.00013}}]}

// --- Expect ---
{"kind":"expect_fs","path":"main.go","content":"package main\n\nfunc hello() {...}\n"}
{"kind":"expect_outcome","outcome":"Success","reflections":0}
{"kind":"expect_history","messages":[{"role":"user","text":"..."},{"role":"assistant","text":"..."}]}
{"kind":"expect_usage","sent":812,"received":96,"cost_known":true,"usd":0.00013}
```

Multi-send scenarios (reflection, continuation) simply repeat `request`/`stream` pairs in order; the replay stub serves them successively and asserts each `request` as it goes.

## 3. Asserting the request — semantic, not byte-exact

`basecoder-spec §11` says "assert exact assembled provider requests." **Byte-exactness is the wrong target** and would fail on day one: Strument is a different client (JSON key order, `User-Agent`, HTTP/2 framing, omitted-vs-null optionals). Assert a **canonical subset**:

- compare parsed JSON, not bytes;
- compare `model`, `stream`, `temperature`, `messages[*].{role,content}`, and any reasoning/service-tier fields the adapter sets;
- ignore: header order, key order, `User-Agent`, and fields Strument deliberately drops (`functions`, tool params — `[Divergence]`, no tool-calling);
- normalize: strip trailing whitespace inside message content before comparison? **No** — content whitespace is load-bearing for editblock. Do not normalize message content at all.

Diff output must print the first differing message index and a unified diff of that message's content; a whole-request dump is unreadable at 8 KB of prompt.

Where Strument intentionally differs from aider's request (dropped tool params; the corrected reminder gate, `§3.4`), the fixture's `request` row is **annotated** rather than asserted:

```jsonc
{"kind":"request","body":{...},"assert":"subset","ignore":["functions","tools"],
 "known_divergence":["reminder-gate-single-count"]}
```

## 4. Scenario corpus (maps 1:1 onto `basecoder-spec §11`)

Layout: `testdata/fixtures/<subsystem>/<scenario>.jsonl`.

**Captured (real model, happy paths) — ~8 files, phase 4/5:**
`edit-success`, `edit-multifile`, `no-edit-conversational`, `edit-plus-shell`, `reflection-search-not-found` (drive it by handing the model a stale SEARCH block), `repo-map-present`, `reasoning-model-inline-think` (DeepSeek), `openrouter-usage-cost`.

**Authored/mutated (failure matrix) — the rest of `§11`:**
`continuation-stitch` + `continuation-cap` (mutate: `finish_reason:"length"` ×N), `retry-discards-partial` (inject an error row mid-stream), `interrupt-mid-stream`, `interrupt-then-mention` (the partial names a file — assert **no** reflect), `empty-stream`, `failed-after-partial`, `context-exhausted-empty` vs `-with-partial`, `checktokens-declined`, `shell-from-failed-attempt`, `suggest-shell-off`, `dup-shell-blocks`, `fence-escalation` (a chat file containing triple backticks), `unreadable-chat-file`, `path-traversal-rejected`, `multifile-rollback`, `dry-run-suppresses-writes`, `reminder-sys` / `reminder-user` / `reminder-unknown-max`, `stale-accumulator-regression` (**the H1 regression: continuation-bearing send followed by a reflection**).

An error row for the retry/failure fixtures:

```jsonc
{"kind":"stream","events":[{"kind":"Answer","text":"partial…"},
                           {"kind":"Error","class":"network","message":"connection reset"}]}
```

`class` ∈ `network | rate_limit | context_window | auth | server` — it drives the `§2.1` classification table, so every branch there gets a fixture.

## 5. Rules

1. **No live LLM in the automated suite.** Capture is a manual dev action; `go test ./...` never opens a socket.
2. **Fixtures are committed; keys never are.** §1 strip-list + a guard test.
3. **A mutated fixture declares its parent** in `meta.notes`, so a reader knows the stream isn't real.
4. **Expectations come from the spec, not from aider's behavior.** If a capture shows aider doing something the spec forbids (the double-append, the compounding `sqrt`, the double-counted reminder), the fixture encodes the **spec's** expectation and lists the delta in `known_divergence`. Fixtures must never silently canonize aider's bugs.
5. **Patching aider is a fallback, not the plan.** Because `reference/` is SHA-pinned, patch-rot is a non-issue — but the proxy already yields the two hard artifacts (real request, real response), and the rest is authored. If a scenario genuinely can't be driven from the CLI, patch `InputOutput.confirm_ask` / `run_cmd` in a **gitignored** working copy, record, and note it in `meta`. Never redistribute patched aider (Apache-2.0 §4: state changes; simplest compliance is not shipping it).
6. **License:** captured model outputs are Strument's own data. The fixtures directory carries no aider code.

## 6. Phase-0 deliverables

- `cmd/strumentrec` record mode + secret-strip + the no-token guard test.
- Schema types + a JSONL loader that fails loudly on `v` mismatch.
- The `ModelClient` / `Confirmer` / `CommandRunner` replay stubs (`basecoder-spec §0` ports).
- One end-to-end smoke fixture (`edit-success`), captured, proving the loop before phase 4 depends on it.
