//go:build unix

package config

import "golang.org/x/sys/unix"

// On Unix, CPython's platform.machine(), .release() and .version() are the
// corresponding uname(2) fields verbatim. Reading uname rather than translating
// GOARCH is not merely equivalent — it is more correct: a 32-bit binary on a
// 64-bit kernel reports the kernel's machine in CPython, and GOARCH would
// report the binary's.
func uname() (machine, release, version string) {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return "", "", ""
	}
	return nullTerm(u.Machine[:]), nullTerm(u.Release[:]), nullTerm(u.Version[:])
}

// nullTerm decodes a fixed-size C string field.
func nullTerm(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func machineName() string  { m, _, _ := uname(); return m }
func unameRelease() string { _, r, _ := uname(); return r }
func unameVersion() string { _, _, v := uname(); return v }
