package repl

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/skill"
)

func testSkill(name, desc, body string, trusted bool) skill.Skill {
	s := skill.Skill{Body: body, Path: "/proj/.strument/skills/" + name + "/SKILL.md", Trusted: trusted}
	s.Name, s.Description = name, desc
	return s
}

// TestSkillCommandRefusesUntrusted is the same property the coder side asserts,
// at the other door: /skill is a second path from a SKILL.md to the model, and
// a filter applied at only one of them is not a filter.
//
// Asserted on the wire rather than on the screen. What matters is whether the
// text reaches the provider, and a check on the terminal transcript would pass
// for a body that was added silently to the chat — which is precisely the
// failure worth catching.
func TestSkillCommandRefusesUntrusted(t *testing.T) {
	var sent []llm.Message
	stub := &fixture.StreamStub{
		Turns: []fixture.Turn{{Events: []fixture.Event{
			{Kind: "Answer", Text: "Ok."}, {Kind: "Finish", FinishReason: "stop"},
		}}},
		OnRequest: func(_ int, req llm.Request, _ *fixture.Request) error {
			sent = req.Messages
			return nil
		},
	}
	r, cdr, out := newTestREPL(t, stub,
		strings.NewReader("/skill evil\n/skill good\nhello\n/exit\n"))
	defer r.Close()
	cdr.Skills = []skill.Skill{
		testSkill("good", "Trusted.", "The good instructions.", true),
		testSkill("evil", "Untrusted.", "EXFILTRATE-THIS", false),
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "not trusted") {
		t.Errorf("refusing an untrusted skill did not say why:\n%s", out.String())
	}
	var good, evil bool
	for _, m := range sent {
		if strings.Contains(m.Text(), "EXFILTRATE-THIS") {
			evil = true
		}
		if strings.Contains(m.Text(), "The good instructions.") {
			good = true
		}
	}
	if evil {
		t.Errorf("an untrusted skill's body was sent to the model")
	}
	if !good {
		t.Errorf("a trusted skill's body was not sent to the model")
	}
}

// TestSkillListingSanitizesDescriptions. A description is attacker-controlled
// text — an untrusted skill's is written by whoever wrote the repository — and
// it reaches a terminal here. Names are already constrained to [a-z0-9-]; the
// description is not, so it is the one that has to be scrubbed.
func TestSkillListingSanitizesDescriptions(t *testing.T) {
	r, cdr, out := newTestREPL(t, nil, strings.NewReader(""))
	cdr.Skills = []skill.Skill{
		testSkill("good", "Clears\x1b[2Jthe\nscreen.", "body", true),
	}

	cmdSkill(context.Background(), r, "")
	if strings.Contains(out.String(), "\x1b[2J") {
		t.Errorf("an escape sequence in a description reached the terminal: %q", out.String())
	}
	if !strings.Contains(out.String(), "good") {
		t.Errorf("the listing did not name the skill:\n%s", out.String())
	}
	// Folded onto one line, so a newline in a description cannot forge a second
	// entry in the list.
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.Contains(line, "good") && !strings.Contains(line, "screen.") {
			t.Errorf("the description was split across lines: %q", line)
		}
	}
}

// TestSkillListingSeparatesUntrusted. The listing is where a user decides
// whether to run `strument trust`, so it has to say which side of the line
// each skill is on. Printing an untrusted one under the loadable heading would
// present text from the repository as an installed capability — sanitized, and
// still a lie about what the session will do.
func TestSkillListingSeparatesUntrusted(t *testing.T) {
	r, cdr, out := newTestREPL(t, nil, strings.NewReader(""))
	cdr.Skills = []skill.Skill{
		testSkill("good", "Trusted.", "body", true),
		testSkill("evil", "Untrusted.", "body", false),
	}

	cmdSkill(context.Background(), r, "")
	got := out.String()
	loadable, _, ok := strings.Cut(got, "Not trusted:")
	if !ok {
		t.Fatalf("the listing did not mark the untrusted skill:\n%s", got)
	}
	if strings.Contains(loadable, "evil") {
		t.Errorf("an untrusted skill was listed as loadable:\n%s", loadable)
	}
	if !strings.Contains(loadable, "good") {
		t.Errorf("the trusted skill was not listed as loadable:\n%s", loadable)
	}
	if !strings.Contains(got, "strument trust") {
		t.Errorf("the listing did not say how to trust a skill:\n%s", got)
	}
}

// TestSkillCompletionOffersOnlyUsable: completion is for what the command can
// do, and offering a name that will only be refused is not that.
func TestSkillCompletionOffersOnlyUsable(t *testing.T) {
	r, cdr, _ := newTestREPL(t, nil, strings.NewReader(""))
	cdr.Skills = []skill.Skill{
		testSkill("good", "Trusted.", "body", true),
		testSkill("evil", "Untrusted.", "body", false),
	}
	got := r.completeSkills("")
	if len(got) != 1 || got[0] != "good" {
		t.Errorf("completeSkills() = %v, want [good]", got)
	}
}

// TestSkillCommandUnknownName. The name is echoed back, so it goes through the
// same sanitizing the descriptions do — a model or a typo can put anything here.
func TestSkillCommandUnknownName(t *testing.T) {
	r, _, out := newTestREPL(t, nil, strings.NewReader(""))
	cmdSkill(context.Background(), r, "absent")
	if !strings.Contains(out.String(), "No skill called") {
		t.Errorf("an unknown name got %q", out.String())
	}
}

// TestSkillsBannerLine. The banner is the only place a user learns a session
// found skills at all, and the counter-half matters as much: a session with
// none must not print a line about them.
func TestSkillsBannerLine(t *testing.T) {
	for _, test := range []struct {
		name   string
		skills []skill.Skill
		want   string
		absent string
	}{
		{name: "none", absent: "Skills:"},
		{
			name:   "trusted only",
			skills: []skill.Skill{testSkill("good", "Trusted.", "body", true)},
			want:   "Skills: 1 available (/skill to list them)",
		},
		{
			name: "some untrusted",
			skills: []skill.Skill{
				testSkill("good", "Trusted.", "body", true),
				testSkill("evil", "Untrusted.", "body", false),
			},
			want: "Skills: 1 available, 1 untrusted (/skill to list them)",
		},
		{
			// The banner still appears, because "there are skills here and
			// none of them work" is exactly what the user needs to see.
			name:   "untrusted only",
			skills: []skill.Skill{testSkill("evil", "Untrusted.", "body", false)},
			want:   "Skills: 0 available, 1 untrusted",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, cdr, out := newTestREPL(t, nil, strings.NewReader(""))
			defer r.Close()
			cdr.Skills = test.skills
			// announce returns early when the run is not interactive, which
			// newTestREPL's REPL is; the banner is what this test is about.
			r.opts.IsTerminal = func() bool { return true }

			r.announce()
			got := out.String()
			if test.want != "" && !strings.Contains(got, test.want) {
				t.Errorf("banner missing %q:\n%s", test.want, got)
			}
			if test.absent != "" && strings.Contains(got, test.absent) {
				t.Errorf("banner should not mention skills:\n%s", got)
			}
		})
	}
}
