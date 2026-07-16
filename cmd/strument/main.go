// Command strument is an AI pair-programming tool for the terminal — a Go
// port of aider trimmed to the essentials. See spec/strument-guide.md.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"slices"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/dbohdan/strument/internal/client"
	"github.com/dbohdan/strument/internal/coder"
	"github.com/dbohdan/strument/internal/config"
	"github.com/dbohdan/strument/internal/repomap"
)

var version = "0.0.0-dev"

type chatCmd struct {
	Message  string   `help:"Send one message, apply the edits, and exit (script mode)."    short:"m"`
	Model    string   `help:"Model alias from config; defaults to the config's default."`
	NoGit    bool     `help:"Disable git integration even inside a repository."             name:"no-git"`
	DryRun   bool     `help:"Report edits without writing files or committing."             name:"dry-run"`
	Yes      bool     `help:"Answer yes to confirmations (never auto-runs shell commands)."`
	YesShell bool     `help:"Also auto-run model-suggested shell commands."                 name:"yes-shell"`
	Files    []string `arg:""                                                               help:"Files to add to the chat." optional:"" type:"existingfile"`
}

func (c *chatCmd) Run() error {
	root, err := os.Getwd()
	if err != nil {
		return err
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
	// Git integration (Repo port) arrives in phase 8; --no-git is honored
	// implicitly until then.
	_ = c.NoGit

	for _, f := range c.Files {
		cdr.AddFile(f)
	}

	if c.Message == "" {
		return errors.New("the interactive REPL arrives in phase 7; use --message for script mode")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cdr.Run(ctx, c.Message)
	return nil
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
