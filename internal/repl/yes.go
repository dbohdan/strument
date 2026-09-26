package repl

import (
	"context"
	"strings"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
)

// cmdYes shows and changes which confirmation prompts are answered without
// asking.
//
// It closes a gap the /commits work named: --yes was a decision you could only
// make before starting, and the moment you want to revoke one is mid-turn, when
// something has started going somewhere odd. It is also what makes a standing
// auto_approve legible -- a grant nothing announces is the "did that apply?"
// ambiguity this codebase keeps legislating against.
//
// Bare reports, the house pattern (/commits, /model, /env, /notes, /context),
// and the report names each grant's source because that is what tells you
// whether editing the config will change it.
func cmdYes(_ context.Context, r *REPL, args string) string {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		r.printYesState()
		return ""
	}

	switch fields[0] {
	case "add", "drop":
		names := fields[1:]
		if len(names) == 0 {
			r.out.Errorf("%s", usage("yes"))
			return ""
		}
		granted, err := config.ParseGrants("/yes", names)
		if err != nil {
			// The parser's message already names what would have worked.
			r.out.Errorf("%v", err)
			return ""
		}
		for _, name := range coder.GrantNames {
			if !granted[name] {
				continue
			}
			if fields[0] == "add" {
				r.coder.Grants.Add(name)
				continue
			}
			// Dropping something that was never granted changes nothing, and
			// a silent no-op here reads exactly like a successful revocation.
			if !r.coder.Grants.Granted(name) {
				r.out.Warningf("%s was not approved automatically; nothing changed.", name)
				continue
			}
			r.coder.Grants.Drop(name)
		}
		r.printYesState()
	case "reset":
		r.coder.Grants.Reset()
		r.printf("Automatic approvals reset to what --yes and the config set.")
		r.printYesState()
	default:
		r.out.Errorf("%s", usage("yes"))
	}
	return ""
}

// printYesState says what is answered automatically and what still asks.
//
// Both halves, deliberately. "websearch is auto-approved" is half an answer;
// the useful sentence is which prompts will still stop the turn, because that
// is what the user is deciding about when they type this.
func (r *REPL) printYesState() {
	g := r.coder.Grants
	sources := g.Sources()
	if len(sources) == 0 {
		r.printf("Nothing is approved automatically; every prompt asks first.")
		r.printf("  /yes add %s approves one for this run.", coder.GrantWebsearch)
		return
	}

	r.printf("Approved automatically:")
	width := 0
	for _, s := range sources {
		width = max(width, len(s.Name))
	}
	for _, s := range sources {
		r.printf("  %-*s  (%s)", width, s.Name, s.Source)
	}

	var asking []string
	for _, name := range coder.GrantNames {
		if !g.Granted(name) {
			asking = append(asking, name)
		}
	}
	if len(asking) == 0 {
		r.out.Warningf("Every prompt is approved automatically; no turn will pause to ask you.")
	} else {
		r.printf("Still asks first: %s.", strings.Join(asking, ", "))
	}
	if dropped := g.Dropped(); len(dropped) > 0 {
		r.printf("  %s came from --yes or the config; this run asks first again.",
			strings.Join(dropped, ", "))
	}
}

// yesBanner is the startup line, or "" when nothing is granted. A standing
// approval that nothing announces is invisible until the turn it silently
// answers.
func yesBanner(g *coder.Grants) string {
	sources := g.Sources()
	if len(sources) == 0 {
		return ""
	}
	names := make([]string, 0, len(sources))
	for _, s := range sources {
		names = append(names, s.Name)
	}
	return "Auto-approved: " + strings.Join(names, ", ")
}
