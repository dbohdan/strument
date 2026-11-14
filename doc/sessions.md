# Sessions, compaction, and resuming work

How Strument keeps a conversation, shrinks it when it grows, and picks it back
up, and why each part is the way it is. `doc/history.md` describes what is on
disk; this is the behavior built on it.

## Sessions and runs

A project holds named **sessions**, each with its own conversation, model
choice and undo history. One invocation of Strument is a **run**; a session
outlives its runs. Every run appends a segment to the session's record, a JSON
Lines file of every message, tool call and result, with large tool output kept
once in a content-addressed blob store.

A run starts with an empty conversation. `--continue` (`-c`) rebuilds it from
the record. Restoring is opt-in for a reason the experiments keep confirming:
a conversation is context the user pays for on every request, and much of an
old one is abandoned approaches and settled questions.

## Restoring a conversation

`--continue` replays the session's record into the conversation, applying the
tombstones `/rewind` left, so it rebuilds the conversation the user was left
with rather than every turn ever recorded.

- **A different model gets a seam.** When the restored turns were made by
  another model, Strument appends a marked harness note saying so: the turns
  above are from earlier in the session, made by a different model, a record
  rather than its own decisions, and disagreeing with them is allowed. Without
  it nothing tells the model that those assistant turns are not its own, and a
  model shown another's replies under its role tends to continue or defend
  them. The note names no model.
- **An oversized restore is compacted first**, by the same rule as below,
  because the turn that would otherwise trigger compaction is the turn that
  would fail (aider #2979). `CheckRestoredContext` warns about what remains.

## Compaction

Compaction runs at the end of a turn, once the model has stopped, the edits
have landed and the automatic checks have run. It does not run in the middle
of one: a summary or a cut made mid-task has to preserve half-finished plans,
and the turn boundary is already the point where the user reviews.

The history gets half the model's context window (at least 2,048 tokens).
Past that:

1. **Old tool output is cleared first** (`pruneHistory`), with a note saying
   so. The output is still in the record.
2. **Then the oldest whole turns are dropped** (`rollHistory`) until the rest
   fits in half the budget. In their place goes one harness note that quotes
   the user's own messages from those turns, word for word, oldest first, and
   says the rest of those turns is in the session record, where the `recall`
   tool searches it.

Nothing in the note is written by a model. That is the point of the design:
summaries paraphrased, invented rationales and continued the transcript instead
of summarizing it (`experiments/2026-10-compaction-prompt`), and a summary of
a summary carries an invention forward as source material. Codex and Kimi keep
the user's messages verbatim beside their summaries
(`experiments/2026-10-compaction-survey`); Strument keeps only those, and
recovers the rest on demand. The note says where to look because without it,
in `experiments/2026-10-recall`, a model with the answer one search away
reasoned that it would have to re-read the files and stopped at "I don't
know".

A request the provider refuses as too long is folded and retried
(`foldForOverflow`): the history is cut to half its size, by the same two
steps, whether or not it had reached the budget. Five of the six surveyed
harnesses do the same; with compaction late in the window, this is the
backstop.

## Clearing, resetting and rewinding

- **`/clear`** empties the conversation and stays in the session: the same as
  starting again without `--continue`. Grants stay.
- **`/reset`** does the same and also withdraws this run's webfetch approvals.
- Neither writes to the record, so `--continue` restores the conversation
  whole, cleared stretch and all. With `--no-history` there is no record, and
  they only empty memory.
- **`/rewind [n]`** takes the last `n` turns out of the conversation. It
  appends a tombstone row rather than deleting anything, so the record keeps
  the turns and a restore applies the tombstone. Whole turns from the end, so
  a tool call and its result are never separated. Files are left alone: the
  rewind names the files the turns changed, and `/undo` reverts them. Turns
  that compaction has already rolled away cannot be rewound.

## Standing instructions: `AGENTS.md` and `AGENTS.local.md`

What should outlive a session belongs in a file the user reviews, not in a
store the model writes unseen.

| | `AGENTS.md` | `AGENTS.local.md` |
| --- | --- | --- |
| holds | the project's standing instructions | the user's private instructions for this checkout |
| shared | committed, like any file | never committed |
| precedence | — | wins where the two disagree |

When either exists at the project root, the system prompt names it and asks
the model to read it. It does not inject the contents: the file arrives
through `read`, the one channel project files use, and shows in the
transcript like any other read. The naming is the part measured to matter:
compliance with a rule contrary to habit was 0/8 with no `AGENTS.md`, 2/8 with
it merely pinned into the context, and 6/8 once the prompt named it as the
project's standing instructions (`experiments/2026-08-agents-md`).

Edits to either are ordinary edits, with a diff, a snapshot and `/undo`. The
first time Strument sees `AGENTS.local.md` untracked and not ignored, it adds
it to `.git/info/exclude` and says so, and a commit that would include it
leaves it out. A tracked one is committed like any other file.

No standard names the private file. DeepSeek Harness reads `AGENTS.local.md`
after the shared file, as Claude Code does `CLAUDE.local.md`; Codex and Pi
read `AGENTS.override.md` in place of `AGENTS.md`.

## `--no-git`

Outside Git there are no commits to record the work, and nothing else is
missing. The session record holds each turn's changed files, the snapshot
holds their prior contents for `/undo`, and Git, where it exists, sits on top
of that rather than underneath it.

## Rejected

- **A model-managed memory store** (`remember()` / `forget()`). It would let
  the model change its future prompt outside the review workflow, for sessions
  the user has not started yet. A note meant to last is a line in
  `AGENTS.md` or `AGENTS.local.md`.
- **Summaries of any kind in the conversation.** See Compaction. A summary
  presented as conversation also fabricates a turn nobody said.
- **Branchable sessions** (Gemini CLI's shadow repository, connectome-host's
  Chronicle). A second unit of state for the user to manage, beside `/undo`
  and Git, which already explore an alternative.
- **Restoring by default.** See Sessions and runs.

## What the panel persists

The record and the blob store were designed after reading these, on
2026-09-20. A dated observation, kept where it can be re-checked.

| harness | commit | what it persists |
| --- | --- | --- |
| Codex CLI | `e29eceb` | `ResponseItem` including `FunctionCallOutput` |
| OpenCode | `d870e22` | full JSON in SQLite `PartTable.data`; truncates in `compaction.ts` |
| Hermes Agent | `8c2f9ca` | full content; caps only its full-text index, with a `LIKE` fallback over the full text |
| Pi | `3390bd9` | throws `SessionInvariantError` when a tool call is missing persisted arguments |
| Chronicle (connectome-host) | `013f138` | never deletes a record; heavy payloads live in a content-addressed store with an individual delete |

Nobody redacts tool content on the durable path; redaction in all of them is
of credentials, and the clip is at the context boundary, never at storage.
Chronicle is the shape Strument took: a permanent, cheap timeline beside
deletable heavy payloads. Hermes is the only one with time-based expiry, which
is why `strument history strip` has a default age at all, and why that age is
a choice rather than a consensus.
