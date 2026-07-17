// Command strument is an AI pair-programming tool for the terminal — a Go
// port of aider trimmed to the essentials. See spec/strument-guide.md.
package main

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alecthomas/kong"

	"dbohdan.com/strument/internal/client"
	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/repl"
	"dbohdan.com/strument/internal/repomap"
)

var version = "0.0.0-dev"

type chatCmd struct {
	Message       string   `help:"Send one message, apply the edits, and exit (script mode)."    short:"m"`
	Model         string   `help:"Model alias from config; defaults to the config's default."`
	NoGit         bool     `help:"Disable git integration even inside a repository."             name:"no-git"`
	NoColor       bool     `help:"Disable ANSI color and styling."                               name:"no-color"`
	NoAutoCommits bool     `help:"Keep git integration but do not auto-commit edits."            name:"no-auto-commits"`
	DryRun        bool     `help:"Report edits without writing files or committing."             name:"dry-run"`
	Yes           bool     `help:"Answer yes to confirmations (never auto-runs shell commands)."`
	YesShell      bool     `help:"Also auto-run model-suggested shell commands."                 name:"yes-shell"`
	Files         []string `arg:""                                                               help:"Files to add to the chat." optional:"" type:"existingfile"`
}

func (c *chatCmd) Run() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}

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
	alias := c.Model
	if alias == "" {
		alias = cfg.Default
	}
	model, ok := cfg.Models[alias]
	if !ok {
		return fmt.Errorf("unknown model alias %q (aliases: %s)", alias, strings.Join(slices.Sorted(maps.Keys(cfg.Models)), ", "))
	}

	cdr := coder.New(root, model)
	cdr.DryRun = c.DryRun
	cdr.Client = client.New(model.Provider)
	cdr.Confirm = coder.AutoConfirmer{Yes: c.Yes, YesShell: c.YesShell, Fallback: terminalConfirmer{}}
	cdr.Scrape = coder.SimpleScraper
	if model.RepoMap {
		rm := repomap.New(root)
		rm.MaxContextWindow = model.Context
		rm.RepoContentPrefix = cdr.Prompts.RepoContentPrefix
		cdr.RepoMap = rm
	}
	if repo != nil {
		weak := model.WeakModel
		repo.CommitTrailer = gitrepo.Trailer(model.Slug)
		repo.Message = coder.CommitMessenger(client.New(weak.Provider), weak, cdr.Platform.Language)
		cdr.Repo = repo
		cdr.AutoCommits = !c.NoAutoCommits
		cdr.Platform.InGit = true
	}

	for _, f := range c.Files {
		cdr.AddFile(f)
	}

	if c.Message == "" {
		return c.runREPL(cfg, cdr, repo, alias)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cdr.Run(ctx, c.Message)
	return nil
}

// runREPL starts the interactive session (basecoder-spec §1.2).
func (c *chatCmd) runREPL(cfg *config.Config, cdr *coder.Coder, repo *gitrepo.Repo, alias string) error {
	r, err := repl.New(repl.Options{
		Coder:      cdr,
		Config:     cfg,
		Git:        repo,
		ModelAlias: alias,
		MakeClient: func(m *config.Model) llm.ModelClient { return client.New(m.Provider) },
		Color:      !c.NoColor && stdoutIsTerminal() && os.Getenv("NO_COLOR") == "",
		HistoryFile: func() string {
			p, err := config.DefaultTrustStorePath()
			if err != nil {
				return ""
			}
			return filepath.Join(filepath.Dir(p), "history")
		}(),
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

type cli struct {
	Chat    chatCmd          `cmd:""                         default:"withargs"                                     help:"Chat with a model about the given files (default command)."`
	Trust   trustCmd         `cmd:""                         help:"Trust the project's .strument.star config file."`
	Version kong.VersionFlag `help:"Print version and exit."`
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
