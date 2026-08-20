package config

import (
	"errors"
	"os"
	"runtime"
	"slices"
	"strconv"

	"go.starlark.net/starlark"
)

// platformValue is the `platform` global: read-only facts about the machine, so
// a config can say what a config often needs to say —
//
//	sandbox = "landlock" if platform.system == "Linux" else ""
//
// It is modelled on CPython's `platform` module rather than on Go's own
// vocabulary, because the shape a config author reaches for is the Python one
// and Starlark is a Python dialect. So GOOS "linux" is reported as "Linux" and
// GOARCH "amd64" as "x86_64" — writing `platform.system == "linux"` and having
// it quietly never match is exactly the bug this naming avoids.
//
// Attributes rather than a function call: these are facts about the host, not
// a computation, and `platform.system` reads the way the Python it imitates
// does.
//
// Two of CPython's fields are deliberately absent rather than faked.
// `platform.platform()` is a composite string assembled differently on every
// OS, with no faithful translation; `platform.processor()` is empty on Linux
// even in CPython and comes from places Go cannot portably reach. An absent
// attribute is an error a config author sees immediately; a plausible wrong
// value is one they ship.
type platformValue struct{}

var platformAttrs = []string{"bits", "machine", "node", "release", "system", "version"}

func (platformValue) String() string        { return "platform" }
func (platformValue) Type() string          { return "platform" }
func (platformValue) Freeze()               {}
func (platformValue) Truth() starlark.Bool  { return starlark.True }
func (platformValue) AttrNames() []string   { return slices.Clone(platformAttrs) }
func (platformValue) Hash() (uint32, error) { return 0, errors.New("unhashable type: platform") }

// Attr returns (nil, nil) for an unknown name, which is how Starlark asks for
// its own "no such attribute" error, listing AttrNames as suggestions. A
// sentinel error here would replace that message with a worse one.
//
//nolint:nilnil // starlark.HasAttrs defines (nil, nil) as "no such attribute".
func (platformValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "bits":
		return starlark.String(strconv.Itoa(strconv.IntSize) + "bit"), nil
	case "machine":
		return starlark.String(machineName()), nil
	case "node":
		// CPython's platform.node() returns "" when it cannot tell, rather than
		// raising, and a config that cannot read the hostname should not fail
		// to load over it.
		host, err := os.Hostname()
		if err != nil {
			return starlark.String(""), nil //nolint:nilerr // "" is what CPython reports here.
		}
		return starlark.String(host), nil
	case "release":
		return starlark.String(unameRelease()), nil
	case "system":
		return starlark.String(systemName()), nil
	case "version":
		return starlark.String(unameVersion()), nil
	}
	return nil, nil
}

// systemName translates GOOS to the string CPython's platform.system() gives.
// Unknown values pass through unchanged rather than being guessed at.
func systemName() string {
	switch runtime.GOOS {
	case "linux":
		return "Linux"
	case "darwin":
		return "Darwin"
	case "windows":
		return "Windows"
	case "freebsd":
		return "FreeBSD"
	case "openbsd":
		return "OpenBSD"
	case "netbsd":
		return "NetBSD"
	case "dragonfly":
		return "DragonFly"
	case "solaris", "illumos":
		return "SunOS"
	case "aix":
		return "AIX"
	}
	return runtime.GOOS
}
