package coder

import (
	"fmt"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/repomap"
	"dbohdan.com/strument/internal/workspace"
)

// Inspector runs the observation tools — read, grep, glob, ls, and symbol, the
// ones doc/README.md means by "observation is free". It holds everything they
// need and nothing they don't: no model, no client, no chat state. That is why
// they can be driven from a command line as readily as from a turn, which is
// what `strument tool …` does.
//
// Not "Observer". That name belongs to subscribe-and-notify, and this is the
// opposite arrangement: nothing here is told anything, it goes and looks.
//
// The split follows SymbolLookup's reasoning one layer out. That was exported
// so /symbol and the model share one answer, because the answer to "where is
// this defined" does not depend on who asked; neither does the answer to "what
// is in this file" or "where does this text appear".
type Inspector struct {
	// Root is the project root the paths in a result are relative to.
	Root string
	// Files is the workspace behind read, grep, glob, and ls.
	Files *workspace.Workspace
	// RepoMap backs symbol. nil is legitimate — the tool then answers that the
	// parser is unavailable rather than failing.
	RepoMap *repomap.RepoMap
	// Out receives the one-line outcome. Never nil; use DiscardReporter to
	// throw it away.
	Out ToolReporter
	// Observe is called with the project-relative path of each file a read
	// answered, so the caller can record the version the model was shown. nil
	// when nobody is tracking — the REPL's own /symbol lookups, say.
	Observe func(rel string)
	// AnchorRows renders a read window as anchored rows, or "" to keep the
	// numbered format. It takes only the window bounds and reads the file
	// itself, because an anchor is an identity within a file rather than within
	// a window: reading lines 40-60 twice must give those lines the same
	// anchors, and reading the whole file afterwards must agree with both, so
	// the registry has to see every line either way.
	AnchorRows func(rel string, start, count int) string
}

// ToolReporter is the outcome line and nothing else — the narrow half of Output
// that the observation tools use. coder.Output satisfies it, and so does
// anything a caller wants to route those lines through.
type ToolReporter interface {
	Toolf(format string, args ...any)
}

// DiscardReporter drops outcome lines, for a caller that wants only the result.
type DiscardReporter struct{}

func (DiscardReporter) Toolf(string, ...any) {}

// Run answers one tool call and returns the text a model would receive for it —
// refusal sentences, truncation notes, and clip markers included, because those
// are part of the answer rather than decoration around it.
//
// It takes the call in the shape it arrives in rather than as typed arguments
// on purpose. A second, friendlier entry point would be a second interpretation
// of "mode" or "glob", and the whole value of reaching these from outside a
// turn is that what you see is what the model saw.
func (i *Inspector) Run(name, argsJSON string) string {
	tc := llm.ToolCall{Name: name, Arguments: argsJSON}
	switch name {
	case toolRead:
		return i.runRead(tc)
	case toolGrep:
		return i.runGrep(tc)
	case toolGlob:
		return i.runGlob(tc)
	case toolLS:
		return i.runLS(tc)
	case toolSymbol:
		return i.runSymbol(tc)
	default:
		// The dispatch's own wording for a name it does not know, so the two
		// agree about what an unknown tool is called.
		return fmt.Sprintf("Unknown tool %q.", name)
	}
}

// InspectorTools names the tools Run accepts, for a caller building a command
// line or a help message over them.
func InspectorTools() []string {
	return []string{toolRead, toolGrep, toolGlob, toolLS, toolSymbol}
}

// inspector is the Coder's own view of itself as one. The observation tools are
// methods on Inspector now, and these fields are the whole of what they read.
func (c *Coder) inspector() *Inspector {
	return &Inspector{
		Root: c.Root, Files: c.Files, RepoMap: c.RepoMap, Out: c.Out,
		Observe:    func(rel string) { c.shown.note(rel, c.fullPath(rel)) },
		AnchorRows: c.anchorRows,
	}
}

// The Coder's observation methods delegate. They stay because the tool dispatch
// and the REPL call them by these names, and because a Coder is the thing that
// has an Out to report through.
func (c *Coder) runRead(tc llm.ToolCall) string   { return c.inspector().runRead(tc) }
func (c *Coder) runGrep(tc llm.ToolCall) string   { return c.inspector().runGrep(tc) }
func (c *Coder) runGlob(tc llm.ToolCall) string   { return c.inspector().runGlob(tc) }
func (c *Coder) runLS(tc llm.ToolCall) string     { return c.inspector().runLS(tc) }
func (c *Coder) runSymbol(tc llm.ToolCall) string { return c.inspector().runSymbol(tc) }

// SymbolLookup is the REPL's door to the same answer, kept on Coder because
// /symbol has one of those and not an Inspector. See the Inspector method.
func (c *Coder) SymbolLookup(rawName, kind string) (text string, count int, problem string) {
	return c.inspector().SymbolLookup(rawName, kind)
}
