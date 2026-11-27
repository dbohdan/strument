//go:build !unix

package coder

// dacRefusesWrite never rules the sandbox out where there is none to rule out;
// see sandboxdac_unix.go.
var dacRefusesWrite = func(string) bool { return false }
