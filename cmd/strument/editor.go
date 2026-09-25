package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"dbohdan.com/strument/internal/shlex"
)

// Opening a file in the user's editor, for `strument config edit` and
// `strument history edit`.
//
// The resolution order is age-edit's (github.com/dbohdan/age-edit) without its
// tool-specific variable: VISUAL, then EDITOR, then a default. Same order and
// the same treatment of the value — an argv, split on shell rules, never a
// string handed to a shell. The default is where this parts company with
// age-edit, which says vi everywhere; see fallbackEditors.

// fallbackEditors names the editors to try, best first, when neither VISUAL nor
// EDITOR is set.
//
// On Unix this is vi, which POSIX requires and which every system that has a
// terminal has.
//
// Windows has no equivalent guarantee. EDIT.COM has not shipped in a 64-bit
// Windows for years and nothing replaced it in the base install until
// Microsoft Edit (github.com/microsoft/edit), a console editor that recent
// Windows 11 carries as `edit`. So the list is tried in order rather than
// asserted:
//
//   - `edit` first, and console-first is the whole point. Someone who reached
//     this program over SSH has a terminal and no desktop, and that is a real
//     way to use a Windows box now.
//   - `notepad` second. It is on every Windows install, and for the desktop
//     case — which is most of them — it is the thing that actually opens. Over
//     SSH it will try to put a window on a station nobody can see, so it is
//     second and not first.
//
// A guess at `vi` on Windows would have failed for nearly everyone; a bare
// `notepad` would fail for the remote case with no better option tried. Hence
// a probe. When nothing on the list is found the last entry is used anyway, so
// runEditor's error names a program rather than nothing at all.
func fallbackEditors(goos string) []string {
	if goos == "windows" {
		return []string{"edit", "notepad"}
	}
	return []string{"vi"}
}

// editorArgv is the command that opens path.
//
// $VISUAL and $EDITOR are commands, not program names — `code --wait`,
// `emacsclient -nw`, `"/opt/My Editor/bin/ed" --wait` — so the value is split
// the way a shell would split it and executed directly. Not passed to a shell:
// a path with a space or a dollar sign in it would then be the shell's problem
// rather than ours, and this is exactly the seam where the difference bites.
//
// Whitespace-only is treated as unset. An editor variable set to " " is
// somebody's stray quoting, not a request to run a program with no name.
func editorArgv(path string, lookup func(string) string) []string {
	return editorArgvFor(path, lookup, exec.LookPath, runtime.GOOS)
}

// editorArgvFor is editorArgv with the environment, the PATH search and the
// platform all named rather than taken from the host.
//
// The seam exists for the same reason shlex.SplitWith's does: the branch that
// is easiest to get wrong is the one CI is least likely to exercise the way a
// user meets it, so both platforms' answers have to be reachable from a test on
// either host.
func editorArgvFor(path string, lookup func(string) string, look func(string) (string, error), goos string) []string {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if argv := shlex.Split(strings.TrimSpace(lookup(name))); len(argv) > 0 {
			return append(argv, path)
		}
	}
	candidates := fallbackEditors(goos)
	for _, c := range candidates {
		if _, err := look(c); err == nil {
			return []string{c, path}
		}
	}
	return []string{candidates[len(candidates)-1], path}
}

// runEditor opens path and waits, wiring the editor to the real terminal.
//
// The editor inherits the whole environment, deliberately. The rule in
// AGENTS.md is about who caused the command: everything the *model* causes runs
// under the env_allow filter, and everything the *user* typed inherits
// everything, because they typed it. `strument config edit` is the user
// reaching for their own editor, which may well read a variable this program
// has never heard of.
func runEditor(path string) error {
	argv := editorArgv(path, os.Getenv)
	// context.Background and not a deadline: the process being started is a
	// person editing a file, and there is no length of time after which
	// Strument should decide they are done.
	//nolint:gosec // Argv from the user's own $VISUAL/$EDITOR, never from the model, and never a shell string.
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		// Naming both variables, because the common case for this failing is
		// the fallback: neither is set and nothing on the platform's list was
		// installed.
		return fmt.Errorf("could not run the editor %s: %w\n"+
			"  Set `VISUAL` or `EDITOR` to the editor you want",
			strings.Join(argv[:len(argv)-1], " "), err)
	}
	return nil
}

// editFile makes sure path's directory exists, then opens it.
//
// The directory and not the file. An editor asked to open a path that does not
// exist offers an empty buffer and writes it on save, which is what someone
// running `config edit` in a fresh install wants; creating the file first would
// instead leave an empty config behind when they change their mind and quit.
//
// Nor is it seeded with the starter block from config.missingConfigError, which
// was tempting: an abandoned seed would be a config that loads, so the careful
// "no configuration file yet" screen a new user reads would be replaced by
// whatever that block does on a machine with no API key set.
func editFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return runEditor(path)
}
