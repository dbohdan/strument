# Preserved reasoning across the panel

**Status: survey, read 2026-11-25.** Open-weights model cards increasingly ask
the harness to send a model's earlier reasoning back to it ("preserved
thinking", "interleaved thinking"). Strument sends none. In short:
- All five open-source harnesses store reasoning with the assistant message
  and send it back, from every turn, not only the current one. None prunes
  older turns' reasoning on the client. Pruning is left to the server or the
  chat template.
- They agree that provider metadata is only valid for the model that produced
  it: Anthropic signatures, OpenAI encrypted reasoning, OpenRouter
  `reasoning_details`, Gemini thought signatures. They disagree about readable
  reasoning after a model switch. OpenCode sends it as text. Pi and DeepSeek
  Harness drop it.
- On chat completions, three harnesses send an empty `reasoning_content` on
  every earlier assistant message for some models. The models named are
  DeepSeek, Kimi K3 and Xiaomi, which includes MiMo, Strument's default.
- Kimi Code is the only one that calls the feature by its model-card name and
  asks the server to keep the reasoning (`thinking.keep = "all"`, on by
  default).
- None of the code I read cites a measurement of what preserving reasoning
  buys.

## What Strument does today

Reasoning is streamed to the screen and kept nowhere. `llm.Message` has no
field for it, so nothing is sent back on any dialect:
- **Chat completions:** `internal/client/sse.go` reads `reasoning` and
  `reasoning_content` from the stream. It does not read `reasoning_details`.
- **Anthropic:** `thinking` deltas are shown. Signatures are not kept.
- **Responses:** the reasoning item's `encrypted_content` is not requested or
  kept (`internal/client/responses.go`).

## The panel

Commits read, all shallow clones of the default branch:

| harness | commit | date |
| --- | --- | --- |
| OpenCode (`sst/opencode`) | `f8fc24ea` | 2026-11-25 |
| Pi (`earendil-works/pi`) | `e24c9d5` | 2026-11-25 |
| Codex (`openai/codex`) | `cec14d2` | 2026-11-25 |
| Kimi Code (`MoonshotAI/kimi-code`) | `c6dec96` | 2026-11-25 |
| DeepSeek Harness (`deepseek-ai/deepseek-harness`) | `4b399c39` | 2026-11-22 |
| Claude Code | closed source | docs only |

| | which turns | to the same model | after a model switch | chat-completions field |
| --- | --- | --- | --- | --- |
| OpenCode | all | text and provider metadata | readable text, no metadata | the field it streamed on, else `reasoning_content`; `reasoning_details` when structured |
| Pi | all | text and signature | dropped | the field it streamed on (`reasoning_content`, `reasoning`, `reasoning_text`); `reasoning_details` when structured |
| Codex | all | encrypted reasoning items | (Responses only) | none: the chat wire was removed |
| Kimi Code | all | text, or `reasoning_details` summaries and encrypted parts | not distinguished | the field it streamed on, else `reasoning_content` |
| DeepSeek Harness | all | Pi's rule, from a durable replay record | dropped | Pi's |

### OpenCode

`packages/core/src/session/runner/to-llm-message.ts` rebuilds every assistant
message with its reasoning. For the same provider and model, the reasoning
keeps its provider metadata. After a switch it is sent as readable text, and
the destination adapter decides what to do with it ("Let the destination
adapter handle readable reasoning after a model/provider switch"). Call-side
metadata, such as Gemini thought signatures and OpenAI encrypted reasoning, is
"model-scoped proof of generation" and goes only to the producing model. A
stream cut by Esc or a failure replays its reasoning only if output followed
it.

On chat completions (`packages/ai/src/protocols/openai-chat.ts`), the
reasoning goes back in the field it streamed on, or in `reasoning_content`, or
in a field configured with `compatibility.reasoningField`. Structured
`reasoning_details` go back as they came. `requireReasoning` sends the field on
every earlier assistant message, even empty. It is on automatically for
DeepSeek, and "Kimi always expects `reasoning_content` on replayed assistant
messages".

### Pi

`packages/ai/src/api/transform-messages.ts` keeps thinking blocks only for the
exact model that wrote them (same provider, API and model ID). Its comment
gives the reason for dropping them otherwise: "Signatures and encrypted
reasoning are only valid for the model that produced them, and sending
readable thinking as plain text makes the new model treat it as part of the
visible reply." Signed or redacted blocks are kept even when their text is
empty, because the signature is what replays.

On chat completions (`packages/ai/src/api/openai-completions.ts`), thinking
goes back in the field it arrived in, recorded as the block's signature, or as
`reasoning_details`. Two compatibility flags matter:
- `requiresReasoningContentOnAssistantMessages` sends an empty
  `reasoning_content` on every replayed assistant message. It is set for
  DeepSeek, Kimi K3 ("need earlier reasoning sent back"), Xiaomi, and some
  Fireworks and OpenRouter entries.
- `requiresThinkingAsText` turns thinking into plain text, deliberately
  without tags "to avoid model mimicking them".

### Codex

Responses only: `wire_api = "chat"` now fails with a message to switch. Every
request includes `reasoning.encrypted_content`
(`codex-rs/core/src/client.rs`). Reasoning items stay in history and are sent
back, and for non-OpenAI providers the encrypted content is kept on reasoning
items while it is cleared from everything else. The context manager counts
earlier turns' reasoning in its token estimate
(`get_non_last_reasoning_items_tokens`), which only makes sense because they
are sent.

### Kimi Code

Stored think parts are lowered into `reasoning_content`, or into
`reasoning_details` as summary and encrypted entries
(`packages/core/src/llm/builtin/protocol/openai/lower.ts`). The documented
setting is `[thinking] keep`, default `"all"`, described as "Preserved Thinking
passthrough". On Kimi's API it is sent as `thinking.keep`. On Anthropic it
becomes a `clear_thinking_20251015` context-management edit, which tells the
server how many turns of thinking to keep. While it is on, every assistant
message carries the reasoning field, even empty. `KIMI_MODEL_THINKING_KEEP`
overrides it, and an off-value disables it.

### DeepSeek Harness

Built on Pi's `pi-ai`, so it inherits Pi's same-model rule. It adds two
things:
- **A durable replay record** (`packages/llm/llm-pi-ai/src/replay.ts`). The
  harness's own content is the source of truth, and provider metadata
  (signatures, IDs) is stored beside it. If the record no longer matches the
  content (another provider or model, or a different block count), the message
  degrades to provider-neutral history, and its thinking is then dropped by
  Pi's rule.
- **Prefix binding for Anthropic** (`prefix-binding.ts`). A signed thinking
  block is bound to the request prefix it was generated under. "Compaction and
  tool-result pruning rewrite that prefix, so a retained signed block becomes
  invalid and endpoints that enforce the check reject the whole request." Each
  response records a digest of its prefix, and signed thinking is replayed only
  while the digest still matches.

### Claude Code

Its docs say nothing about how it handles thinking in history. Anthropic's
API docs, which govern Strument's Anthropic adapter too, say:
- Within a tool-use turn, thinking blocks are passed back with their
  signatures.
- Earlier turns' thinking: "Claude Opus 4.5 and Claude 4.6 and later models
  keep prior turns' thinking blocks in context and bill them as input. Claude
  Sonnet 4.5, Claude Haiku 4.5, and earlier models strip them."
- `clear_thinking_20251015` with `keep` (`{type: "thinking_turns", value: N}`
  or `"all"`) controls that on the server.
- A modified thinking block is rejected, and a changed system prompt, tool
  list or earlier message breaks the prefix check for the thinking after it.

OpenRouter's reasoning docs say `reasoning_details` must go back unmodified
and in order, and that preserving them "maintains this conceptual flow across
multiple API calls".

## What it would take in Strument

The answer is not settled here, but the panel points at its shape:
- **A reasoning part on the assistant message:** the text, the field it came
  in on, and opaque metadata (an Anthropic signature, OpenRouter
  `reasoning_details`, a Responses item's `encrypted_content`). It has to be
  in the session record too, or `/resume` loses it.
- **Same-model replay only.** This is Pi's and DeepSeek Harness's rule, and
  the safe one. OpenCode's readable-text-after-a-switch is the open
  disagreement. Pi's reason against it, that the new model reads it as its own
  visible reply, is a claim that can be tested.
- **Folds invalidate signatures.** Strument's prune and roll rewrite the
  history before the current turn. For Anthropic that breaks the prefix check
  for every signed block after the change, so a fold has to drop them, as
  DeepSeek Harness's digest does.
- **Cost.** Replayed reasoning is input, and on a long session it is a lot of
  it. Caching softens that only while the prefix holds.

Whether it is worth it is a live question, not a reading one. A first check
would ask whether MiMo through OpenRouter behaves differently with
`reasoning_content` replayed than without, since Pi marks Xiaomi as requiring
it and Strument has run MiMo for months without sending it.
