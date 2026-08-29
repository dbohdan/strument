package repl

import (
	"context"
	"fmt"
	"strings"

	"dbohdan.com/strument/internal/render"
	"dbohdan.com/strument/internal/skill"
)

// cmdSkill answers "what instructions could this session load, and where did
// they come from?", and loads one on request.
//
// The listing follows /sandbox: what is in force, then what looked like it
// would be and is not, through r.out.Errorf with the fix beside it. An
// untrusted skill is the /sandbox `Not granted:` case exactly — something the
// user put in place that is not doing anything, which they will otherwise
// discover from its absence.
func cmdSkill(_ context.Context, r *REPL, args string) string {
	name := strings.TrimSpace(args)
	if name == "" {
		return listSkills(r)
	}
	return loadSkill(r, name)
}

func listSkills(r *REPL) string {
	usable := skill.Usable(r.coder.Skills)
	untrusted := skill.Untrusted(r.coder.Skills)
	if len(usable) == 0 && len(untrusted) == 0 {
		r.printf("No skills found. Put a SKILL.md in a directory under .strument/skills " +
			"in this project, or under ~/.local/share/strument/skills for every project.")
		return ""
	}

	if len(usable) > 0 {
		r.printf("Skills the model can load:")
		for _, s := range usable {
			// A description is written by whoever wrote the skill, and an
			// untrusted one is written by whoever wrote the repository. It
			// reaches a terminal here, so it is sanitized the way a fetched
			// page's text is: a description is not allowed to move the cursor.
			r.printf("  %s (%s): %s", s.Name, s.Root.Scope, render.Sanitize(oneLine(s.Description)))
		}
	}
	for _, s := range untrusted {
		r.out.Errorf("Not trusted: %s (%s) will not be loaded.", s.Name, s.Path)
	}
	if len(untrusted) > 0 {
		r.printf("Run `strument trust` in this directory, then /reload, to allow them.")
	}
	return ""
}

// loadSkill puts a skill's instructions in the chat, the way /web puts a page
// there: the user asked for this material, so it goes in as material and the
// next message is what acts on it.
func loadSkill(r *REPL, name string) string {
	for _, s := range skill.Usable(r.coder.Skills) {
		if s.Name == name {
			r.coder.AppendContext(fmt.Sprintf("The user loaded the skill %q from %s.\n\n%s",
				s.Name, s.Path, s.Body))
			r.printf("Added the skill %s to the chat.", s.Name)
			return ""
		}
	}
	// Named rather than folded into "no such skill": the two call for
	// different things from the user, and the one that has a fix should say
	// what it is.
	for _, s := range skill.Untrusted(r.coder.Skills) {
		if s.Name == name {
			r.out.Errorf("The skill %s is not trusted, so it will not be loaded.", s.Name)
			r.printf("Run `strument trust` in this directory, then /reload.")
			return ""
		}
	}
	r.out.Errorf("No skill called %q. /skill lists the ones there are.", name)
	return ""
}

// completeSkills offers the loadable names. Untrusted ones are left out:
// completion is for what the command can do, and offering a name that will
// only be refused is not that.
func (r *REPL) completeSkills(string) []string {
	usable := skill.Usable(r.coder.Skills)
	out := make([]string, 0, len(usable))
	for _, s := range usable {
		out = append(out, s.Name)
	}
	return out
}

// oneLine flattens a description onto the single line the listing gives it.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
