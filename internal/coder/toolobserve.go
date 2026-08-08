package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/workspace"
)

// This file holds the observation tools — read, grep, glob, ls, and verify.
// None of them changes anything, so none of them asks for confirmation, and
// each answers immediately rather than being batched like the edits.
//
// Every result is written for a reader who cannot see the terminal: it says
// what was searched, what was found, and — when a limit cut the answer short —
// that there is more. A silently truncated result reads as "nothing else
// exists" and sends the next step down the wrong path.

// maxToolOutputBytes caps a single tool result. Beyond this the result is cut
// with a note, because one runaway command must not eat the context window.
const maxToolOutputBytes = 60_000

// decodeArgs unmarshals a call's arguments into dst, returning a model-facing
// message on failure.
func decodeArgs(tc llm.ToolCall, dst any) string {
	if strings.TrimSpace(tc.Arguments) == "" {
		return "" // no arguments is legitimate for tools whose fields are optional
	}
	if err := json.Unmarshal([]byte(tc.Arguments), dst); err != nil {
		return fmt.Sprintf("The arguments were not valid JSON: %v", err)
	}
	return ""
}

// runRead answers a read call with a numbered window of the file.
func (c *Coder) runRead(tc llm.ToolCall) string {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}
	if strings.TrimSpace(a.Path) == "" {
		return "The required \"path\" argument was missing."
	}

	ft, err := c.Files.Read(a.Path, a.Offset, a.Limit)
	if err != nil {
		return fmt.Sprintf("Could not read %s: %v", a.Path, err)
	}
	c.Out.Printf("Read %s", readSummary(ft))

	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d lines)\n", ft.Path, ft.Total)
	if len(ft.Lines) == 0 {
		fmt.Fprintf(&b, "\nLine %d is past the end of the file.\n", ft.Start)
		return b.String()
	}
	// Line numbers give stable referents for talking about code, which is worth
	// the tokens in a conversation that will discuss specific lines.
	width := len(strconv.Itoa(ft.Start + len(ft.Lines) - 1))
	for i, line := range ft.Lines {
		fmt.Fprintf(&b, "%*d\t%s\n", width, ft.Start+i, line)
	}
	if ft.Truncated {
		next := ft.Start + len(ft.Lines)
		fmt.Fprintf(&b, "\n(Lines %d-%d of %d. Read from offset %d for more.)\n",
			ft.Start, next-1, ft.Total, next)
	}
	return b.String()
}

// readSummary is the one-line outcome shown to the user.
func readSummary(ft workspace.FileText) string {
	if len(ft.Lines) == 0 {
		return fmt.Sprintf("%s (%d lines, nothing at offset %d)", ft.Path, ft.Total, ft.Start)
	}
	if ft.Start == 1 && !ft.Truncated {
		return fmt.Sprintf("%s (%d lines)", ft.Path, ft.Total)
	}
	return fmt.Sprintf("%s (lines %d-%d of %d)", ft.Path, ft.Start, ft.Start+len(ft.Lines)-1, ft.Total)
}

// runGrep answers a grep call.
func (c *Coder) runGrep(tc llm.ToolCall) string {
	var a struct {
		Pattern    string `json:"pattern"`
		Glob       string `json:"glob"`
		Path       string `json:"path"`
		Mode       string `json:"mode"`
		IgnoreCase bool   `json:"ignore_case"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "The required \"pattern\" argument was missing."
	}

	mode := workspace.GrepFiles
	switch a.Mode {
	case "", "files":
	case "content":
		mode = workspace.GrepContent
	case "count":
		mode = workspace.GrepCount
	default:
		return fmt.Sprintf("Unknown mode %q. Use \"files\", \"content\", or \"count\".", a.Mode)
	}

	res, err := c.Files.Grep(workspace.GrepQuery{
		Pattern: a.Pattern, Glob: a.Glob, Dir: a.Path, Mode: mode, IgnoreCase: a.IgnoreCase,
	})
	if err != nil {
		return fmt.Sprintf("The search pattern was not valid: %v", err)
	}
	c.Out.Printf("Searched for %s — %s", a.Pattern, matchSummary(res))

	if len(res.Files) == 0 {
		return fmt.Sprintf("No matches for %s.", a.Pattern)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", matchSummary(res))
	for _, f := range res.Files {
		switch mode {
		case workspace.GrepContent:
			for _, l := range f.Lines {
				fmt.Fprintf(&b, "%s:%d: %s\n", f.Path, l.Number, l.Text)
			}
		case workspace.GrepCount:
			fmt.Fprintf(&b, "%s: %d\n", f.Path, f.Count)
		case workspace.GrepFiles:
			fmt.Fprintf(&b, "%s\n", f.Path)
		}
	}
	if res.Truncated.Any() {
		b.WriteString("\n(Results were cut short by a limit. Narrow the search with glob or path to see the rest.)\n")
	}
	return truncateResult(b.String())
}

// matchSummary describes a search's outcome in one line.
func matchSummary(res workspace.GrepResult) string {
	if len(res.Files) == 0 {
		return "no matches"
	}
	return fmt.Sprintf("%s in %s", plural(res.Total, "match", "matches"), plural(len(res.Files), "file", "files"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// runGlob answers a glob call.
func (c *Coder) runGlob(tc llm.ToolCall) string {
	var a struct {
		Pattern string `json:"pattern"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "The required \"pattern\" argument was missing."
	}

	paths, trunc, err := c.Files.Glob(a.Pattern)
	if err != nil {
		return fmt.Sprintf("Could not match %s: %v", a.Pattern, err)
	}
	c.Out.Printf("Matched %s against %s", plural(len(paths), "file", "files"), a.Pattern)

	if len(paths) == 0 {
		return fmt.Sprintf("No files match %s.", a.Pattern)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s matching %s:\n\n", plural(len(paths), "file", "files"), a.Pattern)
	b.WriteString(strings.Join(paths, "\n"))
	b.WriteString("\n")
	if trunc.Any() {
		b.WriteString("\n(Results were cut short by a limit; narrow the pattern to see the rest.)\n")
	}
	return truncateResult(b.String())
}

// runLS answers an ls call.
func (c *Coder) runLS(tc llm.ToolCall) string {
	var a struct {
		Path string `json:"path"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}

	entries, err := c.Files.List(a.Path)
	if err != nil {
		return fmt.Sprintf("Could not list %s: %v", displayDir(a.Path), err)
	}
	c.Out.Printf("Listed %s (%s)", displayDir(a.Path), plural(len(entries), "entry", "entries"))

	if len(entries) == 0 {
		return displayDir(a.Path) + " is empty."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n\n", displayDir(a.Path))
	for _, e := range entries {
		if e.IsDir {
			fmt.Fprintf(&b, "%s/\n", e.Path)
			continue
		}
		b.WriteString(e.Path + "\n")
	}
	return truncateResult(b.String())
}

func displayDir(p string) string {
	if strings.TrimSpace(p) == "" {
		return "the project root"
	}
	return p
}

// runVerify runs the configured checks and returns their output.
//
// It never takes a command, only a name, so nothing the model says can change
// what runs. Each check is executed as argv without a shell, the same rule the
// git port follows.
func (c *Coder) runVerify(ctx context.Context, tc llm.ToolCall) string {
	var a struct {
		Name string `json:"name"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}
	if len(c.Verify) == 0 {
		return "No checks are configured for this project."
	}

	checks := c.Verify
	if name := strings.TrimSpace(a.Name); name != "" {
		i := indexCheck(c.Verify, name)
		if i < 0 {
			return fmt.Sprintf("There is no check named %q. Configured checks: %s.",
				name, strings.Join(checkNames(c.Verify), ", "))
		}
		checks = c.Verify[i : i+1]
	}

	var b strings.Builder
	for _, ch := range checks {
		c.Out.Printf("%s $ %s", ch.Name, strings.Join(ch.Argv, " "))
		exit, output := c.runCheck(ctx, ch)
		c.Out.Printf("%s", strings.TrimRight(output, "\n"))

		fmt.Fprintf(&b, "%s: %s\nExit status: %d\n", ch.Name, strings.Join(ch.Argv, " "), exit)
		if strings.TrimSpace(output) != "" {
			fmt.Fprintf(&b, "Output:\n%s\n", output)
		}
		if exit != 0 {
			// Stop at the first failure: the later checks would mostly report
			// the same breakage, and the user ordered them so the fast ones
			// come first.
			if len(checks) > 1 {
				b.WriteString("\nStopped here; later checks were not run.\n")
			}
			return truncateResult(b.String())
		}
		b.WriteString("\n")
	}
	return truncateResult(b.String())
}

// runCheck executes one check's argv, merging stdout and stderr.
func (c *Coder) runCheck(ctx context.Context, ch config.VerifyCheck) (int, string) {
	// The argv is the user's own configuration, reached by name; the model
	// never supplies any part of it, which is what makes running it without a
	// confirmation prompt reasonable.
	cmd := exec.CommandContext(ctx, ch.Argv[0], ch.Argv[1:]...) //nolint:gosec // Argv from the user's config, never from the model.
	cmd.Dir = c.Root
	out, err := cmd.CombinedOutput()
	exit := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		exit = ee.ExitCode()
	case err != nil:
		return -1, fmt.Sprintf("could not run %s: %v", strings.Join(ch.Argv, " "), err)
	}
	return exit, string(out)
}

func indexCheck(checks []config.VerifyCheck, name string) int {
	for i, ch := range checks {
		if ch.Name == name {
			return i
		}
	}
	return -1
}

func checkNames(checks []config.VerifyCheck) []string {
	out := make([]string, 0, len(checks))
	for _, ch := range checks {
		out = append(out, ch.Name)
	}
	return out
}

// truncateResult caps a tool result, saying so rather than cutting silently.
func truncateResult(s string) string {
	if len(s) <= maxToolOutputBytes {
		return s
	}
	return s[:maxToolOutputBytes] + "\n\n(Output was cut short here; it exceeded the size one tool result may carry.)\n"
}
