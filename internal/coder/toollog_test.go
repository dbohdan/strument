package coder

import (
	"strings"
	"testing"
)

// TestToolLogRecordsAndPassesThrough pins both halves of the tee: the line
// still reaches the screen, and a copy is kept for the transcript.
func TestToolLogRecordsAndPassesThrough(t *testing.T) {
	screen := &captureOut{}
	c := &Coder{Out: screen}
	c.recordToolLines()

	c.Out.Toolf("Read %s (%d lines)", "poll/poll.go", 5)
	c.Out.Toolf("‹check› %s\n$ %s", "lint", "golangci-lint run")
	c.Out.Toolf("failed (exit status %d)", 1)

	got := c.TurnToolLines()
	want := []string{
		"Read poll/poll.go (5 lines)",
		// The two-line check render becomes one entry: a newline inside a
		// bullet would end the bullet.
		"‹check› lint $ golangci-lint run",
		"failed (exit status 1)",
	}
	if len(got) != len(want) {
		t.Fatalf("recorded %d lines, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if len(screen.lines) != 3 {
		t.Errorf("the tee swallowed output: screen has %q", screen.lines)
	}
}

// TestToolLogResetsPerTurn pins the lifetime TurnToolLines documents: the
// record belongs to the turn that just ended, not to the session.
func TestToolLogResetsPerTurn(t *testing.T) {
	c := &Coder{Out: &captureOut{}}
	c.recordToolLines()
	c.Out.Toolf("Read a.go (1 lines)")

	first := c.Out
	c.recordToolLines()
	if c.Out != first {
		t.Error("the tee was installed twice; each turn would add a layer")
	}
	if got := c.TurnToolLines(); len(got) != 0 {
		t.Errorf("the previous turn's lines survived: %q", got)
	}
}

// TestToolLogCapsALine bounds the one Toolf line whose length is not the
// harness's to choose: the check runner prints the user's argv.
func TestToolLogCapsALine(t *testing.T) {
	c := &Coder{Out: &captureOut{}}
	c.recordToolLines()
	c.Out.Toolf("‹check› long\n$ %s", strings.Repeat("x", 4000))

	got := c.TurnToolLines()
	if len(got) != 1 {
		t.Fatalf("recorded %d lines, want 1", len(got))
	}
	if len(got[0]) > maxToolLogLine+len("…") {
		t.Errorf("line is %d bytes, cap is %d", len(got[0]), maxToolLogLine)
	}
	if !strings.HasSuffix(got[0], "…") {
		t.Error("the truncation is not marked, so a clipped argv reads as the whole one")
	}
}

// TestTurnToolLinesBeforeAnyTurn pins that the accessor is safe on a Coder
// that has never run one — script mode reads it right after Run returns, and a
// failed send never reaches initBeforeMessage.
func TestTurnToolLinesBeforeAnyTurn(t *testing.T) {
	c := &Coder{Out: &captureOut{}}
	if got := c.TurnToolLines(); got != nil {
		t.Errorf("want nil, got %q", got)
	}
}
