package coder

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// addShellCommand collects a model-proposed shell block, deduped by first
// occurrence in response order.
func (c *Coder) addShellCommand(block string) {
	if !slices.Contains(c.shellCommands, block) {
		c.shellCommands = append(c.shellCommands, block)
	}
}

// runShellCommands offers and runs the collected blocks.
// suggest_shell_commands=false gates execution, not just the prompt
// variant. shellCommands resets only in initBeforeMessage, so blocks from a
// failed attempt run after a later reflected attempt succeeds.
func (c *Coder) runShellCommands(ctx context.Context) string {
	if !c.SuggestShellCommands {
		return ""
	}

	var accumulated strings.Builder
	for _, block := range c.shellCommands {
		output := c.handleShellBlock(ctx, block)
		if output != "" {
			accumulated.WriteString(output)
			accumulated.WriteString("\n\n")
		}
	}
	return accumulated.String()
}

// handleShellBlock confirms and runs one block through a single shell
// (whole block, one shell, merged output with exit
// status visible to the model even when empty).
func (c *Coder) handleShellBlock(ctx context.Context, block string) string {
	lines := strings.Split(strings.TrimSpace(block), "\n")
	commandCount := 0
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(t, "#") {
			commandCount++
		}
	}
	prompt := "Run shell command?"
	if commandCount != 1 {
		prompt = "Run shell commands?"
	}
	yes, _ := c.Confirm.Confirm(ConfirmRequest{
		Prompt:              prompt,
		Subject:             strings.Join(lines, "\n"),
		ExplicitYesRequired: true,
		AllowNever:          true,
		Group:               "run-shell",
	})
	if !yes {
		return ""
	}

	command := strings.TrimSpace(block)
	exitCode, output := c.runAndShow(ctx, command)
	result := fmt.Sprintf("Command: %s\nExit status: %d\nOutput:\n%s", command, exitCode, output)

	addYes, _ := c.Confirm.Confirm(ConfirmRequest{
		Prompt:     "Add command output to the chat?",
		AllowNever: true,
		Group:      "add-output",
	})
	if !addYes {
		return ""
	}
	numLines := len(strings.Split(strings.TrimSpace(result), "\n"))
	plural := "lines"
	if numLines == 1 {
		plural = "line"
	}
	c.Out.Printf("Added %d %s of output to the chat.", numLines, plural)
	return result
}

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
