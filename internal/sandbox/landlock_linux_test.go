//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"
	"github.com/landlock-lsm/go-landlock/landlock"
	"golang.org/x/sys/unix"
)

// This file is the stage-0 probe for the Landlock design: it asks the kernel
// what actually happens, rather than reasoning about it from documentation.
// Every case here is a plausible way for a sandbox to break an ordinary build,
// which is the failure that gets sandboxes switched off.
//
// Each probe runs in a **fresh child process**. Landlock is monotonic and
// applies to the whole process, so a ruleset installed by one test would
// confine every test after it in the same binary — including the test runner's
// own writes.
//
// Run it with:
//
//	go test ./internal/sandbox/ -run Landlock -v
//
// On a kernel without Landlock every case skips, which is itself a result
// worth having: a modern kernel is not sufficient.

const helperEnv = "STRUMENT_LANDLOCK_PROBE"

// TestLandlockMatrix prints what this kernel does, one line per question.
func TestLandlockMatrix(t *testing.T) {
	av := Probe()
	if !av.Supported() {
		t.Skipf("no Landlock on this kernel (%v) — nothing to probe", av.Err)
	}
	t.Logf("Landlock ABI version: %d", av.ABI)

	for _, tc := range []struct {
		probe string
		asks  string
	}{
		{"exec", "does a read-only rule over / still permit executing binaries?"},
		{"devnull", "is `> /dev/null` denied when / is read-only?"},
		{"refer-without", "does mv across directories fail without WithRefer?"},
		{"refer-with", "does WithRefer fix it?"},
		{"nested-deny", "can a nested rule REDUCE rights (.git/hooks read-only inside a writable project)?"},
		{"unix-socket", "can a process still connect to a Unix socket under the top ABI?"},
		{"ioctl-tty", "does an ioctl on a pty still work without WithIoctlDev?"},
		{"errno", "what does a denied write actually look like to a program?"},
	} {
		t.Run(tc.probe, func(t *testing.T) {
			out, err := runProbe(t, tc.probe)
			t.Logf("Q: %s", tc.asks)
			t.Logf("A: %s", strings.TrimSpace(out))
			if err != nil {
				t.Logf("   (helper exited with %v)", err)
			}
		})
	}
}

// runProbe re-execs this test binary as a one-shot helper.
func runProbe(t *testing.T, probe string) (string, error) {
	t.Helper()
	dir := t.TempDir()

	// A Unix socket for the resolve_unix question, listening in the parent
	// because the child is the one that must be denied.
	if probe == "unix-socket" {
		sock := filepath.Join(dir, "s.sock")
		ln, err := net.Listen("unix", sock)
		if err != nil {
			t.Skipf("cannot create a unix socket here: %v", err)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestLandlockHelper", "-test.v=false")
	cmd.Env = append(os.Environ(), helperEnv+"="+probe, "STRUMENT_LANDLOCK_DIR="+dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestLandlockHelper is the child. It does nothing unless re-exec'd by
// runProbe, so an ordinary `go test` run never installs a ruleset.
func TestLandlockHelper(t *testing.T) {
	probe := os.Getenv(helperEnv)
	if probe == "" {
		t.Skip("not the helper")
	}
	dir := os.Getenv("STRUMENT_LANDLOCK_DIR")
	// The helper reports through stdout and never fails the test: the parent
	// wants the answer, not a verdict.
	fmt.Println(runOneProbe(probe, dir))
}

func runOneProbe(probe, dir string) string {
	top := landlock.V9
	switch probe {
	case "exec":
		if err := top.BestEffort().RestrictPaths(landlock.RODirs("/"), landlock.RWDirs(dir)); err != nil {
			return "restrict failed: " + err.Error()
		}
		out, err := exec.Command("/bin/sh", "-c", "echo ran").CombinedOutput()
		return verdict("exec /bin/sh", err, strings.TrimSpace(string(out)) == "ran")

	case "devnull":
		if err := top.BestEffort().RestrictPaths(landlock.RODirs("/"), landlock.RWDirs(dir)); err != nil {
			return "restrict failed: " + err.Error()
		}
		f, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
		if err == nil {
			_, err = f.WriteString("x")
			f.Close()
		}
		return verdict("open /dev/null for write", err, err == nil)

	case "refer-without", "refer-with":
		a := filepath.Join(dir, "a")
		b := filepath.Join(dir, "b")
		_ = os.MkdirAll(a, 0o700)
		_ = os.MkdirAll(b, 0o700)
		src := filepath.Join(a, "f")
		if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
			return "setup failed: " + err.Error()
		}
		rw := landlock.RWDirs(dir)
		if probe == "refer-with" {
			rw = rw.WithRefer()
		}
		if err := top.BestEffort().RestrictPaths(landlock.RODirs("/"), rw); err != nil {
			return "restrict failed: " + err.Error()
		}
		err := os.Rename(src, filepath.Join(b, "f"))
		return verdict("rename across directories inside the writable root", err, err == nil)

	case "nested-deny":
		// The .git/hooks question: a writable project with a read-only child.
		// If the narrower rule wins, the hooks vector closes. If the wider one
		// does, it does not and the fallback in the plan applies.
		hooks := filepath.Join(dir, "hooks")
		_ = os.MkdirAll(hooks, 0o700)
		if err := top.BestEffort().RestrictPaths(
			landlock.RODirs("/"),
			landlock.RWDirs(dir).WithRefer(),
			landlock.RODirs(hooks),
		); err != nil {
			return "restrict failed: " + err.Error()
		}
		inHooks := os.WriteFile(filepath.Join(hooks, "post-commit"), []byte("#!/bin/sh\n"), 0o700)
		inProject := os.WriteFile(filepath.Join(dir, "ok"), []byte("x"), 0o600)
		switch {
		case inHooks != nil && inProject == nil:
			return "YES: the nested read-only rule wins — hooks denied, project still writable"
		case inHooks == nil && inProject == nil:
			return "NO: the nested rule did not reduce rights — hooks are writable"
		default:
			return fmt.Sprintf("UNCLEAR: hooks=%v project=%v", inHooks, inProject)
		}

	case "unix-socket":
		sock := filepath.Join(dir, "s.sock")
		if err := top.BestEffort().RestrictPaths(landlock.RODirs("/"), landlock.RWDirs(dir)); err != nil {
			return "restrict failed: " + err.Error()
		}
		c, err := net.Dial("unix", sock)
		if err == nil {
			c.Close()
		}
		return verdict("connect() to a pathname Unix socket without WithResolveUnix", err, err == nil)

	case "ioctl-tty":
		if err := top.BestEffort().RestrictPaths(landlock.RODirs("/"), landlock.RWDirs(dir)); err != nil {
			return "restrict failed: " + err.Error()
		}
		ptmx, tty, err := pty.Open()
		if err != nil {
			return "could not open a pty: " + err.Error()
		}
		defer ptmx.Close()
		defer tty.Close()
		_, err = unix.IoctlGetWinsize(int(ptmx.Fd()), unix.TIOCGWINSZ)
		return verdict("ioctl(TIOCGWINSZ) on a pty without WithIoctlDev", err, err == nil)

	case "errno":
		if err := top.BestEffort().RestrictPaths(landlock.RODirs("/"), landlock.RWDirs(dir)); err != nil {
			return "restrict failed: " + err.Error()
		}
		home, _ := os.UserHomeDir()
		err := os.WriteFile(filepath.Join(home, ".strument-landlock-canary"), []byte("x"), 0o600)
		if err == nil {
			return "DENIED-NOT: the write to $HOME succeeded; the sandbox is not enforcing"
		}
		return fmt.Sprintf("error text: %q; errors.Is(err, EACCES)=%v, os.IsPermission=%v",
			err.Error(), errors.Is(err, unix.EACCES), os.IsPermission(err))
	}
	return "unknown probe " + probe
}

func verdict(what string, err error, allowed bool) string {
	if allowed {
		return "ALLOWED: " + what
	}
	return fmt.Sprintf("DENIED: %s (%v)", what, err)
}
