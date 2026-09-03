package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// builtBinary is the strument binary the CLI-level tests run, built once for
// the package rather than once per test — the build is several seconds and the
// tests that need it all want the same one.
//
// It is deliberately built here, before any test runs, and not inside a test:
// these tests redirect HOME, and a redirected HOME moves GOPATH's default
// module cache ($HOME/go/pkg/mod) into a t.TempDir the build then fills with
// Go's read-only module files — which RemoveAll cannot unlink (macOS CI:
// "TempDir RemoveAll cleanup: permission denied"). Linux never saw it, since
// only darwin redirects HOME.
var builtBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "strument-cli-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, "test setup:", err)
		os.Exit(1)
	}
	builtBinary = filepath.Join(dir, "strument")
	build := exec.Command("go", "build", "-o", builtBinary, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "test setup: go build: %v: %s\n", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
