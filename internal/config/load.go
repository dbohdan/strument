package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
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
// os.UserConfigDir, which honors XDG_CONFIG_HOME (config-schema §0).
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
}

// Load runs the pipeline of config-schema §8: user config, gated project
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
			return nil, fmt.Errorf("no user config at %s (create it; see config-schema)", userPath)
		}
		return nil, err
	}
	user, err := execConfig(userPath, userSrc, lookup)
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
				if project, err = execConfig(projPath, projSrc, lookup); err != nil {
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
	if project != nil {
		maps.Copy(cfg.Models, project.models)
		if project.hasDefault {
			cfg.Default = project.defaultVal
		}
		if project.hasHistoryFile {
			cfg.HistoryFile = project.historyFile
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
	// Adapter, edit_format, and extra_params were validated at
	// construction time by the builtins.

	return cfg, nil
}

// execConfig executes one Starlark file with the three builtins predeclared
// and extracts the required globals.
func execConfig(path string, src []byte, lookup func(string) (string, bool)) (*fileGlobals, error) {
	thread := &starlark.Thread{Name: path}
	predeclared := starlark.StringDict{
		"provider": starlark.NewBuiltin("provider", builtinProvider),
		"model":    starlark.NewBuiltin("model", builtinModel),
		"env":      builtinEnv(lookup),
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

	return out, nil
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
