package config

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"dbohdan.com/strument/internal/httpx"
)

// ProjectConfigName is the project-root dotfile, untrusted by default.
const ProjectConfigName = ".strument.star"

// Options configures Load. Zero values pick the real environment.
type Options struct {
	UserConfigPath string                           // "" => os.UserConfigDir()/strument/config.star
	ProjectRoot    string                           // "" => no project config discovery
	TrustStorePath string                           // "" => DefaultTrustStorePath()
	LookupEnv      func(string) (string, bool)      // nil => os.LookupEnv
	Warn           func(format string, args ...any) // nil => stderr
}

// DefaultUserConfigPath resolves the user config location via
// os.UserConfigDir, which honors XDG_CONFIG_HOME.
func DefaultUserConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "strument", "config.star"), nil
}

// fileGlobals is the result of executing one config file.
type fileGlobals struct {
	models         map[string]*Model
	hasDefault     bool
	defaultVal     string
	hasHistoryFile bool
	historyFile    string
	hasProxy       bool
	proxyVal       string
	hasScraper     bool
	scraperVal     []string
	hasCheck       bool
	checkVal       []Check
	hasCheckAuto   bool
	checkAutoVal   []string

	hasReasoningDisplay bool
	reasoningDisplayVal ReasoningDisplay

	hasMaxSteps            bool
	maxStepsVal            int
	hasMaxErrorReflections bool
	maxErrorReflectionsVal int

	hasGitSign bool
	gitSignVal string

	hasEnvAllow bool
	envAllowVal []string
}

// parsePositiveInt reads a Starlark int that must be at least 1.
func parsePositiveInt(path, name string, v starlark.Value) (int, error) {
	iv, ok := v.(starlark.Int)
	if !ok {
		return 0, fmt.Errorf("%s: `%s` must be a positive integer, got %s", path, name, v.Type())
	}
	n, ok := iv.Int64()
	if !ok {
		return 0, fmt.Errorf("%s: `%s` is out of range", path, name)
	}
	if n < 1 {
		return 0, fmt.Errorf("%s: `%s` must be at least 1, got %d", path, name, n)
	}
	return int(n), nil
}

// parseReasoningDisplay reads `reasoning_display`: "full", a line count, or
// "off". Zero lines is "off" — the number says it plainly enough.
//
// A count and a word share one setting because they answer one question — how
// much thinking to show — and splitting them into two keys would let a config
// say "off" and "12" at once.
func parseReasoningDisplay(path string, v starlark.Value) (ReasoningDisplay, error) {
	switch val := v.(type) {
	case starlark.String:
		switch s := string(val); s {
		case "full":
			return ReasoningDisplay{Mode: ReasoningFull}, nil
		case "off":
			return ReasoningDisplay{Mode: ReasoningOff}, nil
		default:
			return ReasoningDisplay{}, fmt.Errorf(
				"%s: `reasoning_display` must be \"full\", a number of lines, or \"off\", got %q", path, s)
		}
	case starlark.Int:
		n, ok := val.Int64()
		if !ok || n > math.MaxInt32 {
			return ReasoningDisplay{}, fmt.Errorf("%s: `reasoning_display` line count is out of range", path)
		}
		if n == 0 {
			// Zero lines is "off" by the plainest reading of the number, so it
			// means that rather than failing to load. A second spelling is worth
			// having when it is the one someone would reach for first.
			return ReasoningDisplay{Mode: ReasoningOff}, nil
		}
		if n < 0 {
			return ReasoningDisplay{}, fmt.Errorf(
				"%s: `reasoning_display` cannot be negative; use 0 or \"off\" to hide the thinking", path)
		}
		return ReasoningDisplay{Mode: ReasoningCapped, Lines: int(n)}, nil
	default:
		return ReasoningDisplay{}, fmt.Errorf(
			"%s: `reasoning_display` must be \"full\", a number of lines, or \"off\", got %s", path, v.Type())
	}
}

// missingConfigError is the very first thing a new user can see, so it carries
// a config they can paste rather than a pointer to one.
//
// It used to say "see doc/config.md", which is a path in the source tree — and
// someone who installed with `go install dbohdan.com/strument/cmd/strument` has
// no source tree. The message named a file they did not have, for a program
// that would not start until they wrote one. Four lines of Starlark is less
// text than the sentence explaining where to find them.
//
// The slug is a real one that works today; `strument model-config` fills in
// context, output limits, and pricing for any other, and it runs before a
// config exists, which is exactly when it is needed.
func missingConfigError(userPath string) error {
	//nolint:revive,staticcheck // error-strings: this one is the screen a new
	// user reads, not a clause another message wraps. Nothing wraps it — Load
	// returns it, main prints it, the program exits.
	return fmt.Errorf(`no configuration file yet.

Strument has no model database and assumes nothing about which models you have,
so it needs a config before it will start. Write this to
%s:

    openrouter = provider("openrouter", api_key=env("OPENROUTER_API_KEY"))

    models = {"mimo": model(openrouter, "xiaomi/mimo-v2.5", context=1050000)}
    default = "mimo"

then export OPENROUTER_API_KEY and run strument again.

"strument model-config <slug>" prints a fuller block — context, output limit,
pricing — for any OpenRouter model, and it works before a config exists.
The full reference is %s.`,
		userPath, DocsURL+"/blob/master/doc/config.md")
}

// DocsURL is the documentation's home for someone who installed the binary and
// has no source tree to read it from.
const DocsURL = "https://github.com/dbohdan/strument"

// Load runs the config pipeline: user config, gated project
// config, whole-key merge, post-merge weak_model resolution, validation.
func Load(opts Options) (*Config, error) {
	warn := opts.Warn
	if warn == nil {
		warn = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}

	userPath := opts.UserConfigPath
	if userPath == "" {
		var err error
		if userPath, err = DefaultUserConfigPath(); err != nil {
			return nil, err
		}
	}

	// 1. User config — always trusted (the trust root).
	userSrc, err := os.ReadFile(userPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, missingConfigError(userPath)
		}
		return nil, err
	}
	user, err := execConfig(userPath, userSrc, lookup, opts.ProjectRoot)
	if err != nil {
		return nil, err
	}

	// 2-3. Project config — inert unless trusted.
	var project *fileGlobals
	if opts.ProjectRoot != "" {
		projPath := filepath.Join(opts.ProjectRoot, ProjectConfigName)
		if projSrc, err := os.ReadFile(projPath); err == nil {
			absPath, err := filepath.Abs(projPath)
			if err != nil {
				return nil, err
			}
			tsPath := opts.TrustStorePath
			if tsPath == "" {
				if tsPath, err = DefaultTrustStorePath(); err != nil {
					return nil, err
				}
			}
			ts, err := OpenTrustStore(tsPath)
			if err != nil {
				return nil, err
			}
			if ts.IsTrusted(absPath, projSrc) {
				if project, err = execConfig(projPath, projSrc, lookup, opts.ProjectRoot); err != nil {
					return nil, err
				}
			} else {
				warn("Ignoring untrusted project config %s.", projPath)
				warn("Run `strument trust` to allow it. Re-trust after every edit.")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	// 4. Merge: models whole-key, project wins; default and history_file
	// project-over-user.
	cfg := &Config{Models: map[string]*Model{}}
	maps.Copy(cfg.Models, user.models)
	if user.hasDefault {
		cfg.Default = user.defaultVal
	}
	if user.hasHistoryFile {
		cfg.HistoryFile = user.historyFile
	}
	if user.hasProxy {
		cfg.Proxy = user.proxyVal
	}
	if user.hasScraper {
		cfg.Scraper = user.scraperVal
	}
	if user.hasCheck {
		cfg.Check = user.checkVal
	}
	if user.hasCheckAuto {
		cfg.CheckAuto = user.checkAutoVal
	}
	if user.hasReasoningDisplay {
		cfg.ReasoningDisplay = user.reasoningDisplayVal
	}
	if user.hasMaxSteps {
		cfg.MaxSteps = user.maxStepsVal
	}
	if user.hasMaxErrorReflections {
		cfg.MaxErrorReflections = user.maxErrorReflectionsVal
	}
	if user.hasGitSign {
		cfg.GitSign = user.gitSignVal
	}
	if user.hasEnvAllow {
		cfg.EnvAllow = user.envAllowVal
	}
	if project != nil {
		maps.Copy(cfg.Models, project.models)
		if project.hasDefault {
			cfg.Default = project.defaultVal
		}
		if project.hasHistoryFile {
			cfg.HistoryFile = project.historyFile
		}
		if project.hasProxy {
			cfg.Proxy = project.proxyVal
		}
		if project.hasScraper {
			cfg.Scraper = project.scraperVal
		}
		if project.hasCheck {
			// Per-key rather than wholesale, so a project can override one check
			// or add its own without restating the user's whole set.
			cfg.Check = mergeChecks(cfg.Check, project.checkVal)
		}
		if project.hasCheckAuto {
			// Whole-value, unlike check: this is one ordered decision about what
			// runs unattended, and merging two such lists element-wise would give
			// an order nobody wrote.
			cfg.CheckAuto = project.checkAutoVal
		}
		if project.hasReasoningDisplay {
			cfg.ReasoningDisplay = project.reasoningDisplayVal
		}
		if project.hasMaxSteps {
			cfg.MaxSteps = project.maxStepsVal
		}
		if project.hasMaxErrorReflections {
			cfg.MaxErrorReflections = project.maxErrorReflectionsVal
		}
		if project.hasGitSign {
			cfg.GitSign = project.gitSignVal
		}
		// Whole-value like check_auto, not per-element: this is one decision
		// about what the model's commands may see, and a project's word must be
		// able to narrow what the user's config widened — merging the two lists
		// could only ever widen.
		if project.hasEnvAllow {
			cfg.EnvAllow = project.envAllowVal
		}
	}

	// 5. Resolve weak_model refs post-merge; nil => self, permanently.
	for alias, m := range cfg.Models {
		switch ref := m.weakRef.(type) {
		case nil:
			m.WeakModel = m
		case string:
			target, ok := cfg.Models[ref]
			if !ok {
				return nil, fmt.Errorf("model %q: weak_model alias %q not found in merged models", alias, ref)
			}
			m.WeakModel = target
		case *Model:
			if ref.WeakModel == nil {
				ref.WeakModel = ref // inline weak models are their own weak model
			}
			m.WeakModel = ref
		}
		m.weakRef = nil
	}

	// 5b. Resolve each provider's proxy against the global fallback, once per
	// distinct *Model — aliases and inline weak models repeat pointers, and the
	// "direct"->inherit rewrite is not idempotent. "direct" forces a direct
	// connection; an unset provider proxy inherits the global one.
	if cfg.Proxy == "direct" {
		return nil, errors.New("global `proxy` cannot be \"direct\" (leave it unset for no proxy)")
	}
	if _, err := httpx.ProxyTransport(cfg.Proxy); err != nil {
		return nil, fmt.Errorf("global %w", err)
	}
	resolvedProxy := map[*Model]bool{}
	resolveProxy := func(alias string, m *Model) error {
		if m == nil || resolvedProxy[m] {
			return nil
		}
		resolvedProxy[m] = true
		switch m.Provider.Proxy {
		case "direct":
			m.Provider.Proxy = ""
		case "":
			m.Provider.Proxy = cfg.Proxy
		}
		if _, err := httpx.ProxyTransport(m.Provider.Proxy); err != nil {
			return fmt.Errorf("model %q provider %w", alias, err)
		}
		return nil
	}
	for alias, m := range cfg.Models {
		if err := resolveProxy(alias, m); err != nil {
			return nil, err
		}
		if err := resolveProxy(alias, m.WeakModel); err != nil {
			return nil, err
		}
	}

	// 6. Validate.
	if len(cfg.Models) == 0 {
		return nil, errors.New("config declares no models (the `models` dict is empty)")
	}
	if cfg.Default == "" {
		return nil, errors.New("config sets no `default` model alias")
	}
	if _, ok := cfg.Models[cfg.Default]; !ok {
		return nil, fmt.Errorf("default model alias %q is not a key of `models`", cfg.Default)
	}
	// Validated after the merge, since a project may supply the check a user's
	// check_auto names, or vice versa. A typo here would otherwise mean the
	// harness silently runs nothing — the one failure mode this feature
	// exists to prevent.
	for _, name := range cfg.CheckAuto {
		if indexCheck(cfg.Check, name) < 0 {
			known := "none are configured"
			if len(cfg.Check) > 0 {
				known = "configured checks: " + strings.Join(cfg.CheckNames(), ", ")
			}
			return nil, fmt.Errorf("`check_auto` names %q, which is not a `check` entry (%s)", name, known)
		}
	}
	// Adapter, edit_format, and extra_params were validated at
	// construction time by the builtins.

	return cfg, nil
}

// execConfig executes one Starlark file with the three builtins predeclared
// and extracts the required globals.
func execConfig(path string, src []byte, lookup func(string) (string, bool), root string) (*fileGlobals, error) {
	thread := &starlark.Thread{Name: path}
	predeclared := starlark.StringDict{
		"provider": starlark.NewBuiltin("provider", builtinProvider),
		"model":    starlark.NewBuiltin("model", builtinModel),
		"env":      builtinEnv(lookup),
		// The project's root, not the config file's directory, in both configs:
		// a user-level `check = project_checks()` should adapt to whatever
		// project the session opened.
		"project_checks": builtinProjectChecks(root),
	}
	fileOpts := &syntax.FileOptions{
		Set:             true,
		While:           false,
		TopLevelControl: true,
		GlobalReassign:  false,
		Recursion:       false,
	}
	globals, err := starlark.ExecFileOptions(fileOpts, thread, path, src, predeclared)
	if err != nil {
		var evalErr *starlark.EvalError
		if errors.As(err, &evalErr) {
			return nil, fmt.Errorf("%s: %s", path, evalErr.Backtrace())
		}
		return nil, err
	}

	out := &fileGlobals{models: map[string]*Model{}}

	// Both globals are optional per file; the merged result is validated in
	// Load (a project config may override only `default`, or only aliases).
	if modelsV, ok := globals["models"]; ok {
		dict, ok := modelsV.(*starlark.Dict)
		if !ok {
			return nil, fmt.Errorf("%s: `models` must be a dict, got %s", path, modelsV.Type())
		}
		for k, v := range dict.Entries() {
			alias, ok := starlark.AsString(k)
			if !ok {
				return nil, fmt.Errorf("%s: `models` key %s is not a string", path, k.String())
			}
			mv, ok := v.(*modelValue)
			if !ok {
				return nil, fmt.Errorf("%s: models[%q] is not a model value (got %s)", path, alias, v.Type())
			}
			out.models[alias] = mv.m
		}
	}

	if defaultV, ok := globals["default"]; ok {
		s, ok := starlark.AsString(defaultV)
		if !ok {
			return nil, fmt.Errorf("%s: `default` must be a string alias, got %s", path, defaultV.Type())
		}
		out.hasDefault = true
		out.defaultVal = s
	}

	if hv, ok := globals["history_file"]; ok {
		s, ok := starlark.AsString(hv)
		if !ok {
			return nil, fmt.Errorf("%s: `history_file` must be a string path, got %s", path, hv.Type())
		}
		out.hasHistoryFile = true
		out.historyFile = s
	}

	if pv, ok := globals["proxy"]; ok {
		s, ok := starlark.AsString(pv)
		if !ok {
			return nil, fmt.Errorf("%s: `proxy` must be a string URL, got %s", path, pv.Type())
		}
		out.hasProxy = true
		out.proxyVal = s
	}

	if sv, ok := globals["scraper"]; ok {
		list, ok := sv.(*starlark.List)
		if !ok {
			return nil, fmt.Errorf("%s: `scraper` must be a list of strings (argv, with %%s for the URL), got %s", path, sv.Type())
		}
		argv := make([]string, 0, list.Len())
		for i := range list.Len() {
			s, ok := starlark.AsString(list.Index(i))
			if !ok {
				return nil, fmt.Errorf("%s: `scraper`[%d] must be a string, got %s", path, i, list.Index(i).Type())
			}
			argv = append(argv, s)
		}
		if len(argv) == 0 {
			return nil, fmt.Errorf("%s: `scraper` must not be empty", path)
		}
		out.hasScraper = true
		out.scraperVal = argv
	}

	if rv, ok := globals["reasoning_display"]; ok {
		d, err := parseReasoningDisplay(path, rv)
		if err != nil {
			return nil, err
		}
		out.hasReasoningDisplay = true
		out.reasoningDisplayVal = d
	}

	if vv, ok := globals["check"]; ok {
		checks, err := parseChecks(path, vv)
		if err != nil {
			return nil, err
		}
		out.hasCheck = true
		out.checkVal = checks
	}

	if av, ok := globals["check_auto"]; ok {
		list, ok := av.(*starlark.List)
		if !ok {
			return nil, fmt.Errorf(
				"%s: `check_auto` must be a list of check names from `check`, got %s", path, av.Type())
		}
		names := make([]string, 0, list.Len())
		for i := range list.Len() {
			s, ok := starlark.AsString(list.Index(i))
			if !ok {
				return nil, fmt.Errorf("%s: `check_auto`[%d] must be a string, got %s", path, i, list.Index(i).Type())
			}
			names = append(names, s)
		}
		out.hasCheckAuto = true
		out.checkAutoVal = names
	}

	if ms, ok := globals["max_steps"]; ok {
		n, err := parsePositiveInt(path, "max_steps", ms)
		if err != nil {
			return nil, err
		}
		out.hasMaxSteps = true
		out.maxStepsVal = n
	}

	if er, ok := globals["max_error_reflections"]; ok {
		n, err := parsePositiveInt(path, "max_error_reflections", er)
		if err != nil {
			return nil, err
		}
		out.hasMaxErrorReflections = true
		out.maxErrorReflectionsVal = n
	}

	if gs, ok := globals["git_sign"]; ok {
		switch v := gs.(type) {
		case starlark.Bool:
			out.hasGitSign = true
			if bool(v) {
				out.gitSignVal = "-S"
			}
		case starlark.String:
			// A key-id string becomes `-S<keyid>`, e.g. git_sign = "ABC123"
			// -> -SABC123. An empty string is an explicit "unsigned", so it
			// overrides a project or user setting rather than being ignored.
			out.hasGitSign = true
			if s := string(v); s != "" {
				out.gitSignVal = "-S" + s
			}
		default:
			return nil, fmt.Errorf(
				"%s: `git_sign` must be a boolean or a key-id string, got %s", path, gs.Type())
		}
	}

	if ea, ok := globals["env_allow"]; ok {
		list, ok := ea.(*starlark.List)
		if !ok {
			return nil, fmt.Errorf(
				"%s: `env_allow` must be a list of environment variable names, got %s", path, ea.Type())
		}
		names := make([]string, 0, list.Len())
		for i := range list.Len() {
			s, ok := starlark.AsString(list.Index(i))
			if !ok {
				return nil, fmt.Errorf("%s: `env_allow`[%d] must be a string, got %s", path, i, list.Index(i).Type())
			}
			if s == "" || strings.ContainsAny(s, "=") {
				return nil, fmt.Errorf("%s: `env_allow`[%d] is not an environment variable name", path, i)
			}
			names = append(names, s)
		}
		out.hasEnvAllow = true
		out.envAllowVal = names
	}

	return out, nil
}

// parseChecks decodes the top-level `check` dict: check name -> argv. Starlark
// dicts iterate in insertion order, so the declared order is preserved and
// meaningful — a bare check run stops at the first failure, so fast checks go
// first.
func parseChecks(path string, v starlark.Value) ([]Check, error) {
	dict, ok := v.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf(
			"%s: `check` must be a dict of name -> argv list, e.g. {\"test\": [\"go\", \"test\", \"./...\"]}, got %s",
			path, v.Type())
	}
	checks := make([]Check, 0, dict.Len())
	for _, item := range dict.Items() {
		name, ok := starlark.AsString(item[0])
		if !ok {
			return nil, fmt.Errorf("%s: `check` keys must be strings, got %s", path, item[0].Type())
		}
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%s: `check` has an empty check name", path)
		}
		list, ok := item[1].(*starlark.List)
		if !ok {
			return nil, fmt.Errorf("%s: `check[%q]` must be a list of strings (argv), got %s", path, name, item[1].Type())
		}
		argv := make([]string, 0, list.Len())
		for i := range list.Len() {
			s, ok := starlark.AsString(list.Index(i))
			if !ok {
				return nil, fmt.Errorf("%s: `check[%q]`[%d] must be a string, got %s", path, name, i, list.Index(i).Type())
			}
			argv = append(argv, s)
		}
		if len(argv) == 0 {
			return nil, fmt.Errorf("%s: `check[%q]` must not be empty", path, name)
		}
		checks = append(checks, Check{Name: name, Argv: argv})
	}
	return checks, nil
}

// mergeChecks applies the project's checks over the user's: a shared name is
// replaced in place, keeping the user's ordering, and a project-only name is
// appended. This is the ordered form of the whole-key merge `models` uses.
func mergeChecks(user, project []Check) []Check {
	out := slices.Clone(user)
	for _, p := range project {
		if i := slices.IndexFunc(out, func(c Check) bool { return c.Name == p.Name }); i >= 0 {
			out[i] = p
			continue
		}
		out = append(out, p)
	}
	return out
}

// TrustProject computes the project config's multihash and records it in
// the trust store; the `strument trust` command calls this.
func TrustProject(projectRoot, trustStorePath string) (string, error) {
	projPath := filepath.Join(projectRoot, ProjectConfigName)
	src, err := os.ReadFile(projPath)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(projPath)
	if err != nil {
		return "", err
	}
	if trustStorePath == "" {
		if trustStorePath, err = DefaultTrustStorePath(); err != nil {
			return "", err
		}
	}
	ts, err := OpenTrustStore(trustStorePath)
	if err != nil {
		return "", err
	}
	if err := ts.Trust(absPath, src); err != nil {
		return "", err
	}
	return absPath, nil
}
