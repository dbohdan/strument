// Command strument is an AI pair-programming tool for the terminal — a Go
// port of aider trimmed to the essentials.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"golang.org/x/term"

	"dbohdan.com/strument/internal/client"
	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/history"
	"dbohdan.com/strument/internal/httpx"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/modelconfig"
	"dbohdan.com/strument/internal/render"
	"dbohdan.com/strument/internal/repl"
	"dbohdan.com/strument/internal/repomap"
)

var version = "0.0.0-dev"

type chatCmd struct {
	Message       string   `help:"Send one message, apply the edits, and exit (script mode)."    short:"m"`
	Model         string   `help:"Model alias from config; defaults to the config's default."    short:"M"`
	NoGit         bool     `help:"Disable git integration even inside a repository."             name:"no-git"`
	NoColor       bool     `help:"Disable ANSI color and styling."                               name:"no-color"`
	DarkMode      bool     `help:"Use colors suited to a dark terminal background."              name:"dark-mode"                                           xor:"palette"`
	LightMode     bool     `help:"Use colors suited to a light terminal background."             name:"light-mode"                                          xor:"palette"`
	NoAutoCommits bool     `help:"Keep git integration but do not auto-commit edits."            name:"no-auto-commits"`
	NoHistory     bool     `help:"Do not write the session to the chat-history file."            name:"no-history"`
	DryRun        bool     `help:"Report edits without writing files or committing."             name:"dry-run"`
	Yes           bool     `help:"Answer yes to confirmations (never auto-runs shell commands)."`
	YesShell      bool     `help:"Also auto-run model-suggested shell commands."                 name:"yes-shell"`
	Files         []string `arg:""                                                               help:"Files to add to the chat (they need not exist yet)." optional:""`
}

func (c *chatCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root := cwd

	// Git is on by default inside a repository; the worktree root becomes
	// the project root, like aider (--no-git opts out).
	var repo *gitrepo.Repo
	if !c.NoGit {
		if g, err := gitrepo.Discover(root); err == nil {
			repo = g
			root = g.Root()
		}
	}

	cfg, err := config.Load(config.Options{ProjectRoot: root})
	if err != nil {
		return err
	}
	// The project's state directory, and whatever the last session left in it.
	// Resolved before the model, because a remembered alias participates in
	// choosing one.
	projectRoot, rootErr := historyRoot()
	var res history.Resume
	if rootErr == nil {
		res = history.LoadResume(projectRoot)
	}

	// -M beats a remembered alias beats the config's default.
	alias := c.Model
	fromResume := false
	if alias == "" && res.Model != "" {
		alias, fromResume = res.Model, true
	}
	if alias == "" {
		alias = cfg.Default
	}
	model, ok := cfg.Models[alias]
	if !ok && fromResume {
		// An alias can be renamed out of the config between sessions. That is
		// not the user's mistake here and must not stop them starting.
		fmt.Fprintf(os.Stderr, "strument: remembered model %q is no longer in the config; using %q.\n", alias, cfg.Default)
		alias, ok = cfg.Default, true
		model = cfg.Models[alias]
	}
	if !ok {
		return fmt.Errorf("unknown model alias %q (aliases: %s)", alias, strings.Join(slices.Sorted(maps.Keys(cfg.Models)), ", "))
	}

	cdr := coder.New(root, model)
	cdr.DryRun = c.DryRun
	cdr.Client = client.New(model.Provider)
	// The project's named checks, which the verify tool runs without asking:
	// the model supplies only a name, so nothing it says can change what runs.
	cdr.Verify = cfg.Verify
	cdr.VerifyAuto = cfg.VerifyAuto
	if std, ok := cdr.Out.(*coder.StdOutput); ok {
		// Script mode's output; the REPL swaps in its own and reads the setting
		// from the config it already carries.
		std.Thinking = coder.ThinkingDisplay(cfg.ReasoningDisplay)
	}
	cdr.Summarizer = coder.NewChatSummary(client.New(model.WeakModel.Provider), model.WeakModel, cdr.Tokens)
	cdr.Confirm = coder.AutoConfirmer{Yes: c.Yes, YesShell: c.YesShell, Fallback: terminalConfirmer{}}
	// URL scraping is a non-provider egress action, so it uses the global proxy
	// (validated at load, so the error is dead; nil transport => direct). An
	// explicit `scraper` command overrides the built-in fetcher — the opt-in path
	// for JavaScript-rendered pages — and does its own networking (no proxy).
	if len(cfg.Scraper) > 0 {
		cdr.Scrape = coder.NewCommandScraper(cfg.Scraper, 60*time.Second)
	} else {
		scrapeTransport, _ := httpx.ProxyTransport(cfg.Proxy)
		cdr.Scrape = coder.NewSimpleScraper(scrapeTransport, "Strument/"+version)
	}
	if model.RepoMap {
		cdr.RepoMap = repomap.New(root)
	}
	if repo != nil {
		weak := model.WeakModel
		repo.CommitTrailer = gitrepo.Trailer(model.ReadableName())
		repo.Message = coder.CommitMessenger(client.New(weak.Provider), weak, cdr.Platform.Language)
		cdr.Repo = repo
		cdr.AutoCommits = !c.NoAutoCommits
		cdr.Platform.InGit = true
	}

	// File arguments are relative to the invocation directory, not the git
	// root, so resolve them here — kong no longer does, now that a nonexistent
	// file is accepted (the model creates it on request). AddFile only tracks
	// the path; the file need not exist yet.
	for _, f := range c.Files {
		if !filepath.IsAbs(f) {
			f = filepath.Join(cwd, f)
		}
		cdr.AddFile(f)
	}

	// --no-history means leave no trace: no transcript, no input history, no
	// resume file, and no directory created to hold them. Reading what a past
	// session left is still fine — that writes nothing, and refusing it would
	// make the flag a bigger behavior change than its name suggests.
	keepState := !c.NoHistory && rootErr == nil
	if keepState {
		if _, err := history.EnsureProjectDir(projectRoot); err != nil {
			keepState = false
		}
	}

	var hist *history.Writer
	if keepState {
		if p, err := resolveHistoryPath(cfg, projectRoot); err == nil {
			hist = history.New(p)
		}
	}

	if c.Message == "" {
		// Restoring only in the REPL keeps a scripted run reproducible from its
		// own arguments: `strument -m ...` should send what it was told to, not
		// whatever an interactive session left pinned, which the user would pay
		// for without seeing it.
		note := ""
		if len(c.Files) == 0 && rootErr == nil {
			note = restoreSession(cdr, projectRoot, res)
		}
		return c.runREPL(cfg, cdr, repo, hist, alias, projectRoot, keepState, note)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	sentBefore, recvBefore := cdr.SessionTokens()
	costBefore, _ := cdr.SessionCost()
	answer := cdr.Run(ctx, c.Message)
	if hist != nil {
		sentAfter, recvAfter := cdr.SessionTokens()
		costAfter, known := cdr.SessionCost()
		if err := hist.Append(history.Turn{
			Model:          model.QualifiedSlug(),
			TokensSent:     sentAfter - sentBefore,
			TokensReceived: recvAfter - recvBefore,
			Cost:           costAfter - costBefore,
			CostKnown:      known,
			User:           c.Message,
			Assistant:      answer,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "strument: could not write chat history:", err)
		}
	}
	return nil
}

// historyRoot is the project a transcript belongs to.
//
// It is the git worktree root wherever there is one and the working directory
// otherwise, and it is deliberately independent of --no-git. That flag says how
// a turn is committed, not which project you are in: working on one repository
// sometimes with git and sometimes without should leave one transcript, not two
// scattered by a flag whose name does not hint at it.
//
// chat and history used to derive this separately — chat honoring --no-git,
// history not — so `chat --no-git` in a subdirectory filed the transcript under
// the subdirectory while `strument history` reported the repository root. Two
// paths, two hashes, and no way to find your own history. One function now, so
// they cannot drift again.
func historyRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return historyRootFrom(cwd), nil
}

func historyRootFrom(dir string) string {
	if g, err := gitrepo.Discover(dir); err == nil {
		return g.Root()
	}
	return dir
}

// restoreSession re-pins what the last session had pinned, returning a line for
// the banner or "" when nothing was restored.
//
// Paths are project-root-relative, so they survive the --no-git case where the
// coder's root is the invocation directory and the project is the git worktree.
// A file that has since moved is skipped rather than reported: the point is to
// save retyping, not to litigate what happened to the tree.
func restoreSession(cdr *coder.Coder, projectRoot string, res history.Resume) string {
	abs := func(rel string) (string, bool) {
		p := filepath.Join(projectRoot, filepath.FromSlash(rel))
		if _, err := os.Stat(p); err != nil {
			return "", false
		}
		return p, true
	}
	var files, readOnly int
	for _, rel := range res.Files {
		if p, ok := abs(rel); ok {
			cdr.AddFile(p)
			files++
		}
	}
	for _, rel := range res.ReadOnly {
		if p, ok := abs(rel); ok {
			cdr.AddReadOnlyFile(p)
			readOnly++
		}
	}
	switch {
	case files == 0 && readOnly == 0:
		return ""
	case readOnly == 0:
		return fmt.Sprintf("Restored %s from your last session.", plural(files, "file", "files"))
	case files == 0:
		return fmt.Sprintf("Restored %s from your last session.", plural(readOnly, "read-only file", "read-only files"))
	}
	return fmt.Sprintf("Restored %s and %s from your last session.",
		plural(files, "file", "files"), plural(readOnly, "read-only file", "read-only files"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// saveResumeFunc returns the callback the REPL calls after a command changes
// what a resume would restore, or nil when this session leaves no trace.
//
// The alias is recorded only when it differs from the config's default. That is
// a simpler rule than "explicitly chosen" and a more robust one: recording the
// default would pin a project to whatever it happened to be the first time the
// project was opened, so that later editing `default` in config.star would
// mysteriously not take effect there. It also gives an obvious way out —
// switching back to the default stops the pinning.
func saveResumeFunc(cdr *coder.Coder, cfg *config.Config, projectRoot string, keepState bool) func(alias string) {
	if !keepState {
		return nil
	}
	return func(alias string) {
		toProject := func(rels []string) []string {
			out := make([]string, 0, len(rels))
			for _, rel := range rels {
				p := filepath.Join(cdr.Root, filepath.FromSlash(rel))
				r, err := filepath.Rel(projectRoot, p)
				if err != nil || strings.HasPrefix(r, "..") {
					continue // outside the project; nothing stable to record
				}
				out = append(out, filepath.ToSlash(r))
			}
			return out
		}
		res := history.Resume{
			Files:    toProject(cdr.ChatFiles()),
			ReadOnly: toProject(cdr.ReadOnlyFiles()),
		}
		if alias != cfg.Default {
			res.Model = alias
		}
		_ = history.SaveResume(projectRoot, res)
	}
}

// resolveHistoryPath is the config override (absolute, or relative to the
// history root above) or the XDG default.
func resolveHistoryPath(cfg *config.Config, projectRoot string) (string, error) {
	if cfg.HistoryFile != "" {
		p := cfg.HistoryFile
		if !filepath.IsAbs(p) {
			p = filepath.Join(projectRoot, p)
		}
		return p, nil
	}
	return history.DefaultPath(projectRoot)
}

// paletteTheme picks the color palette from the --dark-mode/--light-mode
// flags (mutually exclusive), defaulting to aider's default palette.
func (c *chatCmd) paletteTheme() render.Theme {
	switch {
	case c.DarkMode:
		return render.DarkTheme()
	case c.LightMode:
		return render.LightTheme()
	default:
		return render.DefaultTheme()
	}
}

// terminalSize reports stdout's width and height for the horizontal rules,
// falling back to 80x24 when stdout is not a terminal.
func terminalSize() (int, int) {
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w, h
	}
	return 80, 24
}

// runREPL starts the interactive session.
func (c *chatCmd) runREPL(cfg *config.Config, cdr *coder.Coder, repo *gitrepo.Repo, hist *history.Writer,
	alias, projectRoot string, keepState bool, resumeNote string,
) error {
	// Scoped to the project like the transcript, in the directory Run already
	// created — and suppressed with it when the session leaves no trace.
	var inputHistory string
	if keepState {
		inputHistory, _ = history.InputHistoryPath(projectRoot)
	}
	r, err := repl.New(repl.Options{
		Coder:      cdr,
		Config:     cfg,
		Git:        repo,
		History:    hist,
		ModelAlias: alias,
		ResumeNote: resumeNote,
		SaveResume: saveResumeFunc(cdr, cfg, projectRoot, keepState),
		MakeClient: func(m *config.Model) llm.ModelClient { return client.New(m.Provider) },
		ReloadConfig: func() (*config.Config, error) {
			return config.Load(config.Options{ProjectRoot: cdr.Root})
		},
		Color:       !c.NoColor && stdoutIsTerminal() && os.Getenv("NO_COLOR") == "",
		HistoryFile: inputHistory,
		Version:     version,
		Theme:       c.paletteTheme(),
		GetSize:     terminalSize,
	})
	if err != nil {
		return err
	}
	defer r.Close()
	// Route confirms through readline; --yes/--yes-shell answer first.
	cdr.Confirm = coder.AutoConfirmer{Yes: c.Yes, YesShell: c.YesShell, Fallback: r.Confirmer()}
	return r.Run(context.Background())
}

func stdoutIsTerminal() bool {
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// terminalConfirmer asks y/n questions on the terminal.
type terminalConfirmer struct{}

func (terminalConfirmer) Confirm(req coder.ConfirmRequest) (bool, bool) {
	if req.Subject != "" {
		fmt.Println(req.Subject)
	}
	fmt.Printf("%s (y/N) ", req.Prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, false
	case "d", "never":
		return false, true
	default:
		return false, false
	}
}

type trustCmd struct {
	Path string `arg:"" help:"Project directory containing .strument.star (default: cwd)." optional:""`
}

func (c *trustCmd) Run() error {
	root := c.Path
	if root == "" {
		var err error
		if root, err = os.Getwd(); err != nil {
			return err
		}
	}
	absPath, err := config.TrustProject(root, "")
	if err != nil {
		return err
	}
	fmt.Printf("Trusted %s. Re-run `strument trust` after every edit to it.\n", absPath)
	return nil
}

// historyCmd prints the chat-history file for the current project (the one
// XDG makes hard to discover). It resolves the same path chat mode writes.
type historyCmd struct{}

func (*historyCmd) Run() error {
	root, err := historyRoot()
	if err != nil {
		return err
	}
	// Honor a config override when the config loads; otherwise fall back to
	// the default path so "where is my history" always answers.
	if cfg, err := config.Load(config.Options{ProjectRoot: root}); err == nil {
		if p, err := resolveHistoryPath(cfg, root); err == nil {
			fmt.Println(p)
			return nil
		}
	}
	p, err := history.DefaultPath(root)
	if err != nil {
		return err
	}
	fmt.Println(p)
	return nil
}

// modelConfigCmd scaffolds model() blocks from a provider's live catalog, so
// the tedious fields (context size, costs, cache capability) don't have to be
// looked up by hand. Output is copy-pastable Starlark on stdout — the user
// reviews it and pastes it into their config.
type modelConfigCmd struct {
	Source       string   `default:"openrouter"                                                            help:"Metadata source (currently only \"openrouter\")."    short:"s"`
	ProviderName string   `default:"openrouter"                                                            help:"Provider variable name emitted in the model() call." name:"provider-name"`
	Proxy        string   `help:"SOCKS5 proxy for the catalog fetch (default: the config's global proxy)." name:"proxy"`
	Models       []string `arg:""                                                                          help:"Exact model slugs, e.g. anthropic/claude-haiku-4.5." name:"model"`
}

// openRouterKeyFromConfig returns the API key of an OpenRouter provider in the
// config, or "" when none is configured.
func openRouterKeyFromConfig(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	for _, m := range cfg.Models {
		if m.Provider.Adapter == config.AdapterOpenRouter && m.Provider.APIKey != "" {
			return m.Provider.APIKey
		}
	}
	return ""
}

func (c *modelConfigCmd) Run() error {
	if c.Source != "openrouter" {
		return fmt.Errorf("unknown source %q (only \"openrouter\" is supported)", c.Source)
	}
	// Best-effort load the config once: it supplies the OpenRouter API key and
	// the global proxy. It may not exist yet on a first run.
	var cfg *config.Config
	if loaded, err := config.Load(config.Options{}); err == nil {
		cfg = loaded
	}

	// --proxy wins, then the config's global proxy.
	proxy := c.Proxy
	if proxy == "" && cfg != nil {
		proxy = cfg.Proxy
	}
	transport, err := httpx.ProxyTransport(proxy)
	if err != nil {
		return err
	}

	// Authentication is mandatory: unauthenticated catalog requests are
	// rate-limited and can get the IP blocked. Prefer the config's OpenRouter
	// key, fall back to OPENROUTER_API_KEY.
	apiKey := openRouterKeyFromConfig(cfg)
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}
	if apiKey == "" {
		return errors.New("model-config needs an OpenRouter API key (set OPENROUTER_API_KEY); anonymous catalog requests are rate-limited and can get your IP blocked")
	}

	src := &modelconfig.OpenRouterSource{
		APIKey:    apiKey,
		UserAgent: "Strument/" + version,
		Transport: transport,
	}
	found, missing, err := src.Lookup(c.Models)
	if err != nil {
		return err
	}
	if len(found) > 0 {
		fmt.Print(modelconfig.EmitStarlark(found, c.ProviderName))
	}
	for _, m := range missing {
		fmt.Fprintf(os.Stderr, "strument: model %q not found on %s\n", m, c.Source)
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d model(s) not found", len(missing))
	}
	return nil
}

type cli struct {
	Chat        chatCmd          `cmd:""                         default:"withargs"                                                 help:"Chat with a model about the given files (default command)."`
	Trust       trustCmd         `cmd:""                         help:"Trust the project's .strument.star config file."`
	History     historyCmd       `cmd:""                         help:"Print the path to this project's chat-history file."`
	ModelConfig modelConfigCmd   `cmd:""                         help:"Print copy-pastable model() config fetched from a provider." name:"model-config"`
	Version     kong.VersionFlag `help:"Print version and exit."`
}

func main() {
	var c cli
	ctx := kong.Parse(&c,
		kong.Name("strument"),
		kong.Description("AI pair programming in your terminal. A Go port of aider."),
		kong.Vars{"version": version},
		kong.UsageOnError(),
	)
	if err := ctx.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "strument:", err)
		os.Exit(1)
	}
}
