package sandbox

import "testing"

// TestShowPolicy is not an assertion; it prints what this machine's policy
// grants, so `go test -run ShowPolicy -v` answers "what can it write?" without
// anyone reading the derivation and guessing.
func TestShowPolicy(t *testing.T) {
	for _, p := range DefaultWritable("/home/u/project", "/home/u/.local/state/strument/projects/x", nil) {
		t.Log(p)
	}
}
