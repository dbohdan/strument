package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"dbohdan.com/strument/internal/editblock"
	"dbohdan.com/strument/internal/llm"
)

// Tool names for the "tool" edit format.
const (
	toolReplaceInFile  = "replace_in_file"
	toolCreateFile     = "create_file"
	toolSuggestCommand = "suggest_command"
	toolRequestFiles   = "request_files"
)

// strProp is a JSON-Schema string property with a description.
func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// toolDefs is the function-tool set offered to the model this turn: the two
// direct-apply edit tools, plus suggest_command (when shell commands are
// enabled) and request_files.
func (c *Coder) toolDefs() []llm.ToolDef {
	defs := editTools()
	if c.SuggestShellCommands {
		defs = append(defs, commandTool())
	}
	defs = append(defs, requestFilesTool())
	return defs
}

// editTools are the direct-apply edit tools — like a SEARCH/REPLACE block,
// not a proposal — with git auto-commit and /undo as the safety net.
func editTools() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name: toolReplaceInFile,
			Description: "Replace an exact span of text in a file that is in the chat. " +
				"The edit applies immediately and is committed to git. Make one call per " +
				"change; call it several times to make several changes.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": strProp("The file's path, exactly as shown in the chat."),
					"search": strProp("The exact existing text to replace, character for character, " +
						"including all whitespace, comments, and docstrings. Include enough surrounding " +
						"lines to match the intended location uniquely."),
					"replace": strProp("The text to put in its place."),
				},
				"required": []any{"path", "search", "replace"},
			},
		},
		{
			Name: toolCreateFile,
			Description: "Write a file with the given full contents, creating it — or " +
				"completely overwriting it if it already exists. Applies immediately and " +
				"commits to git. To change part of an existing file, use replace_in_file " +
				"instead; use this only when you are providing the whole file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    strProp("The new file's path, including any directories."),
					"content": strProp("The complete contents of the new file."),
				},
				"required": []any{"path", "content"},
			},
		},
	}
}

// commandTool proposes a shell command for the user to run — a suggestion,
// not a direct action: the user confirms before it runs and its output comes
// back as the tool result.
func commandTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolSuggestCommand,
		Description: "Suggest a single shell command for the user to run — for example to run tests, " +
			"a build, or the program. The user is asked to confirm before it runs, and its output is " +
			"returned to you. Commands run from the project's root directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": strProp("The complete command, ready to execute, with no placeholders."),
				"purpose": strProp("A short note on what running the command is for."),
			},
			"required": []any{"command"},
		},
	}
}

// requestFilesTool asks the user to add existing files to the chat — a
// request, not a direct action: the user confirms each one before it joins.
func requestFilesTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolRequestFiles,
		Description: "Ask the user to add existing files to the chat so you can edit them. Use this when " +
			"a change needs a file that isn't in the chat yet, then stop and wait — don't propose edits " +
			"to files that haven't been added.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Project-relative paths of the files to add.",
				},
				"reason": strProp("A short note on why these files are needed."),
			},
			"required": []any{"paths"},
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

// toolEdit is one edit-tool call resolved to an editblock edit plus the call
// id it answers. create marks a create_file call, whose content is the file's
// whole text (written fresh, or overwriting an existing file).
type toolEdit struct {
	callID  string
	path    string
	search  string
	replace string
	create  bool
}

// toolCommand is one suggest_command call.
type toolCommand struct {
	callID  string
	command string
}

// toolFileReq is one request_files call.
type toolFileReq struct {
	callID string
	paths  []string
}

// editArgs is the decoded argument object shared by the edit tools.
type editArgs struct {
	Path    string `json:"path"`
	Search  string `json:"search"`
	Replace string `json:"replace"`
	Content string `json:"content"`
}

// parseEditArgs decodes an edit tool call's arguments into a toolEdit.
// create_file becomes an edit with an empty search (a new-file write). The
// second return is a model-facing failure message, "" on success.
func parseEditArgs(tc llm.ToolCall) (toolEdit, string) {
	var a editArgs
	if err := json.Unmarshal([]byte(tc.Arguments), &a); err != nil {
		return toolEdit{}, fmt.Sprintf("The arguments were not valid JSON: %v", err)
	}
	if a.Path == "" {
		return toolEdit{}, "The required \"path\" argument was missing."
	}
	switch tc.Name {
	case toolCreateFile:
		return toolEdit{callID: tc.ID, path: a.Path, replace: a.Content, create: true}, ""
	default: // toolReplaceInFile
		return toolEdit{callID: tc.ID, path: a.Path, search: a.Search, replace: a.Replace}, ""
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

	var edits []toolEdit
	var commands []toolCommand
	var fileReqs []toolFileReq
	results := map[string]string{} // call id -> result text
	needsReflection := false

	for _, tc := range c.partialToolCalls {
		switch tc.Name {
		case toolReplaceInFile, toolCreateFile:
			e, msg := parseEditArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				needsReflection = true
				continue
			}
			edits = append(edits, e)
		case toolSuggestCommand:
			cmd, msg := parseCommandArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				needsReflection = true
				continue
			}
			commands = append(commands, cmd)
		case toolRequestFiles:
			req, msg := parseFileReqArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				needsReflection = true
				continue
			}
			fileReqs = append(fileReqs, req)
		default:
			results[tc.ID] = fmt.Sprintf("Unknown tool %q.", tc.Name)
		}
	}

	// Edits apply directly, then commit — so /undo has a clean base and the
	// tool result can name the commit.
	overwrote := map[string]string{} // call id -> path, for create_file over an existing file
	edited := c.applyToolEdits(edits, results, overwrote, &needsReflection)
	saved := ""
	if len(edited) > 0 {
		for _, f := range edited {
			c.turnEditedFiles[f] = true
		}
		saved = c.autoCommit(edited)
		if saved == "" {
			saved = c.Prompts.FilesContentGPTEditsNoRepo
		}
	}
	for id, text := range results {
		if text == appliedPlaceholder {
			if p, ok := overwrote[id]; ok {
				// Tell the model it replaced an existing file, not created a new
				// one, so it doesn't assume the old contents survived.
				results[id] = fmt.Sprintf("Overwrote the existing file %s. %s", p, saved)
			} else {
				results[id] = saved
			}
		}
	}

	// Proposals: run confirmed commands (edits first, matching the text
	// flow), then add requested files.
	for _, cmd := range commands {
		results[cmd.callID] = c.runSuggestedCommand(ctx, cmd)
	}
	for _, req := range fileReqs {
		results[req.callID] = c.addRequestedFiles(req)
	}

	// Append one tool result per call, in call order, then rotate or reflect.
	c.appendToolResults(results)

	if needsReflection {
		c.toolContinuation = true
		return OutcomeReflect
	}
	if len(edited) > 0 {
		c.moveBackCurMessages("")
	}
	return OutcomeSuccess
}

// parseCommandArgs decodes a suggest_command call. The second return is a
// model-facing failure message, "" on success.
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
	return toolCommand{callID: tc.ID, command: a.Command}, ""
}

// parseFileReqArgs decodes a request_files call. The second return is a
// model-facing failure message, "" on success.
func parseFileReqArgs(tc llm.ToolCall) (toolFileReq, string) {
	var a struct {
		Paths  []string `json:"paths"`
		Reason string   `json:"reason"`
	}
	if err := json.Unmarshal([]byte(tc.Arguments), &a); err != nil {
		return toolFileReq{}, fmt.Sprintf("The arguments were not valid JSON: %v", err)
	}
	if len(a.Paths) == 0 {
		return toolFileReq{}, "No file paths were provided."
	}
	return toolFileReq{callID: tc.ID, paths: a.Paths}, ""
}

// runSuggestedCommand confirms and runs a proposed shell command, returning
// its output as the tool result. The command output always returns to the
// model (it answers the tool call); there is no separate add-to-chat step.
func (c *Coder) runSuggestedCommand(ctx context.Context, cmd toolCommand) string {
	if !c.SuggestShellCommands {
		return "Shell commands are disabled in this session; the command was not run."
	}
	command := strings.TrimSpace(cmd.command)
	yes, _ := c.Confirm.Confirm(ConfirmRequest{
		Prompt:              "Run shell command?",
		Subject:             command,
		ExplicitYesRequired: true,
		AllowNever:          true,
		Group:               "run-shell",
	})
	if !yes {
		return "The user chose not to run the command."
	}

	exitCode, output := c.runAndShow(ctx, command)
	return fmt.Sprintf("Command: %s\nExit status: %d\nOutput:\n%s", command, exitCode, output)
}

// addRequestedFiles confirms and adds requested files to the chat, returning
// a summary as the tool result. Files already in the chat or missing on disk
// are reported without a prompt.
func (c *Coder) addRequestedFiles(req toolFileReq) string {
	inChat := map[string]bool{}
	for _, f := range c.inchatRelativeFiles() {
		inChat[f] = true
	}

	var added, skipped []string
	for _, p := range req.paths {
		rel := strings.TrimSpace(p)
		if rel == "" {
			continue
		}
		switch {
		case inChat[rel]:
			skipped = append(skipped, rel+" (already in the chat)")
		case !c.fileExists(rel):
			skipped = append(skipped, rel+" (not found)")
		default:
			yes, _ := c.Confirm.Confirm(ConfirmRequest{
				Prompt:     "Add file to the chat?",
				Subject:    rel,
				AllowNever: true,
				Group:      "add-file",
			})
			if yes {
				c.AddFile(rel)
				added = append(added, rel)
			} else {
				skipped = append(skipped, rel+" (the user declined)")
			}
		}
	}

	var b strings.Builder
	if len(added) > 0 {
		fmt.Fprintf(&b, "Added to the chat: %s.", strings.Join(added, ", "))
	} else {
		b.WriteString("No files were added to the chat.")
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, " Not added: %s.", strings.Join(skipped, ", "))
	}
	return b.String()
}

// fileExists reports whether rel resolves to a readable file under the root.
func (c *Coder) fileExists(rel string) bool {
	info, err := os.Stat(c.absRootPath(rel))
	return err == nil && !info.IsDir()
}

// appliedPlaceholder marks a success result until the commit message is
// known.
const appliedPlaceholder = "\x00applied\x00"

// appendToolResults appends a RoleTool message per captured call, in the
// order the model produced them, so every tool_call_id is answered.
func (c *Coder) appendToolResults(results map[string]string) {
	for _, tc := range c.partialToolCalls {
		text, ok := results[tc.ID]
		if !ok || text == appliedPlaceholder {
			text = c.Prompts.FilesContentGPTEditsNoRepo // defensive default
		}
		c.curMessages = append(c.curMessages, llm.ToolResult(tc.ID, text))
	}
}

// applyToolEdits applies edit-tool calls in order against a shared overlay so
// sequential edits to one file compose, records each call's result text into
// results, and returns the edited relative paths. A search that doesn't match
// sets *matchFailure and records a focused error (with a did-you-mean) for
// that call.
func (c *Coder) applyToolEdits(edits []toolEdit, results, overwrote map[string]string, matchFailure *bool) []string {
	if len(edits) == 0 {
		return nil
	}

	fen := editblock.Fence{Open: c.fence.open, Close: c.fence.close}
	reader := diskReader{root: c.Root}
	pending := map[string]string{}
	needDirtyCommit := map[string]bool{}
	writeVerb := map[string]string{} // path -> "Created"/"Overwrote"/"Applied edit to"
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
			c.Out.Errorf("Skipping edit to %s: %s", e.path, reason)
			results[e.callID] = fmt.Sprintf("Skipped %s: %s", e.path, reason)
			continue
		}
		if !c.allowedToEdit(e.path, needDirtyCommit) {
			results[e.callID] = fmt.Sprintf("Skipped %s: not allowed or declined.", e.path)
			continue
		}

		content, exists := read(e.path)
		var newContent string
		if e.create {
			// create_file writes the whole file: create it fresh, or overwrite
			// an existing one — never the old append-on-empty-search behavior.
			newContent = e.replace
			if exists {
				overwrote[e.callID] = e.path
				writeVerb[e.path] = "Overwrote"
			} else if writeVerb[e.path] == "" {
				writeVerb[e.path] = "Created"
			}
		} else {
			var ok bool
			newContent, ok = editblock.DoReplace(e.path, content, exists, e.search, e.replace, fen)
			if !ok || newContent == "" {
				results[e.callID] = toolMatchFailure(e, content, fen)
				*matchFailure = true
				continue
			}
			if writeVerb[e.path] == "" {
				writeVerb[e.path] = "Applied edit to"
			}
		}

		if _, seen := pending[e.path]; !seen {
			writeOrder = append(writeOrder, e.path)
		}
		pending[e.path] = newContent
		if !editedSet[e.path] {
			editedSet[e.path] = true
			edited = append(edited, e.path)
		}
		results[e.callID] = appliedPlaceholder
	}

	c.dirtyCommit(needDirtyCommit)

	if len(edited) == 0 {
		return nil
	}
	if !c.DryRun {
		if err := c.writeAtomically(editblock.PlanResult{Writes: pending, WriteOrder: writeOrder}); err != nil {
			c.Out.Errorf("Exception while updating files:")
			c.Out.Errorf("%s", err.Error())
			// The batch rolled back; report each intended write as failed so
			// the model doesn't assume success.
			for _, e := range edits {
				if results[e.callID] == appliedPlaceholder {
					results[e.callID] = "The file write failed; the edit was not applied."
					*matchFailure = true
				}
			}
			return nil
		}
	}
	for _, p := range edited {
		verb := writeVerb[p]
		if verb == "" {
			verb = "Applied edit to"
		}
		if c.DryRun {
			c.Out.Printf("Did not write %s (--dry-run)", p)
		} else {
			c.Out.Printf("%s %s", verb, p)
		}
	}
	return edited
}

// toolMatchFailure builds the tool result for an edit whose search didn't
// match, including a did-you-mean when a near match exists.
func toolMatchFailure(e toolEdit, content string, fen editblock.Fence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The search text was not found in %s, so no change was made.\n", e.path)
	b.WriteString("It must match the current file contents exactly, character for character, " +
		"including all whitespace, comments, and docstrings.\n")
	if didYouMean := editblock.FindSimilarLines(e.search, content, 0.6); didYouMean != "" {
		fmt.Fprintf(&b, "\nDid you mean to match these lines from %s?\n\n%s\n%s\n%s\n",
			e.path, fen.Open, didYouMean, fen.Close)
	}
	if e.replace != "" && strings.Contains(content, e.replace) {
		fmt.Fprintf(&b, "\nThe replacement text is already present in %s; this edit may not be needed.\n", e.path)
	}
	return b.String()
}
