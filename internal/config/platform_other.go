//go:build !unix

package config

import "runtime"

// Without uname there is no faithful source for these. CPython returns ""
// rather than raising when it cannot determine a platform field, and an empty
// string is also what a config comparing against a known release will fail to
// match — which is the safe direction.
// windowsMachine is platform.machine() where there is no uname. CPython reads
// PROCESSOR_ARCHITECTURE there, which reports the same few names in upper case.
func windowsMachine() string {
	switch runtime.GOARCH {
	case "amd64":
		return "AMD64"
	case "arm64":
		return "ARM64"
	case "386":
		return "x86"
	}
	return runtime.GOARCH
}

func machineName() string  { return windowsMachine() }
func unameRelease() string { return "" }
func unameVersion() string { return "" }
