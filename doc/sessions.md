# Sessions, compaction, and what Strument refuses to do

Why picking a project back up works the way it does here, what was borrowed,
and what was rejected. Written down because most of this reasoning existed
nowhere but a conversation, and the alternatives are the ones a reasonable
person re-proposes.

## The one-paragraph version

Strument does not replay conversations. It compacts at a turn boundary rather
than at window overflow, writes ~300 words of session notes regenerated from a
durable transcript, and puts them in the prompt as a **system** message that
says the files outrank it. Notes are generated on demand — `--continue` at
startup, `/notes generate` mid-session — and live in memory for the session
rather than on disk. Every other harness surveyed persists a JSONL transcript
and replays it verbatim.

## Compaction fires at a turn boundary

`maybeSummarize` runs from `moveBackCurMessages`, at the end of a turn, once the
model has stopped, the edits have landed, the automatic checks have run, and the
commit exists.

Every other harness compacts when the context window fills. The window fills
whenever it fills, so those summaries are taken mid-thought — partial plans,
half-tested hypotheses, error threads still in flight — and all of them report
quality problems as a result.

**Strument gets a clean boundary because the turn boundary is the human's.** A
property adopted for review turns out to decide compaction quality. That is the
most transferable idea in this document, and until recently it was an
undocumented accident of where one call sat.

Three related decisions:

- **The summary is a system message.** aider's prompt asked the weak model to
  write *as the user* ("Begin with \"I asked you...\""), the result was injected
  as a user turn, and the coder appended an assistant `"Ok."` agreeing to it — a
  fabricated exchange, the same shape `readOnlyFilesPrefix` was written to
  remove. It survived because it lived inside a ported algorithm, and because
  fixing the injection alone would not have fixed it: the *prompt* commanded the
  impersonation.
- **Summaries are agentless** — no "I", no "you", no "the assistant". First
  person is a lie whenever a different model wrote the text, and the summarizer
  is the `weak_model`, so it usually is. Third person about the assistant is
  alienating the other way and invites the reader to discount it. A changelog
  asserts no authorship and is true whoever wrote it.
- **The summarizer sees tool calls**, not only prose. It read USER and ASSISTANT
  messages alone, which in a harness where every action is a tool call meant a
  twelve-call turn closing with one sentence compacted to that sentence.

Also relevant, and measured: **settled history is produced on every turn**, not
only on turns that edited a file. That gate was an aider vestige, and it meant a
session of questions — or any stretch of `/ask` — never compacted at all. Four
read-only turns produced 8484 tokens of history against a 1024-token budget,
with zero compactions.

## Why notes, and not a replayed conversation

| tool | store | replayed? |
| --- | --- | --- |
| Claude Code | append-only JSONL per session | yes, in full |
| Codex CLI | `rollout-*.jsonl` | yes, in full |
| Gemini CLI | `session-*.json` + shadow git repo | yes |
| OpenCode | per-session store, manual load | yes, plus tool-output pruning |
| Amp | threads | **no — refuses compaction on principle** |
| Cline | markdown in the repo | **no — context resets by design** |
| aider | `.aider.chat.history.md` | opt-in, default off |
| **Strument** | notes + transcript | **no** |

Three reasons, in increasing order of how long they took to see:

**Cost.** A restored history is re-sent with every message of the next session,
uncached, and silently until the token line. Notes are ~300 words.

**Attention.** Amp's argument, and the sharpest: everything in the context
window influences the output, so carrying last week's abandoned approach is
*degrading*, not merely wasteful.

**Attribution.** This is the one worth stating carefully.

> Cross-vendor replay does not fail as confusion. It fails as **forged
> agreement**.

A model reading another vendor's transcript does not experience it as foreign
material. The messages are labelled `assistant`, so it reads them as its own
past self, and will smoothly rationalize and then defend choices it would never
have made — with no seam anywhere to notice. The `assistant` role label is an
assertion of authorship, and replay makes it false invisibly.

What replay actually requires is not shared identity but **prior-compatibility**:
the reader recovering the writer's intent because their dispositions match.
Anthropic's models share a constitution and a character across weights, so
Claude Code can lean on that. Strument is multi-vendor by design — MiMo, Luna,
Haiku, DeepSeek in one config — and cannot. This advantage is structural and not
purchasable, which is why Strument must be *robust to* prior-incompatibility
rather than rely on its absence.

The same reasoning explains the reported discomfort of Claude models resuming
for one another. Shared priors fix the *interpretation* problem and leave the
*authorship* problem untouched: being handed words ascribed to you that you did
not say is uncomfortable regardless of whether you would have said something
similar. A replayed session is one large fabricated turn.

## The two artifacts, split by lifetime

| | session notes | `AGENTS.md` |
| --- | --- | --- |
| lives | in memory | project root |
| written by | the harness, via the weak model | the user, and the model as an ordinary edit |
| generated by | `--continue` at startup; `/notes generate` mid-session | — |
| lifetime | one session, regenerated from transcript | durable |
| reviewed by | reading it; `/notes` | the diff, `/undo`, the commit |
| discarded by | `/notes drop` | `/drop AGENTS.md` |

Splitting on **lifetime rather than topic** is what avoids becoming a memory
store. Cline's five-file Memory Bank splits on the same axis and is worth
copying for that reason; the rest of it exists because Cline has no other
durable state, where Strument has a repository, a transcript, and pins.

`AGENTS.md` needs no new safety story, and this dissolved an objection rather
than answering it. A `remember()` tool would be the model editing its own future
prompt across sessions — the "autonomy across turns" line. But a **pinned file**
updated by an ordinary edit already runs the whole review surface: the diff
scrolls past, it is snapshotted before the write, `/undo` reverts it with or
without git, and it lands in the turn's commit.

> Strument already has a review surface for model-authored durable state. It is
> called an edit.

Pinning it is not enough on its own, and that is measured
(`experiments/2026-08-agents-md.md`): compliance with a rule contrary to habit
was 0/8 with no `AGENTS.md`, 2/8 with it merely pinned, and 6/8 once the prompt
named it as the project's standing instructions.

## `--no-git` is not a degraded case

The harness is meant for live configuration directories and checkouts under
other SCMs. There, **nothing durable records what the turns did**: no commits,
and the undo stack was in memory.

So notes anchor to **turns**, not commits — a turn has identity in both worlds
(index, time, edited files, optionally a hash), and the commit is an enrichment.
This matches the existing layering, where the snapshot substrate is primary and
git sits on top. The transcript records each turn's changed files for the same
reason: without git it is the only account of what a session did to the tree.

It is easy to get backwards. The first draft of this design derived the note's
job from what git already carried, which is exactly wrong in the case the
snapshot substrate exists for.

## The notes lifecycle

Notes live in memory for one session. They are never persisted to disk — the
transcript is the durable artifact, and notes are always derived from it.

Two paths create notes:

- **`--continue` at startup.** Regenerates from the project's transcript, which
  covers every prior session. The user asked to resume, so the cost is expected.
- **`/notes generate` mid-session.** The user's explicit request, available in
  any session — including one that started clean. Reads the transcript that
  exists at that point (which may include the current session's turns, since the
  history writer appends them after each turn).

A session without `--continue` starts clean: no notes in context, no weak-model
call, no delay. The user who wants notes types `/notes generate`; the user who
does not is never charged for them. This makes the cost visible and chosen
rather than hidden and automatic.

Notes do not survive the session. When the next session starts, `--continue`
regenerates from a transcript that now contains the full previous session — so
the fresh notes are always strictly more complete than the stale ones would have
been. There is nothing to lose by not persisting them, and a stale file to
confuse the model by persisting them.

`/clear` does not drop notes. After clearing the conversation, the notes from
the session's opening turns are still in context — which is usually what the
user wants, since the intent and constraints survive the cleared stretch. `/notes
drop` and `/notes generate` are there when they do not.

Compaction does not interact with notes. Compaction operates on the coder's
in-memory message list (`doneMessages`) — it replaces old messages with a system
summary so the next model request fits the context window. Notes are regenerated
from the on-disk transcript, which accumulates turns independently. By the time
notes are regenerated, the transcript already includes everything, compaction or
not.

## Notes are regenerated, never folded

From `transcript.md`, every time. Not from the previous notes.

> Pure regeneration is **self-healing**. Folding is **self-reinforcing**.

A confabulated reason gets exactly one life if every regeneration rebuilds from
the record — the next one wipes it, because the transcript never contained it.
Fold the previous notes back in and the invention is re-endorsed each cycle
until nothing downstream can distinguish it from a real decision. The compaction
trial produced exactly that failure once: an invented rationale ("to balance
between frequent updates and system load") that nobody had given.

Cumulative loss across repeated compaction is the documented defect of every
scheme surveyed. A durable transcript is what makes the fold avoidable, so it is
avoided.

The transcript is trimmed from **both ends** rather than the tail, because a
session has a shape: the opening turns carry intent and stated constraints, the
recent turns carry working state, the middle is mechanics the code now records.
A tail-only window dropped the reason for a decision while keeping the last
hour's step-by-step. The honest limit: a constraint stated in the middle of a
long session can still fall out, and the answer there is a line in `AGENTS.md`,
not a fold.

## Presenting the notes

Its own chunk, between examples and the read-only files. That position is
load-bearing: breakpoints sit on examples-or-system and on read-only files, so
the notes ride inside the cached prefix, and a mid-session `/read-only` — which
rewrites that block — does not invalidate them. Borrowed from connectome-host,
which calls this KV-stable folding.

A **system** message, for the same reason the compaction summary is one. The
notes are the harness's artifact: not something the user said, and not something
this model said either.

The header carries a conflict rule, and it is the most important sentence:

> They are a summary, not a record: they may be incomplete, and the project may
> have changed since. Where they disagree with what you find in the files, the
> files are right.

That is the counter-metric turned into an instruction. The failure this feature
can cause is a model acting confidently on a note the tree has moved past, so
the note says which side loses. No other harness states it.

## Rejected, and why

- **Verbatim replay.** Cost, attention, attribution — above.
- **Named or branchable sessions** (connectome-host's Chronicle, Gemini CLI's
  shadow git repo). A noun the user must manage, layered on a store deliberately
  keyed by project root — and "more session state than the user is tracking" is
  the confusion this design started from. `/undo` and git already answer
  "explore an alternative".
- **A model-managed memory store** with `remember()` / `forget()`. The model
  editing its own future prompt, including sessions the user has not started,
  with no confirmation surface. The distinction that matters: **compression the
  harness runs over the record** is lossy, bounded, derived and regenerable;
  **assertions the model elects to persist** are authored, unbounded in
  lifetime, and derived from nothing. The first can only degrade a record; the
  second creates one.
- **Summarizing the transcript into history**, i.e. injecting a summary as
  though it were the conversation. Fabricates the turn this work removes, and is
  *less* honest than replay, whose messages were at least really said.
- **Folding the previous notes into regeneration.** Self-reinforcing, above.
- **Voicing the notes as the agent** (connectome-host's `summaryParticipant`
  defaults to `agent.name`). A defensible choice for a single-agent system with
  continuity — autobiography is first-person by definition — and wrong here,
  where a different model writes the notes and a different model again reads
  them. Agentless sidesteps the question rather than answering it.

## Influences

- **aider** — the `ChatSummary` this overhauls, and its `--restore-chat-history`
  default-off position, which was right.
- **Cline** — the Memory Bank's lifetime split. Not its location: files in the
  working tree are what `history.go` criticises aider for.
- **Amp** — the attention argument, and the nerve to refuse compaction outright.
- **Codex CLI** — third-person framing of a summary ("Another language model
  started to solve this problem…"). Strument goes further to agentless.
- **connectome-host** — KV-stable folding (cache-prefix placement, adopted) and
  compression coverage as an observable (adopted as the `/tokens` row). Its
  first-person voicing is the deliberate opposite choice, discussed above.

## Where the evidence is

- `experiments/2026-08-compaction-summary-prompt.md` — the prompt rewrite that
  lost, and the scorer that nearly hid it.
- `experiments/2026-08-agents-md.md` — naming `AGENTS.md` in the prompt.
- `experiments/2026-08-session-notes.md` — notes across sessions: 8/8 vs 0/8 on
  recovering a stated reason (p=0.0002), and 3/8 stale assertions when the tree
  moved behind them. The conflict rule in the header does not prevent that,
  because the failure is upstream of a conflict: the model never looked.
- `experiments/2026-08-notes-header.md` — the follow-up that tried to fix that
  with a stronger instruction, and could not: 8/24 vs 6/24, p=0.75. Its real
  finding is that reading is perfectly predictive — across 48 sessions, not one
  that opened the file asserted the stale name, and no wording reliably causes
  the opening. It also records that `--continue` can produce no notes and say
  nothing, in 3 to 5 sessions of 48.
- `../doc/experimenting.md` — how to run one of these without fooling yourself.
