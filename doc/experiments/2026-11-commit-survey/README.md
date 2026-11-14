# Who commits, and what the model is told about it

**2026-11-14. Survey of the standard panel's source, with a small live pilot on
MiMo-V2.6-Flash.**

## Why

The turn-end commit went in November, so the commit tool became the only way
a model's edits get committed. In live sessions MiMo then left finished fixes
uncommitted, and the question was whether a prompt line, a harness reminder,
or nothing was the right answer. Before choosing, this asks what the rest of
the panel does.

## Sources

Shallow clones read on 2026-11-14. These repositories move fast; read every
claim below as an observation of these commits.

| harness | repository | commit (date) |
|---|---|---|
| OpenCode | `sst/opencode` | `d759c54` (2026-11-14) |
| Pi | `earendil-works/pi` | `3f5b779` (2026-11-14) |
| Codex | `openai/codex` | `b1573f1` (2026-11-14) |
| Kimi Code | `MoonshotAI/kimi-code` | `f082f04` (2026-11-14) |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `39163dd3` (2026-11-13) |
| Claude Code | closed source; docs at code.claude.com (`checkpointing`, `common-workflows`), read the same day, and the system prompt of a Claude Code on the web session | — |

## What they do

| harness | does it commit | what the model is told | undo |
|---|---|---|---|
| OpenCode | only when asked | Per-model system prompts. `trinity.txt`: "NEVER commit changes unless the user explicitly asks you to. It is VERY IMPORTANT to only commit when explicitly asked, otherwise the user will feel that you are being too proactive." `kimi.txt`: "DO NOT run `git commit`, `git push`, … unless explicitly asked to do so. Ask for confirmation each time." `gpt.txt` says only not to revert changes the model did not make. | its own snapshots |
| Pi | nothing in core | The system prompt (`coding-agent/src/core/system-prompt.ts`) says nothing about git. Commits exist only as example extensions: `auto-commit-on-exit.ts` runs `git add -A` and commits with the last assistant line as the message at session shutdown. | example extension `git-checkpoint.ts`: `git stash create` at each turn start, offered back on `/fork` |
| Codex | only when asked | `protocol/src/prompts/base_instructions/default.md`: "Do not `git commit` your changes or create new git branches unless explicitly requested." Committing is listed among the next steps to *offer*: "concisely ask the user if they want you to do so. Good examples of this are running tests, committing changes…". `ext/git-attribution` adds a `Co-authored-by: Codex` trailer when the model does commit. | `ghost_snapshot` survives only as a "legacy no-op setting retained for compatibility" (`config/src/config_toml.rs`) |
| Kimi Code | main agent only when asked; tower workers must | bash tool (`agent-core-v2/src/agent/tools/os/bash/bash.md`): "Run git-mutating commands such as `git commit`, `git push`, `git reset`, and `git rebase` only when the user asks for them." Workers in the multi-agent "tower" run in worktrees, and `complete` refuses to finish without "a committed build or artifact diff" (`features/tower/tools/complete/complete.md`). On spawn, uncommitted base-checkout changes become the mission branch's first commit. | worktrees |
| DeepSeek Harness | never | The core system prompt (`core/system-prompt/src/index.ts`) and the bash tool say nothing about git. | `deliverables/workspace-changes`: git snapshots of the working tree at turn start and turn end, diffed to show the user what each turn changed |
| Claude Code | only when asked | The docs say nothing either way; the system prompt of a Claude Code on the web session says "Commit or push only when the user asks." | checkpoints are file snapshots, not commits: "A file that only Bash commands changed isn't tracked and stays as it is. To revert it, use version control, such as Git." `/rewind` restores; for history, "continue using version control, such as Git" |

## What it says

- **Five of six tell the model never to commit unless asked, and the sixth
  says nothing.** No harness commits by default, and none offers a commit
  tool. aider, Strument's ancestor, is the outlier the other way: it commits
  every turn itself.
- **Models are probably trained under that rule.** The prompt habit is close
  to universal, so a model's reluctance to commit unasked is likely learned
  rather than something one line of prompt can talk it out of. The pilot below
  is consistent with that.
- **The only harness that makes a model commit enforces it.** Kimi's tower
  workers are held to committing by a tool that refuses to complete, not by
  wording.
- **Undo is snapshots everywhere.** Every harness with undo uses its own file
  snapshots or git objects outside the history, as Strument's `/undo` already
  does. Commits are not anyone's undo mechanism.

## Pilot

One task, MiMo-V2.6-Flash pinned to the Xiaomi provider, reasoning low, script
mode: `Sum skips the first element. Fix it.` in a two-function Go file, with an
uncommitted user change planted in the other function's doc comment. The metric
is a count: commits the session made. Arms were run in batches, not
randomized, and four sessions an arm cannot separate anything but the
extremes.

| arm | sessions | committed |
|---|---|---|
| the system prompt's commit bullet says "when a change is done, commit it with the commit tool" | 2 | 0 |
| + the loop paragraph's example sequence ends "and commit the fix" | 4 | 0 |
| + a closing line, "commit them with the commit tool before you finish the turn" | 4 | 0 |
| + "A change is not finished until it is committed" | 4 | 1 |
| a once-per-turn `[strument]` reminder when the turn ends with uncommitted edits, prompt unchanged | 4 | 4 |
| neutral wording: the bullet and tool description say what commit does, not when | 4 | 0 |

Wherever a session committed, the planted user change went into its own
commit first and the model's commit held only the fix. Committing unfinished
work, the counter-metric to the reminder, was not measured.

## Decision

Neither steering nor the reminder. The system prompt's bullet and the commit
tool's description now say what a commit takes and nothing about when to
make one, so that whether a model commits unasked can be watched rather than
shaped; the harness stays free of reminders. The maintainer reports that
MiMo-V2.6-Pro, in a long Featherweight session, took to committing on its own
after a few requests, and the plan is to work without automatic commits and
see how models behave over longer sessions than this pilot measures.
