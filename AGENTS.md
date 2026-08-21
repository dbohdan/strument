# Working on Strument

Strument is an AI pair-programming CLI for the terminal, written in Go — a
descendant of [aider](https://github.com/Aider-AI/aider) that now follows its
own direction. If you are an agent or a new contributor, start here.

## Read this first

- **[`doc/README.md`](doc/README.md)** — the developer overview: the project's
  **Philosophy** (read it before changing anything substantive), the codebase
  map, the coder's ports, and how testing works.
- **[`README.md`](README.md)** — user-facing: install, configuration, and what
  differs from aider.

The short version of the philosophy: Strument is a **reviewable loop, not an
autonomous one** — the model calls tools and keeps working within a turn,
because that is the protocol tool calls carry; what does not move is the review
surface, and the turn boundary stays the human's. The **code is the source of
truth** (the old port specs are retired; don't reintroduce a spec the code must
conform to). Prompts are **calm and specific**, written as you'd write for a
competent colleague. Read the Philosophy section of `doc/README.md` for the
reasoning, including why this reverses the position the project started from.

## Build, test, lint

Go 1.26+, no cgo, no C toolchain. Everything runs offline without API keys.

```sh
go build ./...        # or: task build
go test ./...         # the full suite; no network, no sockets
go vet ./...
task lint             # golangci-lint — keep it at 0 issues
task format           # gofmt/golangci-lint fmt; run before committing
```

`task setup:reference` clones aider at the pinned commit into a gitignored
`reference/` for comparison; the build never needs it.

### Installing the tools

`task` and `golangci-lint` are not vendored. Install them with the *project's*
Go toolchain, pinning golangci-lint to the line CI uses
(`.github/workflows/ci.yml`):

```sh
GOTOOLCHAIN=go1.26.0 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
GOTOOLCHAIN=go1.26.0 go install github.com/go-task/task/v3/cmd/task@v3.52.0
```

Both land in `$(go env GOPATH)/bin`, which must come before any older copy on
`PATH` (`GOBIN=/usr/local/bin` if you'd rather replace one in place).

The explicit `GOTOOLCHAIN` is the part that is easy to get wrong, and a prebuilt
release binary hits the same wall. golangci-lint refuses to load a module whose
Go version is newer than the Go it was itself built with — here, "the Go
language version (go1.25) used to build golangci-lint is lower than the targeted
Go version (1.26)". Building from source does not fix that on its own: its
`go.mod` pins Go to *latest-1* as a matter of policy, so under the default
`GOTOOLCHAIN=auto` the install quietly selects a 1.25 toolchain and produces a
binary that still refuses this repo. Naming the toolchain forces a 1.26 build.
Note that `go version` can report 1.26 while `/usr/local/go` is older — `auto`
switches per module, so that reading tells you about Strument, not about what
will build your tools.

## Comparing the terminal UI to aider

Both Strument and aider need a real TTY (readline / prompt_toolkit), so piping
their output or reading their source is not enough to judge how a screen
actually looks. When a UI detail should match aider, **observe both, don't
reason from source** — the byte you capture beats the byte you predicted.

Drive each tool through a pty with `pexpect` (`pip install pexpect`; answer the
`\x1b[6n` cursor query with `\x1b[1;1R` or readline blocks), pin the width with
`dimensions=(rows, cols)`, capture raw bytes, strip ANSI, and diff. Answer that
query **every time it appears**, not once at startup: readline re-queries on
each redraw, so a one-shot reply gets exactly one command out and then silence
— and silence from a process that is still alive and still accepting input
looks like the command was wrong rather than like a wedged handshake. Scan each
chunk you read and reply per occurrence. For startup
chrome (banner, the per-prompt rule, the file list) no API key is needed —
launch, let it reach the prompt, send `/exit`. For streamed answers and
reasoning, run against a live model with `OPENROUTER_API_KEY` in the
environment (never in a file). aider installs in its own venv
(`python -m venv … && pip install aider-chat`).

A worked example of why this matters: aider draws **two different horizontal
rules**. The per-prompt separator is Rich's `console.rule`, a solid `─`
(U+2500); every markdown rule in an answer — including the `--------------`
reasoning-tag headers — is Rich's Markdown thematic break, which is
`Rule(characters="-")`, a dashed hyphen. Reading `reasoning_tags.py` alone
suggests dashes everywhere; rendering aider's own `NoInsetMarkdown` (or just
`rich.markdown`) shows the split. Strument mirrors it: `renderPromptHeader`
uses `─`, the ANSI renderer's `Rule` and the THINKING/ANSWER headers use `-`.

## What to live-test, and what to isolate

The discipline above generalizes past the UI. Every part of Strument that meets
the outside world — a model's wire behavior, a real page's HTML, a terminal's
redraw, a proxy's egress — tends to differ from what the source predicts, and
the difference stays invisible until observed. A live pass against a real model
(`OPENROUTER_API_KEY` in the environment, never a file) has caught bugs no unit
test would: a reversed tool-call field order, surfaced only because a second
model ordered it differently; a code-fence marker leaking into the stream;
reasoning that wouldn't turn off; a cache TTL that wasn't honored. Test with
more than one model — providers disagree.

So push the external→deterministic seam as far upstream as you can: grow the
pure, *seizable* core (its output fully determined by its inputs), unit-test
that cheaply and exhaustively, and spend the slow live pass only on the real I/O
boundary. This is functional-core / imperative-shell read as a map of where
in-head reasoning can be trusted and where you have to go and look. One trap
worth knowing: when you drive readline through a pty, give it a real terminal
size (an Options `GetSize` and the pty winsize), or the completion grid and the
rules silently no-op at zero width.

### Which model to reach for

**Default to MiMo-V2.5 (`xiaomi/mimo-v2.5`) wherever you would otherwise reach
for a frontier model by instinct.** It benchmarks between Claude Haiku 4.5 and
Sonnet 4.6, so it is an adequate stand-in for "a capable model", and on
OpenRouter it costs $0.14/$0.28 per million against Haiku 4.5's $1.00/$5.00 —
roughly seven times cheaper on input and eighteen on output. It is also *fast*,
which matters more than it looks: in a sweep, wall-clock is what caps the sample
you can afford to collect in one sitting.

The reason this is a rule and not a preference: in
[`doc/experiments/2026-08-prompt-scope.md`](doc/experiments/2026-08-prompt-scope.md)
Haiku was $3.93 of a $4.14 total, 95% of the spend for one stratum of four, and
the sample size ended up set by the most expensive model rather than by the
question being asked. Price the strata *before* designing the arms.

This narrows the default, not the coverage. "Test with more than one model —
providers disagree" still holds, and where the point *is* cross-provider
behavior — tool-call streaming, field order, fragment contiguity — one model of
any price answers nothing. Reach past the default deliberately: to check
something specific to a vendor, or when a result hinges on capability and you
want a frontier model to confirm it.

### Before you run one: the handbook

[`doc/experimenting.md`](doc/experimenting.md) collects what has actually gone
wrong running these — equipment faults, not statistics. Read it while designing
the arms, not while debugging the result. The one-line version: a scorer broken
by Strument's own escape sequences turned a real effect (10/12 vs 5/12) into a
clean null (5/12 vs 4/12, p=1.0), and a clean null is the shape that gets a bad
change shipped.

### Comparing two prompts (or two anything) against live models

A live A/B is a different discipline from a live pass, and the trap is not the
one you expect. **Randomize the order of the arms.** Running every baseline and
then every treatment confounds the arm with the time it ran, and providers drift
across that window: in
[`doc/experiments/2026-08-prompt-scope.md`](doc/experiments/2026-08-prompt-scope.md)
the unrandomized design produced p=0.0009 on a prompt change, and shuffling the
order — nothing else — moved the baseline from 65% to 84% while the treatment
arm stayed put, taking the same comparison to p=0.15. In another cell the sign
flipped outright. Randomizing was worth more than tripling the sample.

Three more that cost real time there. Read individual transcripts before
believing an aggregate: a provider returning `Empty response received from LLM`
and a model emitting a tool call as inline text are indistinguishable in a
summary and mean opposite things. Choose metrics that are counts rather than
judgments, so you are not both author and judge. And report the counter-metric
— the thing your change might break — as prominently as the effect you want,
because that is what makes a result safe to act on.

## Conventions

- **Commits**: conventional-commit style (`feat:`, `fix:`, `refactor:`,
  `docs:`, `test:`, …), imperative mood, one logical change per commit.
- **Comments**: match the surrounding density and idiom; explain *why*, not
  *what*. Describe divergences from aider and the reasons for them.
- **Verify before you commit**: build, `go test ./...`, and `task lint` all
  green. For anything with a runtime surface, exercise it, don't just test it.
- **Never commit secrets** — API keys go in the environment
  (`OPENROUTER_API_KEY`), never in files, docs, or commits.
- **Model-run commands get a filtered environment.** Commands the model
  caused — the `bash` tool, `check`, the `scraper` command — run under the
  allowlist in `internal/coder/envallow.go` (see `doc/config.md`, `env_allow`);
  only `/run` inherits everything, because the user typed it. When adding a
  subprocess path the model can cause, pass `FilterEnv(nil, c.EnvAllow)`
  output. Watch the mvdan.cc/sh trap: `interp.Env(ListEnviron(nil...))` means
  an *empty* environment, not inherit — omit the option to inherit.
