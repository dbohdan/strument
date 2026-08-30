package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/editblock"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/skill"
)

// Tool names. These are the conventional names across coding harnesses, on
// purpose: a model's expectations about what "edit" or "grep" does are worth
// more than a bespoke vocabulary, and matching them is the whole reason this
// harness moved to a standard tool set.
const (
	toolRead   = "read"
	toolWrite  = "write"
	toolEdit   = "edit"
	toolBash   = "bash"
	toolGrep   = "grep"
	toolGlob   = "glob"
	toolLS     = "ls"
	toolSymbol = "symbol"
	toolCheck  = "check"
	toolCommit = "commit"
	// toolWebfetch is the field's name, not a chosen one — see webfetch.go.
	toolWebfetch = "webfetch"
	// toolWebsearch pairs with it, and the pairing is the point: the two names
	// sit beside each other in every harness a model has seen.
	toolWebsearch = "websearch"
	// toolSkill loads one of the user's skills. Its catalog lives in the tool
	// description rather than the prompt — see skills.go.
	toolSkill = "skill"
	// toolCode runs a short Python program in Monty, the restricted interpreter
	// in internal/monty — see codetool.go. Computing mutates nothing, so it is
	// offered in ask mode too.
	toolCode = "code"
	// toolAskUser is the model's channel for asking the user a structured
	// question mid-turn. It mutates nothing, so it sits with the read-only
	// tools — a discussion turn is precisely where a clarifying question is
	// most useful.
	toolAskUser = "ask_user_question"
)

// strProp is a JSON-Schema string property with a description.
func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// intProp is a JSON-Schema integer property with a description.
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// toolDefs is the tool set offered to the model this turn.
//
// Observation is always available; mutation is conditional. In ask mode only
// the read-only tools are offered at all, which is what "discussion mode" now
// means — a restricted tool set rather than a separate prompt set with an
// engine that parses nothing.
func (c *Coder) toolDefs() []llm.ToolDef {
	defs := readOnlyTools()
	// ask_user_question is always offered: it has no side effect to gate, and
	// a discussion turn is exactly where a clarifying question belongs.
	defs = append(defs, askTool())
	if c.RepoMap != nil {
		// symbol reads the same tree-sitter layer the repo map is built from,
		// so it is offered exactly when that layer is available.
		defs = append(defs, symbolTool())
	}
	// Offered in ask mode too: fetching mutates nothing, and reading a
	// specification is exactly what a discussion turn is for. The port is
	// always set in the binary, so this is a guard for a directly built
	// Coder, not a setting a user can be on the wrong side of.
	if c.Scrape != nil {
		defs = append(defs, webfetchTool())
	}
	// Offered in ask mode too, and for the same reason: finding out what exists
	// is what a discussion turn is for. Genuinely conditional, unlike webfetch —
	// with no configured instance there is no search to offer.
	if c.Search != nil {
		defs = append(defs, websearchTool())
	}
	// Also before the ask-mode return: loading instructions mutates nothing,
	// and a discussion turn is where a skill about how to discuss something
	// belongs. Offered only when there is something to name, because the enum
	// would otherwise be empty and the description would list nothing.
	if len(skill.Usable(c.Skills)) > 0 {
		defs = append(defs, skillTool(c.Skills))
	}
	// Also before the ask-mode return: computing mutates nothing, and a
	// discussion turn is exactly where a calculator belongs.
	defs = append(defs, codeTool())
	if c.editFormat == "ask" {
		return defs
	}
	defs = append(defs, editTools()...)
	if c.SuggestShellCommands {
		defs = append(defs, bashTool())
	}
	if len(c.Check) > 0 {
		defs = append(defs, checkTool(c.Check))
	}
	// Offered whenever editing is, including where it will decline: a session
	// without git, or with auto-commits off, answers the call with the reason
	// rather than hiding the tool. Withholding it would leave a model that
	// wanted a commit boundary with no way to find out why it could not have
	// one, which is how the plan it wrote silently became one commit.
	defs = append(defs, commitTool())
	return defs
}

// readOnlyTools are the four ways to look at the project. They never mutate
// anything, so they never ask for confirmation.
func readOnlyTools() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name: toolRead,
			Description: "Read a file's contents, with line numbers. Returns a window of the file; " +
				"use offset and limit to page through a long one.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":   strProp("The file's path, relative to the project root. An absolute path that lies inside the project also works; relative is preferred."),
					"offset": intProp("The first line to return, 1-based. Omit to start at the beginning."),
					"limit":  intProp("How many lines to return. Omit for a default window."),
				},
				"required": []any{"path"},
			},
		},
		{
			Name: toolGrep,
			Description: "Search file contents with a regular expression. Files the project ignores " +
				"are not searched.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": strProp("A regular expression (Go syntax, which is close to PCRE without backreferences)."),
					"glob": strProp("Optional. Only search paths matching this glob. It is matched " +
						"against the whole path, so use \"**/*.go\" for every directory; \"*.go\" " +
						"matches only the project root and a bare directory name matches nothing."),
					"path": strProp("Optional. Only search under this directory, relative to the project root " +
						"(an absolute path inside the project also works). This, not glob, " +
						"is how to restrict a search to a subtree."),
					"mode": map[string]any{
						"type": "string",
						"enum": []any{"files", "content", "count"},
						"description": "\"files\" (the default) lists the files that match, \"content\" returns the " +
							"matching lines with line numbers, \"count\" returns a per-file count. " +
							"\"content\" returns at most the first 100 matching lines, each shortened to " +
							"200 characters; when that is not enough, narrow the pattern or the scope " +
							"rather than expecting more.",
					},
					"ignore_case": map[string]any{"type": "boolean", "description": "Match case-insensitively."},
					"context_lines": intProp("Lines to return either side of each match, like grep's -C. " +
						"Asking for context returns content, whatever mode says. Matching lines are " +
						"marked with \":\" and context lines with \"-\"."),
				},
				"required": []any{"pattern"},
			},
		},
		{
			Name: toolGlob,
			Description: "Find files by path pattern. The pattern is matched against the whole path, " +
				"segment by segment, with ** standing for any number of directories.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": strProp("A glob such as \"**/*.go\" or \"internal/*/testdata/*\". " +
						"\"*.go\" matches only the project root, so use \"**/*.go\" to reach every directory."),
				},
				"required": []any{"pattern"},
			},
		},
		{
			Name:        toolLS,
			Description: "List one directory's contents. Useful for getting your bearings in an unfamiliar tree.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": strProp("The directory, relative to the project root. Omit for the root itself. An absolute path that lies inside the project also works; relative is preferred."),
				},
			},
		},
	}
}

// editTools change files directly — the change lands the moment the call
// arrives, exactly like an ordinary edit, with git auto-commit and /undo as the
// safety net.
func editTools() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name: toolEdit,
			Description: "Replace an exact span of text in a file. The edit applies immediately. " +
				"Make one call per change; call it several times to make several changes.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": strProp("The file's path, relative to the project root. An absolute path that lies inside the project also works; relative is preferred."),
					"old_string": strProp("The exact existing text to replace, character for character, " +
						"including all whitespace, comments, and docstrings. It must match exactly once: " +
						"include enough surrounding lines to pick out the one place you mean, and make " +
						"a separate call for each place if you mean several."),
					"new_string": strProp("The text to put in its place."),
				},
				"required": []any{"path", "old_string", "new_string"},
			},
		},
		{
			Name: toolWrite,
			Description: "Write a file with the given full contents, creating it — or completely " +
				"overwriting it if it already exists. Applies immediately. To change part of an " +
				"existing file use edit instead; use this only when you are providing the whole file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    strProp("The file's path, relative to the project root. An absolute path that lies inside the project also works; relative is preferred."),
					"content": strProp("The complete contents of the file."),
				},
				"required": []any{"path", "content"},
			},
		},
	}
}

// bashTool runs a shell command, and is the tool that asks first. Everything
// the model might want to *observe* has its own tool, so what reaches bash is
// the open-ended and possibly destructive remainder — which is exactly what
// deserves a confirmation prompt.
//
// purpose is required, and the description asks for a claim rather than a
// label, because the prompt is worth only what the user reads off it: "run the
// tests" tells them nothing they could not see in the command itself. The
// requirement is a schema one, not a gate — a call without a purpose still runs
// (see runShellTool), because the absence is something to show the user rather
// than a reason to spend a round trip.
func bashTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolBash,
		Description: "Run a shell command. Unless the command is one of the project's configured checks, " +
			"the user is asked to confirm before it runs. Its output is returned to you. Commands run " +
			"from the project's root directory. To read, search, or list files, use the read, grep, " +
			"glob, and ls tools instead — they are never confirmed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": strProp("The complete command, ready to execute, with no placeholders."),
				"purpose": strProp("What this command does and why you are running it now — enough " +
					"for the user to decide without reading the command itself. Say plainly if it " +
					"writes, deletes, installs, or sends anything."),
			},
			"required": []any{"command", "purpose"},
		},
	}
}

// checkTool runs the project's configured checks. It takes a name, never a
// command: everything it can run was written by the user in their config, so
// there is nothing for the model to alter or append. That is what lets it run
// without the confirmation bash requires.
func checkTool(checks []config.Check) llm.ToolDef {
	names := make([]any, 0, len(checks))
	var desc strings.Builder
	desc.WriteString("Run the project's configured checks and return their output. " +
		"Runs without asking the user, because it can only run commands they configured. " +
		"Omit name to run every check in order, stopping at the first failure.\n\nAvailable checks:\n")
	for _, ch := range checks {
		names = append(names, ch.Name)
		fmt.Fprintf(&desc, "- %s: %s\n", ch.Name, strings.Join(ch.Argv, " "))
	}

	return llm.ToolDef{
		Name:        toolCheck,
		Description: desc.String(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"enum":        names,
					"description": "Which check to run. Omit to run all of them.",
				},
			},
		},
	}
}

// askTool lets the model ask the user a structured, multiple-choice question
// mid-turn. Free text is always available to the user as an implicit extra
// option — never a modeled flag to turn on — so there is one less knob for the
// model to reason about.
//
// The questions-per-call and options-per-question caps keep one call legible
// as a single block of scroll in a plain scrolling terminal: a question whose
// options the user must scroll back to read whole is a badly formed question.
func askTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolAskUser,
		Description: "Ask the user a multiple-choice question. Use it when a task has a genuinely " +
			"ambiguous, multiple-valid-approaches decision point — not for questions you could " +
			"resolve by reading the codebase with read/grep/glob/symbol. Prefer 1 question over " +
			"several when the decisions are independent; batch only questions that are genuinely " +
			"related. Options should be mutually exclusive and skimmable: label is the fast scan, " +
			"description carries the actual tradeoff. Order options with your recommended choice " +
			"first when you have one, and say so in the description (e.g. \"— recommended, matches " +
			"existing config style\"). The user can always answer with their own free text instead " +
			"of picking an option. Do not use this tool to ask permission to do something — that " +
			"is what the confirmation prompt is for. Use it only when you cannot proceed without " +
			"the user's input.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"minItems":    1,
					"maxItems":    5,
					"description": "The questions to ask, in order. Ask at most one question per decision.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question": strProp("Full question text shown to the user."),
							"options": map[string]any{
								"type":        "array",
								"minItems":    2,
								"maxItems":    4,
								"description": "The choices. 2-4, mutually exclusive.",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"label":       strProp("The short answer, as it reads in the list."),
										"description": strProp("The tradeoff the label stands for; this is what the user actually decides on."),
									},
									"required": []any{"label", "description"},
								},
							},
							"multiSelect": map[string]any{
								"type":        "boolean",
								"default":     false,
								"description": "Allow several answers to this one question (comma-separated indices).",
							},
						},
						"required": []any{"question", "options"},
					},
				},
			},
			"required": []any{"questions"},
		},
	}
}

// AskOption is one modeled choice of a question. Exported because an Asker
// implementation outside this package (the REPL's, the fixture stub's)
// constructs the request the coder hands it.
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// askQuestion is one question of an ask_user_question call.
type askQuestion struct {
	Question string      `json:"question"`
	Options  []AskOption `json:"options"` // 2-4 entries
	// camelCase on the wire, matching Claude Code's AskUserQuestion shape —
	// the name a post-trained model has seen — rather than the repo's
	// snake_case convention.
	MultiSelect bool `json:"multiSelect"` //nolint:tagliatelle
}

// askUserArgs is the decoded argument object of an ask_user_question call.
type askUserArgs struct {
	Questions []askQuestion `json:"questions"` // 1-5 entries
}

// parseAskArgs decodes an ask_user_question call. The second return is a
// model-facing failure message, "" on success.
func parseAskArgs(tc llm.ToolCall) (askUserArgs, string) {
	var a askUserArgs
	if err := json.Unmarshal([]byte(tc.Arguments), &a); err != nil {
		return askUserArgs{}, fmt.Sprintf("The arguments were not valid JSON: %v", err)
	}
	if len(a.Questions) == 0 || len(a.Questions) > 5 {
		return askUserArgs{}, "The \"questions\" argument must hold 1 to 5 questions."
	}
	for i, q := range a.Questions {
		if q.Question == "" {
			return askUserArgs{}, fmt.Sprintf("Question %d has no \"question\" text.", i+1)
		}
		if len(q.Options) < 2 || len(q.Options) > 4 {
			return askUserArgs{}, fmt.Sprintf("Question %d must offer 2 to 4 options; it offered %d.", i+1, len(q.Options))
		}
	}
	return a, ""
}

// runAskUser asks each of the call's questions in order through the Asker and
// formats the result text both sides of the exchange can read back: the
// question and the answer together, so the transcript stays self-describing.
//
// A validation failure is the model's mistake and reflects like any other
// malformed tool argument. A nil Asker is not the model's fault — it is script
// mode with no terminal — so that answer spends no reflection budget: the
// model is told to proceed on its best judgment, and the turn continues.
func (c *Coder) runAskUser(tc llm.ToolCall, needsReflection *bool) string {
	a, msg := parseAskArgs(tc)
	if msg != "" {
		*needsReflection = true
		return msg
	}
	if c.Asker == nil {
		return "ask_user_question is unavailable without an interactive terminal; " +
			"proceed using your best judgment and state the assumption you made"
	}

	var b strings.Builder
	b.WriteString("The user answered:")
	for _, q := range a.Questions {
		labels := c.Asker.Ask(AskRequest(q))
		answer := strings.Join(labels, ", ")
		if answer == "" {
			answer = "(no answer)"
		}
		fmt.Fprintf(&b, "\n- %q → %q", q.Question, answer)
		c.Out.Toolf("‹answer› %s", answer)
	}
	return b.String()
}

// accumulateToolCall folds a streamed tool-call fragment into
// partialToolCalls. ID and Name arrive on the first fragment for an index;
// later fragments append Args chunks.
func (c *Coder) accumulateToolCall(d *llm.ToolCallDelta) {
	if d == nil {
		return
	}
	pos, ok := c.toolCallIndex[d.Index]
	if !ok {
		pos = len(c.partialToolCalls)
		c.toolCallIndex[d.Index] = pos
		c.partialToolCalls = append(c.partialToolCalls, llm.ToolCall{})
	}
	tc := &c.partialToolCalls[pos]
	if d.ID != "" {
		tc.ID = d.ID
	}
	if d.Name != "" {
		tc.Name = d.Name
	}
	tc.Arguments += d.Args
}

// plannedEdit is one edit-tool call resolved to an editblock edit plus the
// call id it answers. create marks a create_file call, whose content is the file's
// whole text (written fresh, or overwriting an existing file).
type plannedEdit struct {
	callID  string
	path    string
	search  string
	replace string
	create  bool
}

// toolCommand is one bash call.
type toolCommand struct {
	callID  string
	command string
	// purpose is the model's claim about what running the command is for. It
	// is what the confirmation prompt is worth reading for, so it is carried
	// rather than decoded and dropped.
	purpose string
}

// editArgs is the decoded argument object shared by the edit tools.
type editArgs struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
	Content   string `json:"content"`
}

// parseEditArgs decodes an edit tool call's arguments into a toolEdit.
// write becomes an edit with an empty search (a whole-file write). The
// second return is a model-facing failure message, "" on success.
func parseEditArgs(tc llm.ToolCall) (plannedEdit, string) {
	var a editArgs
	if err := json.Unmarshal([]byte(tc.Arguments), &a); err != nil {
		return plannedEdit{}, fmt.Sprintf("The arguments were not valid JSON: %v", err)
	}
	if a.Path == "" {
		return plannedEdit{}, "The required \"path\" argument was missing."
	}
	switch tc.Name {
	case toolWrite:
		return plannedEdit{callID: tc.ID, path: a.Path, replace: a.Content, create: true}, ""
	default: // toolEdit
		return plannedEdit{callID: tc.ID, path: a.Path, search: a.OldString, replace: a.NewString}, ""
	}
}

// applyToolCalls dispatches the captured tool calls of a "tool"-format turn.
// Every call gets a tool result message appended to curMessages so the wire
// protocol stays well-formed on the next send. Edits apply directly (like a
// SEARCH/REPLACE block); suggest_command and request_files are proposals the
// user confirms. A call the model can fix — a search that didn't match, a
// malformed argument — records its error as the tool result and the turn
// re-sends (reflection) without a synthetic user turn.
func (c *Coder) applyToolCalls(ctx context.Context) SendOutcome {
	if len(c.partialToolCalls) == 0 {
		return OutcomeSuccess
	}

	var edits []plannedEdit
	var commands []toolCommand
	var commit *commitArgs
	results := map[string]string{} // call id -> result text
	needsReflection := false

	// Read-only calls answer immediately, in the order the model made them.
	// Edits are collected first and applied as one batch below, so sequential
	// edits to one file compose and the whole batch commits together.
	for _, tc := range c.partialToolCalls {
		switch tc.Name {
		case toolEdit, toolWrite:
			e, msg := parseEditArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				needsReflection = true
				continue
			}
			edits = append(edits, e)
		case toolBash:
			cmd, msg := parseCommandArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				needsReflection = true
				continue
			}
			commands = append(commands, cmd)
		case toolCommit:
			ca, msg := parseCommitArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				needsReflection = true
				continue
			}
			if commit != nil {
				// One per step. Edits in a message apply as one atomic batch —
				// that is what makes sequential edits to a file compose — so a
				// second commit here has nothing of its own to close. Two
				// commits are two steps, which the loop already gives for free
				// once these results re-send.
				results[tc.ID] = "One commit per step. This call closed nothing: " +
					"make the next chunk of edits, then commit that."
				needsReflection = true
				continue
			}
			parsed := ca
			commit = &parsed
		case toolRead:
			results[tc.ID] = c.runRead(tc)
		case toolGrep:
			results[tc.ID] = c.runGrep(tc)
		case toolGlob:
			results[tc.ID] = c.runGlob(tc)
		case toolLS:
			results[tc.ID] = c.runLS(tc)
		case toolSymbol:
			results[tc.ID] = c.runSymbol(tc)
		case toolCheck:
			results[tc.ID] = c.runCheckTool(ctx, tc)
		case toolWebfetch:
			f, msg := parseFetchArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				needsReflection = true
				continue
			}
			results[tc.ID] = c.runWebfetch(ctx, f)
		case toolWebsearch:
			q, msg := parseSearchArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				needsReflection = true
				continue
			}
			results[tc.ID] = c.runWebsearch(ctx, q)
		case toolSkill:
			s, msg := parseSkillArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				needsReflection = true
				continue
			}
			results[tc.ID] = c.runSkill(ctx, s)
		case toolCode:
			cc, msg := parseCodeArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				needsReflection = true
				continue
			}
			results[tc.ID] = c.runCode(ctx, cc)
		case toolAskUser:
			// Not routed through confirmGrouped: a question is not a permission
			// prompt, and --yes must not answer it.
			results[tc.ID] = c.runAskUser(tc, &needsReflection)
		default:
			results[tc.ID] = fmt.Sprintf("Unknown tool %q.", tc.Name)
		}
	}

	// Edits apply directly. The commit comes at turn end, once, so a result
	// here names what the call did rather than a commit that has not happened.
	edited := c.applyToolEdits(edits, results, &needsReflection)
	for _, f := range edited {
		c.turnEditedFiles[f] = true
		if !c.DryRun {
			// Nothing landed under --dry-run, so there is nothing for the
			// automatic checks to check.
			c.editedSinceCheck = true
		}
	}

	// Shell commands run after the edits, so a test run in the same turn sees
	// the edited files.
	for _, cmd := range commands {
		if ctx.Err() != nil {
			// Stopped before this one started. Running it anyway would fail
			// instantly against the cancelled context and read, to the model,
			// as the command itself failing.
			results[cmd.callID] = "Not run: the user stopped the turn before this command started."
			continue
		}
		results[cmd.callID] = c.runShellTool(ctx, cmd)
	}

	// The commit closes the work, so it runs after both the edits and the
	// commands: "commit what I did" includes the test that proved it. A model
	// that wants to gate a commit on a result has to see that result first,
	// which is a second step by construction.
	if commit != nil {
		results[commit.callID] = c.runCommitTool(*commit)
	}

	// Append one tool result per call, in call order, then re-send on them.
	c.appendToolResults(results)

	// Either way the next send re-enters on the tool results already appended
	// to curMessages, adding no user turn. The two outcomes differ only in
	// which budget they spend: a failure the model must fix is a reflection, a
	// result it merely needs to see is a work step.
	c.resumeInPlace = true

	// A Ctrl-C that landed while a tool was running is an interrupt, and has
	// to be reported as one.
	//
	// sendMessage decides "interrupted" from how the *stream* ended, and tool
	// calls run after the stream. So a Ctrl-C during a command used to kill
	// the command and return OutcomeContinue: the turn carried on, the steer
	// menu never appeared, and a second press inside the chord window quit
	// Strument outright. None of the three is what the user asked for by
	// pressing it.
	//
	// The tool results are already appended, so the history stays well-formed
	// — every call has an answer, which is what an interrupt during *streaming*
	// cannot promise and why that path drops partial calls instead.
	if ctx.Err() != nil {
		c.noteToolInterrupt()
		return OutcomeInterrupted
	}

	if needsReflection {
		return OutcomeReflect
	}
	return OutcomeContinue
}

// parseCommandArgs decodes a bash call. The second return is a model-facing
// failure message, "" on success.
//
// A missing purpose is deliberately not a failure. It is required in the
// schema, but refusing the call would spend a reflection on a formality, and
// the user is better served by seeing that no purpose was given than by the
// model being sent back to write one.
func parseCommandArgs(tc llm.ToolCall) (toolCommand, string) {
	var a struct {
		Command string `json:"command"`
		Purpose string `json:"purpose"`
	}
	if err := json.Unmarshal([]byte(tc.Arguments), &a); err != nil {
		return toolCommand{}, fmt.Sprintf("The arguments were not valid JSON: %v", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return toolCommand{}, "The required \"command\" argument was missing."
	}
	return toolCommand{callID: tc.ID, command: a.Command, purpose: strings.TrimSpace(a.Purpose)}, ""
}

// runShellTool confirms and runs a shell command, returning its output as the
// tool result. The output always returns to the model (it answers the tool
// call); there is no separate add-to-chat step.
func (c *Coder) runShellTool(ctx context.Context, cmd toolCommand) string {
	if !c.SuggestShellCommands {
		return "Shell commands are disabled in this session; the command was not run."
	}
	// Refused here rather than in runAndShow, which is where the same check
	// also lives for the paths that do not come through this tool. Leaving it
	// only there meant the user was asked to approve a command that was then
	// refused regardless — a prompt whose answer changes nothing, which is the
	// precise way to teach someone that prompts are noise.
	if c.Sandbox.blocksExecution() {
		return c.Sandbox.refusal()
	}
	command := strings.TrimSpace(cmd.command)

	// A command that *is* one of the project's configured checks runs without
	// asking. The user wrote it in their own config, so the prompt would be
	// asking them to re-approve their own decision — and a prompt that fires on
	// every `go test ./...` is what teaches them to answer without reading.
	//
	// It runs through runChecks rather than the shell: the parse proved the
	// command equals the configured argv, so executing that argv directly is the
	// only version where what ran is certainly what was compared. Sending the
	// model's string back through a shell would reopen word splitting and
	// globbing between the check and the execution.
	if name, ok := matchConfiguredCheck(command, c.Check); ok {
		// The purpose is deliberately not printed. It exists to inform a
		// decision, and here there is no decision; what the user needs is which
		// check matched and how it went, which runChecks' own two lines say.
		transcript, _ := c.runChecks(ctx, []string{name})
		// %q rather than quoteToolArg, which drops the quotes on a word that
		// does not need them: this sentence is showing the model a call it can
		// copy, and check(test) is not one.
		return fmt.Sprintf("That command is the configured check %q, so it ran without asking the "+
			"user. Call check(%q) to run it directly.\n\n%s", name, name, transcript)
	}

	// "a = all this turn" is offered only when the sandbox is enforcing, and the
	// condition is the whole argument rather than a detail of it.
	//
	// Without one, a blanket turn-scoped yes is the last thing this gate should
	// offer: what reaches it is the open-ended remainder left over once every
	// observation tool has taken its share, and approving the next unseen
	// command because the last one was fine is exactly the reflex the prompt
	// exists to interrupt. That argument is about *consequences*, though, not
	// about the reflex — and a sandbox is precisely a bound on consequences. A
	// command approved unseen can no longer write outside the project, so the
	// worst case stops being unbounded and starts being "a wasted turn and a
	// /undo".
	//
	// Tying the affordance to the property that licenses it also puts the
	// incentive the right way round: the way to be asked less is to turn the
	// sandbox on, not to reach for --yes-shell.
	group := ""
	if c.Sandbox.Active {
		group = "shell"
	}
	if !c.confirmGrouped(ConfirmRequest{
		Prompt:  "Run shell command?",
		Command: command,
		Purpose: cmd.purpose,
		Group:   group,
		Grant:   GrantBash,
	}) {
		return "The user chose not to run the command."
	}

	exitCode, output := c.runAndShow(ctx, command)
	return fmt.Sprintf("Command: %s\nExit status: %d\nOutput:\n%s", quoteToolArg(command), exitCode, output)
}

// appendToolResults appends a RoleTool message per captured call, in the
// order the model produced them, so every tool_call_id is answered.
func (c *Coder) appendToolResults(results map[string]string) {
	for _, tc := range c.partialToolCalls {
		text, ok := results[tc.ID]
		if !ok {
			text = "The call produced no result." // defensive; every branch above sets one
		}
		c.curMessages = append(c.curMessages, llm.ToolResult(tc.ID, text))
	}
}

// applyToolEdits applies edit-tool calls in order against a shared overlay so
// sequential edits to one file compose, records each call's result text into
// results, and returns the edited relative paths. A search that doesn't match
// sets *matchFailure and records a focused error (with a did-you-mean) for
// that call.
func (c *Coder) applyToolEdits(edits []plannedEdit, results map[string]string, matchFailure *bool) []string {
	if len(edits) == 0 {
		return nil
	}

	fen := editblock.Fence{Open: c.fence.open, Close: c.fence.close}
	reader := diskReader{root: c.Root}
	pending := map[string]string{}
	needDirtyCommit := map[string]bool{}
	writeVerb := map[string]string{} // path -> "Created"/"Overwrote"/"Applied edit to"
	callVerb := map[string]string{}  // call id -> the same, for that one call
	applied := map[string]bool{}     // call ids whose edit made it into the batch
	orig := map[string]string{}      // path -> contents before the batch's first write
	lastCall := map[string]string{}  // path -> the call that produced its final contents
	var writeOrder []string
	editedSet := map[string]bool{}
	var edited []string

	read := func(path string) (string, bool) {
		if content, ok := pending[path]; ok {
			return content, true
		}
		return reader.ReadFile(path)
	}

	for _, e := range edits {
		// Fold an absolute spelling away before anything keys on it. unsafePath
		// below has already accepted exactly these, so this is a rename of the
		// bookkeeping, not a second gate.
		e.path = c.normalizeToolPath(e.path)
		if reason := c.unsafePath(e.path); reason != "" {
			c.Out.Errorf("Skipping edit to %s: %s", quoteToolArg(e.path), reason)
			results[e.callID] = fmt.Sprintf("Skipped %s: %s", quoteToolArg(e.path), reason)
			continue
		}
		if ok, why := c.allowedToEdit(e.path, needDirtyCommit); !ok {
			results[e.callID] = fmt.Sprintf("Skipped %s: %s", quoteToolArg(e.path), why)
			continue
		}

		content, exists := read(e.path)
		var newContent string
		if e.create {
			// write puts down the whole file: create it fresh, or overwrite an
			// existing one — never the old append-on-empty-search behavior.
			newContent = e.replace
			if exists {
				// Say "overwrote" rather than "created", so the model does not
				// assume the old contents survived somewhere.
				callVerb[e.callID] = "Overwrote"
				writeVerb[e.path] = "Overwrote"
			} else {
				callVerb[e.callID] = "Created"
				if writeVerb[e.path] == "" {
					writeVerb[e.path] = "Created"
				}
			}
		} else {
			// Ambiguity is a failure, not a coin flip.
			//
			// DoReplace takes the first occurrence and reports success, so an
			// old_string appearing twice edited whichever came first and told
			// the model "Applied the edit to x". That is precisely the failure
			// mode edit-tool-bench criticises in *fuzzy* editing — a harness
			// returning success on an underconstrained transformation, leaving
			// the model reasoning from a false local success — and exact
			// matching alone does not prevent it. Exact is not unique.
			//
			// The tool's own description already asks for "enough surrounding
			// lines to match the intended location uniquely", so this enforces
			// the contract it states rather than inventing one. The ambiguity
			// message below was written for this case and was, until now,
			// reachable only by accident.
			var ok bool
			ambiguous := editblock.CountOccurrences(content, e.search) > 1
			if !ambiguous {
				newContent, ok = editblock.DoReplace(e.path, content, exists, e.search, e.replace, fen)
			}
			if ambiguous || !ok || newContent == "" {
				results[e.callID] = toolMatchFailure(e, content, fen)
				*matchFailure = true
				// The model is told through the tool result, and will usually
				// re-read and try again. The user has to be told separately, or
				// the diff that just scrolled past is the last word on an edit
				// that never happened — every other outcome here prints a line,
				// and only this one was silent.
				if n := editblock.CountOccurrences(content, e.search); n > 1 {
					c.Out.Warningf("Could not edit %s: the text to replace appears %d times.", e.path, n)
				} else {
					c.Out.Warningf("Could not edit %s: the text to replace was not found.", e.path)
				}
				continue
			}
			callVerb[e.callID] = "Applied the edit to"
			if writeVerb[e.path] == "" {
				writeVerb[e.path] = "Applied edit to"
			}
		}

		if _, seen := pending[e.path]; !seen {
			writeOrder = append(writeOrder, e.path)
			orig[e.path] = content // before *this batch*, not before this edit
		}
		pending[e.path] = newContent
		lastCall[e.path] = e.callID
		if !editedSet[e.path] {
			editedSet[e.path] = true
			edited = append(edited, e.path)
		}
		applied[e.callID] = true
		results[e.callID] = fmt.Sprintf("%s %s.", callVerb[e.callID], quoteToolArg(e.path))
	}

	c.dirtyCommit(needDirtyCommit)

	if len(edited) == 0 {
		return nil
	}
	if !c.DryRun {
		if err := c.writeAtomically(writePlan{Writes: pending, WriteOrder: writeOrder}); err != nil {
			c.Out.Errorf("Exception while updating files:")
			c.Out.Errorf("%s", err.Error())
			// The batch rolled back, so every intended write must be reported as
			// failed or the model will assume success. It is told what happened
			// but this is deliberately not a reflection: a filesystem failure —
			// a full disk, a path whose parent is a regular file — is not
			// something the model can fix by rewriting its edit, so spending the
			// error-reflection budget on a retry that will fail identically
			// helps nobody. The loop continues and the model decides.
			for _, e := range edits {
				if applied[e.callID] {
					results[e.callID] = fmt.Sprintf(
						"The write failed and the whole batch was rolled back, so %s is unchanged: %v",
						quoteToolArg(e.path), err)
				}
			}
			return nil
		}
	}
	if c.DryRun {
		// Nothing reached the disk, and the model must not be told otherwise.
		for _, e := range edits {
			if applied[e.callID] {
				results[e.callID] = fmt.Sprintf("Did not write %s: this session is --dry-run.", quoteToolArg(e.path))
			}
		}
	}
	for _, p := range edited {
		verb := writeVerb[p]
		if verb == "" {
			verb = "Applied edit to"
		}
		if c.DryRun {
			c.Out.Toolf("Did not write %s (--dry-run)", quoteToolArg(p))
		} else {
			c.Out.Toolf("%s %s", verb, quoteToolArg(p))
		}
	}

	// A file that has stopped parsing is worth saying out loud, to the user in
	// the scroll and to the model in the result of the call that finished it.
	for _, p := range edited {
		note := parseNote(p, orig[p], pending[p])
		if note == "" {
			continue
		}
		c.Out.Warningf("%s", note)
		if id := lastCall[p]; id != "" {
			results[id] += "\n\n" + note
		}
	}
	return edited
}

// toolMatchFailure builds the tool result for an edit whose search didn't
// match, including a did-you-mean when a near match exists.
func toolMatchFailure(e plannedEdit, content string, fen editblock.Fence) string {
	var b strings.Builder

	// Ambiguity and absence are different problems with different fixes, and
	// telling a model its text was "not found" when the file holds three copies
	// sends it hunting for a typo it did not make.
	if n := editblock.CountOccurrences(content, e.search); n > 1 {
		fmt.Fprintf(&b, "The text to replace appears %d times in %s, so it is ambiguous "+
			"and nothing was changed.\n", n, quoteToolArg(e.path))
		b.WriteString("Include enough surrounding lines to pick out the one you mean, " +
			"and make one call per place if you mean several.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "The search text was not found in %s, so no change was made.\n", quoteToolArg(e.path))
	b.WriteString("It must match the current file contents exactly, character for character, " +
		"including all whitespace, comments, and docstrings.\n")
	if didYouMean := editblock.FindSimilarLines(e.search, content, 0.6); didYouMean != "" {
		fmt.Fprintf(&b, "\nDid you mean to match these lines from %s?\n\n%s\n%s\n%s\n",
			quoteToolArg(e.path), fen.Open, didYouMean, fen.Close)
	}
	if e.replace != "" && strings.Contains(content, e.replace) {
		fmt.Fprintf(&b, "\nThe replacement text is already present in %s; this edit may not be needed.\n", quoteToolArg(e.path))
	}
	return b.String()
}
