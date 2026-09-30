package repl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"dbohdan.com/strument/internal/editor"
	"dbohdan.com/strument/internal/shlex"
)

// /editor and Ctrl-X Ctrl-E: write a message in the user's own editor.
//
// Strument has no multi-line editor of its own, and building one means
// keeping a picture of a block of rows in step with a terminal that reflows it
// on resize and scrolls it off the top when it grows. An editor already does
// that. The text comes back to the prompt rather than being sent, with its line
// breaks drawn as ↵, so it is read before it goes: the same review every other
// message gets. The command name is aider's; its argument is not. aider's
// /editor takes the text to start from, which Ctrl-X Ctrl-E covers here, and
// this one takes the editor command to use instead of $VISUAL or $EDITOR.

// cmdEditor opens the editor on an empty message, or with the command given.
func cmdEditor(_ context.Context, r *REPL, args string) string {
	var argv []string
	if args = strings.TrimSpace(args); args != "" {
		argv = shlex.Split(args)
		if len(argv) == 0 {
			r.out.Errorf("Could not read %q as a command.", args)
			return ""
		}
	}
	r.editInEditor("", argv)
	return ""
}

// editInEditor writes initial to a file in a private directory, opens it in
// the editor, and puts what comes back into the next prompt.
//
// The directory, not just the file, is private (0700, from os.MkdirTemp): a
// message can hold anything, and a file created in a shared temporary
// directory can be found by name there even when its own mode is 0600. Both go
// when the editor exits. The file is Markdown so the editor highlights it.
//
// An empty result cancels. An unchanged one puts back what was typed, so
// Ctrl-X Ctrl-E and quitting without saving loses nothing.
func (r *REPL) editInEditor(initial string, command []string) {
	dir, err := os.MkdirTemp("", "strument-editor-")
	if err != nil {
		r.out.Errorf("Could not make a private directory for the editor: %v", err)
		r.prefill = initial
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "message.md")
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		r.out.Errorf("Could not write the file for the editor: %v", err)
		r.prefill = initial
		return
	}
	var argv []string
	if len(command) > 0 {
		argv = append(append([]string{}, command...), path)
	} else {
		argv = editor.ArgvFor(path, os.Getenv, exec.LookPath, runtime.GOOS)
	}
	run := r.opts.RunEditor
	if run == nil {
		run = editor.Run
	}
	if err := run(argv); err != nil {
		r.out.Errorf("%v", err)
		r.prefill = initial
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		r.out.Errorf("Could not read back what the editor saved: %v", err)
		r.prefill = initial
		return
	}
	text := strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	switch {
	case strings.TrimSpace(text) == "":
		r.printf("The editor left the message empty; nothing to send.")
	case text == initial:
		r.prefill = initial
	default:
		r.prefill = text
		r.printf("The message is back in the prompt. Press Enter to send it.")
	}
}
