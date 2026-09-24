package config

import (
	"fmt"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"

	"go.starlark.net/starlark"

	"dbohdan.com/strument/internal/render"
)

// Inspecting a project config is what `strument trust` shows the user before it
// records anything: the file is read, executed in the same sandboxed Starlark
// the load path uses, and asked what it would grant.
//
// It replaced an earlier design that diffed the file against the content
// trusted last time. A diff means the trust store has to hold whole config
// files, and the store is a plaintext state file — a project config can carry
// internal hostnames, a colleague's name in a prompt override, an env_set
// value, none of it secret and all of it unexpected to find sitting in
// ~/.local/state/. The store keeps holding (path, multihash) and nothing else;
// that hash still says whether a file is new, changed, or unchanged, which is
// the half of a diff worth having.

// Capability is one thing a project config would be allowed to do, named by the
// key that grants it. Detail is the specific — which hosts, which variables,
// which commands — because "sets env_allow" is not a disclosure.
type Capability struct {
	Key    string
	Detail string
}

// ProjectInspection is what a project config would change if it were trusted.
type ProjectInspection struct {
	// Path is the config file, or "" when the project has none.
	Path string
	// Capabilities are the keys worth stopping over, in the order the merge
	// block applies them.
	Capabilities []Capability
	// Preferences are the keys whose worst case is an annoyance, named without
	// detail. Named rather than omitted: a key nobody can see is a key nobody
	// classified.
	Preferences []string
	// MissingEnv are the env() variables the file read that are not set *and*
	// gave no default. They were read as empty rather than failing the
	// inspection, so a config written for someone else's machine can still be
	// summarised. A variable with a default is not listed: nothing about it is
	// unknown, and reporting it was a bug that also made the summary show the
	// empty string where a session would use the default.
	MissingEnv []string
}

// Empty reports whether the config changes nothing at all.
func (p *ProjectInspection) Empty() bool {
	return p == nil || (len(p.Capabilities) == 0 && len(p.Preferences) == 0)
}

// inspectSteps bounds the inspection pass.
//
// This is the only place Strument executes code it has *not* been told to
// trust, which is the whole point of the command: the summary has to come from
// the file's real behaviour, not from a guess at it. That is safe as it stands
// — predeclaredGlobals exposes provider, model, search, env, project_checks and
// platform, none of which writes, execs, or opens a socket, and execFileOptions
// already disables `while` and recursion. What is left is a top-level `for` over
// a large range, which cannot do anything but spin. A step limit turns that into
// an error instead of a hang.
const inspectSteps = 50_000_000

// InspectProjectConfig reads the project's config and reports what trusting it
// would grant. A project with no config returns (nil, nil), the same
// not-an-error that TrustProject treats it as.
func InspectProjectConfig(projectRoot string) (*ProjectInspection, error) {
	path, err := FindProjectConfig(projectRoot)
	if err != nil || path == "" {
		return nil, err
	}
	src, err := readProjectConfig(path)
	if err != nil {
		return nil, err
	}

	// A variable that is not set and has no default reads as empty instead of
	// failing the load, the way `strument config` does it: a config written on
	// another machine should still be summarisable here, and the names go in
	// the report so the reader knows which of its values they are not seeing.
	// One that *has* a default is not missing at all and is not reported —
	// envResolver says why that distinction needed a type.
	//
	// The resolved values are kept for the redaction pass below. Only values
	// that came from the environment: a default is written in the file, so
	// showing it reveals nothing the reader could not read there.
	seen := map[string]string{}
	var missing []string
	env := envResolver{
		lookup: func(name string) (string, bool) {
			v, ok := os.LookupEnv(name)
			if ok {
				seen[name] = v
			}
			return v, ok
		},
		onMissing: func(name string) {
			if !slices.Contains(missing, name) {
				missing = append(missing, name)
			}
		},
	}

	g, err := inspectConfig(path, src, env, projectRoot)
	if err != nil {
		return nil, err
	}

	out := &ProjectInspection{Path: path, MissingEnv: missing}
	red := redactor(seen)
	for _, f := range projectKeyOrder {
		k := projectKeys[f]
		if !k.set(g) {
			continue
		}
		if k.detail == nil {
			out.Preferences = append(out.Preferences, k.name)
			continue
		}
		out.Capabilities = append(out.Capabilities, Capability{Key: k.name, Detail: k.detail(g, red)})
	}
	return out, nil
}

// inspectConfig is execConfig with a step limit. Separate rather than a
// parameter on execConfig so the limit cannot be applied to the load path by
// accident: a *trusted* config that legitimately loops for a long time is the
// user's own file and must not be cut off.
func inspectConfig(path string, src []byte, env envResolver, root string) (*fileGlobals, error) {
	return execConfigThread(path, src, env, root, func(t *starlark.Thread) {
		t.SetMaxExecutionSteps(inspectSteps)
	})
}

// redactor returns a function that removes environment values from a string
// meant for the screen.
//
// env() resolves when the file executes, so a check argv, a base URL, or a
// proxy can hold a secret the file does not literally contain —
// provider(api_key = env("...")) always does. Rather than trying to name every
// field that might carry one, anything that came out of the environment is
// replaced by the variable it came from wherever it appears. What the summary
// prints then cannot contain a value the user did not already have.
//
// The substitute is shell-style ${NAME}, which is nobody's config syntax on
// purpose. An earlier draft wrote env(NAME), and that is not Starlark — the
// file would have to say env("NAME"), quoted — nor is it POSIX shell. A
// placeholder that looks like source the reader could paste back, and isn't,
// is worse than one that plainly reads as substitution.
//
// Values shorter than four characters are left alone. A TZ of "UTC" or a
// PAGER of "cat" is not a secret, and substituting every occurrence of a
// three-character string would make the output unreadable for no gain.
func redactor(env map[string]string) func(string) string {
	names := slices.Sorted(maps.Keys(env))
	// Longest value first, so a variable whose value contains another's is
	// replaced whole rather than in pieces.
	slices.SortStableFunc(names, func(a, b string) int { return len(env[b]) - len(env[a]) })
	var pairs []string
	for _, n := range names {
		if len(env[n]) < 4 {
			continue
		}
		pairs = append(pairs, env[n], "${"+n+"}")
	}
	if len(pairs) == 0 {
		return func(s string) string { return s }
	}
	r := strings.NewReplacer(pairs...)
	return r.Replace
}

// projectKey is one key a project config can set, classified once.
//
// Announced keys reach outside the conversation, change what executes, or
// redirect where data goes. The rest are preferences: their worst case is an
// annoyance, and they are named without detail. A key is announced when a
// stranger's repository setting it would be worth a second of the user's
// attention — which is most of them, and deliberately so.
//
// Keyed by the fileGlobals flag field, not by the config key's own name, so the
// guard test in internal/fixture can derive the list from the merge block in
// load.go mechanically. `models` is the one thing here with no flag of its own.
type projectKey struct {
	name string // the key as the user writes it in a config
	set  func(*fileGlobals) bool
	// detail renders the specifics. nil marks a preference.
	detail func(*fileGlobals, func(string) string) string
}

// projectKeyOrder is the merge block's order, so the summary reads in the order
// the settings would be applied.
var projectKeyOrder = []string{
	"models",
	"hasDefault",
	"hasProxy",
	"hasScraper",
	"hasCheck",
	"hasCheckAuto",
	"hasReasoningDisplay",
	"hasMaxSteps",
	"hasMaxUndoTurns",
	"hasMaxErrorReflections",
	"hasWebfetchAllow",
	"hasWebSearch",
	"hasLoopDetection",
	"hasLanguageParser",
	"hasShell",
	"hasAnchoredEdits",
	"hasIndentColumn",
	"hasObservationViaRunCode",
	"hasSandbox",
	"hasSandboxWrite",
	"hasShellTimeout",
	"hasRetryTimeout",
	"hasGitSign",
	"hasEnvAllow",
	"hasAutoApprove",
	"hasEnvSet",
	"hasExampleMessages",
	"hasPromptSystemPrefix",
	"hasPromptCode",
	"hasPromptAsk",
	"hasPromptCommit",
	"hasPromptReadOnly",
	"hasChatLanguage",
}

var projectKeys = map[string]projectKey{
	// models has no has* flag: the merge is maps.Copy, so a project's alias
	// simply wins. It is also the most powerful thing in the file. A redefined
	// alias can send the conversation to any base_url with a key from the
	// user's own environment, and the user would see only the alias they
	// always type.
	"models": {
		name: "models",
		set:  func(g *fileGlobals) bool { return len(g.models) > 0 },
		detail: func(g *fileGlobals, red func(string) string) string {
			aliases := slices.Sorted(maps.Keys(g.models))
			parts := make([]string, 0, len(aliases))
			for _, a := range aliases {
				parts = append(parts, fmt.Sprintf("%s (%s)", a, red(endpoint(g.models[a]))))
			}
			return fmt.Sprintf("defines %s: %s", render.Plural(len(aliases), "alias", "aliases"), strings.Join(parts, ", "))
		},
	},

	// A preference: it picks among models. One that names an alias the project
	// itself defines is already covered by the models line above; one that
	// names a user alias only chooses between models the user wrote.
	"hasDefault": {name: "default", set: func(g *fileGlobals) bool { return g.hasDefault }},

	"hasProxy": {
		name: "proxy",
		set:  func(g *fileGlobals) bool { return g.hasProxy },
		detail: func(g *fileGlobals, red func(string) string) string {
			if g.proxyVal == "" {
				return "no proxy: requests go direct"
			}
			return "sends requests through " + red(g.proxyVal)
		},
	},
	"hasScraper": {
		name: "scraper",
		set:  func(g *fileGlobals) bool { return g.hasScraper },
		detail: func(g *fileGlobals, red func(string) string) string {
			if len(g.scraperVal) == 0 {
				return "fetches pages with Strument's own client"
			}
			return "fetches pages by running: " + red(strings.Join(g.scraperVal, " "))
		},
	},
	"hasCheck": {
		name: "check",
		set:  func(g *fileGlobals) bool { return g.hasCheck },
		detail: func(g *fileGlobals, red func(string) string) string {
			if len(g.checkVal) == 0 {
				return "no checks"
			}
			parts := make([]string, 0, len(g.checkVal))
			for _, c := range g.checkVal {
				parts = append(parts, c.Name+": "+strings.Join(c.Argv, " "))
			}
			return "the check tool runs " + red(strings.Join(parts, "; "))
		},
	},
	"hasCheckAuto": {
		name: "check_auto",
		set:  func(g *fileGlobals) bool { return g.hasCheckAuto },
		detail: func(g *fileGlobals, _ func(string) string) string {
			if len(g.checkAutoVal) == 0 {
				return "nothing runs unattended after an edit"
			}
			return "runs unattended after an edit: " + strings.Join(g.checkAutoVal, ", ")
		},
	},

	"hasReasoningDisplay":    {name: "reasoning_display", set: func(g *fileGlobals) bool { return g.hasReasoningDisplay }},
	"hasMaxSteps":            {name: "max_steps", set: func(g *fileGlobals) bool { return g.hasMaxSteps }},
	"hasMaxUndoTurns":        {name: "max_undo_turns", set: func(g *fileGlobals) bool { return g.hasMaxUndoTurns }},
	"hasMaxErrorReflections": {name: "max_error_reflections", set: func(g *fileGlobals) bool { return g.hasMaxErrorReflections }},

	"hasWebfetchAllow": {
		name: "webfetch_allow",
		set:  func(g *fileGlobals) bool { return g.hasWebfetchAllow },
		detail: func(g *fileGlobals, red func(string) string) string {
			if len(g.webfetchAllowVal) == 0 {
				return "every fetch is asked about"
			}
			return "fetched without asking: " + red(strings.Join(g.webfetchAllowVal, ", "))
		},
	},
	"hasWebSearch": {
		name: "websearch",
		set:  func(g *fileGlobals) bool { return g.hasWebSearch },
		detail: func(g *fileGlobals, red func(string) string) string {
			if g.webSearchVal == nil {
				return "web search off"
			}
			// The backend's api_key is deliberately absent, as it is from
			// searchValue.String and from everything else that prints a search
			// config.
			return "searches through " + g.webSearchVal.Backend + " at " + red(g.webSearchVal.URL)
		},
	},

	"hasLoopDetection": {name: "loop_detection", set: func(g *fileGlobals) bool { return g.hasLoopDetection }},
	// Turns a local parser on or off; grants nothing either way.
	"hasLanguageParser": {name: "language_parser", set: func(g *fileGlobals) bool { return g.hasLanguageParser }},

	"hasShell": {
		name: "shell",
		set:  func(g *fileGlobals) bool { return g.hasShell },
		detail: func(g *fileGlobals, _ func(string) string) string {
			if g.shellVal {
				return "the model may run shell commands"
			}
			return "the model may not run shell commands"
		},
	},

	"hasAnchoredEdits": {name: "anchored_edits", set: func(g *fileGlobals) bool { return g.hasAnchoredEdits }},
	"hasIndentColumn":  {name: "indent_column", set: func(g *fileGlobals) bool { return g.hasIndentColumn }},
	// Withholds tools rather than granting any: the model loses the direct
	// read-only calls and keeps the same reach through run_code.
	"hasObservationViaRunCode": {
		name: "observation_via_run_code",
		set:  func(g *fileGlobals) bool { return g.hasObservationViaRunCode },
	},

	"hasSandbox": {
		name: "sandbox",
		set:  func(g *fileGlobals) bool { return g.hasSandbox },
		detail: func(g *fileGlobals, _ func(string) string) string {
			if g.sandboxVal == "" {
				return `"": the sandbox is off`
			}
			return "confines the model's commands with " + g.sandboxVal
		},
	},
	"hasSandboxWrite": {
		name: "sandbox_write",
		set:  func(g *fileGlobals) bool { return g.hasSandboxWrite },
		detail: func(g *fileGlobals, red func(string) string) string {
			if len(g.sandboxWriteVal) == 0 {
				return "the sandbox may write nothing beyond the project"
			}
			return "the model's commands may also write: " + red(strings.Join(g.sandboxWriteVal, ", "))
		},
	},
	"hasShellTimeout": {
		name: "shell_timeout",
		set:  func(g *fileGlobals) bool { return g.hasShellTimeout },
		detail: func(g *fileGlobals, _ func(string) string) string {
			if g.shellTimeoutVal == 0 {
				return "a model-run command may run forever"
			}
			return fmt.Sprintf("a model-run command is killed after %ds", g.shellTimeoutVal)
		},
	},
	"hasRetryTimeout": {
		name: "retry_timeout",
		set:  func(g *fileGlobals) bool { return g.hasRetryTimeout },
		detail: func(g *fileGlobals, _ func(string) string) string {
			return fmt.Sprintf("a request is retried for up to %ds of waiting", g.retryTimeoutVal)
		},
	},
	"hasGitSign": {
		name: "git_sign",
		set:  func(g *fileGlobals) bool { return g.hasGitSign },
		detail: func(g *fileGlobals, red func(string) string) string {
			if g.gitSignVal == "" {
				return "commits are not signed"
			}
			return "signs the commits Strument makes: " + red(g.gitSignVal)
		},
	},
	"hasEnvAllow": {
		name: "env_allow",
		set:  func(g *fileGlobals) bool { return g.hasEnvAllow },
		detail: func(g *fileGlobals, _ func(string) string) string {
			if len(g.envAllowVal) == 0 {
				return "model-run commands see no environment variables"
			}
			return "model-run commands see: " + strings.Join(g.envAllowVal, ", ")
		},
	},
	"hasAutoApprove": {
		name: "auto_approve",
		set:  func(g *fileGlobals) bool { return g.hasAutoApprove },
		detail: func(g *fileGlobals, _ func(string) string) string {
			if len(g.autoApproveVal) == 0 {
				return "nothing is approved automatically"
			}
			return "stops asking before: " + strings.Join(g.autoApproveVal, ", ")
		},
	},
	"hasEnvSet": {
		name: "env_set",
		set:  func(g *fileGlobals) bool { return g.hasEnvSet },
		detail: func(g *fileGlobals, _ func(string) string) string {
			// Names only, never values. env_set is the one key a project config
			// is *expected* to carry a token in, so the general redaction above
			// is not enough here: a literal secret in the file never went
			// through env() and would not be caught.
			names := slices.Sorted(maps.Keys(g.envSetVal))
			return fmt.Sprintf("sets %s in model-run commands: %s",
				render.Plural(len(names), "variable", "variables"), strings.Join(names, ", "))
		},
	},
	"hasExampleMessages": {
		name: "example_messages",
		set:  func(g *fileGlobals) bool { return g.hasExampleMessages },
		detail: func(g *fileGlobals, _ func(string) string) string {
			return fmt.Sprintf("adds %s the model reads as prior turns",
				render.Plural(len(g.exampleMessagesVal), "example message", "example messages"))
		},
	},

	"hasPromptSystemPrefix": promptKey("prompt_system_prefix", "prepends to every system prompt",
		func(g *fileGlobals) (bool, string) { return g.hasPromptSystemPrefix, g.promptSystemPrefixVal }),
	"hasPromptCode": promptKey("prompt_code", "replaces the code-mode system prompt",
		func(g *fileGlobals) (bool, string) { return g.hasPromptCode, g.promptCodeVal }),
	"hasPromptAsk": promptKey("prompt_ask", "replaces the ask-mode system prompt",
		func(g *fileGlobals) (bool, string) { return g.hasPromptAsk, g.promptAskVal }),
	"hasPromptCommit": promptKey("prompt_commit", "replaces the commit-message prompt",
		func(g *fileGlobals) (bool, string) { return g.hasPromptCommit, g.promptCommitVal }),
	"hasPromptReadOnly": promptKey("prompt_read_only", "replaces the read-only-mode system prompt",
		func(g *fileGlobals) (bool, string) { return g.hasPromptReadOnly, g.promptReadOnlyVal }),

	"hasChatLanguage": {name: "chat_language", set: func(g *fileGlobals) bool { return g.hasChatLanguage }},
}

// promptKey builds the entry for one prompt_* key. The text itself is not
// printed — these run to kilobytes, and a summary nobody reads to the end is
// worse than a size. What matters is that the model's instructions came from
// this repository rather than from Strument.
func promptKey(name, what string, get func(*fileGlobals) (bool, string)) projectKey {
	return projectKey{
		name: name,
		set:  func(g *fileGlobals) bool { ok, _ := get(g); return ok },
		detail: func(g *fileGlobals, _ func(string) string) string {
			_, s := get(g)
			if s == "" {
				return what + " with nothing"
			}
			return fmt.Sprintf("%s (%d bytes)", what, len(s))
		},
	}
}

// endpoint names where a model's requests go, without its credentials.
func endpoint(m *Model) string {
	if m == nil {
		return "unknown"
	}
	if m.Provider.BaseURL == "" {
		return m.Provider.Adapter
	}
	if u, err := url.Parse(m.Provider.BaseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return m.Provider.BaseURL
}
