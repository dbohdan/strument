// Package osinfo describes the operating system in one line for the model:
// the distribution or product and version, and the machine's architecture.
//
// It is written here rather than taken from gopsutil or go-sysinfo. Both are
// mature, but on macOS both pull in purego's fakecgo, which gives a no-cgo
// binary cgo's runtime setup, and gopsutil's host package brings WMI over COM
// on Windows; neither detects WSL. The pitfalls their fixes and CPython's
// platform module encode are kept below, each where it applies. What the model
// needs is one readable string, which removes most of what those libraries
// normalize: os-release's PRETTY_NAME is already that string on Linux.
package osinfo

import (
	"fmt"
	"runtime"
	"strings"
)

// Describe returns a line such as "Ubuntu 24.04.5 LTS (x86_64)" or
// "macOS 15.2 (arm64)". It never fails: what cannot be read falls back to the
// build target, "linux-amd64" and the like.
func Describe() string {
	if s := describe(); s != "" {
		return s
	}
	return runtime.GOOS + "-" + runtime.GOARCH
}

// ParseOSRelease reads an os-release file, following CPython's
// freedesktop_os_release: NAME=value lines, the value optionally in single or
// double quotes, with the five escapes the standard names (\\ \$ \" \' \`)
// undone. Anything else, comments included, is skipped. It is not a shell, and
// does not expand variables.
func ParseOSRelease(text string) map[string]string {
	out := map[string]string{}
	for line := range strings.Lines(text) {
		line = strings.TrimRight(line, "\r\n")
		name, value, ok := strings.Cut(line, "=")
		if !ok || name == "" || strings.Trim(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_") != "" {
			continue
		}
		if n := len(value); n >= 2 && (value[0] == '"' || value[0] == '\'') && value[n-1] == value[0] {
			value = value[1 : n-1]
		}
		var b strings.Builder
		for i := 0; i < len(value); i++ {
			if value[i] == '\\' && i+1 < len(value) && strings.IndexByte("\\$\"'`", value[i+1]) >= 0 {
				i++
			}
			b.WriteByte(value[i])
		}
		out[name] = b.String()
	}
	return out
}

// DistroName is the distribution's own display name, with CPython's fallbacks:
// PRETTY_NAME, else NAME and VERSION, else "Linux".
func DistroName(rel map[string]string) string {
	if s := rel["PRETTY_NAME"]; s != "" {
		return s
	}
	if s := rel["NAME"]; s != "" {
		return strings.TrimSpace(s + " " + rel["VERSION"])
	}
	return "Linux"
}

// WSL returns "WSL1" or "WSL2" when the kernel release is one of Microsoft's,
// "" otherwise. WSL1 reports a fake kernel ending "-Microsoft"; WSL2's real one
// carries "microsoft-standard-WSL2". A custom WSL2 kernel need not say WSL2 but
// keeps "microsoft", which is why anything else with it counts as WSL2.
func WSL(kernelRelease string) string {
	lower := strings.ToLower(kernelRelease)
	switch {
	case !strings.Contains(lower, "microsoft"):
		return ""
	case strings.HasSuffix(kernelRelease, "-Microsoft"):
		return "WSL1"
	default:
		return "WSL2"
	}
}

// Linux composes the line from what linux.go gathers.
func Linux(rel map[string]string, kernelRelease, machine string) string {
	s := DistroName(rel)
	if w := WSL(kernelRelease); w != "" {
		s += " under " + w + " on Windows"
	}
	return withArch(s, machine, "")
}

// WindowsName is the registry's ProductName corrected for Windows 11, which
// kept "Windows 10" there: build 22000 and later is 11 (the rule gopsutil and
// go-sysinfo both apply, after Microsoft's answer on it). Server editions
// never say "Windows 10", so they pass through.
func WindowsName(product string, build uint32) string {
	if product == "" {
		product = "Windows"
	}
	if build >= 22000 && strings.Contains(product, "Windows 10") {
		product = strings.Replace(product, "Windows 10", "Windows 11", 1)
	}
	return product
}

// Windows composes the line: "Windows 11 Pro 23H2 (build 22631.4460, arm64)".
func Windows(product, displayVersion string, build, ubr uint32, machine, emulated string) string {
	s := WindowsName(product, build)
	if displayVersion != "" {
		s += " " + displayVersion
	}
	b := fmt.Sprintf("build %d", build)
	if ubr != 0 {
		b += fmt.Sprintf(".%d", ubr)
	}
	return withArch(s, b+", "+machine, emulated)
}

// MacOS composes the line: "macOS 15.2 (arm64)".
func MacOS(version, machine, emulated string) string {
	s := "macOS"
	if version != "" {
		s += " " + version
	}
	return withArch(s, machine, emulated)
}

// withArch appends the machine's architecture, and the build's when the
// binary runs emulated on another: an x86_64 Strument under Rosetta is on an
// arm64 Mac, which is what decides the binaries a command should fetch.
func withArch(s, machine, emulated string) string {
	if machine == "" {
		return s
	}
	if emulated != "" {
		return s + " (" + machine + "; Strument is running as " + emulated + " under emulation)"
	}
	return s + " (" + machine + ")"
}
