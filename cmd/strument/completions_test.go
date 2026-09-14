package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// The completion scripts are hand-written, so they can drift from the CLI, and
// the drift is silent both ways: a flag that stops completing is not something
// anyone reports, and a name the script offers for a command that no longer
// exists is only discovered by someone pressing Tab and being told to run it.
//
// This checked one direction for a long time — every kong flag had to appear in
// each script — and three names survived in the scripts for months because
// nothing looked the other way: a `version` command (it is `--version`, a
// kong.VersionFlag, and fish offered it as a subcommand), a `--yes-shell` flag
// that had been removed, and a `-r` short for `tool --root` that never existed.
// Kong is the source of truth in both directions now.
//
// What is deliberately not checked: that every *nested* subcommand name the
// scripts offer exists. A bare word in a shell script is a command name, an
// option's value, or a description; telling them apart needs a parser for each
// shell, and the top-level list — where the one real bug lived — is exact.

var (
	// A long option as each shell spells it. This matcher was wrong twice
	// before it was right: first looking for "--name" in both, which fish
	// fails on every flag it does support, then accepting only fish's "-l
	// name". A check that fires for the wrong reason is not a check.
	bashLong = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)
	fishLong = regexp.MustCompile(`(?:^|\s)-l\s+([a-z][a-z0-9-]*)`)
	fishHasL = func(n string) *regexp.Regexp {
		return regexp.MustCompile(`(?:^|\s)-l\s+` + regexp.QuoteMeta(n) + `(?:\s|$)`)
	}
	bashHasLong = func(n string) *regexp.Regexp {
		return regexp.MustCompile(`--` + regexp.QuoteMeta(n) + `\b`)
	}
	// The top-level command list, as each script states it.
	bashCommandList = regexp.MustCompile(`_strument_commands="([^"]*)"`)
	fishTopLevel    = regexp.MustCompile(`-n __fish_use_subcommand -a (\S+)`)
)

// Flags kong defines that a completion script has no business offering, and
// shell words that look like long options and are not.
var notStrumentFlags = map[string]bool{
	"help":    true, // every shell offers it or the user types it blind
	"version": true, // same, and it is also not a command — see below
}

// cliModel walks the parsed CLI once: every command name, the top-level ones,
// and every flag.
func cliModel(t *testing.T) (commands, topLevel, flags []string) {
	t.Helper()
	var root cli
	parser, err := kong.New(&root, kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	seenFlag := map[string]bool{}
	var walk func(n *kong.Node, depth int)
	walk = func(n *kong.Node, depth int) {
		for _, f := range n.Flags {
			if f.Hidden || notStrumentFlags[f.Name] || seenFlag[f.Name] {
				continue
			}
			seenFlag[f.Name] = true
			flags = append(flags, f.Name)
		}
		for _, c := range n.Children {
			if c.Type == kong.CommandNode {
				commands = append(commands, c.Name)
				if depth == 0 {
					topLevel = append(topLevel, c.Name)
				}
			}
			walk(c, depth+1)
		}
	}
	walk(parser.Model.Node, 0)
	return commands, topLevel, flags
}

func completionScripts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, shell := range []string{"bash", "fish"} {
		body, err := os.ReadFile(filepath.Join("completions", "strument."+shell))
		if err != nil {
			t.Fatal(err)
		}
		out[shell] = string(body)
	}
	return out
}

func TestCompletionsCoverEveryFlag(t *testing.T) {
	_, _, flags := cliModel(t)
	if len(flags) < 20 {
		t.Fatalf("the walk found only %d flags; it is not reaching the subcommands", len(flags))
	}
	has := map[string]func(string) *regexp.Regexp{"bash": bashHasLong, "fish": fishHasL}
	for shell, script := range completionScripts(t) {
		for _, name := range flags {
			if !has[shell](name).MatchString(script) {
				t.Errorf("the %s completion is missing --%s (add it to "+
					"cmd/strument/completions/strument.%s)", shell, name, shell)
			}
		}
	}
}

// The other direction. A flag the scripts offer and the CLI does not is worse
// than a missing one: it completes, the user types it, and the program refuses.
func TestCompletionsOfferNoFlagTheCLIRejects(t *testing.T) {
	_, _, flags := cliModel(t)
	known := map[string]bool{}
	for _, f := range flags {
		known[f] = true
	}
	// kong accepts these everywhere; the scripts may offer them.
	known["help"] = true
	known["version"] = true

	patterns := map[string]*regexp.Regexp{"bash": bashLong, "fish": fishLong}
	for shell, script := range completionScripts(t) {
		found := patterns[shell].FindAllStringSubmatch(offeringLines(shell, script), -1)
		if len(found) < 20 {
			t.Fatalf("the %s long-option pattern matched %d names, fewer than the flags "+
				"there are; it has stopped recognising the script and this check is vacuous",
				shell, len(found))
		}
		for _, m := range found {
			if !known[m[1]] {
				t.Errorf("the %s completion offers --%s, which the CLI does not accept. "+
					"Remove it from cmd/strument/completions/strument.%s.", shell, m[1], shell)
			}
		}
	}
}

// Top-level commands, both directions and exactly. This is where `version` was
// offered as a command for as long as the scripts existed, and where `chat` —
// nameable as well as being the default — was never offered at all.
func TestCompletionsOfferExactlyTheTopLevelCommands(t *testing.T) {
	_, topLevel, _ := cliModel(t)
	if len(topLevel) < 5 {
		t.Fatalf("the walk found only %d top-level commands: %v", len(topLevel), topLevel)
	}
	scripts := completionScripts(t)

	got := map[string][]string{
		"bash": strings.Fields(submatch(t, bashCommandList, scripts["bash"], "bash")),
		"fish": allSubmatches(fishTopLevel, scripts["fish"]),
	}
	for shell, offered := range got {
		slices.Sort(offered)
		want := slices.Clone(topLevel)
		slices.Sort(want)
		if !slices.Equal(offered, want) {
			t.Errorf("%s top-level commands:\n offered %v\n want    %v\n"+
				"Every one must exist, and every one that exists must be offered.",
				shell, offered, want)
		}
	}
}

// Subcommand names, one direction: each must appear somewhere in each script.
// The reverse is left to the top-level check above, for the reason in the file
// comment.
func TestCompletionsMentionEverySubcommand(t *testing.T) {
	commands, _, _ := cliModel(t)
	if len(commands) < 15 {
		t.Fatalf("the walk found only %d commands: %v", len(commands), commands)
	}
	for shell, script := range completionScripts(t) {
		for _, name := range commands {
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(script) {
				t.Errorf("the %s completion never mentions the command %q", shell, name)
			}
		}
	}
}

// offeringLines keeps only the lines of a script that actually offer something,
// because "what the script offers" is not the same as "what the file contains".
// Two false positives made that concrete: this file's own comment naming the
// removed --yes-shell, and fish's `set -l commands …`, whose -l is a variable
// scope and not a long option.
func offeringLines(shell, script string) string {
	var out strings.Builder
	for line := range strings.SplitSeq(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if shell == "fish" && !strings.HasPrefix(trimmed, "complete ") {
			continue
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

func submatch(t *testing.T, re *regexp.Regexp, s, shell string) string {
	t.Helper()
	m := re.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("the %s completion no longer declares its command list the way this test "+
			"reads it (%s); the check would otherwise pass by finding nothing", shell, re)
	}
	return m[1]
}

func allSubmatches(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}
