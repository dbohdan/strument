# Ask-User-Question Tool — Design Spec

Revised 2026-08-18 by Claude and GLM-5.3, checked against the current code. The first draft
predates the closed tool-calling loop and named a codebase ("basecoder") and a
spec set that no longer exist; this revision is written against
`internal/coder/tools.go`, `internal/coder/ports.go`, `internal/repl/repl.go`,
and `internal/fixture/` as they stand.

Status: implemented (2026-08-18). The code is now the source of truth; this
document is the design record. One deliberate deviation from the letter of
§3.2/§5: the option type is exported as `coder.AskOption` and the answer-
parsing rule as `coder.ParseAskAnswer`, because the REPL's asker and the
fixture stub both live outside `internal/coder` and must interpret answers
identically to the live terminal path.
Depends on: `doc/README.md` (the tool loop, the coder's ports),
`internal/coder/tools.go` (dispatch), `internal/coder/ports.go` (the
`Confirmer` port), `internal/fixture/` (scenario stubs).

## 1. Purpose

Give the model a structured way to pause mid-turn and collect a bounded
decision from the user, instead of guessing or asking in free-form prose that
the user then has to parse and reply to in kind. Modeled on Claude Code's
`AskUserQuestion` tool: a single blocking tool call, multiple-choice by
default, free text as an escape hatch.

Primary use case for Strument specifically: resolving *task*-shaped ambiguity
before the model commits to an edit plan — e.g. "which of these two config
keys did you mean," "should I also update the test fixtures," "REPLACE the
whole function or just the body." This is a task-disambiguation primitive, not
a general chat mechanism. It fits the loop as it now stands: a reply ending in
a tool call is mid-sentence by construction, and this is one more tool result
to continue on — the question is an ordinary tool call in the model's eyes,
not a distinct protocol message.

## 2. Non-Goals

- Not a permission mechanism. The shell gate (`Confirmer` + the check
  allowlist in `allowlist.go`) authorizes actions with side effects; a question
  has no side effect to authorize, and the `Confirmer` port is yes/no-shaped
  anyway — a multiple-choice question does not fit it. This tool reaches the
  user through its own port (§5), so `AutoConfirmer` (`--yes`, `--yes-shell`)
  structurally cannot answer it: auto-approve flags are about skipping
  permission prompts, and a question isn't a permission prompt, it's the model
  asking for information it cannot proceed without.
- Not a multi-client sync primitive. Strument is a single-process CLI with one
  terminal attached to the session. There is no need for OpenCode's event-bus +
  `Deferred` + HTTP-reply architecture, which exists solely to let multiple
  simultaneous UI surfaces all answer the same pending question. A single
  blocking read is sufficient — do not build an internal pub/sub layer for
  this.
- Not a full-screen TUI. Strument renders by printing lines to the terminal
  and reading one line at a time through `readline` — the same shape
  `rlConfirmer` uses for yes/no prompts. No alternate screen, no cursor
  navigation, no widget that redraws in place. The `header` field from the
  first draft (a ≤12-char label for a TUI breadcrumb) is dropped for exactly
  this reason: with no widget to hang it on, it is a second copy of the
  question the user must read past.
- No timeout. The call blocks until the user answers or interrupts (Ctrl-C /
  SIGINT), same as every other blocking readline read in Strument. Do not
  implement a soft timeout; if the user is away, that's their problem, not a
  reason to error the tool call.
- Not for subagents — Strument has none; if it ever grows the concept, this
  tool stays unavailable to them (a subagent's question would have no clear
  owner in the conversation the user is actually watching).

## 3. Tool Definition

Name: `ask_user_question` — offered always, in ask mode and code mode alike.
It mutates nothing, so it sits with the always-offered tools in `toolDefs`,
before the `editFormat == "ask"` early return; a discussion turn is precisely
where a clarifying question is most useful.

### 3.1 JSON Schema (tool parameters, as sent to the model)

```json
{
  "type": "object",
  "properties": {
    "questions": {
      "type": "array",
      "minItems": 1,
      "maxItems": 5,
      "items": {
        "type": "object",
        "properties": {
          "question": { "type": "string", "description": "Full question text shown to the user." },
          "options": {
            "type": "array",
            "minItems": 2,
            "maxItems": 4,
            "items": {
              "type": "object",
              "properties": {
                "label":       { "type": "string" },
                "description": { "type": "string" }
              },
              "required": ["label", "description"]
            }
          },
          "multiSelect": { "type": "boolean", "default": false }
        },
        "required": ["question", "options"]
      }
    }
  },
  "required": ["questions"]
}
```

Cap questions per call at 5, options per question at 2–4. These caps keep a
single call legible as one block of scroll in a plain scrolling terminal — a
question whose options the user must scroll back to read whole is a badly
formed question.

Free text is always available as an implicit final option, not a modeled
`custom: false` flag. Strument doesn't need OpenCode's opt-out — always render
"Other — type your own answer" and let the reader branch on whether the
response parses as a selection index or falls through to raw text. One fewer
knob for the model to reason about.

### 3.2 Go Types

These live in `package coder` alongside the other tool plumbing in
`internal/coder/tools.go`, following the existing pattern (small args struct,
decoded with `json.Unmarshal`, validation returned as a model-facing message
like `parseEditArgs` and `parseCommandArgs` do):

```go
type askOption struct {
    Label       string `json:"label"`
    Description string `json:"description"`
}

type askQuestion struct {
    Question    string       `json:"question"`
    Options     []askOption  `json:"options"` // 2-4 entries
    MultiSelect bool         `json:"multiSelect"`
}

type askUserArgs struct {
    Questions []askQuestion `json:"questions"` // 1-5 entries
}
```

There is no dedicated result struct. A tool result is a plain string in
Strument (`results map[string]string` → `llm.ToolResult`), and the result text
carries both sides of the exchange so the transcript stays self-describing:

```
The user answered:
- "Which timestamp format?" → "RFC 3339"
- "Update the fixtures too?" → "yes"
```

Validation on decode: reject `len(Questions) == 0 || > 5`, reject any question
with `len(Options) < 2 || > 4`. On validation failure, record the failure as
that call's tool result and set `needsReflection` — the model retries with a
corrected call on the next send, the same path a malformed `edit` or `bash`
argument takes. No Go error up the stack.

## 4. Dispatch Integration

There is no tool-kind enum to extend; `applyToolCalls` dispatches on
`tc.Name` in a plain switch. Add one case:

```go
case toolAskUser:
    results[tc.ID] = c.runAskUser(tc)
```

`runAskUser` decodes and validates the arguments (a model-facing failure
message sets `needsReflection`), then asks each question in order through the
port in §5 and formats the result text. The call is not routed through
`confirmTurn` or `c.Confirm` at all — those are the permission surface, and
the `--yes`/`--yes-shell` flags that answer them must not answer a question.

Rendering: nothing to do. `RendersDiff` in `internal/render/toolargs.go`
returns false for every tool but `edit` and `write`, so the streamed arguments
are not drawn as a diff; the question is displayed at execution time by the
port implementation (§6), which is also where the shell purpose line appears —
the stream is never the surface a decision reads from.

An `ask_user_question` call counts as an ordinary work step toward
`max_steps`. It spends no error-reflection budget unless validation failed.

## 5. Execution Flow

1. The model emits a `tool_use` block for `ask_user_question`.
2. `applyToolCalls` hits the `toolAskUser` case and calls `runAskUser`.
3. `runAskUser` calls the port once per question, in order, blocking on each:

```go
// AskRequest is one question put to the user on the attached terminal.
type AskRequest struct {
    Question    string
    Options     []askOption
    MultiSelect bool
}

// Asker puts one question to the user and returns their answer: the chosen
// labels (one entry, or several for multiSelect), or the raw text they typed.
// A nil Asker means no interactive terminal is attached (script mode); the
// caller answers the model with an error result rather than asking.
//
// Implemented by the REPL through readline; stubbed in tests and fixtures.
type Asker interface {
    Ask(req AskRequest) []string
}
```

   A new port rather than an extension of `Confirmer`, per the rule in
   `doc/README.md` — extend a port rather than reaching around it — because
   `Confirmer`'s shape (`ConfirmRequest` → yes/no/always) cannot carry a
   multiple-choice question, and widening it would break every existing
   implementation and stub for one caller's sake. Nil means non-interactive,
   the same convention as a nil `Repo`.

4. The REPL implementation (`internal/repl/repl.go`, beside `rlConfirmer`)
   prints the question and numbered options through `r.out`, then reads one
   line via `r.rl.ReadLineWithConfig` with `HistoryLimit = -1` (answers stay
   out of the input history, like y/n answers) and `AutoComplete = nil`.
5. `runAskUser` assembles the result text (§3.2), the coder prints one
   narration line per question through `c.Out.Toolf` — `‹ask› "Which
   timestamp format?" → RFC 3339` — mirroring the `‹shell›` purpose-line
   convention, so the decision is recorded in the scroll at the moment it was
   made.
6. The turn continues as normal: the result is appended by
   `appendToolResults` and the turn re-sends (`OutcomeContinue`). From the
   model's perspective this is an ordinary tool result.

No goroutines, no channels, no `Deferred`. `Ask` is a plain blocking function
on the goroutine already handling the session — Strument has exactly one
terminal to read from, so there is nothing to synchronize.

## 6. Terminal Rendering

There is no full-screen TUI. The question prints as ordinary scroll — the
same monospace numbered list either way — and the answer is one line read at
a prompt.

### 6.1 Example

The model emits:

```json
{
  "name": "ask_user_question",
  "arguments": {
    "questions": [
      {
        "question": "Which timestamp format should the new log lines use?",
        "options": [
          { "label": "RFC 3339",
            "description": "2026-08-18T14:03:11Z; matches every other logger in this repo — recommended" },
          { "label": "Unix epoch",
            "description": "1761235391; compact, but needs conversion to read" }
        ]
      }
    ]
  }
}
```

The user sees (plain lines, then the readline prompt):

```
‹question› Which timestamp format should the new log lines use?

1. RFC 3339 — 2026-08-18T14:03:11Z; matches every other logger in this repo — recommended
2. Unix epoch — 1761235391; compact, but needs conversion to read
3. Other — type your own answer

Answer (1-3, or your own text):
> 
```

and the tool result the model receives is:

```
The user answered:
- "Which timestamp format should the new log lines use?" → "RFC 3339"
```

Had the user typed `rfc3339, but with milliseconds` instead of `1`, the arrow
would carry that raw string — the free-text escape hatch is the whole answer,
not a correction the harness interprets.

### 6.2 multiSelect

For `multiSelect: true`, prompt for comma-separated indices and say so:

```
‹question› Which sections should the summary include?

1. Introduction — Opening context
2. Conclusion — Final summary
3. Other — type your own answer

Answer (numbers separated by commas, or your own text):
> 
```

### 6.3 Parsing Rule

Split the input on commas, trim each token, try `strconv.Atoi` per token.

- All tokens parse as valid in-range indices → the answer is the
  corresponding labels (one label for single-select — a multi-index answer to
  a single-select question is out of range, so it falls through — several for
  multiSelect).
- Any token fails to parse or is out of range → treat the *entire* raw input
  as one free-text answer. Do not silently drop the unparseable tokens and
  keep the valid ones — that produces answers the user didn't intend. This
  mirrors Claude Code's reference implementation, which falls back to the raw
  string wholesale rather than partially interpreting it.

An empty line re-prompts for that question once, then falls through to an
empty free-text answer rather than looping forever.

## 7. Non-Interactive Mode

Script mode (`strument <message>`, tests driving `Coder.Run`) and any run
where no terminal is attached has a nil `Asker`. Behavior: the tool result is
an error message to the model — `"ask_user_question is unavailable without an
interactive terminal; proceed using your best judgment and state the
assumption you made"` — rather than hanging or silently picking option 1.
This keeps the failure legible in transcripts and matches Strument's
no-silent-assumptions posture. Do not auto-select a default option; that
produces a decision attributed to the user that the user never made. Unlike a
malformed argument this is not the model's fault, so it spends no
error-reflection budget; the turn continues (`OutcomeContinue`) and the model
adapts within it.

## 8. System Prompt Guidance

Add to the tool's `Description` field, not as separate system prompt text —
the same placement every other rule rides in ("the schema carries the rules",
`doc/README.md`):

> Use this tool when a task has a genuinely ambiguous, multiple-valid-
> approaches decision point — not for questions you could resolve by reading
> the codebase with read/grep/glob/symbol. Prefer 1 question over several
> when the decisions are independent; batch only questions that are genuinely
> related. Options should be mutually exclusive and skimmable: label is the
> fast scan, description carries the actual tradeoff. Order options with your
> recommended choice first when you have one, and say so in the description
> (e.g. "— recommended, matches existing config style"). Do not use this
> tool to ask permission to do something — that is what the confirmation
> prompt is for. Use it only when you cannot proceed without the user's
> input.

The "don't use it as a disguised permission request" line is worth keeping
explicit — it's the model-facing restatement of the structural split in §2/§4,
so the model doesn't try to use a question to route around the shell gate
(e.g. asking "should I run `rm -rf`, yes or no" as a question instead of a
`bash` call).

## 9. Fixture Harness Considerations

Per `internal/fixture`'s record/replay mechanism, `ask_user_question` calls
must be scriptable like any other interaction — the existing shape to follow
is the `confirm` row (`fixture.go`): a scenario carries scripted answers, a
stub consumes them in file order, and an unscripted or mismatched prompt
fails loudly. Concretely:

- Add an `ask` row kind to the scenario schema: the question text and the
  scripted answer, `{"kind":"ask","question":"…","answer":"1"}`.
- A stub alongside `ConfirmScript` implements `Asker` from those rows,
  matching on exact question text and erroring on a mismatch — "fixture: ask
  prompt %q, script expected %q" — the same failure `ConfirmScript` already
  produces for a confirm prompt.
- This is why the result text and the fixture row are keyed by question text
  rather than position: a replayed fixture stays legible and diffable if the
  surrounding turns change, and a wording mismatch is a loud, obvious harness
  failure rather than a silent off-by-one that answers the wrong question.
- Wire the stub in wherever `ConfirmScript` is wired (the coder's `Asker`
  field), not through a second mechanism.

## 10. Open Questions / Deviation Protocol

Per `AGENTS.md`: the code is the source of truth — if the implementor hits a
case not covered above, default to the more conservative option (blocking, no
auto-select, error over guess), and record the deviation in the code comment
and the commit message rather than resolving it silently. Specific open items:

- Whether the per-question `Toolf` narration line (§5 step 5) is worth its
  line of scroll, given the question and the typed answer are both already
  visible above it. Current spec says yes, for symmetry with `‹shell›` and
  because the scroll is the only record of what was chosen if the terminal
  clears; drop it if it reads as noise in practice.
- Whether the one re-prompt on an empty line (§6.3) is worth the special
  case, or whether an empty answer should fall straight through as free text.
- The old draft's open question — sharing option-rendering with a `/ask`
  slash command — is moot: `/ask` shipped as an edit-format switch (a
  one-shot ask-mode turn), not an interactive question command, so there is
  no second renderer to share. If a future command does render options, factor
  the numbered list out then; don't block this tool on it.
