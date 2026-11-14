package repl

import (
	"context"
	"os"
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

// useModel is the only place a model switch happens, for the reason the
// duplicated block was a hazard: /model and /reload carried byte-identical
// copies of five steps, and a sixth added to the obvious one would have left
// /reload half-switching a session silently.
func TestOnlyUseModelSwitchesTheModel(t *testing.T) {
	body, err := os.ReadFile("commands.go")
	if err != nil {
		t.Fatal(err)
	}
	fn, found := "", 0
	for i, line := range strings.Split(string(body), "\n") {
		if rest, ok := strings.CutPrefix(line, "func "); ok {
			fn = strings.TrimSpace(rest)
		}
		if !strings.Contains(line, "r.coder.SetModel(") {
			continue
		}
		found++
		if !strings.Contains(fn, "useModel(") {
			t.Errorf("%s calls SetModel at commands.go:%s; the switch is five steps and "+
				"useModel is where all five live", fn, itoaRepl(i+1))
		}
	}
	// The counter-arm: if SetModel is renamed the scan matches nothing and this
	// passes by checking nothing.
	if found == 0 {
		t.Fatal("found no SetModel call at all; this check is vacuous")
	}
}
