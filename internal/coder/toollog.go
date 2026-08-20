package coder

import (
	"fmt"
	"strings"
	"sync"
)

// toolLog tees the harness's own tool lines — the one-liners Toolf writes to
// the screen — into a per-turn record.
//
// The transcript used to hold prose only: the user's message and the model's
// closing answer. In a harness where everything a turn does arrives as tool
// calls, that is the thin layer on top of the work rather than the work. A turn
// that read four files, ran the checks twice and fixed what failed was recorded
// as "Done." — and session notes regenerate from the transcript, so what they
// could say about a session was bounded by what the model happened to narrate.
//
// The lines are taken from Toolf rather than from the tool calls themselves
// because the summary already exists, is already bounded, and is already the
// version a human reviewed on screen. Rendering the calls again would mean a
// second format to keep true, and the arguments of an edit call are the whole
// new file text — the expensive half of what the summary exists to leave out.
//
// Lines the model did not cause are recorded too: the automatic checks, the
// commit. A check that failed and what was done about it is exactly the kind of
// thing a later session needs and no diff carries.
type toolLog struct {
	Output

	mu    sync.Mutex
	lines []string
}

// maxToolLogLine caps one recorded line. Toolf output is short by construction,
// with one exception: the check runner prints the argv on a second line, and an
// argv is as long as the user's config makes it.
const maxToolLogLine = 200

func (t *toolLog) Toolf(format string, args ...any) {
	line := strings.TrimSpace(fmt.Sprintf(format, args...))
	// A check renders as two lines ("‹check› lint" then "$ golangci-lint run");
	// the record wants one entry, and the newline would break the bullet list.
	line = strings.ReplaceAll(line, "\n", " ")
	if len(line) > maxToolLogLine {
		line = line[:maxToolLogLine] + "…"
	}
	if line != "" {
		t.mu.Lock()
		t.lines = append(t.lines, line)
		t.mu.Unlock()
	}
	t.Output.Toolf(format, args...)
}

func (t *toolLog) reset() {
	t.mu.Lock()
	t.lines = nil
	t.mu.Unlock()
}

func (t *toolLog) recorded() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.lines...)
}

// recordToolLines installs the tee, once, and clears the previous turn's
// record. Called from initBeforeMessage beside the other per-turn resets.
//
// Installed lazily rather than at construction because Out is set by the
// caller after the Coder is built, and there are two callers. Wrapping here
// means the Inspector — which takes c.Out when it is built, per call — picks
// the tee up with no wiring of its own.
func (c *Coder) recordToolLines() {
	if c.toolLog == nil {
		c.toolLog = &toolLog{Output: c.Out}
		c.Out = c.toolLog
	}
	c.toolLog.reset()
}

// TurnToolLines returns what the harness reported doing during the turn that
// just ended, in order. Same lifetime rule as TurnEditedFiles: valid until the
// next turn starts.
func (c *Coder) TurnToolLines() []string {
	if c.toolLog == nil {
		return nil
	}
	return c.toolLog.recorded()
}
