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
		{"btw", "<question>", "Ask a one-off question outside the chat (not added to context)", cmdBtw},
		{"clear", "", "Clear the conversation history", cmdClear},
		{"code", "[request]", "Return to editing (bare: stay in code mode)", cmdCode},
		{"diff", "", "Show the diff of changes since the last message", cmdDiff},
		{"drop", "[file ...]", "Remove files from the chat (all files if none given)", cmdDrop},
		{"exit", "", "Exit Strument", cmdExit},
		{"help", "", "Show this help", cmdHelp},
		{"ls", "", "List files in the chat", cmdLs},
		{"model", "[alias]", "Show or switch the active model", cmdModel},
		{"quit", "", "Exit Strument", cmdExit},
		{"read-only", "<file> [file ...]", "Add reference files the model must not edit", cmdReadOnly},
		{"reload", "", "Reload config.star (new models become available)", cmdReload},
		{"reset", "", "Drop all files and clear the history", cmdReset},
		{"run", "<command>", "Run a shell command; optionally add its output to the chat", cmdRun},
		{"squash", "[n]", "Combine the last n turns' commits into one (default 2)", cmdSquash},
		{"symbol", "<name> [reference]", "Find where a name is defined (or used) with the language parser", cmdSymbol},
		{"tokens", "", "Report approximate context window usage", cmdTokens},
		{"undo", "", "Undo the last turn's edits", cmdUndo},
		{"web", "<url>", "Scrape a web page and stage it for your next message", cmdWeb},
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
// addable file paths; a bare (non-command) line completes file paths in the
// message itself, aider-style. See promptCompleter in completion.go.
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
	return promptCompleter{cmd: readline.NewPrefixCompleter(items...), files: r.completePromptFiles}
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
			st, err := os.Stat(m)
			if err != nil {
				continue
			}
			if st.IsDir() {
				out = append(out, r.expandDir(m)...)
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
	return slices.Compact(out)
}

// expandDir returns the root-relative files under directory abs. In a git repo
// it uses the tracked files (so .gitignore is honored, like aider); without git
// it walks regular, non-hidden files and warns that the set is unfiltered.
func (r *REPL) expandDir(abs string) []string {
	relDir, err := filepath.Rel(r.coder.Root, abs)
	if err != nil || strings.HasPrefix(relDir, "..") {
		r.out.Warningf("Skipping %s: outside the project root.", abs)
		return nil
	}
	relDir = filepath.ToSlash(relDir)

	if tracked := r.coder.TrackedFiles(); tracked != nil {
		prefix := relDir + "/"
		if relDir == "." {
			prefix = ""
		}
		var out []string
		for _, f := range tracked {
			if prefix == "" || strings.HasPrefix(f, prefix) {
				out = append(out, f)
			}
		}
		if len(out) == 0 {
			r.out.Warningf("No tracked files under %s.", relDir)
		}
		return out
	}

	r.out.Warningf("Adding %s without a git repo: files are not gitignore-filtered.", relDir)
	var out []string
	_ = filepath.WalkDir(abs, func(p string, de os.DirEntry, err error) error {
		switch {
		case err != nil:
			// Skip an unreadable entry and keep walking the rest.
		case de.IsDir():
			if p != abs && strings.HasPrefix(de.Name(), ".") {
				return filepath.SkipDir
			}
		case strings.HasPrefix(de.Name(), "."):
			// Skip hidden files.
		default:
			if rel, relErr := filepath.Rel(r.coder.Root, p); relErr == nil && !strings.HasPrefix(rel, "..") {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		//nolint:nilerr // a per-entry walk error is intentionally skipped, not propagated
		return nil
	})
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

	for _, pat := range splitArgs(args) {
		// DropUnder handles an exact path or a whole directory subtree; the glob
		// loop then handles wildcard patterns against what remains.
		dropped := r.coder.DropUnder(pat)
		for _, rel := range append(r.coder.ChatFiles(), r.coder.ReadOnlyFiles()...) {
			if ok, _ := filepath.Match(pat, rel); ok {
				if r.coder.DropFile(rel) {
					dropped = append(dropped, rel)
				}
			}
		}
		for _, rel := range dropped {
			r.printf("Dropped %s from the chat.", rel)
		}
		if len(dropped) == 0 {
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

// cmdSymbol is the human's door to the same lookup the model has. It replaced
// /map: once the repo map left the prompt, the ranked digest was a thing to
// read once on an unfamiliar repository, while "where is this defined" is a
// thing to ask constantly — and the answer names the enclosing function, which
// is what the reader wanted from the map and never got.
func cmdSymbol(_ context.Context, r *REPL, args string) string {
	name, kind, _ := strings.Cut(args, " ")
	if strings.TrimSpace(name) == "" {
		r.out.Errorf("Usage: /symbol <name> [reference]")
		return ""
	}
	text, _, problem := r.coder.SymbolLookup(name, strings.TrimSpace(kind))
	if problem != "" {
		r.out.Errorf("%s", problem)
		return ""
	}
	r.printf("%s", strings.TrimRight(text, "\n"))
	return ""
}

func cmdModel(_ context.Context, r *REPL, args string) string {
	if r.opts.Config == nil {
		r.out.Errorf("No configuration loaded; /model is unavailable.")
		return ""
	}
	aliases := slices.Sorted(maps.Keys(r.opts.Config.Models))
	if args == "" {
		r.printf("Active model: %s (%s).", r.opts.ModelAlias, r.coder.Model.QualifiedSlug())
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
		r.coder.Summarizer = coder.NewChatSummary(r.opts.MakeClient(m.WeakModel), m.WeakModel, r.coder.Tokens)
	}
	r.opts.ModelAlias = args
	r.printf("Switched to model %s (%s).", args, m.QualifiedSlug())
	return ""
}

func cmdBtw(ctx context.Context, r *REPL, args string) string {
	if args == "" {
		r.out.Errorf("Usage: /btw <question>")
		return ""
	}
	// A one-off, general-assistant question: the coder answers it outside the
	// chat (no files, no history, no dev prompt) and nothing is recorded.
	r.runAside(ctx, args)
	return ""
}

func cmdReload(_ context.Context, r *REPL, _ string) string {
	if r.opts.ReloadConfig == nil {
		r.out.Errorf("Config reload is unavailable.")
		return ""
	}
	cfg, err := r.opts.ReloadConfig()
	if err != nil {
		// Keep the running config; a half-loaded session is worse than a stale
		// one.
		r.out.Errorf("Config not reloaded (keeping the current one): %v", err)
		return ""
	}
	r.opts.Config = cfg

	// Re-resolve the active alias so edits to that model take effect; if it was
	// removed, keep the running model rather than stranding the session.
	if m, ok := cfg.Models[r.opts.ModelAlias]; ok {
		r.coder.SetModel(m)
		r.refreshTrailer(m)
		if r.opts.MakeClient != nil {
			r.coder.Client = r.opts.MakeClient(m)
			r.coder.Summarizer = coder.NewChatSummary(r.opts.MakeClient(m.WeakModel), m.WeakModel, r.coder.Tokens)
		}
	} else {
		r.out.Warningf("Active model %q is no longer in the config; keeping the running model.", r.opts.ModelAlias)
	}
	r.printf("Config reloaded. Models: %s.", strings.Join(slices.Sorted(maps.Keys(cfg.Models)), ", "))
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

// cmdWeb scrapes a URL and adds its content to the chat as a completed exchange
// (the same path /run uses for command output), so it's context for your next
// message without re-scanning the page's own links or firing a turn.
func cmdWeb(ctx context.Context, r *REPL, args string) string {
	url := strings.TrimSpace(args)
	if url == "" {
		r.out.Errorf("Usage: /web <url>")
		return ""
	}
	if r.coder.Scrape == nil {
		r.out.Errorf("Scraping is not available.")
		return ""
	}
	r.printf("Scraping %s...", url)
	content, err := r.coder.Scrape(ctx, url)
	if err != nil {
		r.out.Errorf("Unable to fetch %s: %v", url, err)
		return ""
	}
	r.coder.AppendExchange(content, "Ok")
	r.printf("Added %s to the chat.", url)
	return ""
}
