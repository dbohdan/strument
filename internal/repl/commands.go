package repl

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/origin"
	"dbohdan.com/strument/internal/prompts"
	"dbohdan.com/strument/internal/readline"
	"dbohdan.com/strument/internal/workspace"
)

// command is one slash command. run returns a message to send to the model
// ("" for none) — the contract: commands own their I/O and may just
// mutate state.
type command struct {
	name string
	args string // argument syntax, e.g. "<file> ..."; see the notation below
	help string
	run  func(ctx context.Context, r *REPL, args string) string
}

// The notation in args, which TestCommandArgsNotation enforces:
//
//	<x>        a required argument
//	[x]        an optional one
//	...        the argument before it may repeat
//	a | b      alternatives; bare words are literal keywords
//
// Positional and rest-of-the-line arguments are told apart by what the
// metavariable names rather than by a fourth mark, because three marks are
// already as much notation as a help screen can carry.
//
// A metavariable naming a *word* — <file>, <name>, <alias>, <url>, <n> — is one
// argument among several, and `...` can repeat it.
//
// Only <file> is tokenized, and this comment used to claim otherwise: it said
// the line is split on whitespace so a value with a space has to be quoted,
// which is true of /add, /read-only, /drop, and /submit and of nothing else.
// The rest take the trimmed remainder, so `/model "test"` looks up an alias
// spelled with the quotes in it and answers `Unknown model alias "\"test\""`.
// Measured, not read. TestQuotingAppliesToFileArgumentsOnly holds the line
// where it actually is, in both directions, so the next person to widen it has
// to mean to.
//
// A metavariable naming *text* — <question>, <request>, <command> — takes the
// rest of the line exactly as typed. It follows that such an argument is always
// last and never carries `...`, since nothing can come after "everything else";
// the test checks that, which is what keeps the distinction honest rather than
// merely stated.
var restOfLine = []string{"command", "question", "request"}

// quitSentinel distinguishes /exit from "no message to send".
const quitSentinel = "\x00quit"

// commands is populated in init: cmdHelp iterates the table it lives in,
// which a composite-literal initializer would make a cycle.
var commands []command

func init() {
	commands = []command{
		{"add", "<file> ...", "Pin the files this session is about (globs allowed)", cmdAdd},
		{"ask", "[<question>]", "Ask about the code without editing (bare: stay in ask mode)", cmdAsk},
		{"btw", "<question>", "Ask a one-off question outside the chat (not added to context)", cmdBtw},
		{"check", "[<name>]", "Run a project check; optionally add its output to the chat", cmdCheck},
		{"clear", "", "Clear the conversation history", cmdClear},
		{"code", "[<request>]", "Return to editing (bare: stay in code mode)", cmdCode},
		{"context", "[<n>]", "Show the folded chat history as the model sees it (first n summaries)", cmdContext},
		{"diff", "", "Show the diff of changes since the last message", cmdDiff},
		{"drop", "[<file> ...]", "Unpin files (all if none given)", cmdDrop},
		{"env", "[add <name> ... | drop <name> ... | reset]", "Show or change (this session) what environment variables model-run commands see", cmdEnv},
		{"exit", "", "Exit Strument", cmdExit},
		{"help", "", "Show this help", cmdHelp},
		{"ls", "", "List the pinned files", cmdLs},
		{"model", "[<alias>]", "Show or switch the active model", cmdModel},
		{"notes", "[generate | drop]", "Show, regenerate, or discard the session notes", cmdNotes},
		{"quit", "", "Exit Strument", cmdExit},
		{"read-only", "<file> ...", "Pin files the model may read but never edit (may be outside the project)", cmdReadOnly},
		{"reload", "", "Reload config.star (new models become available)", cmdReload},
		{"reset", "", "Unpin everything, clear the history, and forget approved origins", cmdReset},
		{"run", "<command>", "Run a shell command; optionally add its output to the chat", cmdRun},
		{"sandbox", "", "Show whether writes are confined, and to where", cmdSandbox},
		{"skill", "[<name>]", "Show the skills this session found, or add one's instructions to the chat", cmdSkill},
		{"squash", "[<n>]", "Combine the last n turns' commits into one (default 2)", cmdSquash},
		{"submit", "<file>", "Send a file's contents as your message", cmdSubmit},
		{"symbol", "<name> [definition | reference]", "Find where a name is defined (or used) with the language parser", cmdSymbol},
		{"tokens", "", "Report approximate context window usage", cmdTokens},
		{"undo", "", "Undo the last turn's edits", cmdUndo},
		{"web", "[<url> | allow <origin> | drop <origin> | reset]", "Fetch a web page (or one #section of it); bare, show which origins webfetch may reach unasked", cmdWeb},
	}
}

// saveResume records the session state a restart would otherwise make the user
// retype. Called from every command that changes it; a no-op when the session
// leaves no trace.
func (r *REPL) saveResume() {
	if r.opts.SaveResume != nil {
		r.opts.SaveResume(r.opts.ModelAlias)
	}
}

// usage renders one command's line from the same table /help prints, so the
// syntax a command quotes back at a bad invocation cannot drift from the syntax
// the help screen documents. It used to be spelled out twice per command, and
// the two spellings had already parted ways.
func usage(name string) string {
	c := findCommand(name)
	if c == nil || c.args == "" {
		// Unreachable: every caller names its own command, and a command with
		// no arguments has nothing to be wrong about.
		return "Usage: /" + name
	}
	return "Usage: /" + c.name + " " + c.args
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
	pinnedFiles := func(string) []string {
		return append(r.coder.ChatFiles(), r.coder.ReadOnlyFiles()...)
	}
	items := make([]*readline.PrefixCompleter, 0, len(commands))
	for _, c := range commands {
		var sub []*readline.PrefixCompleter
		switch c.name {
		case "add":
			sub = append(sub, recursiveDynamic(r.completeAddable))
		case "submit", "read-only":
			// Arbitrary filesystem paths, absolute included: /read-only's
			// documented purpose is material outside the project, and /submit
			// reads a drafted prompt from anywhere. completeAddable's flat
			// root listing cannot offer those.
			sub = append(sub, recursiveDynamic(r.completePaths))
		case "check":
			sub = append(sub, readline.PcItemDynamic(r.completeChecks))
		case "drop":
			sub = append(sub, recursiveDynamic(pinnedFiles))
		case "env":
			// /env takes a subcommand first, then any number of names: add
			// offers set variables the allowlist does not yet pass, drop offers
			// set variables that the allowlist currently does pass.
			envAdd := func(string) []string {
				var out []string
				for _, kv := range os.Environ() {
					name, _, _ := strings.Cut(kv, "=")
					if !coder.EnvAllowed(name, r.coder.EnvAllow) {
						out = append(out, name)
					}
				}
				return slices.Sorted(slices.Values(out))
			}
			envDrop := func(string) []string {
				seen := map[string]bool{}
				var out []string
				for _, kv := range os.Environ() {
					name, _, _ := strings.Cut(kv, "=")
					if coder.EnvAllowed(name, r.coder.EnvAllow) && !seen[name] {
						seen[name] = true
						out = append(out, name)
					}
				}
				return slices.Sorted(slices.Values(out))
			}
			names := readline.PcItemDynamic(envAdd)
			namesDrop := readline.PcItemDynamic(envDrop)
			names.SetChildren([]*readline.PrefixCompleter{names})
			namesDrop.SetChildren([]*readline.PrefixCompleter{namesDrop})
			sub = append(sub,
				readline.PcItem("add", names),
				readline.PcItem("drop", namesDrop),
				readline.PcItem("reset"),
			)
		case "model":
			sub = append(sub, readline.PcItemDynamic(r.completeAliases))
		case "skill":
			sub = append(sub, readline.PcItemDynamic(r.completeSkills))
		case "web":
			// A URL is not completable. "allow" gets no argument completion
			// either — offering the approved origins there would complete the
			// one argument with no work left to do — but "drop" gets exactly
			// that list, which is the same split /env makes between add and
			// drop.
			sub = append(sub,
				readline.PcItem("allow"),
				readline.PcItem("drop", readline.PcItemDynamic(r.completeSessionOrigins)),
				readline.PcItem("reset"),
			)
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

// completeSessionOrigins offers what "/web drop" can actually withdraw: this
// session's grants, not the config allowlist, which the command cannot change.
func (r *REPL) completeSessionOrigins(string) []string {
	return r.coder.SessionOrigins()
}

func (r *REPL) completeChecks(string) []string {
	names := make([]string, 0, len(r.coder.Check))
	for _, ch := range r.coder.Check {
		names = append(names, ch.Name)
	}
	return names
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
	r.printf("\nArguments: <required>, [optional], \"...\" repeats.")
	r.printf("Quote a <file> argument that has spaces.")
	r.printf("<command>, <question>, and <request> take the rest of the line.")
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
			r.printf("Ask mode: the model will answer questions without editing files. Use /code to switch back.")
		} else {
			r.printf("Code mode: the model will edit files again.")
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

// expandPatterns resolves glob patterns relative to the coder root, returning
// root-relative paths of existing regular files.
//
// outside allows matches that fall outside the project, returned absolute. Only
// /read-only passes it: reference material is the one thing the workspace tools
// cannot reach, since read, grep, glob, and ls are all scoped to the root, so
// pinning it is the only channel there is. Editable out-of-project files stay
// refused — that is the direction where a mistake is worst.
func (r *REPL) expandPatterns(patterns []string, outside bool) []string {
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
			if err != nil || workspace.EscapesRoot(rel) {
				if !outside {
					r.out.Warningf("Skipping %s: outside the project root. Pin it with /read-only instead.", r.coder.DisplayPath(m))
					continue
				}
				abs, err := filepath.Abs(m)
				if err != nil {
					continue
				}
				out = append(out, abs)
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
	if err != nil || workspace.EscapesRoot(relDir) {
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
			if rel, relErr := filepath.Rel(r.coder.Root, p); relErr == nil && !workspace.EscapesRoot(rel) {
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
		r.out.Errorf("%s", usage("add"))
		return ""
	}
	for _, rel := range r.expandPatterns(splitArgs(args), false) {
		r.coder.AddFile(rel)
		r.printf("Pinned %s.", r.coder.DisplayPath(rel))
	}
	r.saveResume()
	return ""
}

func cmdReadOnly(_ context.Context, r *REPL, args string) string {
	if args == "" {
		r.out.Errorf("%s", usage("read-only"))
		return ""
	}
	for _, rel := range r.expandPatterns(splitArgs(args), true) {
		r.coder.AddReadOnlyFile(rel)
		r.printf("Pinned %s read-only.", r.coder.DisplayPath(rel))
	}
	r.saveResume()
	return ""
}

func cmdDrop(_ context.Context, r *REPL, args string) string {
	if args == "" {
		r.coder.DropAll()
		r.printf("Unpinned everything.")
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
			r.printf("Unpinned %s.", rel)
		}
		if len(dropped) == 0 {
			r.out.Warningf("No pinned file matched %q.", pat)
		}
	}
	r.saveResume()
	return ""
}

func cmdLs(_ context.Context, r *REPL, _ string) string {
	chat := r.coder.ChatFiles()
	ro := r.coder.ReadOnlyFiles()
	if len(chat) == 0 && len(ro) == 0 {
		r.printf("No files pinned in this session.")
		return ""
	}
	if len(chat) > 0 {
		r.printf("Pinned:")
		for _, f := range chat {
			r.printf("  %s", f)
		}
	}
	if len(ro) > 0 {
		r.printf("Pinned read-only (their contents are in the request; the model cannot edit them):")
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

// cmdReset is the one command that ends everything the session accumulated,
// which is why the webfetch origins go here and not in /clear. /clear leaves
// the pins alone, so a user reaching for it is asking to forget what was
// *said*, not to give anything back — dropping a permission there would be the
// surprise. There is no "/reset pins": that is /drop with no arguments
// already, and a second spelling would only make the first harder to find.
func cmdReset(_ context.Context, r *REPL, _ string) string {
	r.coder.DropAll()
	r.coder.ClearHistory()
	forgotten := r.coder.ForgetOrigins()
	msg := "Unpinned everything and cleared the chat history."
	if forgotten > 0 {
		msg += fmt.Sprintf(" Forgot %d %s approved for fetching.",
			forgotten, pluralize(forgotten, "origin", "origins"))
	}
	r.printf("%s", msg)
	r.saveResume()
	return ""
}

// cmdNotes shows, generates, or discards the session notes.
//
// The notes are written by the side model and go into a future prompt, so they
// have to be readable: a summary nobody can inspect is a summary nobody should
// trust.
//
// "drop" is a subcommand rather than an argument to /drop, which would have
// read as unpinning a file called "notes" — a plausible real filename, and
// ambiguous the moment a project has one. It also keeps notes out of the pin
// vocabulary entirely, which is right: /drop unpins things the *user* chose,
// and the notes are something the harness wrote.
func cmdNotes(ctx context.Context, r *REPL, args string) string {
	switch strings.TrimSpace(args) {
	case "generate":
		if r.opts.GenerateNotes == nil {
			r.printf("Session notes are off for this session.")
			return ""
		}
		if err := r.opts.GenerateNotes(ctx); err != nil {
			r.out.Errorf("Could not generate notes: %v", err)
			return ""
		}
	case "drop":
		if r.opts.DropNotes == nil {
			r.printf("Session notes are off for this session.")
			return ""
		}
		r.opts.DropNotes()
		r.printf("Discarded the session notes.")
	case "":
		notes := r.opts.Notes
		if notes == nil {
			r.printf("Session notes are off for this session.")
			return ""
		}
		if strings.TrimSpace(notes()) == "" {
			r.printf(`No session notes. Use "/notes generate" to create them from the transcript.`)
			return ""
		}
		r.printf("%s", strings.TrimRight(notes(), "\n"))
	default:
		r.out.Errorf("%s", usage("notes"))
	}
	return ""
}

func cmdTokens(_ context.Context, r *REPL, _ string) string {
	r.printf("%s", r.coder.TokensReport())
	return ""
}

// cmdContext shows the conversation the way the model currently reads it: the
// compaction summaries in order, then the live tail the summaries do not cover.
// /tokens says how full the window is and the transcript says what was actually
// said; what neither shows is the fold — the thing the model sees — which is the
// niche this command fills.
func cmdContext(_ context.Context, r *REPL, args string) string {
	n := -1
	if arg := strings.TrimSpace(args); arg != "" {
		parsed, err := strconv.Atoi(arg)
		if err != nil || parsed <= 0 {
			r.out.Errorf("%s, where n is the number of summaries to show.", usage("context"))
			return ""
		}
		n = parsed
	}
	r.printf("%s", strings.TrimRight(r.coder.ViewContext(n), "\n"))
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
		r.out.Errorf("%s", usage("symbol"))
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
		r.coder.Summarizer = coder.NewChatSummary(r.opts.MakeClient(m.SideModel), m.SideModel, r.coder.Tokens, r.coder.Out, r.coder.Clock)
	}
	r.opts.ModelAlias = args
	r.saveResume()
	r.printf("Switched to model %s (%s).", args, m.QualifiedSlug())
	return ""
}

func cmdBtw(ctx context.Context, r *REPL, args string) string {
	if args == "" {
		r.out.Errorf("%s", usage("btw"))
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
	// The config is the source of truth for the allowlist, so a reload
	// discards /env session changes rather than carrying them over a
	// deliberate re-read.
	r.envAdded = map[string]bool{}
	r.envDropped = map[string]bool{}
	r.rebuildEnvAllow()
	r.coder.MaxSteps = 25
	if cfg.MaxSteps > 0 {
		r.coder.MaxSteps = cfg.MaxSteps
	}
	r.coder.MaxErrorReflections = 3
	if cfg.MaxErrorReflections > 0 {
		r.coder.MaxErrorReflections = cfg.MaxErrorReflections
	}
	r.coder.LoopDetection = !cfg.NoLoopDetection
	r.coder.AnchoredEdits = cfg.AnchoredEdits
	r.coder.IndentColumn = cfg.AnchoredEdits && cfg.IndentColumn
	r.coder.WebfetchAllow = cfg.WebfetchAllow
	r.coder.ShellTimeout = time.Duration(cfg.ShellTimeout) * time.Second
	// Re-applied by SetEditFormat on the next format switch; applied to the
	// current set here so a reload takes effect mid-session.
	r.coder.Examples = cfg.ExampleMessages
	for _, ex := range cfg.ExampleMessages {
		r.coder.Prompts.ExampleMessages = append(r.coder.Prompts.ExampleMessages,
			prompts.Example{Role: ex.Role, Content: ex.Content})
	}
	// The named checks and what runs after an edit: both are plain values, and
	// both were missing here, so editing a check and reloading did nothing.
	r.coder.Check = cfg.Check
	r.coder.CheckAuto = cfg.CheckAuto
	// The ports have to be rebuilt rather than copied, because a proxy lives
	// inside a transport inside a closure. Leaving them alone is what made a
	// reload look like it had worked when it had not.
	if r.opts.ApplyEgress != nil {
		r.opts.ApplyEgress(r.coder, cfg)
	}
	// Skills are not in the config, but they are read from disk at startup and
	// a reload is what a user reaches for after trusting one in another
	// window. 177bd2b made it the rule that a reload re-reads what it claims
	// to.
	if r.opts.Rediscover != nil {
		r.coder.Skills = r.opts.Rediscover()
	}
	// The one thing a reload genuinely cannot change, said rather than left to
	// be discovered: Landlock is applied to the process at startup and its
	// rules only ever add, so a session cannot widen or drop its own sandbox.
	if wasActive := r.coder.Sandbox.Active; wasActive != (cfg.Sandbox != "") {
		r.out.Warningf("The `sandbox` setting changed, which only a restart can apply. " +
			"This session keeps the sandbox it started with.")
	}

	// Re-resolve the active alias so edits to that model take effect; if it was
	// removed, keep the running model rather than stranding the session.
	if m, ok := cfg.Models[r.opts.ModelAlias]; ok {
		r.coder.SetModel(m)
		r.refreshTrailer(m)
		if r.opts.MakeClient != nil {
			r.coder.Client = r.opts.MakeClient(m)
			r.coder.Summarizer = coder.NewChatSummary(r.opts.MakeClient(m.SideModel), m.SideModel, r.coder.Tokens, r.coder.Out, r.coder.Clock)
		}
	} else {
		r.out.Warningf("Active model %q is no longer in the config; keeping the running model.", r.opts.ModelAlias)
	}
	r.printf("Config reloaded. Models: %s.", strings.Join(slices.Sorted(maps.Keys(cfg.Models)), ", "))
	return ""
}

func cmdRun(ctx context.Context, r *REPL, args string) string {
	if args == "" {
		r.out.Errorf("%s", usage("run"))
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

	res := r.Confirmer().Confirm(coder.ConfirmRequest{
		Prompt: "Add command output to the chat?",
		Group:  "add-output",
	})
	if res.Yes {
		// The result shape, so /run context reads like
		// model-proposed shell output.
		result := fmt.Sprintf("Command: %s\nExit status: %d\nOutput:\n%s", args, exitCode, output)
		r.coder.AppendContext(result)
		r.printf("Added the command output to the chat.")
	}
	return ""
}

// cmdCheck runs one or all of the project's configured checks and offers to
// add the output to the chat, like /run does for shell commands. It inherits
// the full environment (the user typed it), unlike model-caused checks which
// run under the allowlist.
func cmdCheck(ctx context.Context, r *REPL, args string) string {
	if len(r.coder.Check) == 0 {
		r.out.Errorf("No checks are configured for this project.")
		return ""
	}

	// Determine which checks to run.
	var checks []config.Check
	if name := strings.TrimSpace(args); name != "" {
		for _, ch := range r.coder.Check {
			if ch.Name == name {
				checks = append(checks, ch)
				break
			}
		}
		if len(checks) == 0 {
			names := make([]string, 0, len(r.coder.Check))
			for _, ch := range r.coder.Check {
				names = append(names, ch.Name)
			}
			r.out.Errorf("There is no check named %q. Configured checks: %s.", name, strings.Join(names, ", "))
			return ""
		}
	} else {
		checks = r.coder.Check
	}

	// Run each check in order, stopping at the first failure.
	var transcript strings.Builder
	for _, ch := range checks {
		r.out.Toolf("‹check› %s\n$ %s", ch.Name, strings.Join(ch.Argv, " "))
		exitCode, output := runUserCheck(ctx, r, ch)

		if exitCode == 0 {
			r.out.Toolf("passed")
		} else {
			r.out.Toolf("failed (exit status %d)", exitCode)
			if trimmed := strings.TrimRight(output, "\n"); trimmed != "" {
				r.printf("%s", trimmed)
			}
		}

		fmt.Fprintf(&transcript, "%s: %s\nExit status: %d\n", ch.Name, strings.Join(ch.Argv, " "), exitCode)
		if strings.TrimSpace(output) != "" {
			fmt.Fprintf(&transcript, "Output:\n%s\n", output)
		}

		if exitCode != 0 {
			if len(checks) > 1 {
				r.out.Warningf("Stopped here; later checks were not run.")
				transcript.WriteString("\nStopped here; later checks were not run.\n")
			}
			break
		}
		transcript.WriteString("\n")
	}

	// A successful run that produced no output has nothing to add.
	transcriptStr := transcript.String()
	if strings.TrimSpace(transcriptStr) == "" {
		return ""
	}

	res := r.Confirmer().Confirm(coder.ConfirmRequest{
		Prompt: "Add check output to the chat?",
		Group:  "add-output",
	})
	if res.Yes {
		r.coder.AppendContext(transcriptStr)
		r.printf("Added the check output to the chat.")
	}
	return ""
}

// runUserCheck executes one check's argv directly (no shell), inheriting the
// full environment — the user typed /check, like /run. Returns the exit code
// and merged stdout+stderr.
func runUserCheck(ctx context.Context, r *REPL, ch config.Check) (int, string) {
	cmd := execCommandContext(ctx, ch.Argv[0], ch.Argv[1:]...)
	cmd.Dir = r.coder.Root
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			return -1, fmt.Sprintf("could not run %s: %v", strings.Join(ch.Argv, " "), err)
		}
	}
	return exitCode, string(out)
}

// execCommandContext is exec.CommandContext, seamable for testing.
var execCommandContext = exec.CommandContext

// pluralize picks the word for a count. The count is printed by the caller,
// which keeps the two apart at the one place a sentence might want them apart.
func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// cmdWeb fetches a URL and adds its content to the chat as a completed exchange
// — the user-typed twin of the webfetch tool, and unconfirmed for the reason
// /run is: the user typed it. It is also what a URL in a message used to
// trigger a prompt about, back when the harness guessed.
//
// (the same path /run uses for command output), so it's context for your next
// message without re-scanning the page's own links or firing a turn.
//
// The subcommands are the webfetch tool's permission surface, put here rather
// than under /reset because the model is /env's: a session-scoped thing
// model-run actions may reach, shown and changed by the one command that names
// it. Showing is the half that earns the command. A session-scoped "a" is
// invisible in a way a turn-scoped one never was — the turn boundary used to
// revoke it in plain sight — so a way to forget the grants without a way to
// see them would replace a permission you cannot revoke with one you can only
// revoke blind. Neither word can collide with a URL: both lack a scheme and a
// dot, so unlike /notes there is no plausible real argument to shadow.
func cmdWeb(ctx context.Context, r *REPL, args string) string {
	url := strings.TrimSpace(args)
	switch verb, rest, _ := strings.Cut(url, " "); verb {
	case "":
		return webOrigins(r)
	case "reset":
		if strings.TrimSpace(rest) != "" {
			r.out.Errorf("%s", usage("web"))
			return ""
		}
		if n := r.coder.ForgetOrigins(); n > 0 {
			r.printf("Forgot %d %s. webfetch will ask again.",
				n, pluralize(n, "origin", "origins"))
		} else {
			r.printf("No origins were approved this session.")
		}
		return ""
	case "allow":
		return webAllow(r, strings.TrimSpace(rest))
	case "drop":
		return webDrop(r, strings.TrimSpace(rest))
	}
	if r.coder.Scrape == nil {
		r.out.Errorf("Scraping is not available.")
		return ""
	}
	r.printf("Scraping %s...", url)
	// No outline from /web: the user typed a URL and wants the page. A fragment
	// in what they typed still narrows it, which is what a fragment means
	// everywhere else they paste one.
	content, err := r.coder.Scrape(ctx, url, coder.ScrapeOptions{})
	if err != nil {
		r.out.Errorf("Unable to fetch %s: %v", url, err)
		return ""
	}
	r.coder.AppendContext(content)
	r.printf("Added %s to the chat.", url)
	return ""
}

// webOrigins shows what webfetch may reach without asking. The two sources are
// listed apart because they differ in the two ways that matter to someone
// reading the list: how long they last, and how they are taken back. Folding
// them into one list would answer "what may be fetched" when the question is
// "what did I grant, and how do I undo it".
func webOrigins(r *REPL) string {
	allow, session := r.coder.WebfetchAllow, r.coder.SessionOrigins()
	if len(allow) == 0 && len(session) == 0 {
		r.printf("webfetch asks before every origin. " +
			`Answer "a" at a prompt, or "/web allow <origin>", to stop being asked for one.`)
		return ""
	}
	if len(allow) > 0 {
		r.printf("Allowed by webfetch_allow in the config (a bare host covers ports 80 and 443):")
		for _, entry := range allow {
			r.printf("  %s", entry)
		}
	}
	if len(session) > 0 {
		r.printf(`Approved for this session ("/web reset" forgets them):`)
		for _, org := range session {
			r.printf("  %s", org)
		}
	}
	return ""
}

// webAllow grants an origin ahead of being asked. It writes nothing to the
// config: this is the session-scoped half, and a command that edited
// config.star would be a Starlark writer answering to a permission prompt —
// more machinery, and a worse surprise, than the user asked for. The durable
// half stays a line the user writes themselves.
func webAllow(r *REPL, entry string) string {
	if entry == "" || strings.ContainsAny(entry, " \t") {
		r.out.Errorf("%s", usage("web"))
		return ""
	}
	added, ok := r.coder.AllowOrigin(entry)
	if !ok {
		r.out.Errorf("%q is not an origin. Give a host or host:port, with no scheme and no path.", entry)
		return ""
	}
	if len(added) == 0 {
		r.printf("%s was already approved.", entry)
		return ""
	}
	r.printf("Fetching %s without asking for the rest of this session.", strings.Join(added, " and "))
	return ""
}

// webDrop withdraws one grant, the granular half of "/web reset". It reaches
// only what this session approved: the config allowlist is a file the user
// wrote, and a command that made Strument disagree with it until restart would
// be a worse surprise than being told to edit the line. So an origin that is
// allowed by config is reported as such rather than silently doing nothing.
func webDrop(r *REPL, entry string) string {
	if entry == "" || strings.ContainsAny(entry, " \t") {
		r.out.Errorf("%s", usage("web"))
		return ""
	}
	dropped, ok := r.coder.DropOrigin(entry)
	if !ok {
		r.out.Errorf("%q is not an origin. Give a host or host:port, with no scheme and no path.", entry)
		return ""
	}
	if len(dropped) == 0 {
		for _, org := range origin.Origins(entry) {
			if origin.Allowed(org, r.coder.WebfetchAllow) {
				r.out.Warningf("%s is allowed by webfetch_allow in the config, "+
					"which this cannot change. Remove the entry there.", entry)
				return ""
			}
		}
		r.printf("%s was not approved this session.", entry)
		return ""
	}
	r.printf("webfetch will ask again before %s.", strings.Join(dropped, " and "))
	return ""
}

// submitLimit caps the file /submit will read. The point is not to fit the
// context window exactly — the model may have one far smaller — but to keep a
// stray log or binary from becoming a prompt at all.
const submitLimit = 100 * 1024

// cmdSubmit reads a file and returns its contents as the message for this
// turn: the same path /ask's one-shot arguments take, so a submitted file goes
// through runTurn like typed input — transcript, token accounting, Ctrl-C, the
// undo hint — without touching readline (the typed /submit line is what input
// history keeps; the file's contents never enter it). The contents are printed
// first, in their trimmed form, so the echo is the very text the model
// receives. Unlike /add, nothing is
// pinned: the text is sent once and only exists in the conversation.
//
// Paths resolve against the coder root but outside paths are allowed, like
// /read-only's: drafting a long prompt in an editor often happens outside the
// project, and unlike a pinned file this is the user's own words being sent
// verbatim, not model-facing reference material.
//
// The returned message is not re-dispatched even when it starts with "/", so a
// file cannot issue commands — /submit's output takes the send path, not the
// dispatch path.
func cmdSubmit(_ context.Context, r *REPL, args string) string {
	paths := splitArgs(args)
	if len(paths) != 1 || paths[0] == "" {
		r.out.Errorf("%s", usage("submit"))
		return ""
	}
	path := paths[0]
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.coder.Root, path)
	}

	st, err := os.Stat(path)
	if err != nil {
		r.out.Errorf("Could not read %s: %v", paths[0], err)
		return ""
	}
	if st.IsDir() {
		r.out.Errorf("%s is a directory.", paths[0])
		return ""
	}
	if st.Size() > submitLimit {
		// Refuse rather than truncate: a silently shortened prompt is worse
		// than one the user has to split themselves.
		r.out.Errorf("%s is %s, over the %s /submit limit.", paths[0], humanBytes(st.Size()), humanBytes(submitLimit))
		return ""
	}

	data, err := os.ReadFile(path)
	if err != nil {
		r.out.Errorf("Could not read %s: %v", paths[0], err)
		return ""
	}
	if !utf8.Valid(data) {
		r.out.Errorf("%s is not valid UTF-8 text.", paths[0])
		return ""
	}
	msg := strings.TrimSpace(string(data))
	if msg == "" {
		r.out.Errorf("%s is empty.", paths[0])
		return ""
	}
	// Print the trimmed text, not the raw file: what is echoed is what is
	// sent, with no framing whitespace to make the two disagree.
	r.printf("%s", msg)
	return msg
}

// humanBytes renders a size for the /submit error the way a person would say
// it ("1.5 MiB"), falling back to plain bytes under a KiB.
func humanBytes(n int64) string {
	switch b := float64(n); {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MiB", b/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KiB", b/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
