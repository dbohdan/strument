# Strument — developer docs

This is the high-level map for people hacking on Strument. For user-facing
docs (install, configuration, what differs from aider) see the top-level
[`README.md`](../README.md).

Strument started life as a close reverse-engineering of
[aider](https://github.com/Aider-AI/aider) at commit `5dc9490`, driven by a
set of written specifications. Those port specs and their journal have since
been retired — the code is now the source of truth, and Strument follows its
own direction: closer to aider in some places, further in others. A read-only
clone of aider is still handy when you want to compare (`task setup:reference`
drops it in a gitignored `reference/`).

## Philosophy

The positions below are the ones worth knowing before you change anything
substantive. They are the project's own, arrived at deliberately — not
inherited from aider.

- **A reviewable loop, not an autonomous one.** The model calls tools, reads
  the results, and keeps working inside one turn. That is a deliberate
  reversal, and it is worth knowing why, because the position it replaced was
  held just as deliberately.

  Strument began as aider's shape: the model responds, the harness acts, the
  human drives the next turn. That shape is *coherent with SEARCH/REPLACE*,
  where a reply carrying an edit is a finished thought. It is not coherent with
  tool calls. A reply that ends in a tool call carries
  `finish_reason: "tool_calls"` — the model is mid-sentence by construction,
  and every post-trained model expects results, then continuation. Strument
  used the tool-call *transport* with aider's *turn semantics*, appended the
  results, and hung up before the model could read them. In practice mid-size
  models terminated early and got confused, worst when a change spanned code
  and its separately-stored tests. That was not the models being weak. It was
  the harness breaking a protocol.

  So the loop closed, and with it the rest: the model finds files with `grep`
  and `read` instead of asking for them, and the repo map left the prompt
  because a model that can look does not need a digest. The ranked map has
  since gone entirely: what survives is its tag layer, which `/symbol` and the
  `symbol` tool read.

  **What did not change is the review surface**, and that is the part worth
  defending. Every edit is snapshotted before it lands and is one `/undo`
  away, with or without git. Mutation through the shell is gated while
  observation runs free. The step budget is a checkpoint a human clears. The
  turn boundary is still the human's — nothing here starts work nobody asked
  for. aider's spirit was never *one send per turn*; it was that nothing is
  unrecoverable and you see everything happen. A twenty-five-step loop that
  snapshots every edit and shows every diff honors that better than a
  single-send turn that does neither.

  The line that remains is autonomy *across* turns, not within one. Keep it
  there.

- **The code is the source of truth.** The written port specs and their
  journal are retired. Don't re-introduce a parallel spec that the code must
  "conform to"; document decisions in the code, the commit, and these docs.
  Where we differ from aider, that is on purpose — say why in the comment.

- **Tool calls are the only path.** Every model in scope has solid function
  calling, so looking, editing, and running go through native tool calls —
  the text formats were removed, because a model that cannot call functions
  cannot find or read a file either, and the harness now assumes it can do
  both. Beyond reliability, tool calls remove the SEARCH/REPLACE
  delimiter-collision problem — a file that itself contains `<<<<<<< SEARCH`
  is just data — which is what makes the harness usable on its own source,
  including its prompt strings. The user still sees code scroll by, rendered
  as red-green Git-style diffs.

- **Prompts you'd hand a competent colleague.** Calm, specific, one clear
  statement per rule; explain the *why* where a mid-size model benefits and a
  frontier model doesn't mind. No shouting, no pre-escalation, no manufactured
  stakes. For this model class (floor ~27B, up to frontier) the
  welfare-respecting register and the performance-maximizing one are the same
  register; a single prompt set serves all of them.

- **Small, honest, self-contained.** One static binary, no cgo. One
  OpenAI-compatible dialect (OpenRouter). Starlark configuration behind a
  direnv-style trust gate. Never fabricate cost or token counts; mark
  estimates as estimates. Never commit secrets.

  The safety model is a snapshot, not git. Every file a turn writes is
  recorded before the first write to it, so `/undo` restores a turn whether or
  not there is a repository — which is what makes the harness usable on a live
  configuration directory or under another SCM. Git is layered on top where it
  exists: one commit per turn, `/squash` to merge turns, and the pre-existing
  dirty state committed separately so a turn never absorbs the user's own work.
  Alongside that: atomic batch writes that roll back whole, path containment,
  and an edit that preserves the file's mode and follows a symlink instead of
  replacing it.

## Relationship to aider

- **Scope.** Essentials only: a standard tool set driven in a closed loop
  (`read`/`grep`/`glob`/`ls`/`symbol` to look, `edit`/`write` to change,
  `bash` and the `check` tool to run things), turn-scoped snapshots with `/undo` and
  `/squash`, git auto-commit where there is a repository, `/ask`, `/symbol`,
  reflection, chat-history summarization.
  Architect mode, voice, GUI, and analytics are out of scope for v1.
- **One dialect.** A single OpenAI-compatible client with OpenRouter
  extensions replaces litellm; Starlark `config.star` replaces layered
  YAML/`.env`/model-database configuration. Prompt caching follows from this:
  with no model database to declare capability, it is a per-model `cache`
  setting that decorates the prompt with cache-control breakpoints (1h TTL) —
  aider's `--cache-prompts` minus the cache-warming pings, which we omit.
  aider also needs `map_refresh="files"` to keep the prefix byte-stable;
  Strument does not, because the repo map was the one thing that changed every
  turn and it no longer exists.
- **Where we differ.** Some behavior is deliberately not aider's — the closed
  loop above all, and then atomic batch writes that roll back whole and
  preserve the file's mode, an undo substrate that does not need git, usage
  accounting that survives an aborted turn, an in-chat-file exemption on path
  containment. Chat-history summarization keeps aider's recursive-split
  algorithm, but runs **synchronously** (aider uses a background thread) and only
  when the model's context window is declared (`context=`), where aider always
  summarizes. Three further divergences are described below. When you diverge,
  say why in the code comment and the commit message.

- **Compaction fires at a turn boundary, and that is the whole trick.** Every
  other harness — Claude Code, Codex CLI, OpenCode, Gemini CLI — compacts when
  the context window fills. The window fills whenever it fills, so those
  summaries are taken mid-thought: partial plans, half-tested hypotheses, error
  threads still in flight. Strument compacts from `moveBackCurMessages`, at the
  end of a turn, when the model has stopped, the edits have landed, the
  automatic checks have run, and the commit exists. It gets that boundary
  *because the turn boundary is the human's* — so a property adopted for review
  and safety turns out to decide compaction quality too. If the loop ever grows
  a reason to compact mid-turn, this is what it would be spending.

  Two things follow from the same principle. The summary is a **system**
  message: aider's prompt asked the side model to write *as the user* ("Begin
  with \"I asked you...\""), the result went in as a user turn, and the coder
  appended an assistant `"Ok."` agreeing to it — a fabricated exchange, the same
  shape `readOnlyFilesPrefix` was written to remove. And the summary is
  **agentless** — no "I", no "you", no "the assistant" — because first person is
  a lie whenever a different model wrote the text, and the summarizer is the
  side model, so it usually is. A changelog asserts no authorship and stays true
  regardless of who wrote it or who reads it.

  The summarizer also sees the **tool calls and results**, not only the prose.
  It read USER and ASSISTANT messages alone, which in a harness where everything
  a turn does arrives as a tool call meant a twelve-call turn that closed with
  one sentence compacted to that sentence. Results are clipped to a budget:
  enough to see what came back, not enough to pay for the file contents twice.
- **Borrowed material.** The tree-sitter tag queries under
  `internal/repomap/queries*/` are copied from aider. The built-in prompts in
  `internal/prompts/` began as aider's and are now ours to change;
  `prompts_test.go` asserts the *shape* of the two sets — that each slot says
  the things the loop depends on — rather than pinning a hash, so wording
  stays free to improve and a rule cannot be deleted by accident.

## Codebase structure

- `cmd/`
  - `strument/` — the CLI: kong command wiring, config load, REPL/script
    dispatch.
  - `strumentrec/` — dev-only fixture recorder: a reverse proxy that logs
    both directions of an OpenAI-compatible exchange verbatim.
- `internal/`
  - `coder/` — the orchestration spine: assemble → stream → reflect →
    apply → shell → commit → cost. Its seams with the outside world are
    interfaces in `ports.go` (see below).
  - `editblock/` — the edit engine: Python-`difflib` sequence matching that
    lands a replacement whose whitespace the model reproduced imperfectly, the
    did-you-mean an unmatched edit returns, and `LineOps` (difflib's
    `get_opcodes`, pinned against CPython) which the renderer diffs an edit's
    two sides with. The SEARCH/REPLACE parser, the whole-file parser, and the
    batch planner went with the text formats.
  - `workspace/` — the file-access layer behind read/ls/glob/grep, walking the
    tree and applying ignore rules in process so it behaves the same with and
    without git.
  - `gitignore/` — go-git's gitignore matcher, vendored; see its `NOTICE`.
  - `llm/` — wire-neutral chat types (messages, stream events, usage,
    money) shared by the client, the coder, and the fixture harness.
  - `client/` — the one HTTP client: OpenAI-compatible chat completions,
    OpenRouter dialect, SSE parsing. Tests stub `Transport`; the suite
    never opens a socket.
  - `config/` — the Starlark configuration surface (`provider()`,
    `model()`, `env()`) and the direnv-style trust gate for project
    configs.
  - `repomap/` — the parse layer. Tree-sitter tag extraction (pure-Go
    grammars via gotreesitter) feeds two things: `Tags`, behind both the
    `symbol` tool and the `/symbol` command, and `ParseStatus` for the
    after-an-edit check — which routes `.go` to `go/parser` instead, because
    for Go that is exact and about a thousand times faster on the pathological
    cases. `queries/` and `queries-legacy/` hold the aider `.scm` files.
  - `skill/` — Agent Skills: a restricted parser for the `SKILL.md`
    frontmatter (a hand-written subset rather than a YAML library, which is
    the point — see `frontmatter.go`), discovery across the project and the
    user's data directory, and the trust filter `Usable`, which every path
    putting skill text in front of a model goes through. Measured in
    [`doc/experiments/2026-08-skill-uptake.md`](experiments/2026-08-skill-uptake.md):
    six models loaded a relevant skill 54/54 times, never loaded it on a task
    it did not fit (0/36), and on-demand loading scored the same as having the
    text in context.
  - `prompts/` — the built-in prompt sets.
  - `render/` — streaming markdown renderer (a Go port of
    thetarnav/streaming-markdown) plus the ANSI terminal renderer and the
    color `Theme`.
  - `repl/` — the interactive layer: line editing (via `readline/`), slash
    commands, double-Ctrl-C chords, live streaming render, chat/input
    history wiring.
  - `readline/` — the terminal line editor: a vendored fork of
    ergochat/readline (MIT, taken at v0.1.3) with a flicker-free single-write
    redraw adapted from jart/bestline and Ctrl+arrow word motion. Kept in
    upstream style and excluded from Strument's linters; see its `NOTICE`.
  - `sandbox/` — the Landlock policy: which paths a session may write to, and
    the one call that confines the process. Linux-only, with a stub that
    refuses elsewhere; the writable-set derivation is pure and tested without
    a kernel. See [`security.md`](security.md).
  - `gitrepo/` — the git port; always argv, never a shell string.
  - `history/` — per-project markdown chat transcripts under
    `$XDG_STATE_HOME/strument`.
  - `fixture/` — the record/replay harness: JSON-Lines scenarios and
    replay stubs for the coder's ports.
- `script/` — release build, the grammar build-tag list, and
  `setup-reference.sh`.
- `testdata/` — distilled scenario fixtures and tests transliterated from
  aider's suite.
- `reference/` — a gitignored clone of aider at commit `5dc9490`; a
  read-only grep target for comparing against upstream
  (`task setup:reference`).
- `attic/` — local capture artifacts; gitignored, never committed.

## The coder's ports

The coder talks to the world only through small interfaces in
`internal/coder/ports.go`; every one has a production implementation and a
test stub. If you need new outside behavior, extend a port rather than
reaching around it.

| Port | Purpose | Production / test |
|---|---|---|
| `llm.ModelClient` | one streaming send | `client.Client` / `fixture.StreamStub` |
| `Output` | user-facing printing + live stream | `repl.termOutput`, `StdOutput` / test buffers |
| `Confirmer` | y/n/don't-ask questions | readline confirmer wrapped in `AutoConfirmer` |
| `Asker` | multiple-choice questions: the `ask_user_question` tool, and what to do about an interrupted turn | readline asker / replay stub (nil in script mode) |
| `CommandRunner` | `/run` and the `bash` tool — one shell block through one shell | `PipeRunner` / replay stub |
| `Repo` | git operations | `gitrepo.Repo` / nil (no-git mode) |
| `TokenCounter` | advisory token estimates | `RuneCounter` (runes/4, measured) |
| `Clock` | retry backoff sleeps | `RealClock` / instant fake |

Checks are **not** a `CommandRunner` client, which is easy to assume and wrong:
`runCheck` (`toolobserve.go`) runs a configured argv with `exec.CommandContext`
directly, deliberately without a shell, because the argv comes from the user's
config and never from the model. The scraper command (`scrape.go`) does the
same, for the same reason. What they share with the port is not the port — it
is the two policies below.

Where a command runs, its environment follows who caused it: `/run` (the user
typed it) inherits the whole session environment, while the `bash` tool,
checks, and the scraper command (the model caused them) run under the env
allowlist in `internal/coder/envallow.go` — see the security notes below.

Confinement does not follow who caused it, because it cannot. The Landlock
policy in `internal/sandbox` is applied to the whole process at startup and is
inherited by everything it spawns, so it covers all three paths — the port, the
two direct `exec` sites, and any future one — without a seam to remember. See
[`security.md`](security.md).

The REPL has the same philosophy: `repl.Options` exposes seams
(`Stdin/Stdout`, `IsTerminal`, `MakeRaw/ExitRaw`, `Notify`, `Exit`, `Now`,
`GetSize`) so the whole interactive loop runs under tests over pipes and a
real pty.

## The tool loop

`edit_format` accepts one value, `"tool"`: everything the model does, it does
through a native function call. The API schema enforces the format, so the
whole class of format-parse failures disappears, the prompts shrink (the
schema carries the rules), and a file that contains `<<<<<<< SEARCH` is just
data — which is what lets the harness edit its own prompt strings. `diff`,
`diff-fenced`, and `whole` have been removed, and `edit_format` now exists
only to give the old values a migration error.

Ten tools, in three natures:

- **Observation is free**, because the cost of looking is what makes a model
  guess. `read(path, offset, limit)` returns a `cat -n`-style window with a
  paging hint when it truncates; `grep(pattern, …)` searches contents, listing
  files, matching lines, or per-file counts; `glob(pattern)` finds files by
  path; `ls(path)` lists a directory and names a symlink's target;
  `symbol(name, kind)` answers "where is this defined" from the tree-sitter
  tags rather than from text, and is offered only where grammars are. None of
  them ask the user anything, and none of them see a file the project ignores.
  Paths are root-relative, and results are always reported that way; an
  absolute path that resolves inside the root is accepted as a spelling of the
  same file (small models habitually send one — see
  [`doc/security.md`](security.md)) while `..`, out-of-root absolutes, and
  symlink escapes are refused exactly as before.

  **A search says what it searched.** The outcome line and the result both name
  the scope and mode, not just the pattern, and an empty result distinguishes
  three things: a scope that admitted no files, files that could not be read,
  and a pattern that is genuinely absent. Only the last means "not in this
  project", and reporting all three as "no matches" was observed sending a model
  to widen its *pattern* when the fault was a directory-shaped glob — the
  identifier was in twenty-one files at the time. Globs match whole paths, so
  `*.go` reaches only the root and a bare directory name matches nothing; that
  rule is now in the tool descriptions and in the message.

  **A path and a matching line are different currencies.** They have separate
  caps because they cost differently by two orders of magnitude. A path in this
  project averages 37 bytes, so a thousand of them is a predictable ~9k tokens;
  a *matching line* is whatever happened to be on that line, and for one
  unscoped search here the median match was 1383 bytes with a longest of 157 KB
  — a single line of a recorded JSON fixture. Content searches therefore return
  at most 100 lines, each clipped to 200 bytes and marked when clipped, while
  path results keep the larger cap. Truncating a file listing is the worse
  failure of the two: a reader concludes the files are not there, where a
  truncated content search only means "narrow it".

  The count cap alone would not have bounded anything. Before the per-line clip,
  that search produced 1.33 MB of matches at a cap of 1000 and 88 KB at a cap of
  100 — both over `maxToolOutputBytes` (60 KB), so both delivered *the same* 60 KB.
  The obvious knob was the one that did nothing.

  **`strument tool …` runs any of them from a shell**, printing the bytes a
  model would receive — refusal sentences, truncation notes, and `…` clip
  markers included. It is a developer instrument, not part of the user-facing
  CLI, so the exact result text stays free to change.

  ```sh
  strument tool grep --mode content 'pattern' --glob '**/*' | wc -c
  strument tool symbol SomeName --kind reference
  strument tool --json ls internal
  ```

  The outcome line goes to stderr and the result to stdout, which is what makes
  `| wc -c` measure the right thing. `--json` wraps that same string as
  `{tool, arguments, result, bytes}` rather than re-rendering it, so the two
  cannot disagree. A refusal exits 0, because that string is what a model gets
  and this command's whole job is faithfulness.

  Every number in the two paragraphs above was originally obtained by writing a
  throwaway test inside `internal/workspace`, running it, and deleting it. The
  command exists so that stops being the way. It earned itself on first use, by
  printing `49 long 49 lines were shortened` — `plural` already carries the
  count, and the test that should have caught it was checking
  `Contains(got, "shortened")`.

  The seam it needed is `coder.Inspector` (`internal/coder/inspect.go`): the
  five observation tools hold nothing but a root, a `Workspace`, a `RepoMap`,
  and somewhere to report, so they run without a model, a client, or a
  conversation. `*Coder` keeps its methods as delegations to one.
- **Pinned files are named, not injected.** `/add` puts a file's *name* in the
  system prompt with an instruction to read it; the contents arrive through a
  `read` call like everything else. This replaced a fabricated user turn
  carrying the contents and a fabricated assistant turn agreeing they were
  current, and it was measured before it was adopted
  ([`doc/experiments/2026-08-add-instruct.md`](experiments/2026-08-add-instruct.md)):
  across 600 samples and three models, identical task success, one extra step,
  and blind edits — a pinned file written without ever reading it — from 383
  across 230 runs to **zero across none**. The earlier
  [characterization pass](experiments/2026-08-add-authority-characterization.md)
  is why it was tried at all: under the old design the model re-read a pinned
  file in 31% of runs anyway, usually before editing. It did not believe the
  block.

  A pinned file that does not exist yet is named as one to create, not one to
  read — there is nothing there to read, and sending the model after it would
  waste a step.
- **Reference material is pinned, and refused as an edit target.** `/read-only`
  puts a file's contents in the prompt and `allowedToEdit` refuses any edit to
  it, answering the call with why so the model adapts within the turn. It is
  also the way to show the model a file *outside* the project: `grep`, `glob`,
  and `ls` walk the workspace root, so an out-of-tree spec or a sibling repo's
  header is invisible to the model's own searching. That is why `/read-only`
  still *injects* where `/add` no longer does — telling the model to go and find
  a file three of the four observation tools cannot see would be an instruction
  it could not follow. Editable files stay inside the root: `/add` and
  command-line arguments both refuse to leave it, and point at `/read-only`
  instead.

  What it no longer does is answer *itself*. The injected block used to be
  followed by a fabricated assistant turn agreeing to use the files as
  references; that is gone, and the prefix now says plainly who pinned them and
  that an edit will be refused. Thirty-six live sessions
  ([`doc/experiments/2026-08-readonly-honest.md`](experiments/2026-08-readonly-honest.md))
  say the agreement was buying nothing — an unfetchable reference was used just
  as readily without it — while "an edit is refused" in place of aider's "do not
  propose edits" stopped models from spending whole turns litigating a request
  that asked for one anyway.

  One correction that pass forced, worth recording because the sentence above
  used to contain it: `read` is *not* scoped to the root. It joins its argument
  to the root with no containment check, so a relative path with `..` reads
  fine, and transcripts show models doing exactly that. Only `grep`, `glob`, and
  `ls` are confined. Edits are confined separately and properly, in
  `unsafePath`.
- **Edits are direct**, exactly as a SEARCH/REPLACE block was.
  `edit(path, old_string, new_string)` replaces an exact span, through the
  same fuzzy matcher aider's format used, and returns a did-you-mean when it
  misses. The span has to be unique: `old_string` appearing twice is a refusal,
  not a coin flip. It used to take the first occurrence and report success,
  which is the failure edit-tool-bench criticises in *fuzzy* edit tools — a
  harness reporting success on an underconstrained transformation, so the model
  reasons on from a change that may have landed in the wrong place. Exact
  matching does not prevent that on its own; exact is not unique. `write(path, content)` puts down a whole file — creating it or
  completely overwriting it, and the outcome line says which, so neither the
  user nor the model assumes the old contents survived. Both land the moment
  the call arrives. The safety net is the snapshot and the diff, not a
  question. Like the observation tools, they accept an absolute path that
  resolves inside the root as a spelling of the relative one, and answer in
  the relative form.
- **Mutation through the shell is gated.** `bash(command, purpose)` runs only
  after the user confirms; its command is parsed and interpreted by the embedded
  pure-Go `mvdan.cc/sh/v3` shell rather than a host `/bin/sh`. Its output
  returns as the tool result. A differential test against real bash found no
  divergence across twenty-five constructs (`shell_test.go` pins the nine a
  model actually emits), with one platform gap: process substitution is
  unimplemented on Windows, where `<(…)` yields a TODO notice from the
  interpreter instead of running.
  `check(name)` runs a *configured* argv from the `check` dict by name, so
  it needs no gate — the model supplies a key, never a command, and there is
  nothing to classify or smuggle. It is offered only when `check` is
  configured.

  A check runs under the environment allowlist, not the full session
  environment (`internal/coder/envallow.go`) — the same filtered set the
  `bash` tool gets. The argv is trusted, but the *output* is not selected by
  it: a failing test suite happily prints its environment, and that output
  goes to the model as a tool result. The allowlist passes `PATH`, `HOME`,
  `GOCACHE`, the proxy variables, and the other non-secret toolchain state;
  it withholds everything credential-shaped, by omission rather than by a
  name filter (a hard one would push users toward writing tokens to files,
  which is worse). `env_allow` in the config and `/env` in the REPL widen it,
  each widening a deliberate, visible act.
- **The execution boundary is *who caused the command*, applied twice.** The
  confirmation prompt already splits on it: `/run` never asks (you typed it),
  `bash` always asks (the model asked). The environment splits on the same
  line: `/run` keeps your full environment, while every command the model
  caused — `bash`, `check`, the `scraper` command — gets the filtered set,
  because both its *input* and its *output* reach the model's context.
  Permissions are computed at use time from one piece of state
  (`coder.EnvAllow`), so no call site needs bookkeeping to see a `/env`
  change. One trap worth knowing when adding a subprocess path:
  `mvdan.cc/sh`'s `interp.Env(ListEnviron(nil...))` means *empty*
  environment, not inherit — omit the option to inherit.
- **The gate is an allowlist, and never a blacklist.** A `bash` command that is
  a configured check *verbatim* skips the prompt (`allowlist.go`); everything
  else asks. The asymmetry is the whole argument. A blacklist — escalate on
  `rm`, `curl`, `sudo` — fails **open**: everything it did not think of sails
  through, and the misses are silent. An allowlist fails **closed**: the worst
  case is a prompt the user did not need, which they notice and can fix by
  naming the check. Strument therefore classifies nothing as dangerous, because
  nothing here could do it reliably. The match is strict — a single simple
  command of bare literal words, no expansions, no quoting — and a match runs
  the configured argv rather than the model's string, so what runs is certainly
  what was compared. `project_checks()` fills the dict from a project's marker
  files, opt-in, so the allowlist is worth configuring without hand-writing one
  per repository.
- **The surviving prompt is worth reading, so it can default to yes.** `purpose`
  is required and shown above the command; an absent one is shown too. That pair
  is deliberate: the earlier `y/N` made the common case cost a keystroke it did
  not need to, and friction in the common case is exactly what erodes a prompt
  into reflex. Cheapening the answer is only defensible alongside making the
  question informative. What reaches the prompt at all is the open-ended
  remainder after every observation tool and the check allowlist have taken
  their share.
- **"All this turn" is offered only under a sandbox.** `a` blanket-approves the
  turn's remaining `bash` commands, and it was removed once for the obvious
  reason: an option that answers a question you have not read is the reflex,
  not a defence against it. It is back because the cost of a bad approval is
  now bounded — `internal/sandbox` confines writes to the project and a known
  list, so the worst case is a `git diff` and an `/undo` rather than an
  afternoon of finding out what else changed. The option is gated on
  `Sandbox.Active`, not on the setting: a required-but-unavailable sandbox does
  not offer it, because there the bound does not exist. That ordering is the
  reason the sandbox landed before `a` came back, along with `shell_timeout` —
  Landlock does nothing about a command that spins forever, and blanket
  approval is exactly the mode where nobody is watching it happen.
  [`security.md`](security.md) is the threat model this rests on.

- **Asking the user is a tool call, and a different channel from the gate.**
  `ask_user_question(questions)` lets the model pause mid-turn and collect a
  bounded decision it genuinely cannot proceed without — "which of these two
  config keys did you mean", "REPLACE the whole function or just the body".
  Questions are multiple-choice (2–4 options, 1–5 questions per call, free
  text always available to the user as an implicit last row), print as
  ordinary scroll with a numbered list, and are read through the `Asker` port
  — a separate port from `Confirmer`, because a yes/no shape cannot carry a
  choice and because `--yes` must structurally not answer one: that flag skips
  named permission prompts, and a question is the model asking for
  information, not permission. Without a terminal (script mode, a nil Asker)
  the call answers the model with "proceed using your best judgment and state
  the assumption you made" rather than hanging or silently picking option 1 —
  a decision the user never made must never be attributed to them. An answer
  typed as indices resolves to labels only when *every* comma-separated token
  is a valid index; anything else is the whole raw line as free text
  (`ParseAskAnswer` — one rule, shared by the terminal prompt and the fixture
  stub so a replay cannot interpret an answer differently from the session it
  stands in for). An ask is an ordinary work step: no reflection budget unless
  the arguments were malformed, and the turn continues on the result like any
  other tool call.

The tools live in `internal/coder/tools.go` (`toolobserve.go` for the
observation half, `toolsymbol.go` for `symbol`); `applyToolCalls` dispatches a
captured turn. Every tool call gets a `tool` result message, always, so the
next request stays well-formed.

**The loop closes.** When a turn produces tool calls, the results are appended
and the turn *sends again* — `applyToolCalls` returns `OutcomeContinue` and
`runOne` goes round. That is the whole pivot: a reply ending in a tool call
carries `finish_reason: "tool_calls"`, so hanging up on it breaks the
protocol every post-trained model was fitted to. The loop is bounded by
`max_steps` (default 25, configurable), which is a checkpoint rather than a
wall: the turn reports what it has done and asks whether to continue.
`max_error_reflections` (default 3, configurable) bounds the rounds an *error*
starts, and `maxAutoCheck` (3) the rounds the harness itself starts, so a
model in a fix-break cycle hands back to the human instead of eating the work
budget.

**Reflection is a tool error, not a synthetic user turn.** When an
`old_string` doesn't match, its call's tool result carries the failure (with
the did-you-mean) and the turn re-sends on those results — no injected "please
fix" user message. `runOne` is outcome-driven, so a text-free tool reflection
still loops. There is one deliberate exception: a failing `check_auto` speaks
as a *user* message, because the harness is talking unprompted and no tool
call is waiting for an answer.

**Ctrl-C stops the send, not the turn.** Each send runs under a child context
of its own (`sendSteerable`), and the REPL's signal handler cancels *that* via
`InterruptSend` rather than the turn's context. It used to cancel the turn's,
which meant every later call in it saw `Canceled` and the turn could only end
there — even though the conversation had survived intact the whole time. What
the human meant by stopping is then a question, put through the same `Asker`
port `ask_user_question` uses: Continue, Stop, or type a correction, where the
free-text row *is* the correction and needs no parsing. A nil Asker (script
mode) stops, which is what an interrupt has always done. Tool execution runs
inside the send's context too, so Ctrl-C still kills a running `bash` command
— and because the stream has already finished by then, `applyToolCalls` reports
that cancellation as an interrupt itself. Without that it returned
`OutcomeContinue`: the command died, the turn carried on as though nothing had
happened, and a second press inside the chord window quit Strument. The command
also says in its own output that the user stopped it, since a result that just
stops is the kind of unexplained failure a model answers by editing code.

The edits made before an interrupt are committed and snapshotted *there*
(`settleEdits`), because the interruption is a review boundary: `git show` gives
what the model did before you stopped it separately from what it did after your
correction, and `/undo` steps through the two halves. `settleEdits` is gated on
`turnSnap` so the turn-end defer does not settle the same edits twice and
announce "the turn left the files as they were" — true of the second attempt,
false of the turn. Usage still flushes once; a steered turn is one turn's spend.

Two things about that path were only findable by running it. The double-Ctrl-C
chord had a hole: it lives in the signal handler, which sees only SIGINT, but
while a question is up readline holds the terminal raw, ISIG is off, and Ctrl-C
arrives as a byte — so a pty probe found two presses 50 ms apart producing one
"^C again to exit" and no exit. `readAskLine` now consults the chord itself. And
**Continue must say so.** The note left at interrupt time can only report that
the reply was cut off; on its own that reads as a full stop and the model
obliges, or — with a partial answer above it and no instruction — starts over.
Both were observed. The note added on Continue names the decision and rules out
both readings, and seven interrupts across three models then resumed, two of
them mid-word.

**SIGUSR1 is the same interrupt without a keyboard.** It shares the Ctrl-C
handler's subscription (the `Notify` seam delivers both to one channel) and
calls the same `InterruptSend`, so the steer menu, `settleEdits`, and the
"Stopped…" hint all come from the coder noticing the cancelled send and need no
second path. What it does *not* share is the chord: a chord is a keyboard
idiom — the second press means "no really, exit" — and a signal arriving from
a program means "interrupt" every time. Between turns the subscription does not
exist (`withinTurn` owns it for the duration of the turn only), so the signal
does nothing, exactly like a Ctrl-C with nothing in flight. Script mode has no
REPL to subscribe for it; main adds the signal to its existing
`NotifyContext` through `repl.UserInterruptSignal()`, and the turn's
`ctx.Done()` reaches `sendSteerable` the same way an in-terminal interrupt
does.

**Fetching a page is a tool, not a guess about what a URL in your message
meant.** `webfetch(url, purpose)` (`internal/coder/webfetch.go`) is the model's
way to read a documentation page, and the name is the field's rather than this
project's — Claude Code has `WebFetch`, OpenCode and MiMo Code `webfetch`,
DeepSeek Harness `web_fetch` — for the reason `tools.go` already gives about
`edit` and `grep`. Bare `fetch` was the tempting short one and is the one to
avoid: in a coding agent that word means git.

The confirmation is the shell gate's shape — purpose above, the thing to read
below — with the URL never shortened, since a query string is where a URL stops
being the one you assumed. What differs is the scope of "a". `bash` ties its
"all this turn" to the sandbox, because a sandbox bounds what an unseen command
can do; nothing bounds an unseen *URL*, so the bound comes from the answer
instead: **a is scoped to one origin**, host and port, and the prompt says which
(`Y/n/a=all on go.dev:443 this turn`). Approving five pages of one docs site is
the workflow people actually want; pivoting to a host you never saw is not.
`webfetch_allow` skips the prompt for origins you name — which is also why it
needs no `--yes` flag, since the flag question only arises for an origin you did
not. The sandbox gates a fetch only when a `scraper` command is configured: the
built-in fetcher spawns nothing, and refusing it for want of a kernel feature
would apply a rule about subprocesses to something that has none.

**A big page is navigated, not truncated.** The Python docs' `stdtypes.html`
is 228 KB of real content against a 60 KB result cap, and the first version of
this told the model to "fetch a more specific page" — advice that assumes a page
which may not exist, given to a model holding a quarter of the one it has. The
predictable next move is to abandon the tool for `curl`.

Two mechanisms replace it, both borrowed from link-preview bots. A **URL
fragment fetches that section alone**: `…/stdtypes.html#string-methods` returns
32 KB instead of 228 KB, and `…#str.join` returns the method. And **`outline:
true` returns the page's headings with their anchors** — 3.5 KB for that same
page, a 61× reduction, and every line names a fragment that can be fetched.
A page that still has to be cut carries its own outline appended, so the map
costs no extra round trip.

The three generators put the fragment's target in three different places, so
`fragmentHTML` handles the shapes rather than one: a heading takes the siblings
that follow it, a container is already the section, and a `<dt>` brings its
`<dd>`. MediaWiki needed the case that decided the approach — it wraps the
heading with an edit link, so the prose is a sibling of the *wrapper*, while
Sphinx nests the other way and would break under a rule that always climbed.
The choice is made by measuring what came back rather than by predicting the
markup, which is the version that works on both. `markHeadings` writes each
anchor into the markdown as a Pandoc heading attribute, which is what makes the
outline a string operation and lets a truncated page map itself.

`internal/origin` owns what an origin is, because two packages need the
identical answer and neither can import the other — the config loader validates
an entry at load, the coder matches a fetch at run time.

**A reply that repeats itself is stopped like an interrupt nobody pressed.**
Small models get stuck emitting one sentence forever, and unlike a repeated
tool call it does not resolve on its own — the context fills instead.
`internal/coder/loopdetect.go` watches the streamed text for a fifty-character
window recurring ten times at close spacing (Gemini CLI's shape and thresholds,
widened on gap) and for one word repeating thirty times running, which is the
stutter no window can see. Answer and reasoning are watched separately, because
they interleave on the wire and a window spanning the seam is a repetition of
nothing; in a corpus of ten real loops across ten models every one was in the
reasoning. Returning early from the stream is what stops it, so it costs one
abandoned request. The outcome is `OutcomeLooping`, deliberately *not*
`OutcomeInterrupted`: the steer menu cannot say "you stopped the model" when the
user did not, and "carry on from where you stopped" — the right thing to say
after a Ctrl-C — is the one instruction that would resume the loop. Off with
`loop_detection = False`. The detector is naive on purpose (a substring per
offset, a map that grows with a bounded tail); the upgrade, if a profile ever
asks for one, is a rolling hash over a ring buffer, which needs no dependency.

**The harness never speaks as the assistant, and mid-conversation it speaks as a
marked user.** `llm.HarnessNote` is the one way to say something into an ongoing
conversation — an interruption, an `/undo`, a decision the user made. The
honest role would be `system`, and `system` is what the *prefix* uses for
session notes, but it cannot be used once the conversation
is under way: Anthropic rejects a system message that follows an assistant turn
outright ("role 'system' must follow a 'user' message or an 'assistant' message
ending in a server tool result"), which is exactly where such a note goes. A
probe across five providers found the marked user turn accepted by all of them
and heeded as well as a system message was by the four that took one. The rule
is worth keeping whole because the tempting shortcut breaks one provider
silently: **system in the prefix, marked user mid-conversation, assistant
never.** Four sites used to write fabricated assistant turns — three of them
saying "Ok." to keep roles alternating, which nothing requires — and
`voice_test.go` now fails if any of them comes back.

The compaction summary was the last exception and is one no longer. It was a
`system` message on the reasoning that a summary is the harness's artifact —
true, and the wrong conclusion, because it lands in `done`, mid-conversation,
which is precisely the position the rule above is about. It escaped the
rejection only by always being spliced at index 0 of `done` with nothing
assistant-role ahead of it: three facts no test pinned. Two further reasons to
move it. The label says "it is not something anyone said" while the role said
"trust this most", and a summary is the *least* reliable thing in the request —
the compaction trial caught one inventing a rationale nobody had given, so the
highest-trust channel was the wrong home for it. Every other harness surveyed
agrees: Codex CLI and Kimi CLI both re-inject the summary as a prefixed user
message, OpenCode as an assistant message flagged `summary: true`. None uses
`system`. Session notes stay in the prefix, because they genuinely are
scaffolding read every turn rather than history.

**Edits compose within a batch.** `applyToolEdits` applies a turn's edit calls
in order against a shared overlay, so two edits to one file build on each
other, then writes the batch atomically — all of it or none of it, each file
keeping its mode and following a symlink rather than replacing it. Each call
gets its own result and its own verb (`Created` / `Overwrote` /
`Applied the edit to`). A file that parsed cleanly before the batch and no
longer does earns a warning to the user and a note on the result of the call
that finished it (`parsecheck.go`); the edit still applies, because the model
may be mid-repair and a check that refuses is a check that has to be right.

**"Code scrolls by" with tool calls.** Providers stream a call's arguments as
JSON-escaped string fragments, so raw rendering would show escaped JSON.
`internal/render/toolargs.go` decodes them live: `ArgScanner` is a streaming
JSON string-field extractor (escape- and UTF-8-boundary-safe) and `ToolDiff`
turns the decoded fields into a red-green Git-style diff. `write` and `bash`
stream line by line, since neither has a second side to compare against. An
`edit` is *buffered* and rendered in `Flush`: its two sides are diffed against
each other with `editblock.LineOps`, so a one-line change inside twenty lines
of matching context reads as a one-line change, with three lines of context
each side and a `… N unchanged lines …` marker between. The `path` header
still prints the moment that field completes, so the file being edited appears
while the rest streams. `ToolDiffSet` fans a turn's calls out by index. There
are two decoders by design: `ArgScanner` for streaming *display*
(best-effort) and `json.Unmarshal` on the complete arguments for the
authoritative *apply*.

**The harness's own voice is a channel.** `Output.Toolf` and `Theme.Tool` (a
recessive gray) carry everything Strument says about its own mechanics — what
a tool did, what was committed, which check is running — so it sits behind the
diffs and the answer rather than competing with them. A passing check prints
one line; only a failing one prints its output.

**Thinking heads its group; the blank line goes before it.** A block of
reasoning explains the tool calls that come *after* it — "let me read the
file", then the read — so `render.GroupSep` puts the separator ahead of the
block rather than behind it. The separator is lazy: a step's outcomes print
after the stream is flushed, and whether another step follows is unknowable
until the next response arrives, so nothing is written when a group ends. A
debt is recorded and paid by whatever starts the next group, which is also what
keeps a separator off the top of a turn and off the bottom of one.

The one exception is an answer, which keeps its blank line *after* the
thinking: it is what the model decided to say rather than what it went and did,
and running the two together blurs that. The policy is written twice, once per
`Output` implementation, and `TestBothOutputsAgreeOnSpacing` holds them
together.

This is a readability fix that matters most where it is least visible in
development: with the reasoning palette unavailable — a terminal without
`faint` — the marker is the only cue there is, and the old spacing grouped each
block with the calls above it, which it had nothing to do with. The same blind
spot hid a doubled blank line in `blankGuard` for as long as it existed, since
every test rendered without color and the bug needed a color reset to appear.
The guard now steps over CSI sequences when counting newlines, and the spacing
tests run both ways.

### Cross-provider streaming quirks

Tool-call *edits* work across the current model field — but the way providers
*stream* a call's arguments diverges, so the clean, uniform UX is the
renderer's doing, not the wire's. A 16-model live sweep (via OpenRouter, one
edit + one shell command + one whole-file write per model) made this concrete.
Fourteen models drove all three tools cleanly and rendered byte-identical
canonical diffs. The two that stumbled did so only on the *edit* — they still
ran the command and wrote the file — and both were the smallest, most
reasoning-heavy models (gpt-oss-120b, qwen3-14b), which spent a modest output
budget thinking and never emitted the call. The ~27B floor holds.

The sweep predates the current tool names (the edit tool was
`replace_in_file(path, search, replace)` then, and is `edit(path, old_string,
new_string)` now), but what it measured is a property of the providers, not of
the schema, and it has not changed.

Underneath that uniform surface, the wire order of an edit's JSON fields is
all over the map:

| Wire order of the edit tool's fields | Models |
| --- | --- |
| `path, search, replace` (schema order) | Claude Haiku 4.5 / Opus 4.8 / Sonnet 5, Cohere North-mini-code, Kimi K3, Laguna-S-2.1, Step-3.7-flash, MiMo v2.5 |
| `path, replace, search` (replace first) | Gemma-4-31b, MiniMax-M3, Kimi K2.6, GPT-5.6-sol, Inkling |
| `replace, search, path` (path last *and* replace first) | Gemini 3.5 Flash |

Only eight of the fourteen keep schema order. Gemini streams `path` **last**,
so nothing in the arguments even names the file until the end. The renderer
absorbs all of it — every row above renders header-first, removed lines above
added — because it never assumes field order. The normalizations, each with a
regression test in `toolargs_test.go`:

- **The two sides in either order.** An edit is buffered whole and diffed in
  `Flush`, so which side arrives first cannot be observed. This *replaces* an
  earlier fix that held added (`+`) lines until the removed (`-`) lines were
  known — buffering for the context diff subsumed it, and the reordering
  machinery went with it. (The problem was seen first with GLM 5.2, then on
  six models in the sweep, Gemini included; the test stayed when the code
  changed.)
- **`path` not first (Gemini, Qwen3.6).** An edit's diff lines are buffered
  until the `path`/header resolves, then the header leads. This still matters
  for `write`, which streams.
- **Interleaved calls (DeepSeek).** With two calls in one turn, fragments
  arrive interleaved; `ToolDiffSet` streams the first call live and buffers
  later ones, appending each whole in first-seen order. (Single-call sweep
  turns didn't re-trigger this; the regression test stands.)

The authoritative `json.Unmarshal` parse is order-independent, so none of this
touches correctness — only display. The lesson worth keeping: **field order
and fragment contiguity are provider-specific and not guaranteed; a streaming
renderer must assume neither.** The per-hunk edit shape itself needed no change
— every capable model produced well-formed single-hunk calls (some with more
surrounding context than others), so batching stays unnecessary.

## Configuration

A single user `config.star` (`$XDG_CONFIG_HOME/strument/config.star`)
declares providers and models; a project-local `.strument.star` can extend
it but is **inert until trusted** (`strument trust`, content-hash gated,
direnv-style). The `README.md` has a worked example covering providers,
model factories, `with_extra_params`, and aliases;
[`config.md`](config.md) is the reference for every built-in and parameter.

Outbound HTTPS can be routed through a **SOCKS5 proxy** (`socks5://` or
`socks5h://`) for restricted networks, and it covers *every* outbound HTTPS
action. A per-provider `proxy` handles that provider's chat/completions; a
top-level `proxy` is the fallback for providers that set none and also drives
the two non-provider egress paths — `strument model-config`'s catalog fetch and
URL scraping. A provider opts out of the global proxy with `proxy="direct"`. The
URL is resolved and validated at config load (once per `*Model`, in
`config.Load`) and turned into an `http.Transport` by the leaf `internal/httpx`
package; Go's `net/http` speaks SOCKS5 natively, including `user:pass@` auth, so
no external dependency is needed.

## Per-project state

Strument keeps one directory per project under
`$XDG_STATE_HOME/strument/projects/<basename>-<hash8>/`, keyed by the SHA-256 of
the project root's absolute path. It holds `root` (the path that hash was taken
over, so a stale directory can be identified without recomputing hashes), the
markdown `transcript.md`, and readline's `input.txt`. The directory is `0700`
and its files `0600`: a transcript records whatever the model read out of the
project, and the case that justified `--no-git` in the first place is a live
configuration directory.

The project, for this purpose, is the git worktree root wherever there is one
and the working directory otherwise — **independent of `--no-git`**, which says
how a turn is committed rather than which project you are in.

Before adding anything here, three tests. A file belongs only if it is:

1. **Per-project.** Global things stay global. The `.strument.star` trust store
   is the sharp case: one file to audit and one file to revoke is a security
   property, and scattering trust records across project directories would turn
   an audit into a `find`.
2. **Not reconstructible.** Anything derivable from the source or refetchable
   from a provider is a *cache* and belongs in `XDG_CACHE_HOME`, where the
   `model-config` catalog already goes. A persisted tag cache and a scraped-page
   cache both fail here.
3. **Not config, and not adjudicated from outside.** Instructions that shape how
   the model works belong *in* the project, versioned and reviewable, on the
   same terms as `.strument.star` — not in a hidden directory that silently
   changes behavior.

A fourth question is worth asking even when all three pass: **does the data
already exist somewhere authoritative?** Strument stamps its own commits with a
trailer, so a file listing "commits this session made" would duplicate git and
go stale under rebase. Read the trailer instead.

These are not hypothetical. Applied to ten candidates, they rejected four
without further argument.

`resume.json` is the one that passed cleanly, and it is deliberately the *cheap*
half of a session: the pins (plain and read-only) and the model alias — what you
would otherwise retype. Not the conversation. Storing `doneMessages` would make
Strument re-send a context the user pays for, assert something about what the
model remembers, and blur the turn boundary; the transcript already exists for
reading. Two rules keep it from surprising anyone: paths are relative to the
project root rather than the coder's, so they survive `--no-git` from a
subdirectory, and the model alias is recorded only when it differs from the
config's `default`, so a project is never silently pinned to a stale default.
Restoring is announced in the banner, and `--no-history` writes none of it.

`cost.jsonl` is one JSON line per turn — the same numbers the closing usage line
prints, kept as data rather than as prose. It exists because the question "which
model is actually worth it" keeps coming up and nothing on disk could answer it:
a prompt A/B in August ended up with its sample size set by the most expensive
model rather than by the question. Appended, never rewritten, so a partial write
costs at most the last line, and `cat projects/*/cost.jsonl` aggregates across
every project — the query the per-project layout would otherwise make harder. At
about a hundred bytes a turn there is no pruning policy to get wrong. The coder
reaches it through a `RecordUsage` callback rather than a writer, so it keeps
knowing nothing about where state lives.

## Testing

- `go test ./...` runs everything without network, sockets, or API keys.
- **Fixtures**: model behavior is replayed from JSON-Lines scenarios
  (`testdata/fixtures/`), recorded through `strumentrec` and distilled by
  hand. The fixture loader fails loudly on schema-version mismatches.
- **Transliterated tests**: aider's own unit tests, ported file-by-file
  (`testdata/transliterated/`, `internal/editblock/editblock_test.go`).
- **REPL tests**: scripted sessions over pipes in readline's
  non-interactive mode, plus a real-pty round trip that answers the
  cursor-position query itself.
- **Pinning tests**: embedded-query compilation, the grammar-tag list,
  `LineOps` against CPython's `difflib`, the prompt sets' shape
  (`prompts_test.go` — the rules the loop depends on, not a hash), and the
  safety-critical behaviors (`rollback_test.go`, `snapshot_test.go`,
  `unsafepath_test.go`, `usage_test.go`) have tests that fail if the
  invariant drifts.
- **Sessions and compaction**: [`doc/sessions.md`](sessions.md) is why picking a
  project back up works the way it does — compaction at a turn boundary, session
  notes instead of a replayed conversation, and the alternatives that were
  rejected. Read it before changing anything in that area; the arguments are not
  reconstructible from the code alone.
- **Live experiments** are the other half, and the one that keeps finding what
  the suite cannot. [`doc/experimenting.md`](experimenting.md) is the handbook:
  what has actually gone wrong when running them, and in what order to doubt a
  result. Read it before designing an arm. The short version is that most of
  what goes wrong is equipment rather than statistics — a scorer broken by the
  harness's own escape sequences turned a real effect (10/12 vs 5/12) into a
  clean null (5/12 vs 4/12, p=1.0), which is the shape that gets a bad change
  shipped.

## Common tasks

### Adding a slash command

1. Add a `command` row to the table in `internal/repl/commands.go` (name,
   argument placeholder, help text, `run` function). The table drives
   `/help` and tab completion automatically.
2. `run` returns the message to send to the model, or `""` for
   commands that only mutate state; use `r.out` for output so colors and
   `--no-color` behave.
3. Extend the scripted-session test in `internal/repl/repl_test.go`.

### Adding a repo-map language

1. Drop the aider `.scm` into `internal/repomap/queries/` (pack) or
   `queries-legacy/` (legacy fallback).
2. Map extensions in `extToLang` (`internal/repomap/lang.go`); check the
   gotreesitter registry name.
3. `TestAllEmbeddedQueriesCompile` must pass — gotreesitter rejects some
   query constructs the upstream `.scm` files use, so extend
   `preprocessQuery` only with care.
4. Regenerate `script/grammar-tags.txt` (a test keeps it in sync) and add
   a fixture row to the language matrix test.

### Adding an ecosystem

An ecosystem has **two** consumers, and they have to agree. `project_checks()`
decides what to run; the sandbox decides what a run may write. A project
Strument offers to run checks for is a project whose checks have to work under
the sandbox — so an ecosystem added to one and not the other does not fail
visibly. It fails for whoever uses that language, months later, with a build
error that looks like their own.

1. Add the detector to `detectors` (`internal/config/projectchecks.go`). The two
   rules there: only commands the marker's own toolchain ships or the project's
   own config names, and a target the command runs has to actually exist.
2. Add its cache to `cacheDirs` (`internal/sandbox/writable.go`), reading the
   toolchain's own environment variable before falling back. If the toolchain
   keeps a cache beside a `bin/` — Cargo, Bun, pnpm, gem, cabal all do — put the
   *root* in the scanned list instead, and `writableSubdirs` will grant its
   contents without granting anything on `PATH`.
3. Add a row to `TestFlatEcosystemOverridesAreHonoured` or
   `TestScannedEcosystemOverridesAreHonoured`. This is the step that matters:
   two dozen environment-variable names is two dozen chances to misspell one,
   and a misspelling fails *silently* — the default is granted, the relocated
   cache is not, and only people who moved it are affected.
4. Add the row to the table in [`doc/config.md`](config.md#language-support),
   which is where both consumers are documented together.

### Adding a `model()` or `provider()` parameter

1. Extend the struct in `internal/config/types.go` and the builtin in
   `builtins.go` (`starlark.UnpackArgs`).
2. Update the config example in `README.md`.
3. Add a parse test in `internal/config/`.

## Verification workflow

1. `go build ./cmd/strument` — full build, every bundled grammar; no cgo.
2. `go test ./...` and `go vet ./...`.
3. `task lint` (`golangci-lint run`) at zero issues; `task format` before
   committing.
4. For repo-map or release work: `task build:strument:subset` builds the
   release variant against `script/grammar-tags.txt`.
5. To compare against upstream: `task setup:reference` clones aider at
   commit `5dc9490` into `reference/`.
