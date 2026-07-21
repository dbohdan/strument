package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dbohdan.com/strument/internal/editblock"
	"dbohdan.com/strument/internal/llm"
)

// Tool names for the "tool" edit format.
const (
	toolReplaceInFile = "replace_in_file"
	toolCreateFile    = "create_file"
)

// editTools is the function-tool set offered to the model in the "tool" edit
// format. These edits apply directly — like a SEARCH/REPLACE block, not a
// proposal — with git auto-commit and /undo as the safety net.
func editTools() []llm.ToolDef {
	strProp := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
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
			Description: "Create a new file with the given contents. The file is created " +
				"immediately and committed to git.",
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

// toolDefs is the function-tool set offered to the model this turn. Commit 5
// adds suggest_command and request_files here.
func (c *Coder) toolDefs() []llm.ToolDef {
	return editTools()
}

// toolEdit is one edit-tool call resolved to an editblock edit plus the call
// id it answers.
type toolEdit struct {
	callID  string
	path    string
	search  string
	replace string
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
		return toolEdit{callID: tc.ID, path: a.Path, search: "", replace: a.Content}, ""
	default: // toolReplaceInFile
		return toolEdit{callID: tc.ID, path: a.Path, search: a.Search, replace: a.Replace}, ""
	}
}

// applyToolCalls dispatches the captured tool calls of a "tool"-format turn.
// Every call gets a tool result message appended to curMessages so the wire
// protocol stays well-formed on the next send. Edits apply directly through
// the same replace primitive as SEARCH/REPLACE; a call whose search does not
// match gets its error as that call's tool result and the turn re-sends
// (reflection) without a synthetic user turn.
func (c *Coder) applyToolCalls(_ context.Context) SendOutcome {
	if len(c.partialToolCalls) == 0 {
		return OutcomeSuccess
	}

	var edits []toolEdit
	results := map[string]string{} // call id -> result text
	matchFailure := false

	for _, tc := range c.partialToolCalls {
		switch tc.Name {
		case toolReplaceInFile, toolCreateFile:
			e, msg := parseEditArgs(tc)
			if msg != "" {
				results[tc.ID] = msg
				matchFailure = true
				continue
			}
			edits = append(edits, e)
		default:
			results[tc.ID] = fmt.Sprintf("Unknown tool %q.", tc.Name)
		}
	}

	edited := c.applyToolEdits(edits, results, &matchFailure)

	// Commit applied edits so /undo has a clean base and the tool result can
	// name the commit.
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
	// Fill in the success results now that the commit (if any) is known.
	for id, text := range results {
		if text == appliedPlaceholder {
			results[id] = saved
		}
	}

	// Append one tool result per call, in call order, then rotate or reflect.
	c.appendToolResults(results)

	if matchFailure {
		c.toolContinuation = true
		return OutcomeReflect
	}
	if len(edited) > 0 {
		c.moveBackCurMessages("")
	}
	return OutcomeSuccess
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
func (c *Coder) applyToolEdits(edits []toolEdit, results map[string]string, matchFailure *bool) []string {
	if len(edits) == 0 {
		return nil
	}

	fen := editblock.Fence{Open: c.fence.open, Close: c.fence.close}
	reader := diskReader{root: c.Root}
	pending := map[string]string{}
	needDirtyCommit := map[string]bool{}
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
		newContent, ok := editblock.DoReplace(e.path, content, exists, e.search, e.replace, fen)
		if !ok || newContent == "" {
			results[e.callID] = toolMatchFailure(e, content, fen)
			*matchFailure = true
			continue
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
		if c.DryRun {
			c.Out.Printf("Did not apply edit to %s (--dry-run)", p)
		} else {
			c.Out.Printf("Applied edit to %s", p)
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
