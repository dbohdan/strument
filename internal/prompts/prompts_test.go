package prompts

import (
	"strings"
	"testing"
)

func TestToolPromptShape(t *testing.T) {
	// The tool format's system prompt uses only the {final_reminders},
	// {platform}, {code_tools}, and {observation_tools} slots (the schema does
	// the rest); no other braces may linger to survive substitution as a
	// literal.
	for _, slot := range []string{"{final_reminders}", "{platform}", "{code_tools}", "{observation_tools}"} {
		if !strings.Contains(Tool.MainSystem, slot) {
			t.Errorf("tool main_system missing slot %s", slot)
		}
	}
	stripped := Tool.MainSystem
	for _, slot := range []string{"{final_reminders}", "{platform}", "{code_tools}", "{observation_tools}"} {
		stripped = strings.ReplaceAll(stripped, slot, "")
	}
	if strings.ContainsAny(stripped, "{}") {
		t.Errorf("tool main_system has an unexpected brace: %q", stripped)
	}
	if !strings.Contains(Tool.SystemReminder, "{final_reminders}") {
		t.Errorf("tool system_reminder missing {final_reminders}")
	}

	// The observation paragraph moved behind {observation_tools}, which the
	// coder fills from the tool-set flag; the pinned phrases live in
	// ObservationBullet now. The other two group separations are still in the
	// template itself — that is the part the schema cannot carry.
	for _, want := range []string{
		"change files directly",            // edit/write
		"the user is asked before it runs", // bash
	} {
		if !strings.Contains(Tool.MainSystem, want) {
			t.Errorf("tool main_system should convey %q", want)
		}
	}
	if obs := ObservationBullet; !strings.Contains(obs, "change nothing and need no permission") {
		t.Errorf("the observation bullet lost its cost sentence: %q", obs)
	}
	if !strings.Contains(Tool.MainSystem, "{observation_tools}") {
		t.Error("tool main_system lost the observation slot")
	}
	// The loop's shape is the other thing only the prompt can say: results come
	// back, and a reply without a tool call is what ends the turn.
	if !strings.Contains(Tool.MainSystem, "result comes back to you") {
		t.Error("tool main_system should tell the model its tool results return")
	}
	if !strings.Contains(Tool.MainSystem, "ends the turn") {
		t.Error("tool main_system should say what ends the turn")
	}
	// The reach clause is measured, not stylistic: without it three models
	// updated a stale test 76 times in 90, with it 87 (CMH p=0.011). It is
	// pinned so a tidy-up cannot quietly revert an experiment.
	// See doc/experiments/2026-08-prompt-scope/README.md.
	for _, want := range []string{
		"Carry the change through everywhere it reaches",
		"the tests that cover it",
		"That is the same request, not extra work",
	} {
		if !strings.Contains(Tool.MainSystem, want) {
			t.Errorf("the scope block should say %q", want)
		}
	}
	// The bans survive alongside it: the clause buys reach, and the block still
	// has to forbid the drive-by work it was inherited to forbid.
	if !strings.Contains(Tool.MainSystem, "no drive-by refactoring") {
		t.Error("the scope block lost its ban list")
	}
}

// The notes header names the session the notes came from and the session
// reading them, but only when naming them draws a contrast.
//
// The two names go in together or not at all. A source label means nothing to
// a reader with no label of its own, and a self label beside notes from the
// same session says nothing at all — both halves of that are checked here,
// because the failure either way is a prompt that reads fine and misleads.
func TestSessionNotesPrefixNamesSessionsOnlyWhenTheyDiffer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ctx     SessionNotesContext
		want    []string
		notWant []string
	}{
		{
			name: "a fork names both sides",
			ctx:  SessionNotesContext{When: "2026-09-18 14:02", From: "spike", In: "impl"},
			want: []string{
				`a different session, named "spike"`,
				`You are working in the session named "impl"`,
				"2026-09-18 14:02",
			},
			notWant: []string{"an earlier session"},
		},
		{
			name:    "the same session names neither",
			ctx:     SessionNotesContext{When: "2026-09-18 14:02", From: "review", In: "review"},
			want:    []string{"an earlier session"},
			notWant: []string{"review", "You are working in"},
		},
		{
			name:    "an unknown source names neither",
			ctx:     SessionNotesContext{When: "2026-09-18 14:02", In: "impl"},
			want:    []string{"an earlier session"},
			notWant: []string{"impl", "You are working in"},
		},
		{
			name:    "a reader that does not know its own session names neither",
			ctx:     SessionNotesContext{When: "2026-09-18 14:02", From: "spike"},
			want:    []string{"an earlier session"},
			notWant: []string{"spike", "You are working in"},
		},
		{
			name:    "no date says so rather than leaving a gap",
			ctx:     SessionNotesContext{},
			want:    []string{"date unknown", "an earlier session"},
			notWant: []string{"You are working in"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SessionNotesPrefix(tc.ctx)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("header does not contain %q:\n%s", want, got)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("header contains %q and should not:\n%s", notWant, got)
				}
			}
		})
	}
}

// The conflict rule keeps the last word.
//
// It is the block's most important sentence — the counter-metric turned into
// an instruction — and the first draft of the session labels appended after
// it, which rendering caught and reading the format string had not. Pinned
// here so it stays caught.
func TestSessionNotesPrefixKeepsTheConflictRuleLast(t *testing.T) {
	got := SessionNotesPrefix(SessionNotesContext{When: "2026-09-18 14:02", From: "spike", In: "impl"})
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "the files are right") {
		t.Errorf("the block ends on %q, want the conflict rule:\n%s", last, got)
	}
	// And the two session labels are adjacent, because they are a pair.
	if strings.Index(got, "named \"impl\"") < strings.Index(got, "a summary, not a record") {
		return
	}
	t.Errorf("the reader's own session is stated after the standing claims:\n%s", got)
}

// The header's standing claims survive every variant: they are the sentences
// a live trial settled, and the conflict rule is the one that keeps a model
// from acting confidently on a note the tree has moved past.
func TestSessionNotesPrefixAlwaysCarriesItsStandingClaims(t *testing.T) {
	for _, ctx := range []SessionNotesContext{
		{When: "2026-09-18 14:02", From: "spike", In: "impl"},
		{When: "2026-09-18 14:02", From: "review", In: "review"},
		{},
	} {
		got := SessionNotesPrefix(ctx)
		for _, want := range []string{
			"written by Strument",
			"a summary, not a record",
			"the files are right",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%+v: header dropped %q:\n%s", ctx, want, got)
			}
		}
		// No model is named, whatever the context: the header makes no claim
		// about authorship, deliberately.
		for _, never := range []string{"model", "Model"} {
			if strings.Contains(got, never) {
				t.Errorf("%+v: header mentions %q:\n%s", ctx, never, got)
			}
		}
	}
}

// The harness speaks to the model in marked user-role messages, and the prompt
// has to say what the mark means: without it, models read an unmarked note as
// the user's words (doc/experiments/2026-09-note-attribution).
func TestPromptDefinesTheHarnessMarker(t *testing.T) {
	if !strings.Contains(Tool.MainSystem, HarnessNoteLine) {
		t.Error("main_system does not define the [strument] marker")
	}
}
