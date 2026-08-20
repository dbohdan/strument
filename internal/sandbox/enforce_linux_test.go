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
	"golang.org/x/sys/unix"
)

// Round two: the same live-probe discipline applied to the policy Strument
// actually proposes to install, rather than to a minimal one.
//
// Round one settled the primitives and turned up three things the design had
// not planned for — a pty cannot be allocated under a read-only "/", a refer
// denial is EXDEV rather than EACCES, and resolve_unix is a break scheduled for
// whenever the kernel reaches ABI 9. All three are answered here against the
// real Policy.rules(), so what passes is the shipped code and not a model of it.
//
//	go test ./internal/sandbox/ -run Enforce -v
//
// The negative cases are the ones that matter. A sandbox that permits
// everything it should also passes every positive test.

const enforceEnv = "STRUMENT_ENFORCE_CASE"

func TestEnforcePolicy(t *testing.T) {
	av := Probe()
	if !av.Supported() {
		t.Skipf("no Landlock on this kernel (%v) — nothing to enforce", av.Err)
	}
	t.Logf("Landlock ABI version: %d", av.ABI)

	for _, tc := range []struct {
		name string
		asks string
	}{
		// Must be permitted, or the sandbox breaks ordinary work.
		{"write-project", "can a turn write inside the project?"},
		{"write-tmp", "can a build write to TMPDIR?"},
		{"devnull", "does `> /dev/null` work now that it is granted?"},
		{"rename-across", "does mv across directories work with WithRefer?"},
		{"pty", "can a pty be allocated and sized (ptmx + WithIoctlDev)?"},
		{"unix-socket", "can a test still reach a local Unix socket?"},
		{"exec-path", "do binaries outside the project still run?"},
		{"go-build", "does a real `go build` complete with its cache writable?"},
		// Must be denied, or the sandbox is decorative.
		{"deny-home", "is a write to $HOME refused?"},
		{"deny-etc", "is a write to /etc refused?"},
		{"deny-parent", "is a write to the project's parent refused?"},
		{"deny-devsda", "is /dev/sda still out of reach?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := runEnforce(t, tc.name)
			answer := strings.TrimSpace(out)
			t.Logf("Q: %s", tc.asks)
			t.Logf("A: %s", answer)
			assertEnforced(t, answer)
		})
	}
}

// assertEnforced turns the helper's answer into a verdict.
//
// It exists because the first run of this file reported PASS on all twelve
// cases while the ruleset was being rejected outright and nothing whatsoever
// was confined — the helper printed "apply failed", the parent logged it, and
// the test suite called that success. A check that cannot fail is not a check,
// and a green sandbox test that means nothing is worse than no test, because it
// is the one thing that would stop anyone looking again.
func assertEnforced(t *testing.T, answer string) {
	t.Helper()
	switch {
	case answer == "":
		t.Error("the helper produced no answer at all")
	case strings.HasPrefix(answer, "apply failed"):
		t.Errorf("the ruleset was not installed, so nothing was confined: %s", answer)
	case strings.HasPrefix(answer, "BROKEN"):
		t.Errorf("the sandbox denied something an ordinary session needs: %s", answer)
	case strings.HasPrefix(answer, "LEAK"):
		t.Errorf("the sandbox permitted something it must refuse: %s", answer)
	case strings.HasPrefix(answer, "setup failed"), strings.HasPrefix(answer, "unknown case"):
		t.Errorf("the probe did not run: %s", answer)
	case !strings.HasPrefix(answer, "OK"):
		t.Errorf("unrecognized answer, which means the probe changed and this check did not: %s", answer)
	}
}

func runEnforce(t *testing.T, name string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(project, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}

	if name == "unix-socket" {
		ln, err := net.Listen("unix", filepath.Join(project, "s.sock"))
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

	cmd := exec.Command(os.Args[0], "-test.run=TestEnforceHelper", "-test.v=false")
	cmd.Env = append(os.Environ(),
		enforceEnv+"="+name,
		"STRUMENT_ENFORCE_PROJECT="+project,
		"STRUMENT_ENFORCE_TMP="+filepath.Join(dir, "tmp"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestEnforceHelper(t *testing.T) {
	name := os.Getenv(enforceEnv)
	if name == "" {
		t.Skip("not the helper")
	}
	project := os.Getenv("STRUMENT_ENFORCE_PROJECT")
	tmp := os.Getenv("STRUMENT_ENFORCE_TMP")
	_ = os.MkdirAll(tmp, 0o700)

	// Exactly what Strument would install, built by the shipped code.
	pol := Policy{Writable: []string{project, tmp, cacheDirForTest()}}
	if err := pol.Apply(); err != nil {
		fmt.Println("apply failed:", err)
		return
	}
	fmt.Println(enforceCase(name, project, tmp))
}

// cacheDirForTest stands in for the toolchain-cache entries the real policy
// derives, so `go build` has somewhere to put its cache.
func cacheDirForTest() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(home, ".cache")
}

func enforceCase(name, project, tmp string) string {
	switch name {
	case "write-project":
		return allow("write inside the project",
			os.WriteFile(filepath.Join(project, "f"), []byte("x"), 0o600))

	case "write-tmp":
		return allow("write to TMPDIR", os.WriteFile(filepath.Join(tmp, "f"), []byte("x"), 0o600))

	case "devnull":
		f, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
		if err == nil {
			_, err = f.WriteString("x")
			f.Close()
		}
		return allow("write to /dev/null", err)

	case "rename-across":
		src := filepath.Join(project, "f")
		if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
			return "setup failed: " + err.Error()
		}
		return allow("rename into a sibling directory",
			os.Rename(src, filepath.Join(project, "sub", "f")))

	case "pty":
		ptmx, tty, err := pty.Open()
		if err != nil {
			return deny("allocate a pty", err)
		}
		defer ptmx.Close()
		defer tty.Close()
		_, err = unix.IoctlGetWinsize(int(ptmx.Fd()), unix.TIOCGWINSZ)
		return allow("allocate a pty and read its size", err)

	case "unix-socket":
		c, err := net.Dial("unix", filepath.Join(project, "s.sock"))
		if err == nil {
			c.Close()
		}
		return allow("connect to a Unix socket in the project", err)

	case "exec-path":
		out, err := exec.Command("/bin/sh", "-c", "echo ran").CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "ran" {
			return deny("run /bin/sh", err)
		}
		return allow("run a binary outside the project", nil)

	case "go-build":
		src := filepath.Join(project, "main.go")
		_ = os.WriteFile(filepath.Join(project, "go.mod"), []byte("module p\n\ngo 1.26\n"), 0o600)
		_ = os.WriteFile(src, []byte("package main\n\nfunc main() {}\n"), 0o600)
		cmd := exec.Command("go", "build", "-o", filepath.Join(project, "out"), ".")
		cmd.Dir = project
		out, err := cmd.CombinedOutput()
		if err != nil {
			return deny("go build", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
		}
		return allow("go build with a writable cache", nil)

	case "deny-home":
		home, _ := os.UserHomeDir()
		return mustDeny("write to $HOME",
			os.WriteFile(filepath.Join(home, ".strument-canary"), []byte("x"), 0o600))

	case "deny-etc":
		return mustDeny("write to /etc",
			os.WriteFile("/etc/strument-canary", []byte("x"), 0o600))

	case "deny-parent":
		return mustDeny("write to the project's parent",
			os.WriteFile(filepath.Join(filepath.Dir(project), "canary"), []byte("x"), 0o600))

	case "deny-devsda":
		f, err := os.OpenFile("/dev/sda", os.O_WRONLY, 0)
		if err == nil {
			f.Close()
		}
		return mustDeny("open /dev/sda for writing", err)
	}
	return "unknown case " + name
}

func allow(what string, err error) string {
	if err != nil {
		return fmt.Sprintf("BROKEN: %s was denied (%v)", what, err)
	}
	return "OK: " + what
}

func deny(what string, err error) string {
	return fmt.Sprintf("BROKEN: %s failed (%v)", what, err)
}

// mustDeny reports the errno too. A refer denial arrives as EXDEV rather than
// EACCES, so "was it refused" and "did it look like a permission problem" are
// different questions, and the hint Strument shows the model depends on which.
func mustDeny(what string, err error) string {
	if err == nil {
		return "LEAK: " + what + " succeeded"
	}
	return fmt.Sprintf("OK: %s refused (%v; EACCES=%v, os.IsPermission=%v)",
		what, err, isErrno(err, unix.EACCES), os.IsPermission(err))
}

func isErrno(err error, target unix.Errno) bool {
	return errors.Is(err, target)
}
