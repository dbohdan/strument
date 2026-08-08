package coder

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// runAndShow runs a confirmed command through the configured runner, echoing
// "Running <cmd>" and then the captured output to the user — the output
// otherwise only reaches the model (as a tool result or chat addition), never
// the terminal. Returns the exit code and captured output.
func (c *Coder) runAndShow(ctx context.Context, command string) (int, string) {
	c.Out.Printf("")
	c.Out.Printf("Running %s", command)

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
	cmd := exec.CommandContext(ctx, shellPath(), "-c", block) //nolint:gosec // Running user-confirmed model shell commands is this feature.
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()

	maxBytes := r.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	output := string(out)
	if len(output) > maxBytes {
		output = output[:maxBytes] + "\n... (output truncated)"
	}

	exitCode := 0
	if err != nil {
		ee := &exec.ExitError{}
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
			err = nil
		} else {
			exitCode = -1
		}
	}
	return exitCode, output, err
}

func shellPath() string {
	return "/bin/sh"
}
