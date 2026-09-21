package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"dbohdan.com/strument/internal/history"
	"dbohdan.com/strument/internal/render"
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
	Name string `arg:""                        help:"The session to delete."`
	Yes  bool   `help:"Delete without asking." short:"y"`
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
