package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dbohdan.com/strument/internal/shlex"
)

// Opening a file in the user's editor, for `strument config edit` and
// `strument history edit`.
//
// The resolution order is age-edit's (github.com/dbohdan/age-edit) without its
// tool-specific variable: VISUAL, then EDITOR, then vi. Same order, same
// fallback, and the same treatment of the value — an argv, split on shell
// rules, never a string handed to a shell.

// editorFallback is what runs when neither variable is set.
//
// vi on every platform, which is age-edit's answer and is wrong on Windows in
// the sense that vi is usually not installed there. It stays wrong on purpose
// rather than becoming a guess at notepad: the failure is a clear one that
// names both variables (see runEditor), and inventing a per-platform default
// would be a decision nobody asked for in a command whose whole job is to
// respect the user's choice of editor.
const editorFallback = "vi"

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
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if argv := shlex.Split(strings.TrimSpace(lookup(name))); len(argv) > 0 {
			return append(argv, path)
		}
	}
	return []string{editorFallback, path}
}

// runEditor opens path and waits, wiring the editor to the real terminal.
//
// The editor inherits the whole environment, deliberately. The rule in
// CLAUDE.md is about who caused the command: everything the *model* causes runs
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
		// the fallback: neither is set and vi is not installed.
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
