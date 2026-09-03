package config

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"dbohdan.com/strument/internal/httpx"
	"dbohdan.com/strument/internal/origin"
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

	hasMaxSteps              bool
	maxStepsVal              int
	hasMaxErrorReflections   bool
	maxErrorReflectionsVal   int
	hasLoopDetection         bool
	loopDetectionVal         bool
	hasAnchoredEdits         bool
	anchoredEditsVal         bool
	hasObservationViaRunCode bool
	observationViaRunCodeVal bool
	hasWebfetchAllow         bool
	webfetchAllowVal         []string
	hasWebSearch             bool
	webSearchVal             *WebSearch

	hasSandbox      bool
	sandboxVal      string
	hasSandboxWrite bool
	sandboxWriteVal []string

	hasShellTimeout bool
	shellTimeoutVal int

	hasGitSign bool
	gitSignVal string

	hasEnvAllow bool
	envAllowVal []string

	hasEnvSet bool
	envSetVal map[string]string

	hasExampleMessages bool
	exampleMessagesVal []ExampleMessage
}

// defaultSandbox is what `sandbox` means when a config does not say.
//
// On by default, because a protection nobody turns on protects nobody, and
// because this one costs nothing to run under: reads and execution are
// untouched. A user who needs it off writes `sandbox = ""`, which is a
// deliberate, visible, greppable act.
//
// A config can express the same rule itself — `sandbox = "landlock" if
// platform.system == "Linux" else ""` — which is the example the platform
// object was added for.
func defaultSandbox() string {
	if runtime.GOOS == "linux" {
		return SandboxLandlock
	}
	return ""
}

// shellTimeoutSeconds maps the config's encoding onto the coder's.
//
// A config says `shell_timeout = 0` to mean "no limit", while the coder's zero
// value has to mean "unset, use the default" — every other numeric setting works
// that way and a Coder built in a test should not silently run without one. The
// two zeroes mean opposite things, so the translation happens here, once, rather
// than being carried as a second boolean field to every reader.
func shellTimeoutSeconds(n int) int {
	if n == 0 {
		return -1
	}
	return n
}

// ValidEnvAllowName reports whether s is acceptable as an env_allow entry:
// a single non-empty word with no "=" (values always come from the real
// environment) and no quoting characters — the config and the /env command
// share this rule, and neither does shell-style unquoting, so a quoted string
// must fail rather than become a name with literal quotes in it.
func ValidEnvAllowName(s string) bool {
	return s != "" && !strings.ContainsAny(s, `="' `)
}

// parsePositiveInt reads a Starlark int that must be at least 1.
// parseNonNegativeInt is parsePositiveInt where zero carries a meaning of its
// own — `shell_timeout = 0` asks for no limit rather than an instant one.
func parseNonNegativeInt(path, name string, v starlark.Value) (int, error) {
	iv, ok := v.(starlark.Int)
	if !ok {
		return 0, fmt.Errorf("%s: `%s` must be a non-negative integer, got %s", path, name, v.Type())
	}
	n, ok := iv.Int64()
	if !ok {
		return 0, fmt.Errorf("%s: `%s` is out of range", path, name)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s: `%s` must not be negative, got %d", path, name, n)
	}
	return int(n), nil
}

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
// config, whole-key merge, post-merge side_model resolution, validation.
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
	// The sandbox is on by default where it can be, and the default is set
	// here rather than at the point of use so `strument model-config` and any
	// other reader sees the same answer the session will act on.
	cfg := &Config{Models: map[string]*Model{}, Sandbox: defaultSandbox()}
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
	if user.hasWebfetchAllow {
		cfg.WebfetchAllow = user.webfetchAllowVal
	}
	if user.hasWebSearch {
		cfg.WebSearch = user.webSearchVal
	}
	if user.hasLoopDetection {
		cfg.NoLoopDetection = !user.loopDetectionVal
	}
	if user.hasAnchoredEdits {
		cfg.AnchoredEdits = user.anchoredEditsVal
	}
	if user.hasObservationViaRunCode {
		cfg.ObservationViaRunCode = user.observationViaRunCodeVal
	}
	if user.hasSandbox {
		cfg.Sandbox = user.sandboxVal
	}
	if user.hasSandboxWrite {
		cfg.SandboxWrite = user.sandboxWriteVal
	}
	if user.hasShellTimeout {
		cfg.ShellTimeout = shellTimeoutSeconds(user.shellTimeoutVal)
	}
	if user.hasGitSign {
		cfg.GitSign = user.gitSignVal
	}
	if user.hasEnvAllow {
		cfg.EnvAllow = user.envAllowVal
	}
	if user.hasEnvSet {
		cfg.EnvSet = user.envSetVal
	}
	if user.hasExampleMessages {
		cfg.ExampleMessages = user.exampleMessagesVal
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
		// Whole-value like env_allow and for the same reason: this is one
		// decision about which hosts stop being asked about, and a project must
		// be able to narrow what the user's config widened. The trust gate is
		// what makes a project's entry the user's own decision.
		if project.hasWebfetchAllow {
			cfg.WebfetchAllow = project.webfetchAllowVal
		}
		// A trusted project may point search at its own instance, or unset it.
		if project.hasWebSearch {
			cfg.WebSearch = project.webSearchVal
		}
		if project.hasLoopDetection {
			cfg.NoLoopDetection = !project.loopDetectionVal
		}
		if project.hasAnchoredEdits {
			cfg.AnchoredEdits = project.anchoredEditsVal
		}
		if project.hasSandbox {
			cfg.Sandbox = project.sandboxVal
		}
		if project.hasSandboxWrite {
			cfg.SandboxWrite = project.sandboxWriteVal
		}
		if project.hasShellTimeout {
			cfg.ShellTimeout = shellTimeoutSeconds(project.shellTimeoutVal)
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
		// Per-entry, unlike env_allow, and for the opposite reason: env_allow is
		// one decision about what the model may see, which a project must be
		// able to narrow wholesale. env_set is a bag of independent settings, so
		// a project naming TZ should not silently drop the user's GOFLAGS.
		if project.hasEnvSet {
			if cfg.EnvSet == nil {
				cfg.EnvSet = map[string]string{}
			}
			maps.Copy(cfg.EnvSet, project.envSetVal)
		}
		// Appended, not replaced: examples are additive teaching, and a
		// project's pair alongside the user's is worth more than either alone.
		if project.hasExampleMessages {
			cfg.ExampleMessages = append(cfg.ExampleMessages, project.exampleMessagesVal...)
		}
	}

	// 5. Resolve side_model refs post-merge; nil => self, permanently.
	for alias, m := range cfg.Models {
		switch ref := m.sideRef.(type) {
		case nil:
			m.SideModel = m
		case string:
			target, ok := cfg.Models[ref]
			if !ok {
				return nil, fmt.Errorf("model %q: side_model alias %q not found in merged models", alias, ref)
			}
			m.SideModel = target
		case *Model:
			if ref.SideModel == nil {
				ref.SideModel = ref // inline side models are their own side model
			}
			m.SideModel = ref
		}
		m.sideRef = nil
	}

	// 5b. Resolve each provider's proxy against the global fallback, once per
	// distinct *Model — aliases and inline side models repeat pointers, and the
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
	// The search backend resolves the same way, because it is the same kind of
	// egress: non-provider, and inheriting the global proxy unless it says
	// otherwise. "direct" matters more here than anywhere else — a self-hosted
	// instance is usually on localhost or the LAN, and a proxy configured for
	// external traffic has no business carrying that request.
	if ws := cfg.WebSearch; ws != nil {
		switch ws.Proxy {
		case "direct":
			ws.Proxy = ""
		case "":
			ws.Proxy = cfg.Proxy
		}
		if _, err := httpx.ProxyTransport(ws.Proxy); err != nil {
			return nil, fmt.Errorf("websearch %w", err)
		}
	}
	for alias, m := range cfg.Models {
		if err := resolveProxy(alias, m); err != nil {
			return nil, err
		}
		if err := resolveProxy(alias, m.SideModel); err != nil {
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

// predeclaredGlobals is everything a config file can reach without defining it.
// Factored out of execConfig so the set has one definition and a test can
// evaluate an expression against exactly what a real config sees.
func predeclaredGlobals(lookup func(string) (string, bool), root string) starlark.StringDict {
	return starlark.StringDict{
		"provider": starlark.NewBuiltin("provider", builtinProvider),
		"model":    starlark.NewBuiltin("model", builtinModel),
		"search":   starlark.NewBuiltin("search", builtinSearch),
		"env":      builtinEnv(lookup),
		// The project's root, not the config file's directory, in both configs:
		// a user-level `check = project_checks()` should adapt to whatever
		// project the session opened.
		"project_checks": builtinProjectChecks(root),
		// Read-only facts about the host, so a config can branch on the OS —
		// the sandbox setting is the first thing that needs to.
		"platform": platformValue{},
	}
}

// execConfig executes one Starlark file with the builtins predeclared and
// extracts the required globals.
func execConfig(path string, src []byte, lookup func(string) (string, bool), root string) (*fileGlobals, error) {
	thread := &starlark.Thread{Name: path}
	predeclared := predeclaredGlobals(lookup, root)
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

	if wa, ok := globals["webfetch_allow"]; ok {
		list, ok := wa.(*starlark.List)
		if !ok {
			return nil, fmt.Errorf(
				"%s: `webfetch_allow` must be a list of hosts, got %s", path, wa.Type())
		}
		origins := make([]string, 0, list.Len())
		for i := range list.Len() {
			str, ok := starlark.AsString(list.Index(i))
			if !ok {
				return nil, fmt.Errorf("%s: `webfetch_allow`[%d] must be a string, got %s", path, i, list.Index(i).Type())
			}
			// Refused at load rather than left to silently never match. An
			// entry is an origin, so a URL written here is a typo with a
			// plausible-looking shape — the worst kind.
			if !origin.ValidEntry(str) {
				return nil, fmt.Errorf(
					"%s: `webfetch_allow`[%d] is %q, which is not a host or host:port — "+
						"write \"docs.python.org\" or \"localhost:3000\", with no scheme and no path", path, i, str)
			}
			origins = append(origins, str)
		}
		out.hasWebfetchAllow = true
		out.webfetchAllowVal = origins
	}

	if ws, ok := globals["websearch"]; ok {
		// None is how a project config turns search off again, which it can
		// only do because it had to be trusted to be read at all.
		if ws == starlark.None {
			out.hasWebSearch = true
			out.webSearchVal = nil
		} else {
			sv, ok := ws.(*searchValue)
			if !ok {
				return nil, fmt.Errorf(
					"%s: `websearch` must be a search() value, got %s — write "+
						"websearch = search(\"searxng\", url=\"http://localhost:8888\")", path, ws.Type())
			}
			cp := sv.s
			out.hasWebSearch = true
			out.webSearchVal = &cp
		}
	}

	if ld, ok := globals["loop_detection"]; ok {
		b, ok := ld.(starlark.Bool)
		if !ok {
			return nil, fmt.Errorf("%s: `loop_detection` must be a boolean, got %s", path, ld.Type())
		}
		out.hasLoopDetection = true
		out.loopDetectionVal = bool(b)
	}

	if ae, ok := globals["anchored_edits"]; ok {
		b, ok := ae.(starlark.Bool)
		if !ok {
			return nil, fmt.Errorf("%s: `anchored_edits` must be a boolean, got %s", path, ae.Type())
		}
		out.hasAnchoredEdits = true
		out.anchoredEditsVal = bool(b)
	}

	if ov, ok := globals["observation_via_run_code"]; ok {
		b, ok := ov.(starlark.Bool)
		if !ok {
			return nil, fmt.Errorf("%s: `observation_via_run_code` must be a boolean, got %s", path, ov.Type())
		}
		out.hasObservationViaRunCode = true
		out.observationViaRunCodeVal = bool(b)
	}

	if sv, ok := globals["sandbox"]; ok {
		name, ok := starlark.AsString(sv)
		if !ok {
			return nil, fmt.Errorf("%s: `sandbox` must be a string, got %s", path, sv.Type())
		}
		// An unknown value is refused rather than ignored. The whole point of
		// this setting is that the user knows whether they are sandboxed, and a
		// typo that silently disabled it would be the worst possible outcome.
		if name != "" && name != SandboxLandlock {
			return nil, fmt.Errorf(
				"%s: `sandbox` must be %q or \"\" (off), got %q", path, SandboxLandlock, name)
		}
		out.hasSandbox = true
		out.sandboxVal = name
	}

	if wv, ok := globals["sandbox_write"]; ok {
		list, ok := wv.(*starlark.List)
		if !ok {
			return nil, fmt.Errorf("%s: `sandbox_write` must be a list of paths, got %s", path, wv.Type())
		}
		paths := make([]string, 0, list.Len())
		for i := range list.Len() {
			str, ok := starlark.AsString(list.Index(i))
			if !ok {
				return nil, fmt.Errorf("%s: `sandbox_write`[%d] must be a string, got %s", path, i, list.Index(i).Type())
			}
			if !filepath.IsAbs(str) {
				return nil, fmt.Errorf(
					"%s: `sandbox_write`[%d] must be an absolute path, got %q — a relative path would depend on where Strument was started", path, i, str)
			}
			paths = append(paths, str)
		}
		out.hasSandboxWrite = true
		out.sandboxWriteVal = paths
	}

	if st, ok := globals["shell_timeout"]; ok {
		// Not parsePositiveInt: zero is meaningful here, and means "no limit".
		n, err := parseNonNegativeInt(path, "shell_timeout", st)
		if err != nil {
			return nil, err
		}
		out.hasShellTimeout = true
		out.shellTimeoutVal = n
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
			if !ValidEnvAllowName(s) {
				return nil, fmt.Errorf("%s: `env_allow`[%d] is not an environment variable name", path, i)
			}
			names = append(names, s)
		}
		out.hasEnvAllow = true
		out.envAllowVal = names
	}

	if es, ok := globals["env_set"]; ok {
		dict, ok := es.(*starlark.Dict)
		if !ok {
			return nil, fmt.Errorf(
				"%s: `env_set` must be a dict of environment variable names to values, got %s", path, es.Type())
		}
		vals := make(map[string]string, dict.Len())
		for _, item := range dict.Items() {
			name, ok := starlark.AsString(item[0])
			if !ok {
				return nil, fmt.Errorf("%s: `env_set` keys must be strings, got %s", path, item[0].Type())
			}
			if !ValidEnvAllowName(name) {
				return nil, fmt.Errorf("%s: `env_set` key %q is not an environment variable name", path, name)
			}
			val, ok := starlark.AsString(item[1])
			if !ok {
				return nil, fmt.Errorf(
					"%s: `env_set[%q]` must be a string, got %s — read one from the environment with env(%q) "+
						"rather than writing a value here", path, name, item[1].Type(), name)
			}
			vals[name] = val
		}
		out.hasEnvSet = true
		out.envSetVal = vals
	}

	if em, ok := globals["example_messages"]; ok {
		list, ok := em.(*starlark.List)
		if !ok {
			return nil, fmt.Errorf(
				"%s: `example_messages` must be a list of [role, content] pairs, got %s", path, em.Type())
		}
		examples := make([]ExampleMessage, 0, list.Len())
		for i := range list.Len() {
			// Both spellings are accepted: ["user", "..."] is what a user
			// naturally writes, and a starlark tuple ("user", "...") is the
			// same pair — refusing the list spelling because it is not a
			// tuple would be refusing the obvious way to write it.
			var items []starlark.Value
			switch pair := list.Index(i).(type) {
			case *starlark.List:
				if pair.Len() != 2 {
					return nil, fmt.Errorf(
						"%s: `example_messages`[%d] must be a [role, content] pair, got a list of %d", path, i, pair.Len())
				}
				items = []starlark.Value{pair.Index(0), pair.Index(1)}
			case *starlark.Tuple:
				if pair.Len() != 2 {
					return nil, fmt.Errorf(
						"%s: `example_messages`[%d] must be a [role, content] pair, got a tuple of %d", path, i, pair.Len())
				}
				items = []starlark.Value{pair.Index(0), pair.Index(1)}
			default:
				return nil, fmt.Errorf(
					"%s: `example_messages`[%d] must be a [role, content] pair, got %s", path, i, list.Index(i).Type())
			}
			role, ok := starlark.AsString(items[0])
			if !ok {
				return nil, fmt.Errorf("%s: `example_messages`[%d] role must be a string", path, i)
			}
			if role != "user" && role != "assistant" {
				return nil, fmt.Errorf(
					"%s: `example_messages`[%d] role must be \"user\" or \"assistant\", got %q", path, i, role)
			}
			content, ok := starlark.AsString(items[1])
			if !ok {
				return nil, fmt.Errorf("%s: `example_messages`[%d] content must be a string", path, i)
			}
			examples = append(examples, ExampleMessage{Role: role, Content: content})
		}
		out.hasExampleMessages = true
		out.exampleMessagesVal = examples
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
//
// A project with no config returns ("", nil) rather than an error. That used to
// be a failure, and stopped being one when a project could carry skills and no
// config.star: `strument trust` is a command about a project, not about one
// file, so having nothing of one kind to trust is not a reason to refuse.
func TrustProject(projectRoot, trustStorePath string) (string, error) {
	projPath := filepath.Join(projectRoot, ProjectConfigName)
	if _, err := os.Stat(projPath); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err := TrustFiles([]string{projPath}, trustStorePath); err != nil {
		return "", err
	}
	return filepath.Abs(projPath)
}

// TrustFiles records each path's current content in the trust store, so a
// later read of that exact content is trusted and an edited one is not.
//
// The store is keyed by absolute path and knows nothing about what a file is
// for, which is why config and skills share it rather than needing a second
// store or a second command: a repository's config and its skills come from
// the same author, so they are one trust decision.
func TrustFiles(paths []string, trustStorePath string) error {
	if len(paths) == 0 {
		return nil
	}
	if trustStorePath == "" {
		var err error
		if trustStorePath, err = DefaultTrustStorePath(); err != nil {
			return err
		}
	}
	ts, err := OpenTrustStore(trustStorePath)
	if err != nil {
		return err
	}
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		if err := ts.Trust(abs, src); err != nil {
			return err
		}
	}
	return nil
}
