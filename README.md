# Strument

Strument is an AI pair-programming tool for the terminal: a ground-up Go port
of [aider](https://github.com/Aider-AI/aider), trimmed to the improved
essentials. It talks to LLMs through a single OpenAI-compatible client
(OpenRouter dialect), applies SEARCH/REPLACE edits to your files, builds a
ranked repository map with tree-sitter, and — in a git repository —
auto-commits each change so every edit is one `git undo` away.

The port follows a set of frozen specifications in [`spec/`](spec/), produced
by reverse-engineering aider at commit
[`5dc9490`](https://github.com/Aider-AI/aider/tree/5dc9490bb35f9729ef2c95d00a19ccd30c26339c)
(0.86.3.dev). See [`spec/strument-guide.md`](spec/strument-guide.md) for the
plan, scope, and the list of features deliberately deferred or dropped.
`STATUS.md` is the running journal of the port.

## What's different from aider

- **One binary, no Python.** Pure Go, including tree-sitter (no cgo).
- **Starlark configuration.** A single `config.star` replaces layered
  YAML/`.env`/model-database configuration; project-local `.strument.star`
  files are inert until explicitly trusted (direnv-style content-hash gate).
- **One model dialect.** OpenAI-compatible chat completions with OpenRouter
  extensions; no litellm, no function calling, no MCP.
- **Essentials only.** SEARCH/REPLACE (plus fenced and whole-file) edit
  formats, repo map, reflection on failed edits, shell-command suggestions,
  git auto-commit with `/undo`. Architect mode, voice, GUI, analytics,
  summarization, and the other long-tail features are out of scope for v1.

## Credits and license

Strument is derived from [aider](https://github.com/Aider-AI/aider) by Paul
Gauthier and the aider contributors, licensed under the
[Apache License 2.0](LICENSE). Strument carries the same license. The
tree-sitter tag queries under `internal/repomap/queries/` and the prompt
strings are copied verbatim from aider; the rest is a reimplementation
against the specs in `spec/`.

The streaming markdown renderer (`internal/render/`) is a port of
[streaming-markdown](https://github.com/thetarnav/streaming-markdown) by
Damian Tarnawski, MIT licensed.
