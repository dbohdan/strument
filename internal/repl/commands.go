package repl

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/readline"
)

// command is one slash command. run returns a message to send to the model
// ("" for none) — the contract: commands own their I/O and may just
// mutate state.
type command struct {
	name string
	args string // help placeholder, e.g. "<file> [file ...]"
	help string
	run  func(ctx context.Context, r *REPL, args string) string
}

// quitSentinel distinguishes /exit from "no message to send".
const quitSentinel = "\x00quit"

// commands is populated in init: cmdHelp iterates the table it lives in,
// which a composite-literal initializer would make a cycle.
var commands []command

func init() {
	commands = []command{
		{"add", "<file> [file ...]", "Add files to the chat (globs allowed)", cmdAdd},
		{"ask", "[question]", "Ask about the code without editing (bare: stay in ask mode)", cmdAsk},
		{"clear", "", "Clear the conversation history", cmdClear},
		{"code", "[request]", "Return to editing (bare: stay in code mode)", cmdCode},
		{"diff", "", "Show the diff of changes since the last message", cmdDiff},
		{"drop", "[file ...]", "Remove files from the chat (all files if none given)", cmdDrop},
		{"exit", "", "Exit Strument", cmdExit},
		{"help", "", "Show this help", cmdHelp},
		{"ls", "", "List files in the chat", cmdLs},
		{"map", "", "Print the current repository map", cmdMap},
		{"model", "[alias]", "Show or switch the active model", cmdModel},
		{"quit", "", "Exit Strument", cmdExit},
		{"read-only", "<file> [file ...]", "Add reference files the model must not edit", cmdReadOnly},
		{"reset", "", "Drop all files and clear the history", cmdReset},
		{"run", "<command>", "Run a shell command; optionally add its output to the chat", cmdRun},
		{"tokens", "", "Report approximate context window usage", cmdTokens},
		{"undo", "", "Undo the last Strument auto-commit", cmdUndo},
	}
}

func findCommand(name string) *command {
	for i := range commands {
		if commands[i].name == name {
			return &commands[i]
		}
	}
	return nil
}

// dispatch runs a "/..." input line. It returns the message the command
// wants sent (usually "") and whether the REPL should exit.
func (r *REPL) dispatch(ctx context.Context, line string) (msg string, quit bool) {
	name, args, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	args = strings.TrimSpace(args)

	cmd := findCommand(name)
	if cmd == nil {
		r.out.Errorf("Invalid command: /%s. Use /help to list commands.", name)
		return "", false
	}
	out := cmd.run(ctx, r, args)
	if out == quitSentinel {
		return "", true
	}
	return out, false
}

// completer offers command names and, under the file commands, chat or
// addable file paths.
func (r *REPL) completer() readline.AutoCompleter {
	chatFiles := func(string) []string { return r.coder.ChatFiles() }
	items := make([]*readline.PrefixCompleter, 0, len(commands))
	for _, c := range commands {
		var sub []*readline.PrefixCompleter
		switch c.name {
		case "add", "read-only":
			sub = append(sub, recursiveDynamic(r.completeAddable))
		case "drop":
			sub = append(sub, recursiveDynamic(chatFiles))
		case "model":
			sub = append(sub, readline.PcItemDynamic(r.completeAliases))
		}
		items = append(items, readline.PcItem("/"+c.name, sub...))
	}
	return readline.NewPrefixCompleter(items...)
}

// recursiveDynamic builds a dynamic completer that re-offers itself for each
// subsequent argument. Without a child to descend into, the prefix-completer
// tree stops after one token, so /add and /drop could only complete a single
// file; making the node its own child lets completion continue across all
// whitespace-separated arguments.
func recursiveDynamic(cb func(string) []string) *readline.PrefixCompleter {
	d := readline.PcItemDynamic(cb)
	d.SetChildren([]*readline.PrefixCompleter{d})
	return d
}

func (r *REPL) completeAddable(string) []string {
	matches, _ := filepath.Glob(filepath.Join(r.coder.Root, "*"))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, err := filepath.Rel(r.coder.Root, m)
		if err == nil {
			out = append(out, rel)
		}
	}
	return out
}

func (r *REPL) completeAliases(string) []string {
	if r.opts.Config == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(r.opts.Config.Models))
}

func cmdHelp(_ context.Context, r *REPL, _ string) string {
	width := 0
	for _, c := range commands {
		if n := len(c.name) + 1 + len(c.args); n > width {
			width = n
		}
	}
	for _, c := range commands {
		left := "/" + c.name
		if c.args != "" {
			left += " " + c.args
		}
		r.printf("  %-*s  %s", width+1, left, c.help)
	}
	r.printf("Anything else is sent to the model.")
	return ""
}

func cmdExit(_ context.Context, _ *REPL, _ string) string { return quitSentinel }

// cmdAsk / cmdCode switch the active edit format (commands.py:1182-1229).
// Bare = persistent switch until the mirror command; with args = one-shot
// (run once in the target format, then restore). History is shared by
// construction, so an /ask answer is in context for the next /code turn —
// the whole point.
func cmdAsk(_ context.Context, r *REPL, args string) string {
	return r.switchFormat("ask", args)
}

func cmdCode(_ context.Context, r *REPL, args string) string {
	return r.switchFormat("", args) // "" restores the model's default format
}

func (r *REPL) switchFormat(target, args string) string {
	if args == "" {
		r.coder.SetEditFormat(target)
		r.rl.SetPrompt(r.prompt())
		if r.coder.EditFormat() == "ask" {
			r.printf("Ask mode: I will answer questions without editing files. Use /code to switch back.")
		} else {
			r.printf("Code mode: I will edit files again.")
		}
		return ""
	}
	// One-shot: remember the current format, switch, send the args, and
	// restore after the turn (in REPL.Run).
	r.oneShotRestore = r.coder.EditFormat()
	r.oneShotPending = true
	r.coder.SetEditFormat(target)
	return args
}

// expandPatterns resolves glob patterns relative to the coder root,
// returning root-relative paths of existing regular files.
func (r *REPL) expandPatterns(patterns []string) []string {
	var out []string
	for _, pat := range patterns {
		abs := pat
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(r.coder.Root, pat)
		}
		matches, err := filepath.Glob(abs)
		if err != nil || len(matches) == 0 {
			r.out.Warningf("No files matched %q.", pat)
			continue
		}
		for _, m := range matches {
			if st, err := os.Stat(m); err != nil || st.IsDir() {
				continue
			}
			rel, err := filepath.Rel(r.coder.Root, m)
			if err != nil || strings.HasPrefix(rel, "..") {
				r.out.Warningf("Skipping %s: outside the project root.", m)
				continue
			}
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out
}

func cmdAdd(_ context.Context, r *REPL, args string) string {
	if args == "" {
		r.out.Errorf("Usage: /add <file> [file ...]")
		return ""
	}
	for _, rel := range r.expandPatterns(splitArgs(args)) {
		r.coder.AddFile(rel)
		r.printf("Added %s to the chat.", rel)
	}
	return ""
}

func cmdReadOnly(_ context.Context, r *REPL, args string) string {
	if args == "" {
		r.out.Errorf("Usage: /read-only <file> [file ...]")
		return ""
	}
	for _, rel := range r.expandPatterns(splitArgs(args)) {
		r.coder.AddReadOnlyFile(rel)
		r.printf("Added %s to the chat (read-only).", rel)
	}
	return ""
}

func cmdDrop(_ context.Context, r *REPL, args string) string {
	if args == "" {
		r.coder.DropAll()
		r.printf("Dropped all files from the chat.")
		return ""
	}

	inChat := append(r.coder.ChatFiles(), r.coder.ReadOnlyFiles()...)
	for _, pat := range splitArgs(args) {
		dropped := false
		for _, rel := range inChat {
			if ok, _ := filepath.Match(pat, rel); ok || rel == pat {
				if r.coder.DropFile(rel) {
					r.printf("Dropped %s from the chat.", rel)
					dropped = true
				}
			}
		}
		if !dropped {
			r.out.Warningf("No chat files matched %q.", pat)
		}
	}
	return ""
}

func cmdLs(_ context.Context, r *REPL, _ string) string {
	chat := r.coder.ChatFiles()
	ro := r.coder.ReadOnlyFiles()
	if len(chat) == 0 && len(ro) == 0 {
		r.printf("No files in the chat.")
		return ""
	}
	if len(chat) > 0 {
		r.printf("Files in the chat:")
		for _, f := range chat {
			r.printf("  %s", f)
		}
	}
	if len(ro) > 0 {
		r.printf("Read-only files:")
		for _, f := range ro {
			r.printf("  %s", f)
		}
	}
	return ""
}

func cmdClear(_ context.Context, r *REPL, _ string) string {
	r.coder.ClearHistory()
	r.printf("Chat history cleared.")
	return ""
}

func cmdReset(_ context.Context, r *REPL, _ string) string {
	r.coder.DropAll()
	r.coder.ClearHistory()
	r.printf("Dropped all files and cleared the chat history.")
	return ""
}

func cmdTokens(_ context.Context, r *REPL, _ string) string {
	r.printf("%s", r.coder.TokensReport())
	return ""
}

func cmdMap(_ context.Context, r *REPL, _ string) string {
	content := r.coder.RepoMapNow()
	if content == "" {
		r.printf("No repository map (disabled for this model, or no mappable files).")
		return ""
	}
	r.printf("%s", content)
	return ""
}

func cmdModel(_ context.Context, r *REPL, args string) string {
	if r.opts.Config == nil {
		r.out.Errorf("No configuration loaded; /model is unavailable.")
		return ""
	}
	aliases := slices.Sorted(maps.Keys(r.opts.Config.Models))
	if args == "" {
		r.printf("Active model: %s (%s).", r.opts.ModelAlias, r.coder.Model.Slug)
		r.printf("Available aliases: %s.", strings.Join(aliases, ", "))
		return ""
	}

	m, ok := r.opts.Config.Models[args]
	if !ok {
		r.out.Errorf("Unknown model alias %q (aliases: %s).", args, strings.Join(aliases, ", "))
		return ""
	}
	r.coder.SetModel(m)
	r.refreshTrailer(m)
	if r.opts.MakeClient != nil {
		r.coder.Client = r.opts.MakeClient(m)
	}
	r.opts.ModelAlias = args
	r.printf("Switched to model %s (%s).", args, m.Slug)
	return ""
}

func cmdRun(ctx context.Context, r *REPL, args string) string {
	if args == "" {
		r.out.Errorf("Usage: /run <command>")
		return ""
	}

	runner := r.coder.Runner
	if runner == nil {
		runner = coder.PipeRunner{}
	}
	exitCode, output, err := runner.Run(ctx, args, r.coder.Root)
	if err != nil {
		r.out.Errorf("Error running command: %v", err)
		return ""
	}
	if output != "" {
		r.printf("%s", strings.TrimRight(output, "\n"))
	}
	if exitCode != 0 {
		r.out.Warningf("Exit status: %d", exitCode)
	}

	// A successful command that produced no output has nothing to add, so
	// don't bother asking. A non-zero exit still offers — the status is
	// informative context even without output.
	if strings.TrimSpace(output) == "" && exitCode == 0 {
		return ""
	}

	yes, _ := r.Confirmer().Confirm(coder.ConfirmRequest{
		Prompt:     "Add command output to the chat?",
		AllowNever: true,
		Group:      "add-output",
	})
	if yes {
		// The result shape, so /run context reads like
		// model-proposed shell output.
		result := fmt.Sprintf("Command: %s\nExit status: %d\nOutput:\n%s", args, exitCode, output)
		r.coder.AppendExchange(result, "Ok")
		r.printf("Added the command output to the chat.")
	}
	return ""
}
