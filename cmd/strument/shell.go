package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
)

//go:embed completions/strument.bash
var bashCompletion string

//go:embed completions/strument.fish
var fishCompletion string

type shellCmd struct {
	Shell string `arg:"" enum:"bash,fish" help:"Shell to generate completions for (bash or fish)."`
}

func (c *shellCmd) Run() error {
	return writeCompletions(io.Writer(outputWriter{}), c.Shell)
}

type outputWriter struct{}

func (outputWriter) Write(p []byte) (int, error) { return fmt.Print(string(p)) }

func writeCompletions(w io.Writer, shell string) error {
	var completion string
	switch shell {
	case "bash":
		completion = bashCompletion
	case "fish":
		completion = fishCompletion
	default:
		return errors.New("unknown shell " + shell + " (choose bash or fish)")
	}
	_, err := io.WriteString(w, completion)
	return err
}
