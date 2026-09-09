# What Strument says

This is the house style for text people read: startup notices, messages during a
turn, `--help`, `/help`, and command output.

Model-facing text—tool results, tool descriptions, and refusals returned as tool
output—is outside this guide’s scope. It needs consistent formatting and is
evaluated through live trials; `internal/coder/tools.go` alone contains about
150 such strings. Treat changes to those strings as prompt changes, with trials
documented in `doc/experiments/`.

An audit found that similar messages followed different conventions across the
codebase. This guide records the conventions to use consistently.

## The channels

Routing and formatting differ between messages printed inside a session and
those printed outside one.

**Inside a session** everything goes to stdout and is told apart by colour.
`internal/repl/output.go` implements one method per voice:

| method | voice | how it looks |
| --- | --- | --- |
| `Printf` | REPL messages, such as the banner and `/add` acknowledgements | plain |
| `Toolf` | tool status and outcomes | `Theme.Tool`, muted |
| `Warningf` | warnings that do not stop the turn | `Theme.Warning` |
| `Errorf` | something failed | `Theme.Error` |
| `ToolBlock`, `Link` | a multi-line payload; a URL | see their doc comments |

**Outside a session** there is no colour to lean on, so:

| | where | how it looks |
| --- | --- | --- |
| **Notices** from `cmd/strument` | stderr | prefixed `strument: ` |
| **Data** a command was asked to produce | stdout | unadorned |
| **Errors** returned up to Kong | stderr | Go convention; Kong prefixes them with the binary name |

Inside a session, colour distinguishes the message types, so no `strument:`
prefix is needed. Outside a session, notices use that prefix. Kong adds it to
returned errors.

Outside a session, requested output goes to stdout and commentary goes to
stderr. Output from `strument config models`, `strument history`, and
`strument project list` is data; an untrusted-config warning is commentary,
even when it appears during the same command. Inside a session, all output goes
to stdout.

**One prefix per message, not per line.** A message that continues indents its
continuation two spaces and carries no second prefix:

```
strument: ignoring 1 untrusted project skill: release-notes
  Run `strument trust` in this directory to allow it.
```

## Quoting

Use the quoting convention appropriate to the item being shown.

- **Backticks for commands, flags, and config keys**: `` `strument trust` ``,
  `` `check_auto` ``, `` `--no-shell` ``.
  Some exceptions are allowed based on readability.
  The following aren't quoted:
    - REPL commands given without arguments (for example, `/add`, `/exit`).
    - REPL commands in `/help`
    - Commands intended to be copied to the end of the line
      (e.g., `Merge saved state: strument project adopt ~/src/proj`)
- **Double quotes for values shown as configuration source.** Preserve quotes
  required by the configuration syntax: `"full"`, `"off"`, and
  `{"test": ["go", "test"]}`.
- **`%q` for externally supplied strings**, such as a user-entered model alias
  or a provider response. `%q` makes empty strings and trailing whitespace
  visible.
- **Bare for paths.** A path is visually distinct already, and quoting it makes
  it worse to copy out of a terminal.

## Periods

`Read poll/poll.go (12 lines)` is a label. It takes no period.

`The turn left the files as they were; nothing to commit.` is a sentence. It
takes one.

Apply this distinction within each channel: `Toolf`, for example, prints both
labels and sentences. Help entries are sentences, so both help systems — Kong's
`help:` tags and the slash-command table in `internal/repl/commands.go` — end
with a period. This prose rule does not override the existing Go-convention
rule for returned errors.

## Counting

Never `(s)`. Use `render.Plural` (`internal/render/count.go`), which both
`cmd/strument` and the REPL reach, and use the appropriate singular or plural
form:

```go
render.Plural(n, "turn", "turns")       // "1 turn", "12 turns"
render.PluralWord(n, "origin", "origins") // when the count goes elsewhere
```

In prose, prefer “no turns” to “0 turns”. Keep the numeral when reporting a
measurement.

## State the problem, its impact, and the next step

`internal/config/envset.go` provides an example:

> `env_set TZ "Europe/Kiev" is not a zone name (unknown time zone). Commands
> and git still get TZ, but Strument's own dates stay in this machine's zone.
> Use a database name like Europe/Kyiv or UTC.`

The message explains what failed, what still works, and what the user can do.
Include the impact when it may not be obvious: whether `/undo` still covers the
edits, for example, or whether history remains on disk.

When a failure needs explanation or action, do not stop at naming it. `model
"mimo" not found on openrouter` does not explain whether the model is absent
from the provider's catalog or how the user should proceed.

Report the **consequence**, not the mechanism, when they differ. `could not save
the undo record` names an internal artifact; what the reader needs is that
`/undo` will not work for this turn.

Use an em dash to introduce a qualification: `Not committing config.star —
outside the repository; /undo still covers it.`

## Tense in the harness's voice

Use past tense for completed actions and a present participle for actions in
progress. The verb should tell the reader whether the action has finished.

- `Read poll.go (12 lines)`, `Listed . (24 entries)`, `Matched foo against bar`
- `Running the automatic checks.`, `Committing config.star before applying edits.`

## The `‹…›` markers

The `‹…›` markers have two forms.

**A bracketed aside** wraps model-authored text over several lines and is closed
by `‹/›`: `‹thinking›` and `‹run_code›`. These markers resemble tags but use `‹`
and `›` rather than `<` and `>`. This keeps them distinct from the
`<tag>…</tag>` syntax that `stripReasoning` removes from model output.

**A labelled line** prefixes one line of model-supplied text and has no closer:
`‹check›`, `‹shell›`, `‹question›`, `‹webfetch›`, `‹websearch›`, `‹skill›`.
Multiline blocks use `‹/›`; single-line blocks need no closing marker.

The README's interruption example uses `‹question›` before “You stopped the
model. What now?”, which appears to be harness-authored. The absolute
provenance rule needs verification; do not infer a new exception from the
example.

Both forms introduce model-supplied text, not Strument's own prose.

## One phrasing per concept

Reuse a shared constant or helper when several messages give the same
instruction. Project-trust instructions use `config.TrustAdviceFor`
(`internal/config/trust.go`).

Keep variants that convey a real difference. `TrustAdviceFor` takes an
`inSession` flag because a running session needs `/reload` after the user trusts
the project from another window. The in-session message must include that step.
