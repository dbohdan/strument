package coder

import (
	"os"
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
	// Proxy endpoints, as a good number of tools read them (a value may embed
	// credentials; that is the operator's choice, same as a proxy= URL in the
	// config). NO_PROXY keeps the proxy off localhost and LAN hosts.
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy",
	"SOCKS5_SERVER",
	// XDG locations; tools resolve their caches from these.
	"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
	"XDG_RUNTIME_DIR",
	// Go. The GO* family is covered by the prefix below; CGO_ENABLED is not.
	"CGO_ENABLED", "CC", "CXX",
	// Rust.
	"CARGO_HOME", "RUSTUP_HOME", "RUST_BACKTRACE", "RUSTFLAGS",
	// Python packaging.
	"VIRTUAL_ENV", "PIP_CACHE_DIR", "UV_CACHE_DIR", "UV_PYTHON_INSTALL_DIR",
	// JVM ecosystems.
	"JAVA_HOME", "GRADLE_USER_HOME",
}

// defaultEnvAllowPrefixes matches families rather than listing every member.
// "LC_" covers LC_ALL, LC_MESSAGES, …; "GO" covers GOPATH, GOCACHE, GOTOOLCHAIN,
// GOPROXY, GOPRIVATE, … — all non-secret toolchain state.
var defaultEnvAllowPrefixes = []string{"LC_", "GO"}

// envAllowed reports whether name passes the allowlist. Extra names come from
// `env_allow` and match exactly (no prefix expansion: a config that says
// "FOO_" almost certainly means FOO_BAR, and a silent prefix match would widen
// permissions past what was written).
func envAllowed(name string, extra []string) bool {
	if slices.Contains(extra, name) {
		return true
	}
	if slices.Contains(defaultEnvAllowNames, name) {
		return true
	}
	for _, p := range defaultEnvAllowPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// FilterEnv builds the environment passed to model-run commands: every
// allowed variable from environ, as NAME=VALUE pairs. environ nil means
// os.Environ. extra is the user's `env_allow` list, on top of the defaults.
func FilterEnv(environ func() []string, extra []string) []string {
	if environ == nil {
		environ = os.Environ
	}
	var out []string
	for _, kv := range environ() {
		name, _, _ := strings.Cut(kv, "=")
		if envAllowed(name, extra) {
			out = append(out, kv)
		}
	}
	return out
}
