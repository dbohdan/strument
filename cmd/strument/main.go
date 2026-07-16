// Command strument is an AI pair-programming tool for the terminal — a Go
// port of aider trimmed to the essentials. See spec/strument-guide.md.
package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"

	"github.com/dbohdan/strument/internal/config"
)

var version = "0.0.0-dev"

type chatCmd struct {
	Message  string   `short:"m" help:"Send one message, apply the edits, and exit (script mode)."`
	Model    string   `help:"Model alias from config; defaults to the config's default."`
	NoGit    bool     `name:"no-git" help:"Disable git integration even inside a repository."`
	DryRun   bool     `name:"dry-run" help:"Report edits without writing files or committing."`
	Yes      bool     `help:"Answer yes to confirmations (never auto-runs shell commands)."`
	YesShell bool     `name:"yes-shell" help:"Also auto-run model-suggested shell commands."`
	Files    []string `arg:"" optional:"" type:"existingfile" help:"Files to add to the chat."`
}

func (c *chatCmd) Run() error {
	// Wired up in phase 5 (script mode) and phase 7 (REPL).
	return fmt.Errorf("not implemented yet: the chat loop arrives in phase 5")
}

type trustCmd struct {
	Path string `arg:"" optional:"" help:"Project directory containing .strument.star (default: cwd)."`
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
	Chat    chatCmd          `cmd:"" default:"withargs" help:"Chat with a model about the given files (default command)."`
	Trust   trustCmd         `cmd:"" help:"Trust the project's .strument.star config file."`
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
