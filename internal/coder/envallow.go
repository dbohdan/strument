package coder

import (
	"os"
	"runtime"
	"slices"
	"strings"
)

// defaultEnvAllowNames and defaultEnvAllowPrefixes define the environment the
// harness passes to commands it runs on the model's behalf — the bash tool,
// named checks, and the scraper command. Everything else in the session's
// environment is withheld, so a model-run `env` (or a build tool that echoes
// its environment into a failing test's output) cannot carry OPENROUTER_API_KEY
// or any other credential into a tool result, i.e. into the transcript and the
// model's context.
//
// The default set is "what makes builds and tests work": identity and terminal
// basics, locale, temp directories, and the non-secret knobs of the common
// toolchains. Credential-shaped names (anything with TOKEN/KEY/SECRET/PASSWORD
// in it) are excluded by omission — deliberately, per the design discussion:
// a hard deny-by-shape filter would just push users toward writing tokens to
// files, which is worse. A variable the workflow genuinely needs is added with
// `env_allow` in the config, where the addition is a visible, deliberate act.
//
// /run is not model-run: the user typed the command, so it keeps the full
// environment.
var defaultEnvAllowNames = []string{
	// Identity, shell, terminal.
	"PATH", "HOME", "SHELL", "USER", "LOGNAME", "TERM", "TERMINFO",
	"COLUMNS", "LINES", "TMPDIR",
	// Windows basics, where HOME/TERM do not exist.
	"COMSPEC", "PATHEXT", "SystemRoot", "USERPROFILE", "TEMP", "TMP",
	// Locale.
	"LANG", "LANGUAGE",
	// The LC_* family, enumerated rather than prefix-matched: an exact list is
	// the same one rule everywhere — no prefix mechanism to widen by accident.
	"LC_ALL", "LC_CTYPE", "LC_MESSAGES", "LC_NUMERIC", "LC_TIME",
	"LC_COLLATE", "LC_MONETARY", "LC_PAPER", "LC_NAME", "LC_ADDRESS",
	"LC_TELEPHONE", "LC_MEASUREMENT", "LC_IDENTIFICATION",
	// Proxy endpoints, as a good number of tools read them (a value may embed
	// credentials; that is the operator's choice, same as a proxy= URL in the
	// config). NO_PROXY keeps the proxy off localhost and LAN hosts.
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy",
	"SOCKS5_SERVER",
	// XDG locations; tools resolve their caches from these.
	"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
	"XDG_RUNTIME_DIR",
	// Go. The GO* family enumerated rather than prefix-matched: a "GO" prefix
	// would also match GOOGLE_API_KEY and GOOGLE_TOKEN — the credential-shaped
	// names the allowlist exists to withhold. These are the non-secret knobs
	// cmd/go reads; CGO_ENABLED, CC and CXX carry no GO prefix.
	"GOROOT", "GOPATH", "GOCACHE", "GOMODCACHE", "GOENV", "GOFLAGS",
	"GOTOOLCHAIN", "GOPROXY", "GOPRIVATE", "GONOPROXY", "GOSUMDB",
	"GONOSUMDB", "GOINSECURE", "GOTOOLDIR", "GOOS", "GOARCH", "GOARM",
	"GO386", "GOAMD64", "GORACE", "GOTRACEBACK", "GODEBUG", "GOMEMLIMIT",
	"GOMAXPROCS", "GOTELEMETRYDIR", "GOCOVERDIR", "GOEXPERIMENT", "GOWASM",
	"GOFIPS140",
	"CGO_ENABLED", "CC", "CXX",
	// Rust.
	"CARGO_HOME", "RUSTUP_HOME", "RUST_BACKTRACE", "RUSTFLAGS",
	// Python packaging.
	"VIRTUAL_ENV", "PIP_CACHE_DIR", "UV_CACHE_DIR", "UV_PYTHON_INSTALL_DIR",
	"POETRY_CACHE_DIR",
	// JVM ecosystems.
	"JAVA_HOME", "GRADLE_USER_HOME",
}

// envNamesFold reports whether variable names are compared without regard to
// case. Windows treats them that way at the API, and os.Environ hands back the
// OS's own spelling — "Path", "ComSpec", "SystemRoot" — so an exact-match
// allowlist there withholds PATH from every model-run command and the tool
// fails to find its own compiler. Unix names are case-sensitive and `path` is a
// different variable from PATH, so the fold stays off.
//
// A variable rather than a runtime.GOOS test at the point of use, so the
// Windows comparison can be exercised from a test on any host: the platform
// this is wrong on is the one CI is least likely to catch it on.
var envNamesFold = runtime.GOOS == "windows"

// envAllowed reports whether name passes the allowlist. Extra names come from
// `env_allow` and match exactly, like the defaults: there is no prefix or
// wildcard expansion anywhere — a config that says "FOO_" almost certainly
// means FOO_BAR, and a silent prefix match would widen permissions past what
// was written.
func envAllowed(name string, extra []string) bool {
	return containsEnvName(extra, name) || containsEnvName(defaultEnvAllowNames, name)
}

// containsEnvName is slices.Contains under the platform's name comparison.
func containsEnvName(list []string, name string) bool {
	if slices.Contains(list, name) {
		return true
	}
	if !envNamesFold {
		return false
	}
	for _, n := range list {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// EnvAllowed is envAllowed for callers outside the package: the REPL's /env
// command uses it to say which set variables a model-run command would see.
func EnvAllowed(name string, extra []string) bool { return envAllowed(name, extra) }

// DefaultEnvAllowNames exposes the default list for display (/env). A copy, so
// a caller cannot widen the defaults.
func DefaultEnvAllowNames() []string { return slices.Clone(defaultEnvAllowNames) }

// FilterEnv builds the environment passed to model-run commands: every
// allowed variable from environ, as NAME=VALUE pairs. environ nil means
// os.Environ. extra is the user's `env_allow` list, on top of the defaults.
//
// The result is never nil, even when nothing matches. Both consumers read nil
// as "inherit the whole environment" — exec.Cmd.Env by documented contract,
// PipeRunner.Env by the zero-value rule that gives /run its unfiltered
// environment — so a nil return would turn the filter's strictest possible
// outcome into no filter at all. An empty environment is the correct answer to
// "nothing was allowed"; failing that way is a broken build, not a leaked key.
func FilterEnv(environ func() []string, extra []string) []string {
	if environ == nil {
		environ = os.Environ
	}
	out := []string{}
	for _, kv := range environ() {
		name, _, _ := strings.Cut(kv, "=")
		if envAllowed(name, extra) {
			out = append(out, kv)
		}
	}
	return out
}
