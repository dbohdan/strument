package coder

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"dbohdan.com/strument/internal/llm"
)

// The reason tail is a parameter and not a pipe: `false | tail` exits 0. With
// the parameter the model gets the command's own failure and only the end of
// the output, and the user still sees every line.
func TestTailKeepsTheExitStatusAndShowsTheUserEverything(t *testing.T) {
	unixOnly(t)
	out := &captureOut{}
	c := &Coder{Out: out, Root: t.TempDir()}
	exit, got := c.runAndShowTail(context.Background(),
		"for i in 1 2 3 4 5 6 7 8 9 10; do echo line$i; done; exit 3", 0, 2)

	if exit != 3 {
		t.Errorf("exit = %d, want the command's own 3", exit)
	}
	if !strings.Contains(got, "line9\nline10\n") || strings.Contains(got, "line8") {
		t.Errorf("the model should get the last two lines only:\n%s", got)
	}
	if !strings.Contains(got, "last 2 of 10 lines") {
		t.Errorf("the model is not told lines were left out:\n%s", got)
	}
	if shown := strings.Join(out.lines, "\n"); !strings.Contains(shown, "line1\n") {
		t.Errorf("the user should see all the output:\n%s", shown)
	}
}

// A notice explains why the command stopped, so a tail never cuts it off.
func TestTailKeepsTheTimeoutNotice(t *testing.T) {
	unixOnly(t)
	c := &Coder{Out: &captureOut{}, Root: t.TempDir(), ShellTimeout: 150 * time.Millisecond}
	_, got := c.runAndShowTail(context.Background(), "echo a; echo b; echo c; sleep 30", 0, 1)
	if !strings.Contains(got, "shell_timeout") || !strings.Contains(got, "c\n") || strings.Contains(got, "a\n") {
		t.Errorf("want the last line and the timeout notice:\n%s", got)
	}
}

func TestLastLines(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"a\nb\nc\n", 0, "a\nb\nc\n"},
		{"a\nb\nc\n", 3, "a\nb\nc\n"},
		{"a\nb\nc\n", 5, "a\nb\nc\n"},
		{"a\nb\nc\n", 1, "(The last 1 of 3 lines. The user saw all of them.)\nc\n"},
		{"a\nb\nc", 2, "(The last 2 of 3 lines. The user saw all of them.)\nb\nc\n"},
	} {
		if got := lastLines(tc.in, tc.n); got != tc.want {
			t.Errorf("lastLines(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

// Long output keeps both ends: the first compiler error and the test
// summary are the two parts worth reading.
func TestCapMiddleKeepsBothEnds(t *testing.T) {
	var b strings.Builder
	for i := range 1000 {
		fmt.Fprintf(&b, "line %04d\n", i)
	}
	got := capMiddle(b.String(), 1000)
	if !strings.HasPrefix(got, "line 0000\n") || !strings.HasSuffix(got, "line 0999\n") {
		t.Errorf("both ends should survive:\n%s", got)
	}
	if !strings.Contains(got, "bytes of output omitted") || len(got) > 1100 {
		t.Errorf("the middle should be cut and said so (len %d):\n%s", len(got), got)
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(got, "\n"), "\n") {
		if !strings.HasPrefix(line, "line ") && !strings.HasPrefix(line, "…") {
			t.Errorf("a line was cut mid-way: %q", line)
		}
	}
	if short := "short\n"; capMiddle(short, 1000) != short {
		t.Error("output under the cap must pass through unchanged")
	}
}

func TestTailArgumentIsParsed(t *testing.T) {
	cmd, errMsg := parseCommandArgs(llm.ToolCall{ID: "1", Name: toolBash, Arguments: `{"command":"go test ./...","purpose":"test","tail":20}`})
	if errMsg != "" || cmd.tail != 20 {
		t.Errorf("tail = %d, err %q", cmd.tail, errMsg)
	}
	if _, errMsg := parseCommandArgs(llm.ToolCall{ID: "2", Name: toolBash, Arguments: `{"command":"ls","tail":-1}`}); errMsg == "" {
		t.Error("a negative tail should be refused")
	}
}
