package coder

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/llm"
)

// TestCommitContextIncludesToolCalls pins the half that used to be invisible.
// Message.Text() returns Content, and a model's calls live in ToolCalls, so a
// context built from text alone handed the commit-message model the tool
// results without the calls that produced them — every purpose string, path
// and query dropped.
func TestCommitContextIncludesToolCalls(t *testing.T) {
	c := &Coder{curMessages: []llm.Message{
		llm.TextMessage("user", "Make the poll interval configurable."),
		{
			Role:    "assistant",
			Content: llm.TextContent("Checking the current value first."),
			ToolCalls: []llm.ToolCall{
				{Name: "read", Arguments: `{"path": "poll/poll.go"}`},
				{Name: "bash", Arguments: `{"purpose": "confirm the tests still pass", "command": "go test ./..."}`},
			},
		},
		llm.ToolResult("1", "poll/poll.go (5 lines)\n1\tpackage poll\n"),
	}}

	got := c.commitContext()
	for _, want := range []string{
		"Make the poll interval configurable.",
		"CALL: read",
		"poll/poll.go",
		"CALL: bash",
		"confirm the tests still pass",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("commit context is missing %q:\n%s", want, got)
		}
	}
}

// TestCommitContextCapsToolArguments keeps an edit call from carrying the whole
// new text of a file into a request whose job is to write one subject line —
// especially since the diff is passed to the commit model separately.
func TestCommitContextCapsToolArguments(t *testing.T) {
	c := &Coder{curMessages: []llm.Message{{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{{
			Name:      "edit",
			Arguments: `{"path": "poll/poll.go", "new_text": "` + strings.Repeat("x", 50_000) + `"}`,
		}},
	}}}

	got := c.commitContext()
	if len(got) > maxCommitArgs+200 {
		t.Errorf("commit context is %d bytes; the cap is %d", len(got), maxCommitArgs)
	}
	// What the cap keeps has to be the identifying head, not an arbitrary slice.
	if !strings.Contains(got, "poll/poll.go") {
		t.Errorf("truncation dropped the path, which is the part worth keeping:\n%s", got)
	}
}

// TestCommitContextIncludesEarlierTurns pins the widening. The reason for a
// change is usually settled a turn or two before the change lands, so a model
// shown only this turn does not know it — and a model faithfully following
// "add a body only for something the diff cannot say" then correctly writes
// nothing. Live, the narrow context recorded the reason 2 times in 28; the wide
// one 12 in 27.
func TestCommitContextIncludesEarlierTurns(t *testing.T) {
	c := &Coder{
		doneMessages: []llm.Message{
			llm.TextMessage("user", "The load balancer idles connections out at 60 seconds."),
			llm.TextMessage("assistant", "Understood; the interval has to stay under that."),
		},
		curMessages: []llm.Message{
			llm.TextMessage("user", "Set defaultTimeout to 45."),
		},
	}

	got := c.commitContext()
	if !strings.Contains(got, "idles connections out at 60 seconds") {
		t.Errorf("the earlier turn is missing, so the reason is unavailable:\n%s", got)
	}
	if !strings.Contains(got, "Set defaultTimeout to 45") {
		t.Errorf("this turn is missing:\n%s", got)
	}
	// Order matters: the turn being committed reads last, closest to the diff.
	if strings.Index(got, "60 seconds") > strings.Index(got, "Set defaultTimeout") {
		t.Error("the earlier turn is rendered after this one")
	}
}

// TestCommitContextKeepsTheTailOfHistory bounds the widening, and pins which
// end survives: a reason is stated near the change, not at the start of a
// session, so the recent end is the half worth keeping.
func TestCommitContextKeepsTheTailOfHistory(t *testing.T) {
	var done []llm.Message
	done = append(done, llm.TextMessage("user", "ancient: "+strings.Repeat("x", maxCommitHistory)))
	done = append(done, llm.TextMessage("user", "recent: the load balancer idles at 60 seconds"))
	c := &Coder{doneMessages: done, curMessages: []llm.Message{llm.TextMessage("user", "do it")}}

	got := c.commitContext()
	if strings.Contains(got, "ancient:") {
		t.Error("the oldest turn survived; the budget is not bounding anything")
	}
	if !strings.Contains(got, "recent: the load balancer") {
		t.Errorf("the tail was dropped instead of the head:\n%s", got[:200])
	}
	if !strings.Contains(got, "Earlier conversation omitted") {
		t.Error("the elision is unmarked, so a clipped history reads as the whole one")
	}
}

// TestCommitTurnNoopAfterToolCommit pins the message that ends a turn whose
// writes netted to zero after the turn already committed. The pre-commit-tool
// wording — "the turn left the files as they were" — is false the moment the
// turn holds a commit, and it was the model's commits that made that ordinary:
// a part-one commit followed by part-two edits that net out is normal commit
// tool usage, not a failure.
//
// Real repository on purpose: the no-op is decided by git status, not by the
// snapshot, and a stub repo answers ok=true unconditionally.
func TestCommitTurnNoopAfterToolCommit(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "base")

	c := toolCoder(t, dir)
	out := &captureOut{}
	c.Out = out
	c.AutoCommits = true
	repo, err := gitrepo.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	c.Repo = repo

	// Part one: an edit and its commit, the way the commit tool produces one.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("a.go", snapEntry{}, "one\n")
	c.settleEdits("part one")
	if c.lastCommitHash == "" {
		t.Fatal("part one did not commit; the test's premise is broken")
	}

	// Part two nets to zero: rewritten to exactly the committed content.
	c.turnSnap = newTurnSnapshot()
	c.turnSnap.record("a.go", snapEntry{}, "one\n")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.settleEdits("")

	// The announced no-op must reference the commit that stands, not claim
	// the turn changed nothing.
	want := "Nothing to commit since " + c.lastCommitHash[:7] + "."
	found := false
	for _, line := range out.lines {
		if strings.Contains(line, want) {
			found = true
		}
		if strings.Contains(line, "left the files as they were") {
			t.Errorf("the pre-commit-tool wording fired although the turn holds commit %s:\n%s",
				c.lastCommitHash, line)
		}
	}
	if !found {
		t.Errorf("no \"Nothing to commit since %s\" announcement; got:\n%s",
			c.lastCommitHash[:7], strings.Join(out.lines, "\n"))
	}
}

// The commit-message input used to have no bound at all: gitrepo passes
// `git diff --cached` through verbatim and renderCommitMessages writes every
// message's full text, so the call grew with the turn until a provider refused
// it. These pin the bound and, more importantly, pin *what gives* when the
// input does not fit.

// Both paths have to respect the budget, and they are different code: a diff
// that overflows on its own is truncated, while a diff that fits leaves a
// remainder for the chat context to be trimmed into. An earlier version of this
// test passed a 50k diff for every case, which took the first path every time
// and left the second unexercised — it stayed green against a bound that let
// the context run past the budget by an order of magnitude.
func TestCommitInputFitsWithinTheSideModelsWindow(t *testing.T) {
	bound := 1000 // tokens
	budget := (bound - summaryInputBuffer) * commitCharsPerToken

	for _, tc := range []struct {
		name string
		diff string
	}{
		{"diff overflows on its own", strings.Repeat("d", 50_000)},
		{"diff fits, context must be trimmed", strings.Repeat("d", 600)},
		{"both tiny", "d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fitCommitInput(strings.Repeat("c", 50_000), tc.diff, bound, &summaryOutput{})
			if len(got) > budget {
				t.Errorf("input is %d chars, over the %d-char budget for a %d-token window",
					len(got), budget, bound)
			}
		})
	}
}

// The order is the whole point. The prompt tells the model to describe only
// what the diff does and treats earlier turns as background, so background is
// what goes first. A message written from a full diff and no context is
// worse-explained; one written from half a diff is wrong about what changed.
func TestCommitInputSacrificesContextBeforeTheDiff(t *testing.T) {
	diff := "diff --git a/x b/x\n" + strings.Repeat("+line\n", 200)
	chatContext := strings.Repeat("CHATTER\n", 5000)

	bound := 1000
	budget := (bound - summaryInputBuffer) * commitCharsPerToken
	got := fitCommitInput(chatContext, diff, bound, &summaryOutput{})

	if !strings.Contains(got, diff) {
		t.Error("the diff was cut while chat context was still present")
	}
	if strings.Count(got, "CHATTER") >= 5000 {
		t.Error("nothing was cut; the fixture is too small to exercise the bound")
	}
	// Keeping the diff whole is only half the rule; the result still has to fit.
	// Without this the test passes on an implementation that keeps everything.
	if len(got) > budget {
		t.Errorf("the diff survived but the result is %d chars, over the %d-char budget", len(got), budget)
	}
}

// A diff that exceeds the budget on its own has to give, and must say so — a
// model handed a silently truncated diff reads it as a change that ends there
// and describes a commit that does not exist.
func TestCommitInputAnnouncesATruncatedDiff(t *testing.T) {
	out := &summaryOutput{}
	got := fitCommitInput("", strings.Repeat("d", 100_000), 1000, out)

	if !strings.Contains(got, commitInputCutNote) {
		t.Error("the diff was cut without telling the model it was cut")
	}
	if !strings.Contains(strings.Join(out.lines, "\n"), "too large") {
		t.Errorf("the cut was not reported to the user:\n%s", strings.Join(out.lines, "\n"))
	}
}

// The common case must not be touched at all. A bound that trimmed ordinary
// commits would be a regression wearing a guard rail's clothes.
func TestCommitInputLeavesAnOrdinaryCommitAlone(t *testing.T) {
	// The median diff over this repository's last 200 commits is ~10k chars.
	diff := strings.Repeat("+a line of a patch\n", 500)
	chatContext := "USER: fix the thing\nASSISTANT: done\n"

	got := fitCommitInput(chatContext, diff, summaryFallbackInput, &summaryOutput{})

	if !strings.Contains(got, chatContext) || !strings.Contains(got, diff) {
		t.Error("an ordinary commit was trimmed; the fallback bound is too tight")
	}
	if strings.Contains(got, commitInputCutNote) {
		t.Error("an ordinary commit was marked as cut")
	}
}

// sideInputBound is the rule compaction already used; the commit message now
// reads the same one. A model that reports its window uses it, and only a model
// that does not falls back.
func TestSideInputBoundPrefersTheModelsOwnWindow(t *testing.T) {
	if got := sideInputBound(&config.Model{Context: 200_000}); got != 200_000 {
		t.Errorf("bound = %d, want the model's own 200000", got)
	}
	if got := sideInputBound(&config.Model{}); got != summaryFallbackInput {
		t.Errorf("bound = %d, want the %d fallback", got, summaryFallbackInput)
	}
	if got := sideInputBound(nil); got != summaryFallbackInput {
		t.Errorf("bound = %d for a nil model, want the %d fallback", got, summaryFallbackInput)
	}
}
