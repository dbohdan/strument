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

// quoteToolArg puts s in quotes when it contains whitespace so the boundary
// between a status message and the model-supplied argument it names is always
// visible. Strings without whitespace are returned as-is. When s contains
// double quotes but no single quotes, single-quoting is used instead of
// escaping, because '"foo" bar' reads better than "\"foo\" bar".
func quoteToolArg(s string) string {
	if !strings.ContainsAny(s, " \t\n\r") {
		return s
	}

	if strings.Contains(s, `"`) && !strings.Contains(s, `'`) {
		return `'` + s + `'`
	}

	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// This file holds the observation tools — read, grep, glob, ls, and check.
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
func (i *Inspector) runRead(tc llm.ToolCall) string {
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

	ft, err := i.Files.Read(a.Path, a.Offset, a.Limit)
	if err != nil {
		return fmt.Sprintf("Could not read %s: %v", quoteToolArg(a.Path), err)
	}
	i.Out.Toolf("Read %s", readSummary(ft))

	var b strings.Builder
	if ft.Link != "" {
		fmt.Fprintf(&b, "%s -> %s (%d lines)\n", ft.Path, ft.Link, ft.Total)
	} else {
		fmt.Fprintf(&b, "%s (%d lines)\n", ft.Path, ft.Total)
	}
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
	p := quoteToolArg(ft.Path)
	if len(ft.Lines) == 0 {
		return fmt.Sprintf("%s (%d lines, nothing at offset %d)", p, ft.Total, ft.Start)
	}
	if ft.Start == 1 && !ft.Truncated {
		return fmt.Sprintf("%s (%d lines)", p, ft.Total)
	}
	return fmt.Sprintf("%s (lines %d-%d of %d)", p, ft.Start, ft.Start+len(ft.Lines)-1, ft.Total)
}

// runGrep answers a grep call.
func (i *Inspector) runGrep(tc llm.ToolCall) string {
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
	modeName := "files"
	switch a.Mode {
	case "", "files":
	case "content":
		mode, modeName = workspace.GrepContent, "content"
	case "count":
		mode, modeName = workspace.GrepCount, "count"
	default:
		return fmt.Sprintf("Unknown mode %q. Use \"files\", \"content\", or \"count\".", a.Mode)
	}

	res, err := i.Files.Grep(workspace.GrepQuery{
		Pattern: a.Pattern, Glob: a.Glob, Dir: a.Path, Mode: mode, IgnoreCase: a.IgnoreCase,
	})
	if err != nil {
		return fmt.Sprintf("The search pattern was not valid: %v", err)
	}
	// The scope and the mode go in the line, not just the pattern. They shape
	// the answer completely — a glob that admits no files turns any pattern into
	// "no matches" — so a report naming only the pattern blames the wrong
	// argument, and leaves both the model and the user with nothing to correct.
	query := fmt.Sprintf("%s%s as %s", quoteToolArg(a.Pattern), grepScope(a.Path, a.Glob), modeName)
	i.Out.Toolf("Searched for %s — %s", query, matchSummary(res))

	if len(res.Files) == 0 {
		return grepNothing(query, res)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s for %s.\n\n", matchSummary(res), query)
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
	// A clipped line ends in "…", but only saying so once makes it a fact about
	// the search rather than a character that happened to be in the file.
	if res.Shortened > 0 {
		// plural carries the count itself; passing it again alongside printed
		// "49 long 49 lines were shortened". Found by the first real use of
		// `strument tool grep`, which is the argument for the command.
		fmt.Fprintf(&b, "\n(%s shortened, marked with \"…\". Read the file for the whole line.)\n",
			plural(res.Shortened, "long line was", "long lines were"))
	}
	return truncateResult(b.String())
}

// globSyntaxNote explains the two glob shapes that silently admit nothing. Both
// are natural first guesses, and both used to fail to an empty result that named
// only the pattern, so a reader concluded the code was not there.
const globSyntaxNote = "A glob is matched against the whole path, segment by segment. " +
	"\"*.go\" therefore matches only files in the project root, and a bare directory name " +
	"matches nothing at all; use \"**/*.go\" to reach every directory. To search one subtree, " +
	"grep's \"path\" argument is the direct way to say so."

// grepScope renders the part of a search that is not the pattern, so a report
// can name it. Empty when the search covered the whole project.
func grepScope(dir, glob string) string {
	var b strings.Builder
	if d := strings.TrimSpace(dir); d != "" {
		fmt.Fprintf(&b, " under %s", quoteToolArg(d))
	}
	if g := strings.TrimSpace(glob); g != "" {
		fmt.Fprintf(&b, " matching %s", quoteToolArg(g))
	}
	return b.String()
}

// grepNothing explains an empty result, which is three different situations
// that used to be reported as one.
//
// Saying "no matches" when the scope admitted no files states that the pattern
// is absent from the project. Watched live, that is exactly how a model read it
// — a search scoped with a directory-shaped glob returned nothing, and the next
// step widened the *pattern*, because the pattern was the only thing the report
// mentioned. The identifier was in 21 files at the time.
func grepNothing(query string, res workspace.GrepResult) string {
	switch {
	case res.InScope == 0:
		return "No files were searched for " + query + ": nothing is in that scope, " +
			"so the pattern was never tested.\n\n" + globSyntaxNote
	case res.Scanned == 0:
		return fmt.Sprintf("No files were searched for %s: %s in that scope, but every one of "+
			"them is binary or over the size limit.", query, plural(res.InScope, "file is", "files are"))
	default:
		return fmt.Sprintf("No matches for %s. %s searched.", query,
			plural(res.Scanned, "file", "files"))
	}
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
func (i *Inspector) runGlob(tc llm.ToolCall) string {
	var a struct {
		Pattern string `json:"pattern"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "The required \"pattern\" argument was missing."
	}

	paths, trunc, err := i.Files.Glob(a.Pattern)
	if err != nil {
		return fmt.Sprintf("Could not match %s: %v", quoteToolArg(a.Pattern), err)
	}
	i.Out.Toolf("Matched %s against %s", plural(len(paths), "file", "files"), quoteToolArg(a.Pattern))

	if len(paths) == 0 {
		// The same rules that make a grep glob silently admit nothing apply
		// here, so the same explanation does. It is cheaper to say it than to
		// let a caller conclude the files do not exist.
		return fmt.Sprintf("No files match %s.\n\n%s", quoteToolArg(a.Pattern), globSyntaxNote)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s matching %s:\n\n", plural(len(paths), "file", "files"), quoteToolArg(a.Pattern))
	b.WriteString(strings.Join(paths, "\n"))
	b.WriteString("\n")
	if trunc.Any() {
		b.WriteString("\n(Results were cut short by a limit; narrow the pattern to see the rest.)\n")
	}
	return truncateResult(b.String())
}

// runLS answers an ls call.
func (i *Inspector) runLS(tc llm.ToolCall) string {
	var a struct {
		Path string `json:"path"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}

	entries, err := i.Files.List(a.Path)
	if err != nil {
		return fmt.Sprintf("Could not list %s: %v", displayDir(a.Path), err)
	}
	i.Out.Toolf("Listed %s (%s)", quoteToolArg(displayDir(a.Path)), plural(len(entries), "entry", "entries"))

	if len(entries) == 0 {
		return displayDir(a.Path) + " is empty."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n\n", displayDir(a.Path))
	for _, e := range entries {
		switch {
		case e.IsDir:
			fmt.Fprintf(&b, "%s/\n", e.Path)
		case e.Link != "":
			// Named the way ls -l names it. Without this a symlink and its
			// target read as two identical files, and a model shown both spends
			// the turn deciding which one is meant — observed live, on a
			// dotfiles directory where aliases.sh links to real/aliases.sh.
			fmt.Fprintf(&b, "%s -> %s\n", e.Path, e.Link)
		default:
			b.WriteString(e.Path + "\n")
		}
	}
	return truncateResult(b.String())
}

func displayDir(p string) string {
	if strings.TrimSpace(p) == "" {
		return "the project root"
	}
	return p
}

// runCheckTool runs the configured checks and returns their output.
//
// It never takes a command, only a name, so nothing the model says can change
// what runs. Each check is executed as argv without a shell, the same rule the
// git port follows.
func (c *Coder) runCheckTool(ctx context.Context, tc llm.ToolCall) string {
	var a struct {
		Name string `json:"name"`
	}
	if msg := decodeArgs(tc, &a); msg != "" {
		return msg
	}
	if len(c.Check) == 0 {
		return "No checks are configured for this project."
	}

	names := checkNames(c.Check)
	if name := strings.TrimSpace(a.Name); name != "" {
		if indexCheck(c.Check, name) < 0 {
			return fmt.Sprintf("There is no check named %q. Configured checks: %s.",
				name, strings.Join(names, ", "))
		}
		names = []string{name}
	}
	// The model asked, so it gets the whole transcript either way — a passing
	// run is information too.
	transcript, _ := c.runChecks(ctx, names)
	return transcript
}

// runChecks runs the named checks in the order given, stopping at the first
// failure. It returns the transcript and whether everything passed.
//
// Order is the caller's, wherever the list came from: check() uses the order
// the checks were declared in, check_auto uses the order that list was written
// in. One rule, stated once — checks run in the order they are listed.
func (c *Coder) runChecks(ctx context.Context, names []string) (transcript string, passed bool) {
	var b strings.Builder
	for _, name := range names {
		i := indexCheck(c.Check, name)
		if i < 0 {
			// Unreachable through either caller: the tool validates the name and
			// config validates check_auto at load. Report rather than skip, so a
			// future third caller cannot silently check nothing.
			fmt.Fprintf(&b, "%s: no such check is configured.\n", name)
			return truncateResult(b.String()), false
		}
		ch := c.Check[i]

		// The command prints before it runs, because a suite can take a minute
		// and silence in the middle of a turn reads as a hang. The ‹check› tag
		// marks the line as the harness announcing a check rather than a
		// transcript of the model's own words, the same reason ‹shell› prefixes
		// a confirmation's purpose.
		c.Out.Toolf("‹check› %s $ %s", ch.Name, strings.Join(ch.Argv, " "))
		exit, output := c.runCheck(ctx, ch)
		if exit == 0 {
			// A passing check's output is noise to the user — with check_auto on
			// it lands on every editing turn and buries the diffs they are here to
			// read. The model still gets the whole transcript below: to it, what a
			// passing run printed is information.
			c.Out.Toolf("passed")
		} else {
			// A failure is the one thing here that has to be read, so it keeps the
			// plain color and all of its output.
			c.Out.Printf("failed (exit status %d)", exit)
			if trimmed := strings.TrimRight(output, "\n"); trimmed != "" {
				c.Out.Printf("%s", trimmed)
			}
		}

		fmt.Fprintf(&b, "%s: %s\nExit status: %d\n", ch.Name, strings.Join(ch.Argv, " "), exit)
		if strings.TrimSpace(output) != "" {
			fmt.Fprintf(&b, "Output:\n%s\n", output)
		}
		if exit != 0 {
			// Stop at the first failure: the later checks would mostly report
			// the same breakage, and the user ordered them so the fast ones
			// come first.
			if len(names) > 1 {
				b.WriteString("\nStopped here; later checks were not run.\n")
			}
			return truncateResult(b.String()), false
		}
		b.WriteString("\n")
	}
	return truncateResult(b.String()), true
}

// runCheck executes one check's argv, merging stdout and stderr.
func (c *Coder) runCheck(ctx context.Context, ch config.Check) (int, string) {
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

func indexCheck(checks []config.Check, name string) int {
	for i, ch := range checks {
		if ch.Name == name {
			return i
		}
	}
	return -1
}

func checkNames(checks []config.Check) []string {
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
