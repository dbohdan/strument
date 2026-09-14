package config

import (
	"fmt"
	"slices"
	"strings"
)

// The permission names --yes takes. Three kinds, deliberately in one flag: the
// first three grant the model a capability, the next two answer a question the
// harness asks about its own pacing, and the last answers a question about what
// the user is putting in front of the model. All of them need a name for a
// session with no terminal to answer on, and a name that says which prompt it
// covers beats a flag that means "everything except the scary one".
const (
	GrantBash      = "bash"      // run a shell command the model wrote
	GrantWebfetch  = "webfetch"  // fetch a URL the model chose
	GrantWebsearch = "websearch" // send the model's query to the configured backend
	// GrantSteps answers "Keep going?" at the step budget. Not a capability:
	// the model gains nothing it did not have, the turn simply continues. Worth
	// knowing before typing it that the budget resets each time it is answered,
	// so granting this is granting an unbounded number of steps — max_steps
	// stops being a limit and becomes an interval.
	GrantSteps = "steps"
	// GrantContext answers "Try to proceed anyway?" when the estimated request
	// exceeds the model's input limit. Also not a capability: the request is
	// sent and the provider decides, which is what the prompt's own text says
	// is probably fine.
	GrantContext = "context"
	// GrantAddOutput answers "Add … to the chat?" — /run's command output,
	// /check's transcript, /consult's answer. The third kind: not a capability
	// and not pacing, but a question about what the *user* is putting in front
	// of the model.
	//
	// It exists because these three had no name at all, so a piped session
	// answered them with "there is no terminal to ask on, and no --yes name
	// covers this prompt" no matter what was passed — and the typed "y" then
	// went to the model as a chat message. Found by running the real binary;
	// no test could have, because a test that supplies its own confirmer never
	// meets the terminal-less path.
	GrantAddOutput = "add-output"
	// GrantAll is every name above. A word someone types, never a default.
	GrantAll = "all"
)

// GrantNames are the individual permissions, in the order help text lists them.
var GrantNames = []string{GrantBash, GrantWebfetch, GrantWebsearch, GrantSteps, GrantContext, GrantAddOutput}

// ParseGrants turns values into the set AutoConfirmer reads. Each value
// may be a comma-separated list, and the flag may repeat, so
// "--yes bash --yes webfetch,websearch" and "--yes bash,webfetch,websearch"
// are the same thing. An unknown name is an error naming what would have
// worked, rather than a silent no-op that looks like a permission was granted.
func ParseGrants(what string, values []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, v := range values {
		for name := range strings.SplitSeq(v, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if name == GrantAll {
				for _, g := range GrantNames {
					out[g] = true
				}
				continue
			}
			if !slices.Contains(GrantNames, name) {
				return nil, fmt.Errorf("%s %s: unknown name (want %s, or %q for all of them)",
					what, name, strings.Join(GrantNames, ", "), GrantAll)
			}
			out[name] = true
		}
	}
	return out, nil
}
