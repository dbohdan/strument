# Can the model search what compaction removed?

**2026-10-12. Survey of the standard panel's source; no live runs.**

## Why

`2026-10-compaction-roll` found that rolling the history keeps every user fact
and none of the model's own results: asked about them, Ling 3.1 Flash said "I
don't know" 24 times of 24 and never guessed. A rolled-away result is still in
the session record on disk. A tool that searches the record could turn that
"don't know" into a lookup. This survey asks whether anyone on the panel gives
the model such a tool, and how they built it.

## Sources

Shallow clones read on 2026-10-12. Read every claim as an observation of these
commits.

| harness | repository | commit (date) |
|---|---|---|
| OpenCode | `sst/opencode` | `a349618` (2026-10-12) |
| Pi | `earendil-works/pi` | `ee409a8` (2026-10-12) |
| Codex | `openai/codex` | `a9b0815` (2026-10-11) |
| Kimi Code | `MoonshotAI/kimi-code` | `c15638b` (2026-10-12) |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `d7432673` (2026-10-09) |
| Claude Code | closed source; docs at code.claude.com (`how-claude-code-works`), read the same day | — |

## What each does

| harness | search the current session's dropped history | other memory |
|---|---|---|
| OpenCode | no | — |
| Pi | no | — |
| Codex | no | cross-session memory files, read with the shell; a model-called reset |
| Kimi Code | no | the user's messages kept verbatim beside the summary |
| DeepSeek | **yes, opt-in, not shipped by default** | the same tools search earlier sessions |
| Claude Code | no tool; the transcript is on disk as JSONL | `MEMORY.md` auto memory, CLAUDE.md |

**OpenCode** (`packages/core/src/tool/plugin/`) ships twelve tools: shell,
read, write, edit, patch, glob, grep, webfetch, websearch, question, skill and
subagent. None reads history.

**Pi** (`packages/coding-agent/src/core/tools/`) ships file, shell and search
tools only. Session search exists, but for the user, in the `/resume`
picker (`session-selector-search.ts`).

**Codex** has two mechanisms nearby, and neither is retrieval within a session:

- `new_context_window` (`core/src/tools/handlers/new_context_window.rs`) lets
  the model end its own window: "A new context window will start without
  summarizing conversation history." The new window is rebuilt from the
  initial context, a world-state snapshot (environment, the user's goals) and
  selected retained items under a token budget. It is a reset chosen by the
  model, the closest thing on the panel to the Cyborgists' rolling context,
  and nothing lets the model read the old window back.
- Memories (`codex-rs/memories/`) are built in the background from threads
  that have gone idle: a per-thread extraction, then a consolidation into
  `MEMORY.md`, `memory_summary.md` and `rollout_summaries/*.md`. The model
  reads them with ordinary shell commands and cites them in a
  `<memory_citation>` block; telemetry classifies those reads. This is memory
  across sessions, not within one.

**Kimi Code** (`packages/agent-core-v2/src/agent/tools/`) has no history tool.
Its `Lookup` tool appears only in test fixtures. Its answer to lost context is
the verbatim carry of the user's messages that `2026-10-compaction-survey`
describes.

**DeepSeek** built the full design
(`packages/experimental/tool-session-query/`, over
`packages/session-query/`):

- five read-only tools: `session_search` (across sessions in the same
  `cwd`), `session_event_search` (within one session, the current one by
  default), `session_trace`, `session_event_trace`, and `session_event_read`
  (one event, whole, as JSON, with neighbours);
- **literal full-text search** over a SQLite FTS5 index, with no embeddings;
- results ranked, each with a snippet, capped (100 by default); at the cap the
  model is told to narrow its query, with no cursor or page size exposed;
- a search of the current session "stops before the step that invoked it",
  so a query cannot find itself;
- a `surfaces` filter of `current`, `shadowed` and `log-only`, where
  *shadowed* names what compaction replaced in the model's view: the design
  treats compaction and retrieval as a pair;
- one fixed prompt section: "Use session_search to find relevant work from
  prior sessions, or session_event_search to search earlier events in one
  session…".

It is **not mounted by default**
(`.agents/notes/archived/feature/2026-08-02-session-search-not-shipped-default.md`).
The reason given is roster scope, not results: the tools had become a default
against an earlier opt-in decision, and the prompt section "taught a
prior-work search workflow that no user had asked for". No measurement is
cited either way.

**Claude Code** has no such tool. Its docs say the transcript is written "to
a plaintext JSONL file under `~/.claude/projects/`" and that compaction keeps
"your requests and key code snippets", while "detailed instructions from
early in the conversation may be lost". A model with a shell can grep that
file, and nothing tells it the file is there.

## What this suggests for Strument

1. **Nobody has measured it.** DeepSeek built the whole design and shipped
   it opt-in; the rest have nothing. A trial that measures recall after
   compaction would be the first evidence on the panel.
2. **Copy DeepSeek's shape, smaller.** Literal search; exclude the calling
   step; return snippets with positions; cap the results and ask for a
   narrower query at the cap. Add a way to read one entry whole, since a
   snippet of a 600-line file read is not the file read.
3. **Search what compaction removed, plus the current context.** DeepSeek
   searches both and labels them, which lets the model check a summary
   against what it replaced. A search limited to the removed part would be
   smaller but could not do that.
4. **Offer it from the start** (the maintainer's call, from experience with
   `/ask` and `/code`: a tool list that changes mid-session confuses even
   strong models; it also keeps the request prefix, and so the cache,
   stable). Before any compaction there is nothing removed to find, and the
   tool says so.
