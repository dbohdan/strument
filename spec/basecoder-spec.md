# Spec: Strument base coder / chat loop — v4 (final)

Source: `aider/coders/base_coder.py` @ `5dc9490` (0.86.3.dev), with `chat_chunks.py`, `reasoning_tags.py`, editblock coder + prompts (separate specs).

**v4 changes:** fixes the H1 accumulator-reset bug (reset per-send, not per-message); recasts `sendMessage` as a phase table with explicit terminal returns for every outcome; defines the continuation cap, Failed-after-partial, and empty-response outcomes; corrects the reminder gate (sys/user paths + unknown-max); enumerates the fence list and the `chooseFence` drop-unreadable side effect; makes shell execution gate on config; inlines slot contents so this file is self-contained.

## Parity labels
**[Exact]** byte-identical · **[Observable]** same effect, reimplemented · **[Divergence]** intentional · **[Deferred]** dropped v1. Global: text edit formats only, no tool-calls [Divergence]; edit-failure + file-mention reflection only [Divergence]; no architect/voice/GUI/analytics/cache-warming/images/summarization [Deferred]; git is a mode, `--no-git` default; prompt content [Exact], rendering machinery [Observable] (§3.0).

---

## 0. State and interfaces

```go
type Coder struct {
    absFnames, absReadOnlyFnames *OrderedSet
    doneMessages []Message   // committed history (multi-turn, unsummarized v1)
    curMessages  []Message   // running; spans reflections AND no-edit turns; rotated only on edited turns (§7.4)
    shellCommands []string   // response order, deduped by first occurrence; reset in initBeforeMessage (§6.1)
    turnEditedFiles map[string]bool // reset per TOP-LEVEL message; spans reflections; v1 = observability/reporting only

    numReflections int // max 3

    // send-scoped buffers (lifecycles in §2)
    partialResponseContent  string // ANSWER of current attempt; reset by client.Send per attempt
    partialReasoningContent string // native reasoning, current attempt; reset per attempt; never persisted/parsed
    multiResponseContent    string // accumulated prior answer segments across continuations; reset PER SEND (§2, H1)

    messageCost, totalCost Money  // accumulate across all requests in one send (§8); sendMessage owns, defer-finalizes
    messageTokensSent, messageTokensReceived int

    fence               Fence    // chosen per turn by chooseFence (§3.0)
    commitBeforeMessage []string  // git HEADs pushed by initBeforeMessage
    lastCommitHash      string    // for /undo
    ignoreMentions      map[string]bool // declined file mentions — never re-prompt
    rejectedUrls        map[string]bool // declined URL scrapes — never re-prompt
    lastKeyboardInterrupt time.Time      // double-Ctrl-C chord
}
```

`reflectedMessage` is **not** state — it is the `reflection` return value of `sendMessage`, a local in `runOne` (avoids staleness).

Ports (inject `Clock` so retry/continuation tests don't sleep): `ModelClient`, `TokenCounter` (§10), `EditEngine`, `Repo`, `CommandRunner` (§6.3), `Confirmer`, `Clock`, `Output`.

`Message.Content` is structured, not `any`: `Content{ Text *string; Blocks []ContentBlock }`; `ContentBlock{ Type, Text string; CacheControl *CacheControl }`.

## 1. The run loop

### 1.1 Script mode [Observable] — build first
`Run(ctx, withMessage) -> string` returns `multiResponseContent + partialResponseContent` of the **last** send **regardless of outcome** (including reflection-cap and Failed paths), matching aider's `return self.partial_response_content`. Confirmations per §6.4 (`--yes` does not auto-run shell).

### 1.2 REPL mode [Observable]
`getInput -> runOne -> showUndoHint(git)`. `ctx` from `signal.NotifyContext(ctx, os.Interrupt)`. **Double-Ctrl-C:** during a turn, first Ctrl-C cancels the send and prints "^C again to exit", second within **2s** exits. At the input prompt, first Ctrl-C clears the line (readline), second within 2s exits.

### 1.3 `runOne` — loop on the outcome [Divergence — self-consistent]
```
initBeforeMessage()  // turnEditedFiles={}, numReflections=0, shellCommands=[]; git: push HEAD
message = preproc ? preprocUserInput(userMessage) : userMessage   // "" -> return early, no send
for message != "":
    outcome, reflection = sendMessage(ctx, message)
    if outcome != Reflect: break
    if numReflections >= 3: warn("Maximum of 3 follow-up attempts reached."); return
    numReflections++
    message = reflection
```
Budget: up to **4 sends** (initial + 3 follow-ups); test both numbers.

### 1.4 `preprocUserInput` [Observable]
`""` -> `""` (runOne returns without sending). Slash command -> dispatch; the command **either returns a message string to send or `""`** (commands own their I/O and may just mutate state). Else `checkForFileMentions` (minus `ignoreMentions`), `checkForUrls` (minus `rejectedUrls`), return input.

## 2. `sendMessage` — phase machine

Returns `(SendOutcome, reflection)`. `const ( Success; Reflect; Interrupted; ContextExhausted; OutputExhausted; Failed )`. Stream is `iter.Seq2[StreamEvent, error]`, `StreamEvent{ Kind: Answer|Reasoning|Usage|Finish; Text string; Usage *Usage; FinishReason string }`.

| Phase | Steps |
|---|---|
| **Setup** | append `{user, inp}` to `curMessages`; `chooseFence()` (§3.0); `chunks=formatMessages()`; `messages=chunks.allMessages()`; **checkTokens**: if est ≥ input window, print tips + ask "Try to proceed anyway?"; decline → **remove appended user msg, return `(Failed,"")`** (`--yes` proceeds). |
| **Stream** | `multiResponseContent=""` (**per-send reset, H1**); spinner; retry+continuation loop (§2.1) builds `partialResponseContent` (per attempt) and grows `multiResponseContent`. `finally` **always runs**: flush render; `answer = multiResponseContent + partialResponseContent`; strip inline reasoning tag (§5); `multiResponseContent=""`. |
| **Post-stream dispatch** (in order) | 1. `showUsageReport()` (§8). 2. `addAssistantReply`: append `{assistant, answer}` **only if answer non-empty** (this is why the exhausted-diagnostic condition below is reachable). 3. Terminal returns below. |
| → empty answer | treat as `Failed`-before-output: remove trailing user msg, warn "Empty response…", return `(Failed,"")`. |
| → `ContextExhausted` | if answer empty (trailing msg is `user`) add `{assistant,"FinishReasonLength exception: you sent too many tokens"}`; show exhausted error; return `(ContextExhausted,"")`. |
| → `OutputExhausted` (incl. continuation-cap hit) | answer (large partial) already kept by step 2; **no diagnostic** (trailing is assistant); return `(OutputExhausted,"")`. |
| → `Failed` after partial | answer kept by step 2; no annotation added; return `(Failed,"")`. |
| → `if not interrupted` | file mentions in `answer` (minus `ignoreMentions`) → return `(Reflect, msg)`; then `replyCompleted()` — **truthy return ends the turn** (no-op hook in v1, but honor the contract). |
| → `if interrupted` | annotate (§2.11 shape); return `(Interrupted,"")`. |
| **Success** | `edited=applyUpdates()` (§7) — reflectable error → `(Reflect, report)`. If `edited`: add to `turnEditedFiles`; `saved=autoCommit(edited)`; `saved==""` → no-git note (§7.4); `moveBackCurMessages(saved)`. **[lint would re-enter here, §9]** `runShellCommands()` (§6) → append `{user,output}`+`{assistant,"Ok"}` if output added. **[test would re-enter here, §9]** return `(Success,"")`. |

### 2.1 Retry, continuation, cancellation [Observable + one Divergence]
`send()` resets `partialResponseContent=""` at entry. **Retry** (transient): `retry_delay` starts 0.125, **doubles before sleeping** (first real sleep 0.25s), terminate when `delay > RETRY_TIMEOUT`; via `Clock.Sleep`. A retry discards the current partial (reset next `send()`), leaves `multiResponseContent` untouched. **Continuation** (`length` + prefill support): set `multiResponseContent = accumulated+partial`; if the trailing message is already assistant (2nd+ continuation) **replace its content** with `multiResponseContent`, else **append** a prefixed assistant message; re-send. **[Divergence]** cap at 4 continuations (aider is unbounded); cap-hit → `OutputExhausted`.

Finish-reason → outcome:

| finish_reason | prefill? | outcome |
|---|---|---|
| `length` | yes | continuation (until cap → `OutputExhausted`) |
| `length` | no | `OutputExhausted` |
| context-window error | — | `ContextExhausted` |
| `stop`/`end_turn` | — | success |
| `ctx.Canceled` | — | `Interrupted` (save partial) |
| network drop after retries | — | `Failed` |

Usage from every attempt accumulates (§8). Non-retryable provider error: show a plain error and return `Failed` — **[Divergence]** aider additionally offers docs URLs found in the error (`check_and_open_urls`); Strument drops that.

### 2.11 History policy (per-row labels)
| Outcome | user | assistant partial | rotate | label |
|---|---|---|---|---|
| Success + edits | yes | yes | yes (§7.4) | [Observable] |
| Success, no edits | yes | yes | no (recent history) | [Observable] |
| Reflect | yes | yes | no | [Observable] |
| checkTokens declined / empty answer / Failed-before-output | **remove** | no | no | [Divergence] |
| Failed after partial | yes | yes (partial, no extra note) | no | [Divergence] |
| Interrupted w/ partial | yes | yes (unannotated) | no | [Observable] |
| Context/Output exhausted | yes | partial if non-empty; diagnostic note only to repair trailing-user | no | [Observable] |

**Interrupt shape [Exact]:** if trailing msg is `user`, append `"\n^C KeyboardInterrupt"` to it; else add `{user,"^C KeyboardInterrupt"}` (this is the usual case — the partial assistant is the trailing msg, so a **new user message** carries the annotation, not the partial); then always add `{assistant,"I see that you interrupted my previous reply."}`.

## 3. Message assembly

### 3.0 `chooseFence` + prompt rendering
**`chooseFence`** runs first in `formatChatChunks`. It calls `getAbsFnamesContent`, which **drops unreadable files from the chat** with a "Dropping {fname} from the chat." warning — an observable state mutation during assembly; keep it (or diverge deliberately). Concatenate all chat + read-only content; walk the 7-entry escalation list, pick the first fence whose open/close begins no line; else fall back to entry 0 with a warning. The list [Exact]:

```
("```","```"), ("````","````"),
("<source>","</source>"), ("<code>","</code>"), ("<pre>","</pre>"),
("<codeblock>","</codeblock>"), ("<sourcecode>","</sourcecode>")
```

The chosen `fence` feeds `EditEngine.Parse`, the `{fence}` prompt slot, and disables `show_pretty` for non-backtick fences.

**Prompt rendering (`fmtSystemPrompt`)** substitutes into [Exact] templates (prompts spec): `{fence}`, `{quad_backtick_reminder}`, `{final_reminders}`, `{platform}` (OS, shell var/value, user language, **current date**, in-git flag), `{shell_cmd_prompt}`/`{no_shell_cmd_prompt}` variant (per `suggest_shell_commands`), `{lang}`. Strings verbatim; substitution reimplemented.

### 3.1 Canonical order [Exact]
`allMessages()` = `system + examples + readonly_files + repo + done + chat_files + cur + reminder`. Every context slot uses the **fake-priming** `{user}`+`{assistant,"Ok…"}` pattern. Slot contents (inlined, [Exact] strings):
- **system**: `main_system` (+ optional `system_prompt_prefix`); examples folded here if `examples_as_sys_msg`; `system_reminder` appended. Models with `use_system_prompt=false` → `{user, main_sys}`+`{assistant,"Ok."}`.
- **examples**: few-shots (else in system) + reset pair `{user,"I switched to a new code base. Please don't consider the above files…"}`+`{assistant,"Ok."}`.
- **readonly_files**: `read_only_files_prefix` + contents; `{assistant,"Ok, I will use these files as references."}`.
- **repo**: map; `{assistant,"Ok, I won't try and edit those files without asking first."}`.
- **done**: `doneMessages`.
- **chat_files**: `files_content_prefix` + **all** editable file contents (`getFilesContent` `fnames` param dead [Observable]); `{assistant, files_content_assistant_reply}`. No editable files → `files_no_full_files` / `files_no_full_files_with_repo_map` variant + its reply.
- **cur**: `curMessages`.
- **reminder**: `system_reminder`, per §3.4.

Files sorted deterministically (§3.3). `formatMessages()` = `formatChatChunks()` + cache decoration (§3.2); one name, aliased.

### 3.2 Cache-control [Observable]
Only when the provider supports caching. aider's breakpoints land only on **freshly-built** slots (examples-else-system; repo-else-readonly; chat_files) — never on `done`/`cur` — so its in-place mutation is safe; Strument still decorates a **clone** as defensive practice and to keep `doneMessages` read-only through the alias. **At most 3** breakpoints (Anthropic permits 4). Cacheable prefix ends at `chat_files`; cacheable ≠ stable.

### 3.3 Determinism [Divergence] — sort editable/read-only injection, repo-map inputs, mention prompts, commit lists, fixtures. `shellCommands` keeps response order, deduped by first occurrence.

### 3.4 Reminder gate [Divergence — corrected]
Two insertion paths: `reminder=="sys"` → a trailing **system** message in the reminder slot; `reminder=="user"` → concatenate the reminder into the **final user message** content, reminder slot empty (only when the final message role is `user` — state the invariant). Gate: if `max_input_tokens` is unknown/0 → **always add** (matches aider intent); else add iff `tokens(all messages w/o reminder) + tokens(candidate) < max_input_tokens - margin`, `margin = min(1024 tokens, 5%)`. Pipeline is linear: **assemble canonical order → add reminder if gate passes → clone → attach cache-control**.

## 4. Streaming: two channels [Divergence]
Route `StreamEvent`s: `finish=="length"`→continuation (§2.1); Reasoning→`partialReasoningContent` (display only, reset per attempt, never parsed/persisted); Answer→`partialResponseContent`; Usage→accumulate, OpenRouter cost rides the final event; empty stream→Failed-before-output (§2). Edit parser never sees reasoning.

## 5. Inline reasoning-tag strip [Observable]
On `answer`, config `reasoning_tag`: remove `<tag>…</tag>` (DOTALL, `regexp.QuoteMeta(tag)`), trim; lone `</tag>` → split once, keep tail. `""` = no inline strip (native already separate, §4). Before `applyUpdates`. Tag attributes (`<think format=…>`) [Deferred].

## 6. Model-proposed shell
### 6.1 Source [Observable]
Editblock yields `(nil, shellText)`; `getEdits` appends to `shellCommands` (deduped, response order) and returns file edits only — `edited` never contains `""`.
### 6.2 Confirm flow [Observable]
Per unique block: "Run shell command?"/"Run shell commands?", `explicit_yes_required`, `ConfirmGroup`, `allow_never`. After running, "Add command output to the chat?" **per block**; accepted outputs accumulate across blocks, combined, returned as `{user,output}`+`{assistant,"Ok"}`. **Cross-reflection:** `shellCommands` resets only in `initBeforeMessage`, so a parse-ok/apply-fail attempt's blocks run after a later reflected attempt succeeds — faithful to aider; stated + tested (§11).
### 6.3 Execution [Divergence]
Whole accepted block through **one shell** (`sh -c`/`cmd /c`). `CommandResult` merges stdout+stderr, includes exit status; model sees `Command / Exit status: n / Output:` even when empty. `ctx` cancellation, timeout, byte/line cap + truncation notice. `CommandRunner`: **pipe runner is the default everywhere** (deterministic; covers record/replay); PTY (`creack/pty`) is **opt-in** only for commands needing a TTY.
### 6.4 Gating [Divergence — safety]
`--yes` does **not** auto-run model shell; needs `--yes-shell`/`--unsafe-auto-run`. Separately, `suggest_shell_commands=false` **gates execution** (early-return from `runShellCommands`), not just the prompt variant — models emit blocks anyway.

## 7. Applying edits + reflection
### 7.1 Pipeline [Divergence — path-safe + atomic]
`Parse → reject out-of-root/absolute/symlink-escape paths BEFORE any FS read → ApplyDryRun → prepareToEdit (permission/add-file confirm; git dirtyCommit) → build in-memory plan → write atomically (temp+rename; roll back batch on any failure) → edited={e.Path}`. **`--dry-run` suppresses the writes themselves** (not just commits). Path containment is the first security boundary.
### 7.2 Reflectable errors [Divergence]
`ReflectableEditError.ReflectionText()`. Only malformed-syntax / search-not-found / ambiguous-match reflect (editblock §5 report). Internal/fs/git errors reported, **not** reflected (leak + not model-repairable).
### 7.3 Auto-commit — git mode [Divergence]
No-op if `!repo || !autoCommits || dryRun`. Else commit `edited`, weak-model message; **do not override `GIT_AUTHOR_*` or `GIT_COMMITTER_*`**; add `Assisted-by: {{model}} via Strument` via `git commit --trailer` (**argv, never shell string**; model name sanitized — strip newlines/control chars). Contract: dirty-commit-before failure aborts; hooks run (hook-modified files committed); commit failure after write leaves edits + reports; **shell runs after commit, so shell changes aren't in it**. Track `lastCommitHash`.
### 7.4 `moveBackCurMessages` [Observable]
Called **only when `edited` non-empty**, so `curMessages` is recent unsummarized history spanning no-edit turns. Rotate: `doneMessages += curMessages`; append `{user, saved}`+`{assistant,"Ok."}`; clear `curMessages`. **`saved`:** git commit note when committed; else (**the `--no-git` default path**) `files_content_gpt_edits_no_repo` — the default edited-turn rotation message; carry it. (`summarizeStart` no-op [Deferred].)

## 8. Cost + tokens [Observable]
Accumulate `usage` across every request in one send (retries + continuations); track `T_input, T_cacheWrite, T_cacheRead, T_output` independently. Resolution (never fabricate): (1) OpenRouter in-band cost from the final event → `Money{Known:true}`; absent/null → `Known:false`, never `$0`. (2) config pricing → `T_input·P_in + T_cacheWrite·P_cw + T_cacheRead·P_cr + T_output·P_out`; Anthropic `P_cw=1.25·P_in, P_cr=0.10·P_in`; DeepSeek `P_cw=1.0·P_in, P_cr=0.10·P_in`. (3) else tokens-only. `sendMessage` owns the accumulator; a `defer` finalizes logical-send usage, merges to session totals, emits an immutable report, resets — so a mid-format panic can't lose accounting.

## 9. Deferred-reflection re-entry map
For when lint/test return in v2: in the source the sequence is `applyUpdates → autoCommit → lint → shell → test`, and **mention/edit reflections return before shell runs**. So: **lint** re-enters between §2-Success's `moveBackCurMessages` and `runShellCommands`; **test** re-enters after `runShellCommands`. Shell sits between them. Marked so adding them later doesn't require re-deriving the order.

## 10. Token counting + history growth
**`TokenCounter`:** tiktoken port for OpenAI-family; `chars/4` (rune-based) elsewhere. The consumers (`checkTokens`, reminder gate, exhausted diagnostics) treat counts as **advisory-conservative**; never gate irreversibly on an estimate (hence the §3.4 margin and unknown-max→always-add). **Unbounded `doneMessages`:** grows until `checkTokens` fires each turn; v1 mitigation is `/clear` + `/tokens` visibility; naive oldest-pair truncation is the first v2 step if it bites before summarization.

## 11. Testing
Primary oracle: script-mode record/replay, **JSON-Lines fixtures** (human-diffable):
```
Given:  coder state; fs/git state; confirmation script; model StreamEvent sequence
        (chunks, finish_reason, usage); command-runner results
Assert: assembled requests; emitted events; reflection messages; fs/git result; history; usage totals
```
Failure/invariant tests: **reflection after continuation-bearing send has no stale accumulator prefix (H1 regression)**; continuation-cap → `OutputExhausted` + history shape; retry-discards-partial vs continuation-stitches (usage summed); **second continuation replaces (not stacks) prefill**; provider fails before/after first token; **Failed-after-partial history bytes**; checkTokens-declined / empty-response don't poison history; **interrupt-then-mention does NOT reflect**; interrupt during stream and shell; exhausted-with-partial keeps partial; no-edit turn lifecycle; **no-git edited-turn rotation message**; mixed edit+shell; duplicate shell blocks keep first occurrence; **shell block from apply-fail attempt 1 runs after reflected attempt 2**; `suggest_shell_commands=false` → no execution; **fence escalation when a chat file contains ```**; **unreadable chat file dropped during chooseFence**; declined mention not re-prompted (`ignoreMentions`); path-traversal/symlink rejected; multi-file apply rollback; `--dry-run` suppresses writes; dirty-commit + auto-commit-after-write failures; **reminder sys vs user path; final-msg-assistant skips user-path reminder; unknown max_input_tokens always adds**; cache-breakpoint snapshot (examples vs system-only, repo vs readonly-only); cache decoration doesn't mutate history; deterministic map ordering; native reasoning + inline `<think>` together; regex metachars in `reasoning_tag`; double-Ctrl-C exits (turn and prompt); reflection cap = 4 sends / 3 follow-ups.
