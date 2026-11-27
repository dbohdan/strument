//go:build !linux

package main

// undumpable is Linux-only; see dumpable_linux.go.
func undumpable() error { return nil }
