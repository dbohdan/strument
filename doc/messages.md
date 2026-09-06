# What Strument says

The house style for the text a **person** reads: startup notices, the harness's
own voice during a turn, `--help`, `/help`, and what a command prints when it is
done.

Strings the **model** reads — tool results, tool descriptions, the refusal texts
fed back as tool output — are a different contract and are not covered here.
Those want a stable shape a model can parse and are tuned by live evidence
rather than by taste; `internal/coder/tools.go` alone holds about a hundred and
fifty of them. Changing one is a prompt change, and belongs with the trials in
`doc/experiments/`, not with this page.

Most of what follows was already true somewhere in the tree. It is written down
because it was true *by accident*, in some files and not others, and an audit
found the same message class rendered three different ways depending on which
file it happened to live in.

## The channels

Which one a message is on decides almost every other question below, and there
are two groups: inside a running session, and outside one.

**Inside a session** everything goes to stdout and is told apart by colour.
`internal/repl/output.go` implements one method per voice:

| method | voice | how it looks |
| --- | --- | --- |
| `Printf` | the REPL's own — the banner, `/add` acknowledgements | plain |
| `Toolf` | the harness narrating a tool's work | `Theme.Tool`, recessive |
| `Warningf` | something is off but the turn goes on | `Theme.Warning` |
| `Errorf` | something failed | `Theme.Error` |
| `ToolBlock`, `Link` | a multi-line payload; a URL | see their doc comments |

**Outside a session** there is no colour to lean on, so:

| | where | how it looks |
| --- | --- | --- |
| **Notices** from `cmd/strument` | stderr | prefixed `strument: ` |
| **Data** a command was asked to produce | stdout | unadorned |
| **Errors** returned up to Kong | stderr | Go convention; Kong prefixes them with the binary name |

**The prefix marks harness voice where colour cannot.** That is its whole job,
and it is why the two groups differ. Inside the REPL the four themed methods
already separate the harness from the model and from itself, so a prefix would
be noise. Outside there is no such channel. Kong reaches the same answer
independently — it renders a returned error as `strument: <err>` — so a notice
and a fatal error read as the same program speaking.

**Outside a session, stdout is data and stderr is commentary.** The test is
whether piping it into `grep` is reasonable. `strument config models`, `strument
history` and `strument project list` are data. A warning about an untrusted
config is not, even though it appears in the same run. Inside a session the
distinction does not arise: the terminal is the whole surface and there is
nothing to pipe.

**One prefix per message, not per line.** A message that continues indents its
continuation two spaces and carries no second prefix:

```
strument: ignoring 1 untrusted project skill: release-notes
  Run `strument trust` in this directory to allow it.
```

## Quote by kind, not by mood

The kind of thing decides the quoting, so the same kind looks the same
everywhere. `internal/config` already had this right and the rule is its
convention, generalised.

- **Backticks for a name you type bare**: a command, a flag, a config key.
  `` `strument trust` ``, `` `check_auto` ``, `` `--no-shell` ``.
- **Double quotes for a value shown as source** — `"full"`, `"off"`,
  `{"test": ["go", "test"]}`. The quotes are not ours; they are part of what
  you type into a `.star` file, and stripping them would show something that
  does not parse. This is the case that makes the rule *by kind* rather than
  *one quote character everywhere*.
- **`%q` for a value that came from outside** — a model alias the user gave, a
  string a provider returned. The quotes are load-bearing: they are what makes
  an empty value or a stray trailing space visible.
- **Bare for paths.** A path is visually distinct already, and quoting it makes
  it worse to copy out of a terminal.

An earlier draft of this page said "backticks for anything you can type, never
double quotes in prose we wrote". Applied to `internal/config` it would have
turned `reasoning_display must be "full"` into a value that no longer parses
where the reader has to put it. The rule above is what the code already did.

## A period if and only if it is a sentence

`Read poll/poll.go (12 lines)` is a label. It takes no period.

`The turn left the files as they were; nothing to commit.` is a sentence. It
takes one.

This is the rule rather than a per-channel decree, because `Toolf` legitimately
emits both. Help entries are sentences, so both help systems — Kong's `help:`
tags and the slash-command table in `internal/repl/commands.go` — end with a
period.

## Counting

Never `(s)`. Use `render.Plural` (`internal/render/count.go`), which both
`cmd/strument` and the REPL reach, and give the reader a real word:

```go
render.Plural(n, "turn", "turns")       // "1 turn", "12 turns"
render.PluralWord(n, "origin", "origins") // when the count goes elsewhere
```

Zero usually deserves its own phrasing rather than "0 turns" — `no turns` reads
as an answer where `0 turns` reads as a measurement.

## Shape: fact, then what still works, then the remedy

The best message in the tree is `internal/config/envset.go`'s, and it is worth
copying:

> `env_set TZ "Europe/Kiev" is not a zone name (unknown time zone). Commands
> and git still get TZ, but Strument's own dates stay in this machine's zone.
> Use a database name like Europe/Kyiv or UTC.`

Three moves: what is wrong, what still works anyway, what to do. The middle one
is the part that gets dropped, and it is the part a reader most wants — a
person who has just been told something failed is asking *what did I lose*, and
answering that before they ask is the most distinctive thing about how Strument
talks. `/undo still covers them`, `Its history is still on disk`, `Reading what
a past session left is still fine`. Keep doing it.

A message that states a fact and stops is the weak shape. `model "mimo" not
found on openrouter` — found where? What now?

Report the **consequence**, not the mechanism, when they differ. `could not save
the undo record` names an internal artifact; what the reader needs is that
`/undo` will not work for this turn.

The em dash is the house joint for a qualifier: `Not committing config.star —
outside the repository; /undo still covers them.`

## Tense in the harness's voice

`Toolf` uses past tense for something finished and a present participle for
something in flight, and a reader can tell from the verb whether to wait:

- `Read poll.go (12 lines)`, `Listed . (24 entries)`, `Matched foo against bar`
- `Running the automatic checks.`, `Committing config.star before applying edits.`

## The `‹…›` markers

Two related conventions share one glyph, which is fine as long as both are
known.

**A bracketed aside** wraps model-authored text over several lines and is closed
by `‹/›`: `‹thinking›` and `‹run_code›`. The shape is deliberately tag-like and
deliberately not tag-valid, because `stripReasoning` removes real `<tag>…</tag>`
from model output — printing that shape would make the harness's voice
indistinguishable from the model's.

**A labelled line** prefixes one line of model-supplied text and has no closer:
`‹check›`, `‹shell›`, `‹question›`, `‹webfetch›`, `‹websearch›`, `‹skill›`.

What both have in common is the thing to remember: **`‹name›` introduces text
the model supplied.** Strument's own prose never wears one.

## One phrasing per concept

Where the same instruction appears in several places, it lives in one constant
and is used from all of them. The instruction to trust a project's files had
five spellings before this page existed, which is five chances for four of them
to go stale; it is now `config.TrustAdviceFor` (`internal/config/trust.go`).

A variant that carries real information is not drift. That helper takes an
`inSession` flag because trusting from another window does nothing to a running
session until `/reload` — dropping the extra clause to make one string would
send someone to run a command that appears not to work.
