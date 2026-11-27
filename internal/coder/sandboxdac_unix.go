//go:build unix

package coder

import (
	"errors"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// dacRefusesWrite reports whether the ordinary file permissions already refuse
// a write to path: to the file if it exists, else to the nearest directory
// that does, where it would be created. access(2) answers from those
// permissions alone; Landlock does not hook it (a probe on ABI 7 saw it report
// a directory writable that Landlock then refused a create in), which is what
// lets the answer separate the two causes. A package variable so tests can
// stand in for the file system.
var dacRefusesWrite = func(path string) bool {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		err := unix.Access(p, unix.W_OK)
		if !errors.Is(err, unix.ENOENT) || p == "/" {
			return errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EROFS)
		}
	}
}
