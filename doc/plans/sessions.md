# Plan: sessions, a durable record, and replay

**Status: in progress.** Phases are listed in dependency order; each is
shippable on its own. Delete this file once Phase 7's experiment has run and
been written up, the way `attachments.md` said to.

---

## Context

Strument's durable artifact is `transcript.md`: rendered prose plus the
one-line tool summaries `TurnToolLines()` collects. Session state does not
survive the process — `history.Resume` restores pinned files and a model alias
(`internal/history/resume.go:21-51`), and `--continue` regenerates about three
hundred words of notes from the transcript. A laptop losing power loses the
thread.

Three findings drove this design.

### The transcript cannot be the source of truth

Surveyed against a real transcript rather than against the writer's source, it
keeps the things `prompts.Summarize` most asks for — the user's prompt
verbatim, which `doc/experiments/2026-08-compaction` identified as the
highest-value item, and the turn's whole prose arc, since `Run` returns
`turnHistory + multiResponseContent + partialResponseContent`
(`internal/coder/coder.go:690`).

It loses the tool layer entirely:

| | `renderForSummary` has | the transcript has |
| --- | --- | --- |
| tool call arguments | full JSON, clipped at `summaryToolBytes` | a rendered summary line |
| tool results | full text, clipped at `summaryToolBytes` | an outcome at best |
| role interleaving | every message in order | prompt / work list / response |
| harness and system notes | the interrupt note, the exhausted note | absent |

In a harness where a turn is mostly tool calls, that is most of a turn. What an
edit *did*, what a `grep` came back with, what a failing command actually
printed — none of it survives. Answers to `ask_user_question` arrive as tool
results, so the user's own mid-turn words vanish too.

### The panel stores full fidelity and clips only at the context boundary

Read 2026-09-20, at the commits named. Codex (`e29eceb`) persists
`ResponseItem` including `FunctionCallOutput`. OpenCode (`d870e22`) keeps full
JSON in SQLite `PartTable.data` and truncates in `compaction.ts`. Hermes Agent
(`8c2f9ca`) stores full content and caps only its FTS index, with a `LIKE`
fallback over the full text "so no search capability is lost". Pi (`3390bd9`)
throws `SessionInvariantError` when a tool call is *missing* persisted
arguments.

Nobody redacts tool content on the durable path; the redaction in all of them
is auth — keys in headers, tokens in logs. And the clip is consistently at the
*context* boundary, never at the *storage* one. Strument already has that
architecture. It just does not persist the thing it clips.

Hermes's comment on why its index is capped is also the footprint argument in
one line: *"Tool results are often multi-megabyte machine payloads."*

### Chronicle shows the retention answer

Chronicle (`anima-research/chronicle`, `013f138`), the store behind
connectome-host, never deletes records — there is no `delete_record` and no log
truncation. `compact_state` *appends* a `Snapshot` and leaves the prior
operations in place; `CompactionSummary.compactable_bytes` is reclaimable
"(logically)". Heavy payloads live in a content-addressed blob store with an
individual `delete`, and a record whose blob is gone degrades to `None`.

That separation is the design: **a permanent, cheap timeline and a deletable,
heavy sidecar.** It is what lets privacy pruning happen without forgetting.

Hermes reaches the same shape from the other direction: compaction there *ends*
a session with `end_reason = 'compression'` and forks a child, joined by a
lineage chain, so the original stays addressable. Neither rewrites history in
place.

---

## Decisions

**Replay on resume.** `doc/sessions.md` argues against replay on three grounds
— cost, attention, attribution — and names the deepest one explicitly: *"This
matters in Strument because users can switch between vendors and models."* A
session is one model, and switching happens at a session boundary, so replaying
a session's own history to the model that produced it is not the impersonation
that argument is about. Cost and attention remain, and are bounded by
compaction rather than by refusing to replay.

**Notes stay**, with a narrower job. Replay restores *this* session; notes
carry context *across* sessions and models, which is the case the attribution
argument still covers. A long session cannot be fully replayed anyway.

**A bare `strument` starts a new session.** Codex (`resume --last`), OpenCode
(`-c`, `-s <id>`), Hermes (`-r <session>`, `--resume latest`), Claude Code
(`-c`, `-r [id]`) and aider (`--restore-chat-history`) are unanimous: resuming
is explicit. Decisive locally — `doc/experiments/` runs hundreds of `strument
-m` invocations per trial, and a resuming default would replay a conversation
into every one, wrecking both the cost model and the independence of the runs.

**`-c`/`--continue` resumes the most recent session; `--session <name>`
resumes or creates a named one.** Names rather than ids: a project holds a
handful, and ids are unpleasant to type. This changes what `--continue` means —
from notes to the conversation — which is the point.

**`--jsonl` is retired.** `strument history path` points at the record. Note
that `--jsonl` is currently independent of `--no-history`; the session record
must not be, because `--no-history` means leave no trace.

**Blobs by per-tool policy with a 1 KiB floor.** `ask_user_question` results
stay inline whatever their size: they are the user's own words, part of the
conversation rather than machine payload.

**Never auto-delete.** Privacy is lossy compression, not forgetting — drop the
blob, keep the summary line already written beside it. Hermes is the only
panel member with time-based expiry, and it is the outlier.

**SHA-256, recorded once per segment rather than per file.** The tree has
de facto standardized on it: `trust.go` sets `DefaultTrustHash =
multihash.SHA2_256`, and `history.go` and `anchors.go` both use it. BLAKE3 is
already in the module graph and measurably faster — 24µs against 177µs on a
64 KiB input here — but `maxToolOutputBytes` is 60,000, so the largest blob
this can produce saves 153µs against turns that run for seconds. `sha256sum`
also verifies a blob with coreutils, which matters for an artifact whose job
includes being handed to someone else.

The algorithm is not baked into the filename. Blobs are named with bare hex,
and the `session` record's header — which already carries `version` — names
the algorithm its segment's hashes were made with. That is `trust.go`'s
property without its format: *"a future default-hash migration invalidates
nothing"*. A store-level marker file would be worse, since it would sit inside
a mergeUnion directory and two stores with different algorithms would collide
on one name with different contents.

**Content hashes, not tool call ids, name blobs.** The deciding case is the
accidental secret: under hash addressing, removing a `.env` that was read three
times is one `rm` and every reference converges on it; under id addressing you
must find all three and missing one leaves the secret on disk. Dedup is a
smaller, real bonus. The record still carries the call id — the hash names the
*file*, the id names the *event*.

**Flat `blobs/`, no sharding.** Measured in this environment: 200,000 files
`readdir` in 214ms, and lookup does not degrade with size (3.5ms → 4.4ms per
thousand across a 20× range). Git shards for reasons that predate indexed
directories. The only whole-directory operation is the strip sweep.

**Segments named by millisecond timestamp, with no counter.** ISO-8601 basic
sorts lexically, milliseconds make same-second collisions a non-issue in a
scripted loop, and a counter grows gaps the moment anything is deleted.

**No migration in Strument.** A one-off script covers the existing state.

---

## Layout

```
projects/<key>/
  root  lock  dismissed  input.txt      # unchanged, project-level
  cost.jsonl                            # unchanged path, rows gain "session"
  current                               # NEW - name of the session to resume
  blobs/                                # NEW - flat, sha256-named payloads
  sessions/<name>/
    log/20260920T180612.481Z.jsonl      # NEW - one segment per process start
    resume.json                         # MOVED from project level
    undo.json                           # MOVED from project level
```

`input.txt` stays project-level: you want to recall a command typed in the
other session. `cost.jsonl` stays project-level and gains a `session` field, so
both aggregations fall out of one file — the same trick `doc/README.md` already
describes for `cat projects/*/cost.jsonl`. `blobs/` is project-level so dedup
and the strip sweep both span sessions.

---

## Phase 0 - the artifact registry learns directories

`internal/history/artifact.go` maps an id to one *file* name, and
`TestProjectDirHoldsOnlyRegisteredArtifacts` inspects only top-level entries.
Three new entries are directories, and without this phase the guard silently
stops guarding — a registered directory passes while everything nested inside
it is invisible to both the registry and `Adopt`'s per-artifact loop, which is
exactly the silent-drop failure the table exists to prevent.

`Adopt` also breaks: `mergeArtifact`'s three real policies begin with
`os.ReadFile`, which on a directory returns `EISDIR`. That is not
`os.ErrNotExist`, so the escape hatches at `adopt.go:316`, `:377` and `:420` do
not fire, and because `Adopt` iterates `artifacts` in map order and returns on
first error, a merge aborts after an arbitrary, non-reproducible subset has
already been rewritten.

- Add a directory kind to `artifact`, with a policy for each new entry:
  `blobs/` is **union** — content-addressed, so a collision is the same bytes
  and a union is well defined by construction; `sessions/*/log/` needs
  rename-on-collision; `current` is `keepNewest`.
- Make the guard test walk recursively, in both directions as it does now.
- Teach `describe()` (`adopt.go:124-147`) to sum a subtree, or `strument
  project list` under-reports a directory as its inode size.

The codebase already anticipates this: `history.go:44-47` and `undo.go:20-24`
note a deferred "undo spill as a subtree of copied source per session" that the
current table cannot model.

**Drive-by fix.** In `-m` mode `appendTurn` dereferences `hist` unguarded from
the `OnCrash` path (`cmd/strument/main.go:410-448`), so a panic under
`--no-history` nil-derefs inside the crash handler.

## Phase 1 - per-session layout

Move `resume.json` and `undo.json` under `sessions/default/`; add the
project-level `current`. No behaviour change: the layout lands on its own so
that the phases after it are not also a move.

Per-session undo is safe under the project lock, and safe against the
interleaving case — session A edits `a.txt`, you switch to B, edit it again,
switch back and `/undo` — because `UndoLastTurn` already refuses any file whose
contents no longer match what Strument wrote. That guard is what makes
per-session undo defensible; without it, undo would have to stay project-level.

## Phase 2 - the durable record

- Emit the existing `coder.Record` stream (`internal/coder/record.go`) into
  `sessions/<name>/log/<ts>.jsonl` whenever `keepState`, reusing
  `internal/jsonlog`, which already flushes per record so a killed session
  keeps everything it emitted.
- Retire `--jsonl`.
- `transcript.md` stops being written. `strument history markdown
  [-t/--turns <n>]` derives it on demand, reusing `Turn.render()`
  (`internal/history/history.go:279`). `--turns` rather than a bare `n` so the
  flag stays unambiguous once a session selector exists.
- `history path` prints the current session's newest segment; `history edit`
  opens it. Editing JSONL by hand is occasionally the only way to excise
  something that should not have been recorded, which on a never-delete design
  is worth keeping.
- Cost rows gain `session`.

Two things the turn record has to carry that the plan did not foresee, both
found by trying to rebuild a turn out of the message rows.

The **tool lines belong on the turn**, not on each tool-result message.
`toollog.go` tees `Toolf`, and the automatic checks and the commit write to it
too; neither is a tool the model called, so per-message summaries would
silently drop the lines a later session most wants. Phase 3's per-result
summary — which exists so that dropping a payload leaves its description
behind — is a different field for a different job.

The **prompt and the answer belong on the turn** as the transcript received
them. The answer is assembled from live state the messages do not carry: an
interrupted send's content is accumulated with its steer as a blockquote,
while a failed automatic check re-enters the loop as an ordinary user message
whose reply *replaces* what came before. The two are indistinguishable in the
message stream, so a renderer that reconstructed would show a sentence the
transcript never did — verified live, where the answer the check interrupted
is correctly absent from both documents. Recording the string the transcript
was written from is what makes the record and the screen agree by
construction; the test for it compares the two documents byte for byte.

## Phase 3 - content-addressed blobs

- A flat sha256 store under `blobs/`. A tool-result record carries the summary
  line inline plus the hash.
- A per-tool policy table with a 1 KiB floor. `ask_user_question` always
  inline.
- Resolution on read, for `history markdown` and for replay. A missing blob
  degrades to the summary line rather than failing — the same shape as
  Chronicle's `get` returning an `Option`.
- The summary lines already exist: `Toolf` → `toolLog` → `TurnToolLines()`
  produces exactly the text the record should keep.

Two more corrections, of the same kind as Phase 2's and found the same way.

**The summary beside a hash is the payload's own first line, not the harness's
`Toolf` line.** The plan said to reuse `TurnToolLines()`, which Phase 2 had
already shown to be turn-scoped: `toollog.go` tees `Toolf`, the automatic
checks and the commit write to it too, and there is no per-call attribution to
be had without bracketing every handler. The payload's first line needs no
plumbing and is as good — Strument's tools put a header there ("big.txt (200
lines)"), and a tool call's arguments are one line of JSON whose front is the
path, ordered that way on purpose. The verb the `Toolf` line adds is redundant:
the tool's name and its arguments are already on the assistant message above.

**A tool call's arguments are stored separately too, not only its results.**
The plan named results alone. The same two arguments apply to arguments, and
`toollog.go` had already named the case: a `write` call's arguments are the
whole new file, and an `edit` call's are the original text being replaced —
text that came out of a file that may hold something nobody wanted recorded.

A per-tool policy table turned out to have one meaningful row, so the rule is
the floor plus a named set of tools whose results always stay inline.

## Phase 4 - replay

- On `-c` or `--session`, rebuild `[]llm.Message` from the record, seed
  `doneMessages`, and let `maybeSummarize` bound it.
- Respect the message-shape invariants. `summarizeAll`'s comment records that
  Anthropic rejects a system message after an assistant turn, and that the
  compaction summary escaped that "only by always landing at index 0 of done" —
  "three unpinned facts holding up an invariant a five-provider probe
  established". A replayed conversation must satisfy the same rules the
  assembler enforces.
- Reuse `internal/fixture`'s deserialisation if its shape fits.
- `-m` never replays unless `--session` is given explicitly.

Four corrections, found the same way as the earlier phases'.

**Not called "replay" in `internal/coder`.** The package already uses the word
for the fixture harness that re-runs the coder against recorded streams. This
is the other direction, so it is "restore", which is also what the pins and the
undo stack already call it.

**Seeded into `doneMessages`, never `curMessages`.** The recorder's watermark
walks `curMessages`, so a conversation seeded there would be written into this
run's segment as though it had just happened — duplicating the whole history on
every resume.

**Compaction runs at restore time, not at the first turn boundary.** The turn
that would trigger it is the turn that would fail. This is aider #2979, which
`CheckRestoredContext` already warned about while being able to say Strument
restored less than a conversation; it no longer can, and its comment says so.

**Tool call ids are matched within the answering window, not across the
record.** This was a real bug, and running it is what found it: a provider need
only make an id unique within one request, while the record spans every request
a session ever made, so a global match let a later result answer an earlier
call of the same name — restoring an unanswered call as though it were fine.
The stub that caught it reuses one id, which is the cheap version of a provider
that happens to. The wire validator built to check this missed it at first for
the same reason the code did: it tested set membership where it had to count.

Notes did not move to `--continue`'s old job by accident — `--continue`
restores instead of summarizing, and notes keep the job they were always the
answer to, which Phase 5's fork is the other half of.

## Phase 5 - sessions plural

- `--session <name>`, created on first use; `current` tracks the last used.
- `/session` in the REPL: list, switch, new, fork, rename. Fork carries notes
  forward — the ritual of `/notes generate`, `/clear` and `/model`, reified.
- `/model` does **not** fork. Many sessions on one strong model is the common
  case, so forking belongs to the session, not to the model.
- `strument session list|delete` outside a session.
- Register the slash command per `doc/README.md`'s "Adding a slash command".

Three corrections.

**`--session` selects; it does not resume.** The plan had it resuming, which
would have replayed a conversation into every one of the hundreds of scripted
invocations a trial in `doc/experiments/` makes. `-c` resumes whatever
`--session` selected, so the two compose.

**`/session switch` does restore, and that asymmetry is the point.** The flag's
non-resuming default exists for scripted runs; there is no such concern
interactively, where opening a named conversation and not getting it is
surprising. `/clear` is the way to have the name without the history.

**The session name is checked on the path builder, not at the flag.** It is a
directory name arriving from outside twice over — `--session` is whatever was
typed and `current` is a file `strument history edit` opens — so a name
containing `..` would otherwise reach out of the state directory.

Two things the design did not anticipate needing. The record has to move when
the session does, or a switch writes the new conversation's turns into the old
conversation's file; a small indirection owns the open segment so the coder
never learns the session can change. And every callback that had captured the
session name at startup — the cost row, the undo save, the resume save, the
notes writer — reads the coder's own `Session` instead, so one assignment moves
all of them.

`/session delete` asks for the name to be typed rather than for y/n. Every
other confirm in the REPL defaults to yes on an empty line, which is right for
a prompt the user just read a command in and wrong for the only irreversible
operation in the tool.

## Phase 6 - privacy and retention

- `strument history strip [--older-than …]`: delete every blob no retained
  record needs, keeping every record. Sweep-based, which is what makes shared
  blobs correct and what makes the accidental-secret case converge.
- Update `doc/README.md`'s "Per-project state", which already justifies
  `0700`/`0600` with "a transcript records whatever the model read out of the
  project". Under this design that is more true, not less, and the doc should
  say what is now kept and how to strip it.

The default is **90 days**, not "everything". A bare `strument history strip`
is what someone types to find out what the command does, and this is the one
operation that destroys recorded material — so the bare form reaches for what
is plainly old. The number is a choice rather than a measurement: Hermes is the
panel's only time-based expiry, which is why a number is defensible here and
why there is no consensus to cite. It could not be re-read while this landed,
so the interval is not attributed to it.

A reference's age is the modification time of the segment holding it, which
fails in the safe direction — a copy or a restore that resets mtimes makes
everything look recent, and the sweep then keeps rather than removes.

Blobs nothing refers to go whatever the cutoff, since no age can be established
for them.

**A Phase 5 bug surfaced here:** deleting the session `current` pointed at left
the pointer dangling, so the next run recreated an empty conversation under a
name the user associated with work. DeleteSession now clears the pointer it
invalidates, and CurrentSession falls back when the session it names is gone —
which caught RenameSession reading `current` *after* the move, by which point
the old name no longer resolved.

## Phase 7 - compaction and notes converge

Today notes regenerate from the rendered transcript and compaction folds the
live message list. Afterwards both are derivations of one record: two prompts,
one source, one blob-resolution path. Notes gain the tool layer the transcript
throws away — which is the whole reason `Turn.Tools` was added, since "a turn
that made a dozen tool calls and closed with one sentence was, to the next
session, that sentence".

It also makes compaction self-healing, which `notes.go` already argues for:
*"regenerating purely from the record is self-healing, because a confabulated
reason has exactly one life and the next regeneration wipes it, while folding
the previous notes in is self-reinforcing"* — and it records that the
compaction trial produced exactly that failure once. Compaction is currently
the pattern notes were designed against.

**This ships on a measurement, not on the argument above.** Nobody has measured
what twenty folds do, because no session has run long enough to do twenty;
`validCompaction` only checks that the result got smaller and is non-empty.
Preregister in `doc/experiments/`, randomize arm order, choose counts over
judgments, and report the counter-metric as prominently as the effect. Read
`doc/experimenting.md` before designing the arms.

---

## Verification

- `task check` green in one run at the end of every phase.
- Exercise each phase against a live model in a scratch project outside the
  repository, per CLAUDE.md — not tests alone. Phase 4 especially: kill the
  process mid-session and confirm the conversation comes back.
- Phase 0's guard test must fail when a directory artifact goes unregistered.
  Verify by sabotage before trusting it; a guard that cannot fail is worse than
  none, and this one is load-bearing for every phase after it.

## Non-goals

- **Parallel sessions.** The project `lock` stays; one session at a time. The
  monorepo case is served by switching, and concurrency would make per-session
  undo unsafe.
- **Automatic deletion of anything.** Retention is manual and lossy.
- **Migrating existing state in Strument.** A one-off script covers it.
