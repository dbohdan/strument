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

The short version of the philosophy: Strument is a **propose/direct-apply
tool, not an autonomous agent** — the model responds, the harness acts, the
human drives the next turn. The **code is the source of truth** (the old port
specs are retired; don't reintroduce a spec the code must conform to). Prompts
are **calm and specific**, written as you'd write for a competent colleague.

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

## Conventions

- **Commits**: conventional-commit style (`feat:`, `fix:`, `refactor:`,
  `docs:`, `test:`, …), imperative mood, one logical change per commit.
- **Comments**: match the surrounding density and idiom; explain *why*, not
  *what*. Describe divergences from aider and the reasons for them.
- **Verify before you commit**: build, `go test ./...`, and `task lint` all
  green. For anything with a runtime surface, exercise it, don't just test it.
- **Never commit secrets** — API keys go in the environment
  (`OPENROUTER_API_KEY`), never in files, docs, or commits.
