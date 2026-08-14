package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/editblock"
	"dbohdan.com/strument/internal/llm"
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
	toolVerify = "verify"
	// toolCommitMessage is offered only where a commit will actually happen.
	toolCommitMessage = "commit_message"
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
	if c.RepoMap != nil {
		// symbol reads the same tree-sitter layer the repo map is built from,
		// so it is offered exactly when that layer is available.
		defs = append(defs, symbolTool())
	}
	if c.editFormat == "ask" {
		return defs
	}
	defs = append(defs, editTools()...)
	if c.SuggestShellCommands {
		defs = append(defs, bashTool())
	}
	if len(c.Verify) > 0 {
		defs = append(defs, verifyTool(c.Verify))
	}
	if c.commitsThisTurn() {
		defs = append(defs, commitMessageTool())
	}
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
					"path":   strProp("The file's path, relative to the project root."),
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
					"path": strProp("Optional. Only search under this directory. This, not glob, " +
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
					"path": strProp("The directory, relative to the project root. Omit for the root itself."),
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
					"path": strProp("The file's path, relative to the project root."),
					"old_string": strProp("The exact existing text to replace, character for character, " +
						"including all whitespace, comments, and docstrings. Include enough surrounding " +
						"lines to match the intended location uniquely."),
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
					"path":    strProp("The file's path, including any directories."),
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

// verifyTool runs the project's configured checks. It takes a name, never a
// command: everything it can run was written by the user in their config, so
// there is nothing for the model to alter or append. That is what lets it run
// without the confirmation bash requires.
func verifyTool(checks []config.VerifyCheck) llm.ToolDef {
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
		Name:        toolVerify,
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
		case toolCommitMessage:
			results[tc.ID] = c.runCommitMessage(tc)
		case toolVerify:
			results[tc.ID] = c.runVerify(ctx, tc)
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
			c.editedSinceVerify = true
		}
	}

	// Shell commands run after the edits, so a test run in the same turn sees
	// the edited files.
	for _, cmd := range commands {
		results[cmd.callID] = c.runShellTool(ctx, cmd)
	}

	// Append one tool result per call, in call order, then re-send on them.
	c.appendToolResults(results)

	// Either way the next send re-enters on the tool results already appended
	// to curMessages, adding no user turn. The two outcomes differ only in
	// which budget they spend: a failure the model must fix is a reflection, a
	// result it merely needs to see is a work step.
	c.toolContinuation = true
	if needsReflection {
		return OutcomeReflect
	}
	// A step whose only call was commit_message ends the turn. There is nothing
	// to come back with — the model set a string — and re-sending the whole
	// conversation so it can read "Noted." is the extra round trip this tool
	// exists to delete. Alongside any other call the normal rules apply, because
	// then there is a real result to see.
	if onlyCommitMessage(c.partialToolCalls) {
		return OutcomeSuccess
	}
	return OutcomeContinue
}

// onlyCommitMessage reports that a step asked for nothing but the commit
// message.
func onlyCommitMessage(calls []llm.ToolCall) bool {
	if len(calls) == 0 {
		return false
	}
	for _, tc := range calls {
		if tc.Name != toolCommitMessage {
			return false
		}
	}
	return true
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
	if name, ok := matchConfiguredCheck(command, c.Verify); ok {
		// The purpose is deliberately not printed. It exists to inform a
		// decision, and here there is no decision; what the user needs is which
		// check matched and how it went, which runChecks' own two lines say.
		transcript, _ := c.runChecks(ctx, []string{name})
		// %q rather than quoteToolArg, which drops the quotes on a word that
		// does not need them: this sentence is showing the model a call it can
		// copy, and verify(test) is not one.
		return fmt.Sprintf("That command is the configured check %q, so it ran without asking the "+
			"user. Call verify(%q) to run it directly.\n\n%s", name, name, transcript)
	}

	// No Group, so no "a=all turn" here. A blanket turn-scoped yes is the last
	// thing this gate should offer: what reaches it is the open-ended remainder
	// left over once every observation tool has taken its share, and approving
	// the next unseen one because the last was fine is exactly the reflex the
	// prompt exists to interrupt. The repetition it was answering is the case
	// just handled above.
	if !c.confirmTurn(ConfirmRequest{
		Prompt:           "Run shell command?",
		Command:          command,
		Purpose:          cmd.purpose,
		RequiresYesShell: true,
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
			var ok bool
			newContent, ok = editblock.DoReplace(e.path, content, exists, e.search, e.replace, fen)
			if !ok || newContent == "" {
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
