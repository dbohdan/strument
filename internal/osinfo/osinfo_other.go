//go:build !linux && !darwin && !windows

package osinfo

// describe has nothing to read here; Describe falls back to the build target.
func describe() string { return "" }
