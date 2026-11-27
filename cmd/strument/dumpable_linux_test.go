package main

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

// undumpable takes effect on the process that calls it. Run in a child so the
// test binary itself stays dumpable for whatever runs after it.
func TestUndumpable(t *testing.T) {
	if os.Getenv("STRUMENT_TEST_UNDUMPABLE") == "1" {
		if err := undumpable(); err != nil {
			t.Fatal(err)
		}
		if d, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0); err != nil || d != 0 {
			t.Fatalf("dumpable = %d, %v; want 0", d, err)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestUndumpable$")
	cmd.Env = append(os.Environ(), "STRUMENT_TEST_UNDUMPABLE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
}
