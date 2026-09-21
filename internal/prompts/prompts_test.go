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

	// The schema carries the format, so no few-shot examples are needed.
	if len(Tool.ExampleMessages) != 0 {
		t.Errorf("tool example_messages should be empty, got %d", len(Tool.ExampleMessages))
	}
}

func TestAskPromptShape(t *testing.T) {
	// Ask has no few-shot examples: the examples chunk is absent, so the
	// cache-placement breakpoint falls back to system (assembly test).
	if len(Ask.ExampleMessages) != 0 {
		t.Errorf("ask example_messages must be empty, got %d", len(Ask.ExampleMessages))
	}
	// system_reminder is not empty, so the reminder gate still runs. It used to
	// say "overeager rides in through {final_reminders}", which was never true:
	// finalReminders holds the language line and nothing else, and Ask's
	// OvereagerPrompt field was read by no code in the tree. A reviewer grepped
	// for the splice, found none, and reported that the whole block — "Do not
	// return fully detailed code or full diffs" — had never reached a model.
	// The text lives in MainSystem now and the fields are gone.
	if Ask.SystemReminder != "{final_reminders}" {
		t.Errorf("ask system_reminder = %q, want {final_reminders}", Ask.SystemReminder)
	}
	// The falsy sentinel: empty string disables the repo-map branch (it is
	// NOT "emit an empty pair"). assemble.go must treat "" as "skip".
	if Ask.FilesNoFullFilesWithRepoMap != "" {
		t.Errorf("ask files_no_full_files_with_repo_map must be the empty sentinel, got %q", Ask.FilesNoFullFilesWithRepoMap)
	}
	// Ask must describe the tools it actually has. It offers read, grep, glob,
	// ls, and symbol; it once named none of them and opened by saying what it
	// could not do, which left a model with no picture of the mode and no reason
	// to look at anything. These are the sentences whose absence caused that, so
	// they are pinned rather than left to a future tidy-up.
	for _, want := range []string{
		"result comes back to you", // the loop closes here too
		"ends the turn",
	} {
		if !strings.Contains(Ask.MainSystem, want) {
			t.Errorf("ask main_system should convey %q:\n%s", want, Ask.MainSystem)
		}
	}
	// The observation paragraph rides the {observation_tools} slot; its pinned
	// phrases live in AskObservationBullet.
	if !strings.Contains(Ask.MainSystem, "{observation_tools}") {
		t.Error("ask main_system lost the observation slot")
	}
	if obs := AskObservationBullet; !strings.Contains(obs, "change nothing and need no permission") {
		t.Errorf("the ask observation bullet lost its cost sentence: %q", obs)
	}
	// The no-editing rule has to name the mechanism rather than sound like a ban
	// on acting: this mode has no editing tools, so describe changes instead.
	if !strings.Contains(Ask.MainSystem, "no editing tools") {
		t.Errorf("ask main_system should say why it cannot edit:\n%s", Ask.MainSystem)
	}
	if strings.Contains(Ask.MainSystem, "cannot apply edits from it") {
		t.Error("ask main_system reverted to the phrasing that read as 'you cannot act'")
	}
	// The brake on a full rewrite. It sat in the unread OvereagerPrompt field
	// for the whole life of the tool format, so ask mode's only limit on output
	// was "say briefly", and two reviewers independently said they would be
	// tempted to paste a finished file. Pinned in the string that ships.
	if !strings.Contains(Ask.MainSystem, "full diff") {
		t.Errorf("ask main_system does not rule out returning a full diff:\n%s", Ask.MainSystem)
	}
	// Nothing is pinned for editing here because nothing can be: the flat
	// denial "No files are pinned to this session" co-occurs with a read-only
	// reference block whose contents are in the next message, which is
	// 2026-08-readonly-honest.md's twelve-step file hunt.
	if strings.Contains(Ask.FilesNoFullFiles, "No files are pinned") {
		t.Errorf("the ask empty-pin line is a flat denial again:\n%s", Ask.FilesNoFullFiles)
	}
	// {language} and {observation_tools} are the slots Ask substitutes; a stray
	// brace would survive into the prompt as a literal.
	stripped := strings.ReplaceAll(Ask.MainSystem, "{language}", "")
	stripped = strings.ReplaceAll(stripped, "{observation_tools}", "")
	if strings.ContainsAny(stripped, "{}") {
		t.Errorf("ask main_system has an unexpected brace:\n%s", Ask.MainSystem)
	}
}

// TestCommitSystemScopesToTheDiff pins the clause that makes the widened
// commit context safe. Shown earlier turns, models described the *previous*
// turn's change on this commit — with a BREAKING CHANGE marker for a break the
// diff did not contain.
func TestCommitSystemScopesToTheDiff(t *testing.T) {
	if !strings.Contains(CommitSystem, "Earlier turns are background") {
		t.Error("the commit prompt does not scope the message to the diff")
	}
	if !strings.Contains(CommitSystem, "not part of this commit") {
		t.Error("the prompt permits earlier work to be described as this change")
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
