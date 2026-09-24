package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"dbohdan.com/strument/internal/client"
	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/history"
	"dbohdan.com/strument/internal/jsonlog"
	"dbohdan.com/strument/internal/render"
	"dbohdan.com/strument/internal/repl"
)

// sessionCmd groups the commands for a project's conversations.
//
// A project holds several. Each is a conversation with its own record, its own
// pins and its own undo stack, under sessions/<name>/ in the project's state
// directory; `current` says which one a bare `strument` picks up. Inside a
// session the same operations are `/session`, which can also switch between
// them — something a command that exits cannot do.
type sessionCmd struct {
	List   sessionListCmd   `cmd:"" help:"List this project's sessions."`
	Rename sessionRenameCmd `cmd:"" help:"Rename a session, keeping its record."`
	Delete sessionDeleteCmd `cmd:"" help:"Delete a session and everything recorded in it."`
}

type sessionListCmd struct{}

func (*sessionListCmd) Run() error {
	root, err := historyRoot()
	if err != nil {
		return err
	}
	sessions, err := history.ListSessions(root)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		// Not an error and not empty output: a project nobody has chatted in
		// is the ordinary state of a fresh checkout, and the answer to "what
		// sessions are there" is a sentence rather than silence.
		fmt.Println("No sessions yet. One is created the first time you chat here.")
		return nil
	}
	for _, s := range sessions {
		fmt.Println(sessionLine(s))
	}
	return nil
}

// sessionLine is one row of the listing, shared with `/session` so the two
// cannot describe the same session differently.
func sessionLine(s history.Session) string {
	mark := "  "
	if s.Current {
		mark = "* "
	}
	turns := "no turns"
	if s.Turns > 0 {
		turns = render.Plural(s.Turns, "turn", "turns")
	}
	last := "never used"
	if !s.LastUsed.IsZero() {
		last = "last used " + humanAge(time.Since(s.LastUsed))
	}
	return fmt.Sprintf("%s%s\n      %s over %s, %s, %s", mark, s.Name, turns,
		render.Plural(s.Runs, "run", "runs"), last, humanBytes(s.Bytes))
}

type sessionRenameCmd struct {
	From string `arg:"" help:"The session to rename."`
	To   string `arg:"" help:"Its new name."`
}

func (c *sessionRenameCmd) Run() error {
	root, err := historyRoot()
	if err != nil {
		return err
	}
	if err := history.RenameSession(root, c.From, c.To); err != nil {
		return err
	}
	fmt.Printf("Renamed %s to %s.\n", c.From, c.To)
	return nil
}

type sessionDeleteCmd struct {
	Name string `arg:""                                         help:"The session to delete."`
	Yes  bool   `help:"Delete without asking for confirmation." short:"y"`
}

// Run deletes a session, after saying what that costs.
//
// The one deletion in Strument that is not a blob, and it asks first. Nothing
// here is ever dropped on the harness's own initiative — the retention rule is
// about what Strument decides, not about what a person may do with their own
// files — so this exists, and so does the prompt in front of it.
//
// Blobs stay. They are shared between sessions and named by their contents, so
// the only correct way to reclaim them is a sweep over what is still
// referenced.
func (c *sessionDeleteCmd) Run() error {
	root, err := historyRoot()
	if err != nil {
		return err
	}
	sessions, err := history.ListSessions(root)
	if err != nil {
		return err
	}
	var target *history.Session
	for i, s := range sessions {
		if s.Name == c.Name {
			target = &sessions[i]
		}
	}
	if target == nil {
		return fmt.Errorf("no session named %q; `strument session list` shows what there is", c.Name)
	}

	fmt.Println("This deletes the conversation, its pins and its undo record:")
	fmt.Println(sessionLine(*target))
	fmt.Println("\nStored tool payloads are shared between sessions and are left alone.")
	if !c.Yes && !confirmDeleteSession() {
		return nil
	}
	if err := history.DeleteSession(root, c.Name); err != nil {
		return err
	}
	fmt.Printf("Deleted %s.\n", c.Name)
	return nil
}

func confirmDeleteSession() bool {
	if !isCharDevice(os.Stdin) {
		fmt.Println("\nDeclined: there is no terminal to ask on. Pass --yes to delete without one.")
		return false
	}
	fmt.Print("\nDelete? (y/N) ")
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// sessionLog holds the record segment for whichever conversation is active.
//
// The coder is handed this once and never learns that the session can change.
// `/session` closes the segment in progress and opens one in the new session's
// directory, so a conversation's records always land in its own file — the
// alternative being a switch that writes the new session's turns into the old
// session's record, which is the one thing a per-session record exists to
// prevent.
//
// Locked because Record is called from the turn and Open from a command, and
// although the REPL runs those in sequence today, a recorder that assumed so
// would be a landmine for whoever changes that.
type sessionLog struct {
	projectRoot string

	mu sync.Mutex
	w  *jsonlog.Writer
}

func (l *sessionLog) Record(r coder.Record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		l.w.Record(r)
	}
}

// Open starts a segment in session, closing whichever one was in progress.
//
// A failure leaves the log closed rather than pointing at the old session: a
// record written into the wrong conversation is worse than one not written,
// because nothing downstream could tell it was misfiled.
func (l *sessionLog) Open(session string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		_ = l.w.Close()
		l.w = nil
	}
	seg, err := history.NewLogSegment(l.projectRoot, session, time.Now())
	if err != nil {
		return err
	}
	w, err := jsonlog.Create(seg)
	if err != nil {
		return err
	}
	l.w = w
	return nil
}

func (l *sessionLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w == nil {
		return nil
	}
	err := l.w.Close()
	l.w = nil
	return err
}

// sessionSwitcher is what `/session` needs from the host: everything about
// where a conversation lives, which internal/repl deliberately does not know.
//
// One struct of closures rather than a parameter per operation, in the shape
// repl.Options already uses for GenerateNotes and SaveResume. It is built once
// the coder, the project root and the record are all in hand.
type sessionSwitcher struct {
	cdr         *coder.Coder
	projectRoot string
	log         *sessionLog
	saveResume  func(alias string)
}

func (s *sessionSwitcher) list() ([]history.Session, error) {
	return history.ListSessions(s.projectRoot)
}

func (s *sessionSwitcher) current() string { return s.cdr.Session }

// exists reports whether a session has a directory on disk.
func (s *sessionSwitcher) exists(name string) bool {
	dir, err := history.SessionDir(s.projectRoot, name)
	if err != nil {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// switchTo moves the process to another conversation.
//
// create says which of the two mistakes to refuse: opening a session that is
// not there, and creating one over a session that is. Both are refused rather
// than guessed at, because the two commands mean different things and the cost
// of picking wrong is a conversation that is not the one the user meant.
//
// The order matters. What the current session should keep is written first,
// because everything after it is destructive; then the record moves, so no
// turn can land in the wrong file; then the coder is emptied and refilled.
func (s *sessionSwitcher) switchTo(name string, create bool, alias string) (string, error) {
	if err := history.ValidSessionName(name); err != nil {
		return "", err
	}
	if name == s.cdr.Session {
		return fmt.Sprintf("Already in %s.", name), nil
	}
	switch here := s.exists(name); {
	case create && here:
		return "", fmt.Errorf("a session named %q already exists; /session switch %s opens it", name, name)
	case !create && !here:
		return "", fmt.Errorf("no session named %q; /session new %s creates one", name, name)
	}

	s.saveResume(alias)

	if _, err := history.EnsureSessionDir(s.projectRoot, name); err != nil {
		return "", err
	}
	if s.log != nil {
		if err := s.log.Open(name); err != nil {
			// Refused rather than continued: carrying on would write this
			// conversation's turns into the previous session's record, and
			// nothing downstream could tell they were misfiled.
			return "", fmt.Errorf("could not open a record for %s, so the switch did not happen: %w", name, err)
		}
	}
	if err := history.SetCurrentSession(s.projectRoot, name); err != nil {
		noticef("switched to %s, but could not record it as the current session: %v", name, err)
	}
	s.cdr.Session = name
	s.cdr.RecordSession(alias)

	// Everything that belonged to the conversation being left. Notes go with
	// it: they describe the session they were written from, and carrying them
	// into another one is what /session fork does deliberately.
	s.cdr.ClearHistory()
	s.cdr.DropAll()
	s.cdr.SetUndoStack(nil)
	s.cdr.RestoreSessionCommits(nil, "")
	s.cdr.SessionNotes, s.cdr.SessionNotesDate, s.cdr.SessionNotesSession = "", "", ""

	return s.open(name), nil
}

// open brings a session's own state back: its pins, its undo record and its
// conversation. Shared by switching and by starting up, so the two cannot
// restore different things.
func (s *sessionSwitcher) open(name string) string {
	lines := []string{fmt.Sprintf("Now in %s.", name)}
	res := history.LoadResume(s.projectRoot, name)
	if pins, offered, _ := restoreSession(s.cdr, s.projectRoot, name, res); pins != "" {
		lines = append(lines, pins)
		_ = offered
	}
	// Switching is an interactive "open that conversation", so it restores
	// one. The --session flag does not, and the difference is deliberate: the
	// flag has to stay safe for the scripted runs in doc/experiments/, and
	// there is no such thing here. A user who wants the name without the
	// history has /clear.
	if conv := restoreConversation(s.cdr, s.projectRoot, name); conv != "" {
		lines = append(lines, conv)
	}
	return strings.Join(lines, "\n")
}

// fork starts a new conversation carrying this one's notes.
//
// It is the ritual it replaces: generate notes, clear the history, keep
// working. Doing it by hand meant /notes generate, /clear, and remembering
// that the notes now describe a session you are no longer in — which nothing
// recorded, so the header could not say so. Here the parent is known, and the
// notes arrive in the new session labelled with where they came from.
//
// /model does not do this. Many conversations on one strong model is the
// common case, so forking belongs to the session rather than to the model.
func (s *sessionSwitcher) fork(name, alias string) (string, error) {
	if err := history.ValidSessionName(name); err != nil {
		return "", err
	}
	if s.exists(name) {
		return "", fmt.Errorf("a session named %q already exists", name)
	}
	if s.cdr.Model.SideModel == nil {
		return "", errors.New("no side model is configured, so there are no notes to carry forward")
	}
	parent := s.cdr.Session
	transcript := sessionMarkdown(s.projectRoot, parent)
	if transcript == "" {
		return "", fmt.Errorf("%s has nothing recorded yet, so there is nothing to carry forward; "+
			"/session new %s starts an empty one", parent, name)
	}
	notes, err := writeSessionNotes(s.cdr, transcript)
	if err != nil {
		return "", err
	}
	if notes == "" {
		return "", errors.New("the model returned no notes, so nothing would be carried forward")
	}

	note, err := s.switchTo(name, true, alias)
	if err != nil {
		return "", err
	}
	s.cdr.SessionNotes = notes
	s.cdr.SessionNotesDate = time.Now().UTC().Format("2006-01-02 15:04")
	s.cdr.SessionNotesSession = parent
	return note + fmt.Sprintf("\nNotes from %s are in context.", parent), nil
}

// rename renames a session, following the process into it when it is the one
// being renamed.
func (s *sessionSwitcher) rename(from, to string) error {
	if err := history.RenameSession(s.projectRoot, from, to); err != nil {
		return err
	}
	if s.cdr.Session == from {
		s.cdr.Session = to
		// The record in progress is under the old directory, which the rename
		// moved with everything else, so the open file is still the right
		// one. Nothing to reopen.
	}
	return nil
}

// remove deletes a session that is not the one in use.
//
// Refusing to delete the current one is not timidity: the process holds an
// open record inside that directory, and the pins, undo stack and
// conversation in memory would have nowhere to be written at the end of the
// next turn.
func (s *sessionSwitcher) remove(name string) error {
	if name == s.cdr.Session {
		return fmt.Errorf("%s is the session you are in; switch to another one first", name)
	}
	return history.DeleteSession(s.projectRoot, name)
}

// sessionOps builds the /session surface, or nil when there is no state to
// hold another conversation in.
//
// A fork's notes come from writeSessionNotes, the function `/notes generate`
// uses, because a fork that summarized differently from `/notes generate` would
// be two answers to one question.
func sessionOps(cdr *coder.Coder, defaultAlias func() string, projectRoot string, slog *sessionLog, keepState bool) *repl.SessionOps {
	if !keepState {
		return nil
	}
	sw := &sessionSwitcher{
		cdr:         cdr,
		projectRoot: projectRoot,
		log:         slog,
		saveResume:  func(string) {},
	}
	if save := saveResumeFunc(cdr, defaultAlias, projectRoot, keepState); save != nil {
		sw.saveResume = save
	}
	return &repl.SessionOps{
		Current: sw.current,
		List:    sw.list,
		Switch:  sw.switchTo,
		Fork:    sw.fork,
		Rename:  sw.rename,
		Delete:  sw.remove,
	}
}

// errNoSideModel is what asking for notes gets when the active model has no
// side model to write them.
var errNoSideModel = errors.New("no side model configured")

// writeSessionNotes has the active model's side model write notes from a
// session transcript, accounting its usage like any side call.
//
// The side model is read here, at the call, and not when the session was set
// up. /model and /reload change it, and the fork path used to bind the startup
// model's side model into a closure, so a fork after a switch was written by —
// and billed to — a model the session had left.
func writeSessionNotes(cdr *coder.Coder, transcript string) (string, error) {
	side := cdr.Model.SideModel
	if side == nil {
		return "", errNoSideModel
	}
	write := coder.NotesWriter(client.ForProvider(side.Provider), side,
		cdr.RecordSideUsage, cdr.Out, cdr.Clock, cdr.RecordSideCall)
	notes, err := write(transcript)
	cdr.FlushSideUsage()
	if err != nil {
		return "", err
	}
	cdr.ReportSideUsageDone()
	return notes, nil
}
