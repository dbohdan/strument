package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/alecthomas/kong"
	"github.com/miekg/king"
)

type shellCmd struct {
	Shell string `arg:"" enum:"bash,fish,zsh" help:"Shell to generate completions for (bash, fish, or zsh)."`
}

func (c *shellCmd) Run(ctx *kong.Context) error {
	return writeCompletions(io.Writer(outputWriter{}), ctx.Model.Node, c.Shell)
}

type outputWriter struct{}

func (outputWriter) Write(p []byte) (int, error) { return fmt.Print(string(p)) }

func writeCompletions(w io.Writer, node *kong.Node, shell string) error {
	var completion king.Completer
	switch shell {
	case "bash":
		completion = &king.Bash{}
	case "fish":
		completion = &king.Fish{}
	case "zsh":
		completion = &king.Zsh{}
	default:
		return errors.New("unknown shell " + shell + " (choose bash, fish, or zsh)")
	}
	completion.Completion(node, "")
	_, err := w.Write(completion.Out())
	return err
}
