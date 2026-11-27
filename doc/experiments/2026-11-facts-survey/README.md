# What the panel tells the model about its environment

**Status: survey, 2026-11-27.** Before adding facts to Strument's system
prompt, the six harnesses of the fixed panel were read for what their
prompts say about the environment the model works in: the operating system,
the directories, the shell, the date, the sandbox, the network and the
environment commands get. Five were read in source, shallow clones of each
default branch on 2026-11-27:

| harness | commit | where the facts are built |
| --- | --- | --- |
| OpenCode | `36bfb85` | `packages/core/src/instructions/builtins.ts` |
| Pi | `2126569` | `packages/coding-agent/src/core/system-prompt.ts` |
| Codex | `8cd512c` | `codex-rs/core/src/context/world_state/` (`environment.rs`, `permissions.rs`, and their snapshots) |
| Kimi Code | `63338e9` | `packages/agent-core-v2/src/app/agentProfileCatalog/system.md`, `profile-shared.ts`, `session/agentLifecycle/profile/gitContext.ts` |
| DeepSeek Harness | `4b399c39` | `packages/context/time-context`, `packages/shell/shell-env`, `packages/sandbox/sandbox` |

Claude Code is closed. Its environment block was read where it appears, in
the system prompt of the Claude Code session that did this survey.

## What each one says

| fact | OpenCode | Pi | Codex | Kimi Code | DeepSeek Harness | Claude Code |
| --- | --- | --- | --- | --- | --- | --- |
| OS | `process.platform` (`linux`) | — | — | OS kind | — | `linux`, and the kernel's `uname` string as "OS Version" |
| OS distribution or version | — | — | — | — | — | kernel only |
| WSL | — | — | — | — (a Git Bash note on native Windows) | — | — |
| working directory | yes | yes | yes | yes, with a listing of it | not found | yes |
| project root, separately | yes ("Workspace root folder") | — | yes (workspace roots) | — | not found | — |
| shell | — | — | the user's, and PowerShell's version | name and path | as `DSH_SHELL` in each command's environment | the user's |
| date | yes, re-sent when it changes | — | date, timezone, re-sent when changed | in reminders, re-sent when it changes | a timestamp at every step, with the time elapsed since the last | at startup |
| sandbox | a temp directory "approved for external access" | — | mode, writable roots, read-only entries, approval policy | "The environment is not a sandbox" | stated on denial, with an offer to retry wider | — |
| network | — | — | allowed and denied domains | — | not found | — |
| filtered environment | — | — | filters (`shell_environment_policy`), does not say so | — (a note that bash does not guard secret files) | describes the managed `DSH_*` variables it adds | — |
| git | repository yes/no | — | — | up to 20 dirty files and recent commits | not found | repository yes/no |
| updates | diffed and re-sent | sections diffed | diffed world state | reminders | every step | none seen |

"Not found" means a search of the source did not turn it up, not that it is
certainly absent; DeepSeek Harness assembles its prompt from plugins, and the
search covered the context, shell and sandbox packages.

## What it says for Strument

- **The sandbox:** Codex states it in full, Kimi Code states its absence,
  and DeepSeek Harness, like Strument's hint, explains a denial after the
  fact. Strument's sandbox confines writes only, so the Codex shape, reduced
  to the writable roots, fits.
- **The filtered environment:** nobody tells the model. Codex filters and
  is silent, which is what Strument did. There is no precedent, and none
  against it.
- **The operating system:** nobody names the distribution or version, and
  nobody detects WSL. Claude Code's kernel string is the closest. Strument's
  `osinfo` line is ahead of the panel here, not behind it.
- **The root and the working directory:** OpenCode and Codex name both, as
  Strument now does.
- **The date:** four of six keep it current: three re-send it when it
  changes, and DeepSeek Harness stamps every step. The three that re-send it
  append the change as a new message instead of rewriting the prompt, which
  keeps the cached prefix.
  Strument gives the start date and points to the about tool. A note appended
  when the date changes would match them without touching the cache.
- **The shell:** those that name the user's shell run commands in it.
  Strument does not, which is why its line names the built-in shell instead.
