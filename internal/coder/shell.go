package coder

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// addShellCommand collects a model-proposed shell block, deduped by first
// occurrence in response order (§6.1).
func (c *Coder) addShellCommand(block string) {
	if !slices.Contains(c.shellCommands, block) {
		c.shellCommands = append(c.shellCommands, block)
	}
}

// runShellCommands offers and runs the collected blocks (§6.2-6.4).
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
// (§6.3 [Divergence]: whole block, one shell, merged output with exit
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
	c.Out.Print("")
	c.Out.Print("Running %s", command)

	runner := c.Runner
	if runner == nil {
		runner = PipeRunner{}
	}
	exitCode, output, err := runner.Run(ctx, command, c.Root)
	if err != nil {
		c.Out.Error("Error running command: %v", err)
	}

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
	c.Out.Print("Added %d %s of output to the chat.", numLines, plural)
	return result
}

// PipeRunner is the default deterministic CommandRunner (§6.3): the whole
// block through one shell, stdout+stderr merged. PTY execution is opt-in
// elsewhere.
type PipeRunner struct {
	// MaxBytes caps captured output; 0 means the default (64 KiB).
	MaxBytes int
}

func (r PipeRunner) Run(ctx context.Context, block string, cwd string) (int, string, error) {
	cmd := exec.CommandContext(ctx, shellPath(), "-c", block)
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
		if ee, ok := err.(*exec.ExitError); ok {
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
