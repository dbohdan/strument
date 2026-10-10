# When other harnesses compact, and what they tell the summarizer

**2026-10-10. Survey of the standard panel's source; no live runs.**

## Why

`2026-10-compaction-threshold` found that, on MiMo-V2.6-Flash, folding at
context/8 cost 16% more than not folding and recalled none of the six facts
the user had stated. That raised two questions: where everyone else draws the
line, and what their summarizers are asked to keep.

## Sources

Shallow clones read on 2026-10-10. These repositories move fast; read every
claim below as an observation of these commits.

| harness | repository | commit (date) |
|---|---|---|
| OpenCode | `sst/opencode` | `055d95b` (2026-10-09) |
| Pi | `earendil-works/pi` | `c5f5b32` (2026-10-10) |
| Codex | `openai/codex` | `806d973` (2026-10-10) |
| Kimi Code | `MoonshotAI/kimi-code` | `c9f01f0` (2026-10-10) |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `d7432673` (2026-10-09) |
| Claude Code | closed source; docs at code.claude.com (`model-config`, `how-claude-code-works`, `costs`), read the same day | — |

## When they compact

| harness | trigger | kept verbatim beside the summary |
|---|---|---|
| OpenCode | usable window = window − min(20k, max output); compacts at or past it | the most recent 2k–15k tokens (a quarter of usable); 8k in the newer `core` path |
| Pi | context > window − 16,384 | the most recent 20k tokens |
| Codex | 90% of the window; config can only lower it | the user's own messages, newest first, up to 20k tokens |
| Kimi Code | 85% of the window, or once within 50k of it | the user's messages (20k cap, the first 2k of a long one), the last 4 messages, recent ≤ 20% |
| DeepSeek | 80%, capped at window − output reserve − 64k | the most recent 16% |
| Claude Code | at the model's limit ("about 967K" on a native 1M window); `/autocompact` sets 100K–1M | "Your requests and key code snippets are preserved" |
| Strument | settled history past context/8 (131k of MiMo's 1.05M) | a tail under half the budget |

Where to find each:

- OpenCode: `packages/opencode/src/session/overflow.ts` (`usable`,
  `isOverflow`), `session/compaction.ts` (tail selection, pruning), and
  `packages/core/src/session/compaction.ts` (the prompts).
- Pi: `packages/coding-agent/src/core/compaction/compaction.ts`
  (`DEFAULT_COMPACTION_SETTINGS`, `shouldCompact`).
- Codex: `codex-rs/protocol/src/openai_models.rs`
  (`auto_compact_token_limit`) and `codex-rs/core/src/compact.rs`
  (`COMPACT_USER_MESSAGE_MAX_TOKENS`, `build_compacted_history`).
- Kimi: `packages/agent-core-v2/src/agent/fullCompaction/strategy.ts` and
  `src/human/compaction/shape.ts`.
- DeepSeek: `packages/compaction/compaction-basic/src/config.ts` and
  `types.ts`.

Every harness on the panel compacts in the last fifth of the window. Strument
compacts in the first eighth. The threshold trial found the panel's choice
also the cheaper one on MiMo.

## Three things most of them do besides summarizing

**Recover from overflow.** Five of the six fold and retry when the provider
refuses a request as too long:

- OpenCode: `compactAfterOverflow`.
- Pi: `_checkCompaction`'s "overflow with retry", which removes the failed
  reply, compacts, and retries once.
- Kimi: `maxOverflowCompactionAttempts: 3`.
- DeepSeek: a `context-overflow` trigger beside the step-boundary pressure
  check.
- Claude Code: compacts after a recognized "prompt is too long".

Codex compacts mid-turn when a model switch shrinks the window. Strument
already classifies the refusal (`llm.ErrContextWindow` →
`resContextExhausted`), then ends the turn with a note instead of folding
and trying again.

**Prune tool output before summarizing.**

- Claude Code: "It clears older tool outputs first, then summarizes the
  conversation if needed."
- DeepSeek: a separate `compaction-tool-result-pruner` package. On overflow
  it prunes, measures again, and only then picks a range to summarize.
- OpenCode: an opt-in pass (`compaction.prune`). Walking back past the last
  two turns, it keeps 40k tokens of tool output and marks older outputs
  "[Old tool result content cleared]", but only when that frees more than
  20k. A `skill` result is never pruned.

Pruning is cheaper than a summary and loses only what can be read again.

**Make the summary call reuse the cache.** DeepSeek sends its compaction
instruction as the final user message after replaying the last request's
system prompt, tools and messages, so "the auxiliary call [is] a genuine
prefix of the last routed request, so the provider's KV cache is reused." Its
summarizer is the routed model unless configured otherwise. Strument renders
the history as text for a separate side call, which nothing has cached.

## What the summarizer is told

**Three templates** — OpenCode, Pi, DeepSeek:

- OpenCode: Objective / Important Details / Work State (Completed, Active,
  Blocked) / Next Move / Relevant Files. "Use terse bullets, not prose
  paragraphs. Preserve exact file paths, symbols, commands, error strings,
  URLs, and identifiers when known."
- Pi: Goal / Constraints & Preferences / Progress / Key Decisions / Next
  Steps / Critical Context. "Use this EXACT format… Preserve exact file
  paths, function names, and error messages." Its system prompt adds: "Do
  NOT continue the conversation. Do NOT respond to any questions in the
  conversation."
- DeepSeek: Primary Request and Intent / Key Technical Concepts / Files and
  Code / Errors and Fixes / Pending Jobs / Current Work / Next Step /
  Critical Context. "Preserve exact file paths, commands, error strings,
  identifiers, numeric values, function signatures, and syntax fragments.
  Capture user feedback and explicit instructions faithfully, especially
  corrections." It also requires "concise English engineering prose".

**Codex** (`codex-rs/prompts/templates/compact/prompt.md`) has six lines: "Create
a handoff summary for another LLM that will resume the task. Include: Current
progress and key decisions made; Important context, constraints, or user
preferences; What remains to be done; Any critical data, examples, or
references needed to continue. Be concise, structured…" It can afford to be
short, because the user's messages are kept beside the summary by code.

**Kimi** (`src/human/compaction/compaction-instruction.md`, about 600 words)
rejects templates: "Do not impose rigid section headings; let the shape follow
the task." It asks for:

- the summary in the conversation's own language;
- exact commands, paths and returned values;
- what is still unknown;
- a forward plan ("Right now you hold more context on this task than you ever
  will again");
- honesty about the unverified: "If an earlier step claimed something was
  done but was never verified… say so plainly";
- length "proportional to the task".

It also tells the model to "re-quote any still-relevant earlier request that
may have scrolled out of the kept messages".

**The earlier summary is handled explicitly** by four of the five:

- OpenCode: "The <prior-summary> is discarded after this: anything you do
  not carry into the new summary is lost… Where they conflict, the
  conversation wins."
- Pi: "PRESERVE all existing information from the previous summary."
- DeepSeek: "preserve still-true facts, drop stale ones."
- Kimi: the summary prefix tells the next model which user messages were
  kept and which the summary covers.

**Claude Code** does not publish its prompt. Users steer it with `/compact
<focus>` or a "Compact Instructions" section in CLAUDE.md.

**Strument's** prompt (`prompts.Summarize`, adapted from aider) is the only one
on the panel that asks for less detail on older messages ("Briefly summarize…
Give more detail to the most recent messages and less to the older ones"). It
is also the only one with no rule for an earlier summary, so each fold
re-summarizes the last summary under an instruction to shorten its oldest
part. The threshold trial measured that compounding: sixteen folds kept
nothing, one fold kept most.

## What this suggests for Strument

1. **Compact late.** The whole panel does, and the threshold trial found it
   cheaper on MiMo. With late compaction, the overflow path matters more,
   since it is the backstop.
2. **Fold and retry on overflow.** The refusal is already classified, so
   only the recovery is missing.
3. **Prune tool output before summarizing.** It is deterministic, and it
   loses nothing that can't be read again.
4. **Keep the user's messages verbatim (Codex, Kimi).** It would have saved
   every fact the threshold trial lost, since each was in a user message,
   and it is decided by a cap, not a prompt.
5. **Fix the prompt at the edges.** Add a rule for the earlier summary, and
   stop asking for less detail on older messages. Leave templates out:
   `2026-08-compaction` measured a template losing stated reasons, and none
   of the panel's templates has been tested against that.
6. **A cache-friendly fold (DeepSeek)** is worth having once the summarizer
   is the main model, which it already is by default, since `side_model`
   resolves to the model itself. It changes how the request is laid out,
   not what it asks.
