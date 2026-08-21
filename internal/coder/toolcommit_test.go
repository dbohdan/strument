// The commit tool: what it closes, what it declines, and what it says about it.

package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

func commitCall(id, subject, body string) llm.ToolCall {
	args := `{"subject":` + quoteForTest(subject)
	if body != "" {
		args += `,"body":` + quoteForTest(body)
	}
	return llm.ToolCall{ID: id, Name: toolCommit, Arguments: args + `}`}
}

func quoteForTest(s string) string {
	return `"` + strings.NewReplacer(`"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

// The model's own message reaches git, subject and body assembled the way git
// expects: summary, blank line, prose.
//
// This is half the reason the tool exists. The automatic path asks the *weak*
// model to infer intent from a diff and a truncated transcript; the model that
// made the change is the one that knows why.
func TestCommitToolUsesTheModelsMessage(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	repo := &countingRepo{}
	c.AutoCommits = true
	c.Repo = repo
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("a.go", snapEntry{}, "one")

	args, msg := parseCommitArgs(commitCall("call_1", "refactor(coder): rename verify to check",
		"The old name collided with the verb in the prompts."))
	if msg != "" {
		t.Fatalf("parse: %s", msg)
	}
	got := args.message()
	want := "refactor(coder): rename verify to check\n\nThe old name collided with the verb in the prompts."
	if got != want {
		t.Errorf("message =\n%q\nwant\n%q", got, want)
	}

	result := c.runCommitTool(args)
	if !strings.HasPrefix(result, "Committed ") {
		t.Errorf("result = %q, want it to name the commit", result)
	}

	// The half that matters: it reached git. Asserting on message() alone
	// would pass against a wiring that assembled the message perfectly and
	// then let the weak model overwrite it.
	if len(repo.msgs) != 1 {
		t.Fatalf("%d commits, want 1", len(repo.msgs))
	}
	if repo.msgs[0] != want {
		t.Errorf("git got %q, want the model's own message", repo.msgs[0])
	}
}

// Two commits in one turn are two commits, two undo steps, and disjoint file
// lists.
func TestCommitToolCommitsTwiceInOneTurn(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	repo := &countingRepo{}
	c.AutoCommits = true
	c.Repo = repo

	for _, f := range []string{"a.go", "b.go"} {
		c.turnSnap = newTurnSnapshot()
		c.turnSnap.record(f, snapEntry{}, "x")
		args, msg := parseCommitArgs(commitCall("call", "chore: touch "+f, ""))
		if msg != "" {
			t.Fatalf("parse: %s", msg)
		}
		c.runCommitTool(args)
	}

	if len(repo.calls) != 2 {
		t.Fatalf("%d commits, want 2", len(repo.calls))
	}
	if len(c.undoStack) != 2 {
		t.Errorf("undo stack = %d, want one entry per commit", len(c.undoStack))
	}
	if strings.Join(repo.calls[1], ",") != "b.go" {
		t.Errorf("second commit named %v, want only b.go", repo.calls[1])
	}
}

// Committing after the last write leaves the turn-end settle nothing to do.
//
// This is the "no commit needed at the end of the turn" signal, and it needs no
// mechanism of its own: settleEdits gates on turnSnap, which the commit emptied.
func TestCommitToolLeavesTheTurnEndNothingToDo(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	repo := &countingRepo{}
	c.AutoCommits = true
	c.Repo = repo
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("a.go", snapEntry{}, "one")
	// Both, as applyToolCalls sets them: turnEditedFiles accumulates for the
	// turn's history record and outlives the commit, which is precisely why
	// commitTurn must not read it.
	c.turnEditedFiles["a.go"] = true

	args, _ := parseCommitArgs(commitCall("call_1", "feat: add a thing", ""))
	c.runCommitTool(args)
	c.settleEdits("") // what the turn-end defer does

	if len(repo.calls) != 1 {
		t.Errorf("%d commits, want the turn end to add none", len(repo.calls))
	}
}

// Every way this cannot commit is answered, not refused — and each answer says
// which of them it was, because "nothing was committed" alone would leave the
// model guessing whether to retry.
func TestCommitToolExplainsWhyItDidNot(t *testing.T) {
	args, _ := parseCommitArgs(commitCall("call_1", "feat: whatever", ""))

	for _, tc := range []struct {
		name   string
		setup  func(*Coder)
		expect string
	}{
		{"no repo", func(c *Coder) { c.Repo = nil }, "no git repository"},
		{"auto-commits off", func(c *Coder) {
			c.Repo = &countingRepo{}
			c.AutoCommits = false
		}, "--no-auto-commits"},
		{"dry run", func(c *Coder) {
			c.Repo = &countingRepo{}
			c.AutoCommits = true
			c.DryRun = true
		}, "dry run"},
		{"nothing written", func(c *Coder) {
			c.Repo = &countingRepo{}
			c.AutoCommits = true
			c.turnSnap = nil
		}, "Nothing has been written"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := toolCoder(t, t.TempDir())
			c.turnSnap = newTurnSnapshot()
			c.turnSnap.record("a.go", snapEntry{}, "one")
			tc.setup(c)

			got := c.runCommitTool(args)
			if !strings.Contains(got, tc.expect) {
				t.Errorf("result = %q, want it to mention %q", got, tc.expect)
			}
		})
	}
}

// A malformed call is the one case worth a reflection, because it is the only
// one the model can fix.
func TestCommitToolRejectsMalformedCalls(t *testing.T) {
	for _, tc := range []struct {
		name   string
		call   llm.ToolCall
		expect string
	}{
		{"no subject", commitCall("c", "", ""), "needs a subject"},
		{"multi-line subject", commitCall("c", "feat: a\nand more", ""), "one line"},
		{"overlong subject", commitCall("c", strings.Repeat("x", maxCommitSubject+1), ""), "characters"},
		{"bad json", llm.ToolCall{ID: "c", Name: toolCommit, Arguments: "{"}, "parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, msg := parseCommitArgs(tc.call)
			if msg == "" {
				t.Fatal("accepted a call it should have refused")
			}
			if !strings.Contains(strings.ToLower(msg), strings.ToLower(tc.expect)) {
				t.Errorf("message = %q, want it to mention %q", msg, tc.expect)
			}
		})
	}
}
