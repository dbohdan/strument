// Package history writes a human-readable markdown transcript of a chat
// session. It is the chat-history half of Strument's on-disk state: one
// file per project root under $XDG_STATE_HOME/strument/history, so the
// tool never scatters dotfiles into the working tree (unlike aider's
// .aider.chat.history.md, which every user ends up gitignoring). Input
// (readline) history lives separately and globally; see
// InputHistoryPath.
//
// The transcript answers "what did I ask, what did I get, and what did it
// cost" — each turn carries a header with the model, token counts, and
// message cost. Growth is unbounded in v1 (the same lifecycle as the
// coder's doneMessages); rotation is a v2 concern.
package history

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stateDir returns $XDG_STATE_HOME/strument, defaulting to
// ~/.local/state/strument — the same root the trust store uses.
func stateDir() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "strument"), nil
}

// projectPath is one of a project's state files:
// $XDG_STATE_HOME/strument/history/<basename>-<hash8><ext>, keyed by the
// absolute root path (readable prefix, hash suffix against collisions — the
// trust store's keying style). One key for every kind of file, so a project's
// transcript and its input history sit adjacent and obviously paired.
func projectPath(projectRoot, ext string) (string, error) {
	base, err := stateDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs))
	name := filepath.Base(abs) + "-" + hex.EncodeToString(sum[:])[:8] + ext
	return filepath.Join(base, "history", name), nil
}

// DefaultPath is the chat-history file for a project root.
func DefaultPath(projectRoot string) (string, error) {
	return projectPath(projectRoot, ".md")
}

// InputHistoryPath is the readline input-history file for a project root,
// beside that project's transcript and keyed identically.
//
// This was global, on the reasoning that every other REPL — bash, python, psql —
// keeps input history global, and that recalling a prompt across projects is
// exactly when you want it. Real use reversed it: prompts are about the project
// you are in, and one shared file fills with lines that mean nothing where you
// are now. Scoping costs the cross-project recall, which turned out to be the
// rarer want by a wide margin.
//
// It lives under history/ rather than in an input-history/ directory of its own
// because $XDG_STATE_HOME/strument/input-history is already a regular file for
// anyone who ran an earlier version, and MkdirAll over a file fails. That file
// is left alone; nothing reads it now.
func InputHistoryPath(projectRoot string) (string, error) {
	return projectPath(projectRoot, ".input")
}

// Turn is one recorded exchange.
type Turn struct {
	Time           time.Time
	Model          string
	TokensSent     int
	TokensReceived int
	Cost           float64
	CostKnown      bool
	User           string
	Assistant      string
}

// Writer appends turns to a markdown file, creating it (and its parent
// directory, and a one-time title header) on first write.
type Writer struct {
	path string
}

// New returns a Writer for path.
func New(path string) *Writer { return &Writer{path: path} }

// Path returns the file the Writer appends to.
func (w *Writer) Path() string { return w.path }

// Append writes one turn. A turn with an empty assistant answer (a failed
// or interrupted send) is skipped, so the transcript records real
// exchanges only.
func (w *Writer) Append(t Turn) error {
	if strings.TrimSpace(t.Assistant) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Title header, once, when the file is new/empty.
	if info, err := f.Stat(); err == nil && info.Size() == 0 {
		if _, err := f.WriteString("# Strument chat history\n\n"); err != nil {
			return err
		}
	}

	if _, err := f.WriteString(t.render()); err != nil {
		return err
	}
	return nil
}

// render formats one turn as a markdown block.
func (t Turn) render() string {
	var b strings.Builder

	ts := t.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	model := t.Model
	if model == "" {
		model = "model"
	}
	fmt.Fprintf(&b, "## %s — %s\n\n", ts.Format("2006-01-02 15:04:05"), model)

	meta := fmt.Sprintf("%d tokens sent, %d received", t.TokensSent, t.TokensReceived)
	if t.CostKnown {
		meta += fmt.Sprintf(" · $%.4f", t.Cost)
	}
	fmt.Fprintf(&b, "_%s_\n\n", meta)

	b.WriteString("### Prompt\n\n")
	b.WriteString(strings.TrimRight(t.User, "\n"))
	b.WriteString("\n\n### Response\n\n")
	b.WriteString(strings.TrimRight(t.Assistant, "\n"))
	b.WriteString("\n\n---\n\n")
	return b.String()
}
