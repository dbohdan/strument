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

	"github.com/ergochat/readline"

	"github.com/dbohdan/strument/internal/coder"
)

// command is one slash command. run returns a message to send to the model
// ("" for none) — the §1.4 contract: commands own their I/O and may just
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
		{"clear", "", "Clear the conversation history", cmdClear},
		{"diff", "", "Show the diff of the last change (git mode, phase 8)", cmdDiff},
		{"drop", "[file ...]", "Remove files from the chat (all files if none given)", cmdDrop},
		{"exit", "", "Exit strument", cmdExit},
		{"help", "", "Show this help", cmdHelp},
		{"ls", "", "List files in the chat", cmdLs},
		{"map", "", "Print the current repository map", cmdMap},
		{"model", "[alias]", "Show or switch the active model", cmdModel},
		{"quit", "", "Exit strument", cmdExit},
		{"read-only", "<file> [file ...]", "Add reference files the model must not edit", cmdReadOnly},
		{"reset", "", "Drop all files and clear the history", cmdReset},
		{"run", "<command>", "Run a shell command; optionally add its output to the chat", cmdRun},
		{"tokens", "", "Report approximate context window usage", cmdTokens},
		{"undo", "", "Undo the last strument commit (git mode, phase 8)", cmdUndo},
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
			sub = append(sub, readline.PcItemDynamic(r.completeAddable))
		case "drop":
			sub = append(sub, readline.PcItemDynamic(chatFiles))
		case "model":
			sub = append(sub, readline.PcItemDynamic(r.completeAliases))
		}
		items = append(items, readline.PcItem("/"+c.name, sub...))
	}
	return readline.NewPrefixCompleter(items...)
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
	for _, rel := range r.expandPatterns(strings.Fields(args)) {
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
	for _, rel := range r.expandPatterns(strings.Fields(args)) {
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
	for pat := range strings.FieldsSeq(args) {
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

	yes, _ := r.Confirmer().Confirm(coder.ConfirmRequest{
		Prompt:     "Add command output to the chat?",
		AllowNever: true,
		Group:      "add-output",
	})
	if yes {
		// The §6.2/§6.3 result shape, so /run context reads like
		// model-proposed shell output.
		result := fmt.Sprintf("Command: %s\nExit status: %d\nOutput:\n%s", args, exitCode, output)
		r.coder.AppendExchange(result, "Ok")
		r.printf("Added the command output to the chat.")
	}
	return ""
}

func cmdUndo(_ context.Context, r *REPL, _ string) string {
	r.out.Warningf("/undo arrives with git mode (phase 8).")
	return ""
}

func cmdDiff(_ context.Context, r *REPL, _ string) string {
	r.out.Warningf("/diff arrives with git mode (phase 8).")
	return ""
}
