package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"dbohdan.com/strument/internal/editor"
)

// editorArgvFor is editor.ArgvFor, named here for the tests that pin its
// resolution order from this package.
func editorArgvFor(path string, lookup func(string) string, look func(string) (string, error), goos string) []string {
	return editor.ArgvFor(path, lookup, look, goos)
}

// runEditor opens path in the user's editor and waits.
func runEditor(path string) error {
	return editor.Run(editor.ArgvFor(path, os.Getenv, exec.LookPath, runtime.GOOS))
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
