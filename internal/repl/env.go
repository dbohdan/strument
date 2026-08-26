package repl

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
)

// rebuildEnvAllow recomputes coder.EnvAllow from the config's list plus this
// session's /env changes. Called after every /env subcommand, at startup, and
// on /reload — where the session sets are cleared first, making the config the
// source of truth again.
func (r *REPL) rebuildEnvAllow() {
	var cfgAllow []string
	if r.opts.Config != nil {
		cfgAllow = r.opts.Config.EnvAllow
	}
	merged := make([]string, 0, len(cfgAllow)+len(r.envAdded))
	merged = append(merged, cfgAllow...)
	for name := range r.envAdded {
		if !slices.Contains(merged, name) {
			merged = append(merged, name)
		}
	}
	out := merged[:0]
	for _, name := range merged {
		if !r.envDropped[name] {
			out = append(out, name)
		}
	}
	r.coder.EnvAllow = out
}

// credentialShaped reports whether a variable name looks like a credential.
// Used for a notice, never a block: a hard filter here would push users toward
// writing tokens to files, which is worse than passing them deliberately.
func credentialShaped(name string) bool {
	return strings.Contains(strings.ToLower(name), "token") ||
		strings.Contains(strings.ToLower(name), "key") ||
		strings.Contains(strings.ToLower(name), "secret") ||
		strings.Contains(strings.ToLower(name), "password") ||
		strings.Contains(strings.ToLower(name), "credential")
}

// envDisplay renders the effective allowlist by origin: the defaults, the
// config's env_allow, and this session's /env changes. Names only — a value is
// never printed, for the same reason the config carries names only.
func (r *REPL) envDisplay() string {
	var b strings.Builder
	fmt.Fprintf(&b, "default  %s", setDefaults())
	b.WriteString("\n")

	var cfgAllow []string
	if r.opts.Config != nil {
		cfgAllow = r.opts.Config.EnvAllow
	}
	if len(cfgAllow) == 0 && len(r.envAdded) == 0 && len(r.envDropped) == 0 {
		b.WriteString("config   (none)\n")
	} else {
		for _, name := range cfgAllow {
			mark := ""
			if r.envDropped[name] {
				mark = "  (dropped this session)"
			} else if os.Getenv(name) == "" {
				mark = "  (not set)"
			}
			fmt.Fprintf(&b, "config   %s%s\n", name, mark)
		}
		for name := range r.envAdded {
			if !r.envAdded[name] {
				continue
			}
			mark := ""
			if r.envDropped[name] {
				continue // reset display: both sets never carry the same name
			}
			if os.Getenv(name) == "" {
				mark = "  (not set)"
			}
			fmt.Fprintf(&b, "session  + %s%s\n", name, mark)
		}
		for name := range r.envDropped {
			if !r.envDropped[name] {
				continue
			}
			if slices.Contains(cfgAllow, name) {
				continue // shown beside its config entry above
			}
			fmt.Fprintf(&b, "session  − %s\n", name)
		}
	}
	return b.String()
}

// setDefaults renders the default allowlist as /env shows it: only the
// variables actually set in the environment (the unset ones are noise now that
// the defaults are an enumeration), with " ..." when any are hidden.
func setDefaults() string {
	var set []string
	for _, name := range coder.DefaultEnvAllowNames() {
		if os.Getenv(name) != "" {
			set = append(set, name)
		}
	}
	if len(set) < len(coder.DefaultEnvAllowNames()) && len(set) > 0 {
		set = append(set, "...")
	}
	return strings.Join(set, " ")
}

// cmdEnv is the ad-hoc allowlist command: /env shows the effective list by
// origin, /env add and /env drop change it for this session, /env reset
// returns to what the config says. Session-scoped by design — a persistent
// change belongs in env_allow, where it is written down and (for a project
// file) trusted.
func cmdEnv(_ context.Context, r *REPL, args string) string {
	sub, rest, _ := strings.Cut(args, " ")
	names := strings.Fields(rest)
	switch sub {
	case "":
		r.printf("%s", strings.TrimRight(r.envDisplay(), "\n"))

	case "add":
		if len(names) == 0 {
			r.out.Errorf("Usage: /env add <name> ...")
			return ""
		}
		for _, name := range names {
			if !config.ValidEnvAllowName(name) {
				r.out.Errorf("%q is not an environment variable name (names only; values come from the environment).", name)
				continue
			}
			if os.Getenv(name) == "" {
				r.out.Errorf("%s is not set in the environment and cannot be added.", name)
				continue
			}
			delete(r.envDropped, name)
			r.envAdded[name] = true
			if credentialShaped(name) {
				r.out.Warningf("%s looks like a credential; it is now visible to model-run commands.", name)
			}
		}
		r.rebuildEnvAllow()

	case "drop":
		if len(names) == 0 {
			r.out.Errorf("Usage: /env drop <name> ...")
			return ""
		}
		for _, name := range names {
			if !config.ValidEnvAllowName(name) {
				r.out.Errorf("%q is not an environment variable name.", name)
				continue
			}
			delete(r.envAdded, name)
			r.envDropped[name] = true
			if name == "PATH" {
				r.out.Warningf("Most commands will stop working without PATH.")
			}
		}
		r.rebuildEnvAllow()

	case "reset":
		r.envAdded = map[string]bool{}
		r.envDropped = map[string]bool{}
		r.rebuildEnvAllow()
		r.printf("Environment allowlist reset to the config.")

	default:
		r.out.Errorf("%s", usage("env"))
	}
	return ""
}
