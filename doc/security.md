# Security

This document says what Strument's sandbox protects, what it does not, and why
the line falls where it does. Read the second section even if you skip the
rest: the most common mistake with a sandbox is believing it bought something
it did not.


## The threat model

Strument is a pair-programming tool with a human in the loop. Every turn ends
in front of you, every shell command is confirmed, and every edit is a diff you
can see and `/undo`. The sandbox is built for the failures that survive that
arrangement:

- **Mistakes.** A model asked to clean a build directory writes `rm -rf $DIR/`
  with `DIR` unset. You approved the command; it did not do what either of you
  read it as doing.
- **Prompt injection.** A README, an issue, a scraped page, or a dependency's
  test output tells the model to do something you did not ask for. The
  instruction arrives inside content you had every reason to let it read.
- **The yes-reflex.** Confirmation prompts are a real gate exactly once. By the
  fiftieth one they are a keystroke, and the prompt whose "no" costs a round
  trip is the one that trains the reflex hardest. Strument's own comment on
  `allowedToEdit` has made this argument against itself for a while.

It is **not** built for a misaligned agent working patiently over hundreds of
turns to escape. That adversary reads the policy, notices reads are open,
notices `/run` exists, and works from there. Defending against it would mean a
different tool: no open reads, no shared network, an approval surface that
cannot be worn down. Strument would be worse at its job and still not be a
jail.

Saying which adversary a control is for is not a disclaimer. It is what makes
the control checkable — you can ask whether a change helps against mistakes and
injection, and get an answer.


## Integrity, not confidentiality

The sandbox protects **integrity, not confidentiality**.

Writes are confined to a known set of paths. Reads are not confined at all.
With the sandbox on, a command the model runs cannot modify your home
directory, your dotfiles, your other repositories, your SSH keys, or anything
in `/etc` — and it can read every one of them.

That is the whole guarantee, and both halves are deliberate.

Confining writes is what makes the review loop safe to lean on. A bad approval
costs you a `git diff` and an `/undo`, not an afternoon of finding out what
else changed. That is the thing being bought: pressing Enter without much
worry.

Confining reads is not attempted because it cannot be done here without
breaking the tool. A compiler reads the standard library, a linter reads its
own config, a test reads a fixture three directories up; a read policy tight
enough to matter would deny one of those every session, and a read policy loose
enough to work would deny nothing an attacker wants. And the process holds an
API key and an open connection to a model provider, so a command that can run
at all can exfiltrate — no filesystem rule changes that.

**If a secret is readable by you, treat it as readable by the model.** If that
is not acceptable for some file, the sandbox is not the control you need;
filesystem permissions, a separate account, or a container is.


## What is confined

Strument applies a [Landlock](https://landlock.io/) ruleset to **its own
process**, once, at startup, before any model interaction. Landlock is
inherited across `fork`/`exec` and cannot be undone, so everything the session
later spawns is confined by the same policy — the `bash` tool, `check`
commands, the `scraper` command, and every grandchild of those.

The policy is two lines wide:

- **Read and execute everywhere.** One rule covering `/`. Landlock's read
  right on a directory permits executing files in it, so `/usr/bin`,
  `~/.local/bin`, `~/go/bin`, `~/.cargo/bin` and everything else on your `PATH`
  works with nothing enumerated and nothing to keep current.
- **Write only under a derived list of paths** — the project, the session's
  state directory, a temporary directory, this machine's toolchain caches, and
  whatever `sandbox_write` adds.

`/sandbox` in the REPL prints the effective list for the current session.
[`doc/config.md`](config.md#language-support) documents where the cache paths
come from, one ecosystem at a time.

Three consequences of confining the process rather than each command, all of
them accepted rather than hidden:

- **`/run` is confined too.** Landlock is monotonic — there is no call that
  removes a ruleset — so a command you typed yourself runs under the same
  policy as one the model caused. This is the clearest cost of the design.
  `/run` keeps its other privileges (it inherits your full environment, and it
  is exempt from `shell_timeout`), but it cannot write outside the list.
- **The network is not restricted.** Landlock can restrict TCP by port, but the
  harness itself needs to reach the model provider from inside the same
  process, so a port policy would have to admit 443 — which is the port
  anything would use anyway.
- **Nothing enforces limits on CPU, memory, or processes.** A command that
  spins or forks is bounded by `shell_timeout`, not by the sandbox.


## Deliberate holes

Three places where the policy is looser than it could be. Each is a choice with
a reason, and a reason is something you can disagree with.

### `.git` is writable

Codex CLI grants its writable root and then makes `.git/` read-only inside it.
Strument cannot: it commits your work at the end of a turn, and `/undo` reaches
through git.

The follow-on is that `.git/hooks` is writable, and a `post-commit` hook is code
execution on the next commit. Landlock's rules are purely additive — a nested
read-only rule inside a writable root grants reading and revokes nothing, which
we verified on a real kernel rather than assumed — so there is no way to carve
the hooks directory back out. It would need a per-command sandbox, which is the
one place this design is weaker than that one.

The reason to accept it is the user's, and it is sharper than the alternatives:
a model that wants to attack you can already hide an exploit in the code it is
writing. The hooks directory adds a path, not a capability, and every path into
this threat model runs through a diff you are reading.

### Toolchain caches are writable

Codex CLI and Claude Code grant the working directory and a temp directory, and
leave you to discover the rest. Strument grants `~/.cache`, `GOMODCACHE`,
`~/.cargo/registry`, `~/.m2` and a dozen more.

That is a real widening and it is chosen with eyes open. Strument's core loop is
running your project's checks, and the first `go test` of a session writes
`~/.cache/go-build`. A sandbox whose first act is to break the build is a
sandbox you switch off within the hour, and a sandbox that is off protects
nothing.

What the widening exposes is caches — content a model can poison only to
sabotage its own later builds. Executable directories are excluded from it
specifically: where a toolchain keeps a cache and a `bin/` side by side, the
contents are granted one subdirectory at a time, minus anything on your `PATH`.
`~/go/pkg` is writable, `~/go/bin` is not.

The cost of that precision: a toolchain that has never run on this machine has
no cache directory yet, so there is nothing to grant and its first run inside
the sandbox fails on a write. Name the path in `sandbox_write` and it works
from then on.

### `/tmp` is writable even when `TMPDIR` points elsewhere

Enough tools have `/tmp` compiled in that denying it breaks builds for nothing.
`/tmp` is mode 1777 — every process on the machine can already write there —
and the integrity this policy protects is your own files.


## When there is no sandbox

`sandbox = "landlock"` on a kernel without Landlock does not proceed
unsandboxed and does not merely warn. Strument starts, reading and editing and
committing all work, and **everything the model can cause to execute refuses**
with one line naming the setting. `/run` still works, because you typed it.

The refused-versus-warned distinction is the point. A mode that says "no
sandbox today" and runs the command anyway trains you to skip the line, and the
one session where it mattered looks exactly like the fifty where it did not.

`sandbox = ""` turns confinement off. That is the pre-sandbox behavior and a
legitimate choice — it is the default off Linux, because Landlock is a Linux
LSM and there is nothing to fall back to. What you give up is the whole
integrity guarantee: with it off, an approved command has your full authority,
and the confirmation prompt is the only thing between a mistake and your home
directory.


## The environment, which is a separate control

Commands the model causes to run do not inherit your environment. They get an
allowlist — see [`env_allow`](config.md#env_allow) — so a model-run `env`, or a
test that prints its environment on failure, cannot carry `OPENROUTER_API_KEY`
into a tool result and from there into the model's context and the transcript.

This is not part of the sandbox and does not depend on it. It answers a
different question: the sandbox is about what a command can *change*, and the
allowlist is about what it *carries*. `/run` is exempt from the allowlist for
the same reason it is not exempt from the sandbox — the allowlist is a policy
Strument chooses to apply, and Landlock is a property of the process.


## How this was verified

The policy was developed on kernels without Landlock, where the enforcement
tests skip — and a skip reads as a pass in a summary line. It was checked on a
kernel that has it (ABI 8) before the feature shipped:
[`doc/experiments/2026-08-landlock-live.md`](experiments/2026-08-landlock-live.md)
records the run. Three claims on this page rest on it rather than on reading:
that read-only `/` still permits execution, that a cross-directory rename is
denied as EXDEV rather than EACCES, and that a nested rule cannot reduce
rights — which is why `.git/hooks` is a documented hole rather than a fixed
one.

`script/sandbox-trial.py` re-runs that trial. `--sandbox ""` is its control,
and the point of it: with confinement off every ordinary-work check should go
green and every denial check should go red. A run where those do not flip is
measuring something other than the sandbox.

## Reporting a problem

Strument is pre-1.0 and developed in the open. Open an issue; if you would
rather not describe it publicly, say so in the issue and leave the details out.
