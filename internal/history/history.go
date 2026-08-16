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

// ProjectDir is a project's state directory:
// $XDG_STATE_HOME/strument/projects/<basename>-<hash8>/, keyed by the absolute
// root path (readable prefix, hash suffix against collisions — the trust
// store's keying style).
//
// A directory rather than a family of <key>.<ext> siblings, because the
// extension scheme only works while every artifact is one file, and the
// deferred undo spill is a subtree of copied source per session. One directory
// per project also makes "forget this project" an rm -rf and gives the mode
// below one place to be right.
func ProjectDir(projectRoot string) (string, error) {
	base, err := stateDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs))
	name := filepath.Base(abs) + "-" + hex.EncodeToString(sum[:])[:8]
	return filepath.Join(base, "projects", name), nil
}

// dirMode and fileMode keep a project's state to its owner.
//
// The transcript records whatever the model read out of the project, and the
// harness is meant to be usable on a live configuration directory — where
// reading an .env or an SSH config into the chat is the ordinary case, not the
// exotic one. The deferred undo spill would put verbatim copies of source here,
// whose modes internal/coder goes to some trouble to preserve; world-readable
// copies would undo that work one directory over.
const (
	dirMode  = 0o700
	fileMode = 0o600
)

// EnsureProjectDir creates the directory and records which project it belongs
// to, returning the path.
//
// The root file is the affordance a flat <key>.<ext> layout had nowhere to put:
// it holds the absolute path the hash was taken over, so a stale directory can
// be identified by reading it instead of recomputing SHA-256 over candidates.
func EnsureProjectDir(projectRoot string) (string, error) {
	dir, err := ProjectDir(projectRoot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	// Rewritten every session: cheap, and it self-heals if the directory is
	// copied between machines or the file is lost.
	if err := os.WriteFile(filepath.Join(dir, "root"), []byte(abs+"\n"), fileMode); err != nil {
		return "", err
	}
	return dir, nil
}

// DefaultPath is the chat-history file for a project root.
func DefaultPath(projectRoot string) (string, error) {
	dir, err := ProjectDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "transcript.md"), nil
}

// InputHistoryPath is the readline input-history file for a project root,
// beside that project's transcript.
//
// This was global, on the reasoning that every other REPL — bash, python, psql —
// keeps input history global, and that recalling a prompt across projects is
// exactly when you want it. Real use reversed it: prompts are about the project
// you are in, and one shared file fills with lines that mean nothing where you
// are now. Scoping costs the cross-project recall, which turned out to be the
// rarer want by a wide margin.
func InputHistoryPath(projectRoot string) (string, error) {
	dir, err := ProjectDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "input.txt"), nil
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
	// Files is what the turn changed, root-relative.
	//
	// It makes the transcript a record of the work and not only of the talk,
	// which helps a human immediately — the assistant's prose often says "done"
	// without naming what it touched — and matters more without git, where
	// there are no commits and this is the only durable account of what a
	// session did to the tree.
	Files []string
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
	if err := os.MkdirAll(filepath.Dir(w.path), dirMode); err != nil {
		return err
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
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
	if n := len(t.Files); n > 0 {
		meta += fmt.Sprintf(" · %d %s changed", n, map[bool]string{true: "file", false: "files"}[n == 1])
	}
	fmt.Fprintf(&b, "_%s_\n\n", meta)

	// Paths as a list rather than folded into the italic line above: a turn can
	// touch a dozen files, and the metadata line is scanned rather than read.
	if len(t.Files) > 0 {
		b.WriteString("### Changed\n\n")
		for _, f := range t.Files {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}

	b.WriteString("### Prompt\n\n")
	b.WriteString(strings.TrimRight(t.User, "\n"))
	b.WriteString("\n\n### Response\n\n")
	b.WriteString(strings.TrimRight(t.Assistant, "\n"))
	b.WriteString("\n\n---\n\n")
	return b.String()
}
