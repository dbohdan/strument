package coder

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// runAndShow runs a confirmed command through the configured runner, echoing
// "Running <cmd>" and then the captured output to the user — the output
// otherwise only reaches the model (as a tool result or chat addition), never
// the terminal. Returns the exit code and captured output.
func (c *Coder) runAndShow(ctx context.Context, command string) (int, string) {
	c.Out.Printf("")
	c.Out.Toolf("Running %s", quoteToolArg(command))

	runner := c.Runner
	if runner == nil {
		runner = PipeRunner{}
	}
	exitCode, output, err := runner.Run(ctx, command, c.Root)
	if err != nil {
		c.Out.Errorf("Error running command: %v", err)
	}
	if output != "" {
		// Printf adds the trailing newline; trim the runner's so output that
		// already ends in one doesn't print a blank line.
		c.Out.Printf("%s", strings.TrimRight(output, "\n"))
	}
	return exitCode, output
}

// PipeRunner is the default deterministic CommandRunner: the whole
// block through one shell, stdout+stderr merged. PTY execution is opt-in
// elsewhere.
type PipeRunner struct {
	// MaxBytes caps captured output; 0 means the default (64 KiB).
	MaxBytes int
}

func (r PipeRunner) Run(ctx context.Context, block string, cwd string) (int, string, error) {
	maxBytes := r.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}

	var output bytes.Buffer
	file, err := syntax.NewParser().Parse(strings.NewReader(block), "")
	if err != nil {
		return -1, "", err
	}

	runner, err := interp.New(
		// nil stdin rather than the output buffer, which was also passed as
		// stdin. That wiring is self-referential: a command reading stdin reads
		// what the block has printed so far, and reading a bytes.Buffer drains
		// it. A standalone reproduction does exactly that — `echo one; echo two;
		// cat -n` comes back as the numbered version of its own output — though
		// it does not reproduce through this function, and I could not isolate
		// what differs. So this is a correctness change rather than a fix for
		// observed misbehavior: a tool-invoked command has no user at a keyboard,
		// exec.Command gave it an empty stdin before the interpreter replaced it,
		// and nil says that unambiguously instead of relying on whatever keeps
		// the self-reference from biting.
		interp.StdIO(nil, &output, &output),
		interp.Dir(cwd),
	)
	if err != nil {
		return -1, "", err
	}
	err = runner.Run(ctx, file)

	captured := output.String()
	if len(captured) > maxBytes {
		captured = captured[:maxBytes] + "\n… output truncated"
	}

	exitCode := 0
	if err != nil {
		var status interp.ExitStatus
		if errors.As(err, &status) {
			exitCode = int(status)
			err = nil
		} else {
			exitCode = -1
		}
	}
	return exitCode, captured, err
}
