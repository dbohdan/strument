package repl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// saveRecorder stands in for history.SaveResume so a test can see whether a
// command's change would survive a restart.
type saveRecorder struct{ calls int }

func (s *saveRecorder) save(string) { s.calls++ }

func replWithSaves(t *testing.T) (*REPL, *saveRecorder, string) {
	t.Helper()
	r, cdr, _ := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	rec := &saveRecorder{}
	r.opts.SaveResume = rec.save
	return r, rec, cdr.Root
}

// The reported bug. `/drop` with no arguments unpinned everything in memory and
// returned before its own saveResume call, so every restart restored the pin —
// and nothing else saves: a turn does not, so running one after the drop did not
// help either. Three sessions in dbohdan's log dropped the same file and the
// fourth still restored it.
func TestBareDropPersists(t *testing.T) {
	r, rec, root := replWithSaves(t)
	if err := os.WriteFile(filepath.Join(root, "pinned.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.dispatch(context.Background(), "/add pinned.txt")
	afterAdd := rec.calls
	if afterAdd == 0 {
		t.Fatal("/add did not save; the resume file would never be written at all")
	}

	r.dispatch(context.Background(), "/drop")
	if len(r.coder.ChatFiles()) != 0 {
		t.Fatal("/drop did not unpin in memory")
	}
	if rec.calls == afterAdd {
		t.Error("bare /drop changed the pins and saved nothing, so a restart restores them")
	}
}

// The counter-arm, and the regression that slipped past build and `go test`
// while an empty `if` body sat in dispatch: a command that *does* change pins
// must save. Without this, "nothing was written" and "nothing needed writing"
// are the same green.
func TestPinningPersists(t *testing.T) {
	for _, cmd := range []string{"/add pinned.txt", "/read-only pinned.txt"} {
		t.Run(cmd, func(t *testing.T) {
			r, rec, root := replWithSaves(t)
			if err := os.WriteFile(filepath.Join(root, "pinned.txt"), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			r.dispatch(context.Background(), cmd)
			if rec.calls == 0 {
				t.Errorf("%s pinned a file and saved nothing", cmd)
			}
		})
	}
}

// And the other direction: a command that changes nothing must not write, or the
// file and its timestamp churn on every keystroke. This is what a per-command
// flag or an unconditional save would cost.
func TestCommandsThatChangeNothingDoNotSave(t *testing.T) {
	for _, cmd := range []string{"/help", "/ls", "/add", "/drop nosuchfile", "/tokens"} {
		t.Run(cmd, func(t *testing.T) {
			r, rec, _ := replWithSaves(t)
			r.dispatch(context.Background(), cmd)
			if rec.calls != 0 {
				t.Errorf("%s changed no resume state but saved %d times", cmd, rec.calls)
			}
		})
	}
}

// The shape guard. dispatch owns the save, so a command calling it directly is
// either redundant or — the original bug — a sign that some exit path does not.
// A list of pin-changing commands would have to be maintained; this does not.
func TestOnlyDispatchSavesResume(t *testing.T) {
	body, err := os.ReadFile("commands.go")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	fn := ""
	for i, line := range strings.Split(string(body), "\n") {
		if rest, ok := strings.CutPrefix(line, "func "); ok {
			fn = strings.TrimSpace(rest)
		}
		if !strings.Contains(line, "r.saveResume()") {
			continue
		}
		if strings.Contains(fn, "dispatch(") {
			continue
		}
		offenders = append(offenders, fn+" at commands.go:"+itoaRepl(i+1))
	}
	for _, o := range offenders {
		t.Errorf("%s calls r.saveResume() directly; dispatch saves when resumeState changes, "+
			"so a command that needs its own call has an exit path dispatch cannot see", o)
	}
}

func itoaRepl(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
