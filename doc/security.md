# Security

This document describes what Strument's sandbox protects, what it does not, and
why. Start with [Integrity, not
confidentiality](#integrity-not-confidentiality): the sandbox restricts
filesystem writes, but it does not restrict reads or network access.


## The threat model

Strument is a pair-programming tool with a human in the loop. Shell commands
requested by the model normally require confirmation, except for configured
checks and commands covered by an existing permission grant. File-tool edits
are shown as diffs and can be undone. The sandbox is intended to limit damage
when those controls are not enough:

- **Mistakes.** A model asked to clean a build directory writes `rm -rf $DIR/`
  with `DIR` unset. You approved the command, but it did not do what you
  expected.
- **Prompt injection.** A README, an issue, a scraped page, or a dependency's
  test output tells the model to do something you did not ask for. These
  instructions arrive in material the model needs to read for the task.
- **The yes-reflex.** Repeated confirmation prompts can become automatic
  keystrokes rather than decisions. If refusing a command means another
  exchange with the model, accepting it becomes the easier habit.

It is **not** intended to contain an agent deliberately attempting to escape
the sandbox over many turns. Such an agent reads this document, notices that
reads are unrestricted, notices that `/run` exists and runs unfiltered on the
user's say-so, and works from there. Defending against it would require a
different design: restricted reads, restricted network access, and an approval
process that cannot be worn down. Those restrictions would require
compatibility trade-offs and would still need their own threat model.

This threat model gives maintainers a basis for evaluating changes: does a
proposed control reduce damage from mistakes or prompt injection during
supervised use?


## Integrity, not confidentiality

The sandbox protects **integrity, not confidentiality**.

Filesystem writes are confined to the paths allowed by the sandbox policy.
Reads are not. Files outside the writable set — including, in a typical
configuration, your dotfiles, other repositories, SSH keys, and files under
`/etc` — remain protected from direct filesystem writes, but commands can still
read them wherever your normal permissions allow.

This distinction is deliberate. Confining writes limits filesystem damage from
an approved command to the paths in the policy. A bad approval can still have
effects outside the file tools' undo coverage, including changes to writable
caches, files under `/tmp`, or remote systems reached over the network.

Reads are unrestricted to preserve compatibility with ordinary development
tools. Compilers read standard libraries, linters read configuration outside
the project, and tests may use external fixtures. A restrictive read policy
would need exceptions for these workflows. Under the current policy, any secret
readable by a command is available to that command. Strument itself also holds
credentials and connects to the model provider. Model-run commands normally
receive only an allowlisted set of environment variables, but they can still
send readable data over the network. The user's `/run` command inherits the
full environment.

**If a secret is readable by you, treat it as readable by the model.** If that
is not acceptable for some file, the sandbox is not the control you need; use
an execution environment that cannot read the file, such as a separate account
or a container without access to it.


## What is confined

Strument applies a [Landlock](https://landlock.io/) ruleset to **its own
process** at startup, before any model interaction. Landlock is inherited
across `fork`/`exec` and cannot be undone, so everything the session later
spawns is confined by the same policy — the `bash` tool, `check` commands, the
`scraper` command, and all their descendant processes.

The policy has two main rules:

- **Read and execute everywhere.** The `RODirs("/")` rule used here grants read
  and execute access under `/`. That is why `/usr/bin`, `~/.local/bin`,
  `~/go/bin`, `~/.cargo/bin`, and other executables on your `PATH` remain
  usable, subject to normal filesystem permissions, without enumerating
  individual files or keeping the list current.
- **Write only under a derived list of paths** — the project, the session's state
  directory, a temporary directory, the detected toolchain cache directories,
  and whatever `sandbox_write` adds.

`/sandbox` in the REPL prints the effective list for the current session.
[`doc/config.md`](config.md#language-support) documents where the cache paths
come from, one ecosystem at a time.

The process-wide policy has three consequences:

- **`/run` is confined too.** Landlock is monotonic — there is no call that
  removes a ruleset — so a command you typed yourself runs under the same
  filesystem policy as one the model caused. This also limits commands you type
  yourself. `/run` keeps its other privileges: it inherits your full
  environment and is exempt from `shell_timeout`, but it cannot write outside
  the list.
- **The network is not restricted.** Landlock can restrict TCP by port, but the
  harness itself needs to reach the model provider from inside the same
  process. A port policy would have to admit 443, which is also the port a
  model-caused command would normally use.
- **The sandbox imposes no CPU, memory, or process limits.** `shell_timeout`
  limits how long model-run commands may run; it does not limit their resource
  consumption while they run.


## Exceptions and remaining risks

The writable paths and execution policies leave several risks. The sections
below explain why these exceptions exist and what they permit.

### The sandbox permits writes to `.git`; file tools refuse access

Strument needs write access to `.git` to commit changes and implement `/undo`.
Because the sandbox applies to Strument itself and all its subprocesses,
commands it launches have the same access.

Landlock rules are additive: a read-only rule for a directory inside a writable
root does not revoke write access. This behavior was verified on a real kernel.
Protecting `.git/hooks` from subprocesses while retaining Strument's own access
would require a different sandbox arrangement, such as per-command confinement.

The `read` and edit tools refuse paths into `.git`. The read tool already
enforced this restriction, but the edit tools did not, and that was a bug
rather than a choice — `git check-ignore` reports `.git/config` as not ignored,
so nothing stopped a `write` call to it. Both read and edit operations now use
the same check (`workspace.UnderGitDir`), which checks for `.git` path
components at any depth, case-insensitively, with trailing dots and spaces
trimmed, because `.GIT/config` opens the real file on APFS and NTFS. Pinning a
file does not bypass this check. The model can grow the pinned-file list
through the edit path, so membership in that list cannot authorize access to
`.git`.

The path check applies after normalization. An absolute path is resolved
against the root, and the git-directory check runs on the resolved form as well
as the supplied path. Thus `/proj/.git/config` is refused for the same reason
`.git/config` is, and the check applies to both relative and absolute paths.
Landlock separately checks filesystem access at the kernel level.

Code the model writes into the project normally runs under the sandbox and
environment allowlist, so it does not receive `OPENROUTER_API_KEY`. However,
settings in `.git/config` — such as `core.fsmonitor`, `core.pager`,
`core.sshCommand`, or aliases — can cause Strument's own Git invocations to
execute code. Those invocations retain the full environment. Modifying Git
configuration can therefore give code access to credentials withheld from
ordinary model-run commands. Because `.git` is untracked, these changes do not
appear in `git show`, and a turn may report “nothing to commit.”

Remaining risk: the file-tool restriction blocks direct edits through those
tools, but it does not prevent other commands from writing `.git`. A `bash`
command can still write `.git/config` behind a confirmation prompt, and so can
a test that `check_auto` runs without one if the model wrote the test. Closing
that route would require a per-command sandbox or a filtered environment for
Strument's Git process. The current implementation keeps the full environment
for Git so the user's identity, credential helpers, and signing setup continue
to work.

### Toolchain caches are writable

Strument grants `~/.cache`, `GOMODCACHE`, `~/.cargo/registry`, `~/.m2`, and
other toolchain cache directories.

These directories are writable because project checks use them. For example,
`go test` writes to `~/.cache/go-build`. A sandbox that breaks the project's
checks is likely to be disabled, leaving nothing to limit mistakes or prompt
injection. The widening is deliberate, but it exposes cache contents to
modification.

Cache poisoning can affect later builds, including builds of other projects or
processes that share a cache; it is not limited to the model's next build in
the current session. Executable directories are excluded: where a toolchain
keeps a cache and a `bin/` directory side by side, the contents are granted one
subdirectory at a time, excluding anything on your `PATH`. `~/go/pkg` is
writable, while `~/go/bin` is not.

Only existing cache directories can be granted access. If a toolchain has never
run on the machine, its cache directory may not exist, and its first run inside
the sandbox may fail when creating it. Create the directory before starting the
session, or add its existing path to `sandbox_write`.

### `/tmp` is writable even when `TMPDIR` points elsewhere

Enough tools have `/tmp` compiled in that denying it breaks builds for little
benefit. `/tmp` is mode 1777: every local process can create files there, while
the sticky bit limits removal and renaming of files owned by other users. A
process running as you can still modify files you own there, so the sandbox
does not provide integrity protection for a user's files under `/tmp`.


## What Landlock does not confine: the display server

Landlock governs *filesystem* access rights. Connecting to a Unix domain socket
is not one of them — a path grant is irrelevant either way — so **a
model-caused command can reach your display server, sandbox or no sandbox.**

A live probe confirmed this:

On a Wayland laptop, `socat - UNIX-CONNECT:$XDG_RUNTIME_DIR/wayland-0` exited
with status 0 from inside the sandbox, confirming that it could connect. This
probe did not test any Wayland capabilities: a client must still speak the
protocol, and sending `test` does not perform a useful operation.

The environment allowlist affects how commands discover display servers, but it
is not a display-access control:

- **X11 variables are absent from the model-run environment.** `DISPLAY` and
  `XAUTHORITY` are not in the default allowlist, so ordinary X11 discovery does
  not work for a model-run command. This reduces the common path but does not
  make a known X11 endpoint inaccessible. X11's XTEST extension lets any
  authenticated client synthesize input into any window, which can let it
  control applications outside the sandbox.
- **Wayland discovery is available through the same allowlist.**
  `XDG_RUNTIME_DIR` is included because tools resolve runtime and cache paths
  from the XDG variables. It also identifies the compositor's socket, so the
  legitimate use makes that socket discoverable.

Wayland clients cannot generally snoop other clients' input or contents by
design. A client that connects can normally open a window and use the
compositor's available protocols, but it does not have the broad
input-injection and screen-observation capabilities of an authenticated X11
client.

Removing `XDG_RUNTIME_DIR` from the allowlist would close the usual discovery
path, at the cost of breaking checks that need a session bus. Strument
currently retains `XDG_RUNTIME_DIR` for compatibility. The filesystem sandbox
does not isolate commands from the display server.

This limitation came to attention when a model on another user's machine found
`xdotool` while trying to complete a task that required GUI confirmation. The
behavior was not adversarial; it showed that an ordinary task could lead the
model to use capabilities outside the filesystem sandbox.


## When the sandbox is unavailable or disabled

If `sandbox = "landlock"` is configured but the kernel lacks Landlock support,
Strument does not silently fall back to unsandboxed execution. Reading,
file-tool editing, and Git commits still work, but model-run shell commands,
checks, and scraper commands are refused with an error naming the setting. The
user's `/run` command remains available.

A warning alone would allow execution without the requested protection and
would be easy to overlook.

`sandbox = ""` turns confinement off. That is the pre-sandbox behavior and a
legitimate choice — it is the default off Linux, because Landlock is a Linux
LSM and there is nothing to fall back to. What you give up is filesystem write
restrictions: with it off, an approved command has your full authority. Without
confinement, commands run with your normal filesystem permissions. Confirmation
prompts still apply, with the exceptions for configured checks and existing
permission grants.


## Fetching a page

`webfetch` asks before it fetches an origin you have not listed, and the prompt
shows the whole URL. Approval controls prompting, not network access or the
trustworthiness of the response.

**`webfetch_allow` is not a network boundary.** It says which fetches skip the
prompt. It cannot say which hosts are reachable, because `bash` can `curl`
anywhere and Landlock confines the filesystem rather than the network.

**A fetched page is untrusted input.** Whatever comes back enters the model's
context and can carry instructions. That is why approval is scoped to a single
origin rather than to all pages fetched during a turn: a turn-wide grant would
also cover pages from unrelated origins the user had not reviewed. It is also
why the prompt shows the full URL. URL query parameters can contain the part
that distinguishes one resource from another, or instructions a reviewer should
see.

## The environment allowlist

Model-run commands receive an allowlisted subset of your environment rather
than inheriting it in full; see [`env_allow`](config.md#env_allow).

This is not part of the sandbox and does not depend on it. The sandbox
restricts filesystem writes; the allowlist controls which environment variables
a command receives. It is also, by accident, the main thing keeping a
model-caused command from ordinary X11 discovery — see [what Landlock does not
confine](#what-landlock-does-not-confine-the-display-server).

`/run` is exempt from the allowlist because the user typed the command. It is
not exempt from the sandbox because the allowlist is a policy Strument chooses
to apply, while Landlock is a property of the process.

Strument's *own* `git` invocations are not filtered: they inherit the whole
environment, including `OPENROUTER_API_KEY`, since the current implementation
keeps the user's identity, credential helpers, and signing setup available to
git. Because these Git processes receive credentials, substituting a different
`git` executable could expose them. The `git` on `PATH` is resolved once at
startup, before any config is read. Later changes to `PATH` through `env_set`
affect model-run commands, but Strument continues to use the Git executable
resolved at startup.


## How this was verified

During development, enforcement tests were skipped on kernels without Landlock.
An overall passing test summary therefore did not establish that enforcement
worked. It was checked on a kernel that has it (ABI 8) before the feature
shipped:
[`doc/experiments/2026-08-landlock-live/README.md`](experiments/2026-08-landlock-live/README.md)
records the run. The run verified three behaviors described on this page:

- read-only `/` still permits execution;
- a cross-directory rename is denied as EXDEV rather than EACCES; and
- a nested rule cannot reduce rights.

The last result is why `.git/hooks` remains a documented exception rather than
a fixed one.

`script/sandbox-trial.py` re-runs that trial. `--sandbox ""` is its control:
with confinement off, ordinary-work checks should pass and denial checks should
fail. If the denial checks do not change outcome when confinement is disabled,
the trial has not isolated the sandbox's effect.

## Reporting a problem

Strument is pre-1.0 and developed in the open. Open an issue; if you would
rather not describe it publicly, say so in the issue and leave the details out.
