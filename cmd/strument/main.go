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
	"golang.org/x/term"

	"dbohdan.com/strument/internal/client"
	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/history"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/render"
	"dbohdan.com/strument/internal/repl"
	"dbohdan.com/strument/internal/repomap"
)

var version = "0.0.0-dev"

type chatCmd struct {
	Message       string   `help:"Send one message, apply the edits, and exit (script mode)."    short:"m"`
	Model         string   `help:"Model alias from config; defaults to the config's default."`
	NoGit         bool     `help:"Disable git integration even inside a repository."             name:"no-git"`
	NoColor       bool     `help:"Disable ANSI color and styling."                               name:"no-color"`
	DarkMode      bool     `help:"Use colors suited to a dark terminal background."              name:"dark-mode"                 xor:"palette"`
	LightMode     bool     `help:"Use colors suited to a light terminal background."             name:"light-mode"                xor:"palette"`
	NoAutoCommits bool     `help:"Keep git integration but do not auto-commit edits."            name:"no-auto-commits"`
	NoHistory     bool     `help:"Do not write the session to the chat-history file."            name:"no-history"`
	DryRun        bool     `help:"Report edits without writing files or committing."             name:"dry-run"`
	Yes           bool     `help:"Answer yes to confirmations (never auto-runs shell commands)."`
	YesShell      bool     `help:"Also auto-run model-suggested shell commands."                 name:"yes-shell"`
	Files         []string `arg:""                                                               help:"Files to add to the chat." optional:""   type:"existingfile"`
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

	var hist *history.Writer
	if !c.NoHistory {
		if p, err := resolveHistoryPath(cfg, root); err == nil {
			hist = history.New(p)
		}
	}

	if c.Message == "" {
		return c.runREPL(cfg, cdr, repo, hist, alias)
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
			Model:          model.Slug,
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

// resolveHistoryPath is the config override (absolute, or relative to the
// project root) or the XDG default.
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

// runREPL starts the interactive session (basecoder-spec §1.2).
func (c *chatCmd) runREPL(cfg *config.Config, cdr *coder.Coder, repo *gitrepo.Repo, hist *history.Writer, alias string) error {
	inputHistory, _ := history.InputHistoryPath()
	r, err := repl.New(repl.Options{
		Coder:       cdr,
		Config:      cfg,
		Git:         repo,
		History:     hist,
		ModelAlias:  alias,
		MakeClient:  func(m *config.Model) llm.ModelClient { return client.New(m.Provider) },
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
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if g, err := gitrepo.Discover(root); err == nil {
		root = g.Root()
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

type cli struct {
	Chat    chatCmd          `cmd:""                         default:"withargs"                                         help:"Chat with a model about the given files (default command)."`
	Trust   trustCmd         `cmd:""                         help:"Trust the project's .strument.star config file."`
	History historyCmd       `cmd:""                         help:"Print the path to this project's chat-history file."`
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
