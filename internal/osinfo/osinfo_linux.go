package osinfo

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// describe reads os-release, /etc before /usr/lib as the standard and CPython
// order them, and the kernel's release and machine from uname.
func describe() string {
	var rel map[string]string
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		if b, err := os.ReadFile(p); err == nil {
			rel = ParseOSRelease(string(b))
			break
		}
	}
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return ""
	}
	return Linux(rel, unix.ByteSliceToString(u.Release[:]), strings.TrimSpace(unix.ByteSliceToString(u.Machine[:])))
}
