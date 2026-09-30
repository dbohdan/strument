package repl

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// /editor opens a file in a private directory, and what the editor saves
// comes back to the prompt — line breaks kept, the trailing newline an editor
// adds dropped — rather than being sent. Both the file and its directory are
// gone afterwards.
func TestEditorPutsTheMessageBackInThePrompt(t *testing.T) {
	r, _, out := newTestREPL(t, answerStub("ok"), strings.NewReader(""))
	var seen string
	r.opts.RunEditor = func(argv []string) error {
		path := argv[len(argv)-1]
		seen = path
		if runtime.GOOS != "windows" {
			if fi, err := os.Stat(filepath.Dir(path)); err != nil || fi.Mode().Perm() != 0o700 {
				t.Errorf("directory mode = %v, err %v; want 0700", fi.Mode().Perm(), err)
			}
			if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("file mode = %v, err %v; want 0600", fi.Mode().Perm(), err)
			}
		}
		if filepath.Ext(path) != ".md" {
			t.Errorf("file %q is not Markdown", path)
		}
		return os.WriteFile(path, []byte("line one\r\nline two\n\n"), 0o600)
	}
	cmdEditor(context.Background(), r, "")
	if r.prefill != "line one\nline two" {
		t.Errorf("prefill = %q, want the two lines without the editor's trailing newlines", r.prefill)
	}
	if _, err := os.Stat(filepath.Dir(seen)); !os.IsNotExist(err) {
		t.Errorf("the editor's directory is still there: %v", err)
	}
	if !strings.Contains(out.String(), "back in the prompt") {
		t.Errorf("no word that the message is waiting in the prompt:\n%s", out.String())
	}
}

// The argument is the editor command, used in place of $VISUAL and $EDITOR,
// split on shell rules, with the file last.
func TestEditorArgumentIsTheCommand(t *testing.T) {
	r, _, _ := newTestREPL(t, answerStub("ok"), strings.NewReader(""))
	var got []string
	r.opts.RunEditor = func(argv []string) error {
		got = argv
		return os.WriteFile(argv[len(argv)-1], []byte("hi"), 0o600)
	}
	cmdEditor(context.Background(), r, `code --wait "--profile=My Notes"`)
	if len(got) != 4 || !slices.Equal(got[:3], []string{"code", "--wait", "--profile=My Notes"}) {
		t.Errorf("argv = %q, want the command split on shell rules, then the file", got)
	}
}

// Ctrl-X Ctrl-E starts from what was typed. Quitting without a change puts it
// back; an empty file cancels and sends nothing.
func TestEditorKeepsOrDropsWhatWasTyped(t *testing.T) {
	for _, tc := range []struct {
		name, saved, want string
	}{
		{"unchanged", "draft so far", "draft so far"},
		{"emptied", "", ""},
		{"edited", "draft, finished\nwith a second line\n", "draft, finished\nwith a second line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _ := newTestREPL(t, answerStub("ok"), strings.NewReader(""))
			r.opts.RunEditor = func(argv []string) error {
				path := argv[len(argv)-1]
				b, _ := os.ReadFile(path)
				if string(b) != "draft so far" {
					t.Errorf("the editor opened on %q, want what was typed", b)
				}
				return os.WriteFile(path, []byte(tc.saved), 0o600)
			}
			r.editInEditor("draft so far", nil)
			if r.prefill != tc.want {
				t.Errorf("prefill = %q, want %q", r.prefill, tc.want)
			}
		})
	}
}
