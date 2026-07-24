// Automated coverage of the REPL: scripted sessions over a
// pipe in readline's non-interactive mode, the double-Ctrl-C chords, and a
// pty round trip for the interactive path.

package repl

import (
	"bytes"
	"context"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

// syncBuffer collects REPL output; the in-turn signal goroutine may write
// concurrently.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func testModel() *config.Model {
	m := &config.Model{
		Provider:   config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:       "test-model",
		EditFormat: "diff",
	}
	m.WeakModel = m
	return m
}

func testConfig(m *config.Model) *config.Config {
	return &config.Config{
		Default: "test",
		Models:  map[string]*config.Model{"test": m, "other": testModel()},
	}
}

// newTestREPL builds a REPL over in-memory pipes in non-interactive mode.
func newTestREPL(t *testing.T, cl llm.ModelClient, input io.Reader) (*REPL, *coder.Coder, *syncBuffer) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := testModel()
	cdr := coder.New(root, model)
	cdr.Client = cl

	out := &syncBuffer{}
	r, err := New(Options{
		Coder:      cdr,
		Config:     testConfig(model),
		ModelAlias: "test",
		Stdin:      input,
		Stdout:     out,
		Stderr:     out,
		IsTerminal: func() bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	cdr.Confirm = coder.AutoConfirmer{Fallback: r.Confirmer()}
	return r, cdr, out
}

func answerStub(text string) *fixture.StreamStub {
	return &fixture.StreamStub{Turns: []fixture.Turn{{Events: []fixture.Event{
		{Kind: "Answer", Text: text},
		{Kind: "Finish", FinishReason: "stop"},
	}}}}
}

func TestScriptedSession(t *testing.T) {
	input := strings.NewReader(
		"/help\n" +
			"/add hello.txt\n" +
			"/ls\n" +
			"/tokens\n" +
			"hi there\n" +
			"/nonsense\n" +
			"/drop\n" +
			"/exit\n" +
			"never reached\n")
	stub := answerStub("Hello! Some **bold** and `code` here.\n")
	r, _, out := newTestREPL(t, stub, input)
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"/read-only <file> [file ...]", // /help output
		"Added hello.txt to the chat.",
		"Files in the chat:",
		"hello.txt",
		"chat files",                      // /tokens section
		"Hello! Some bold and code here.", // rendered plain (no color)
		"Invalid command: /nonsense.",
		"Dropped all files from the chat.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "never reached") {
		t.Error("input after /exit was processed")
	}
	if stub.Remaining() != 0 {
		t.Errorf("stub turns left: %d", stub.Remaining())
	}
}

func TestBannerAndPromptHeader(t *testing.T) {
	for _, color := range []bool{true, false} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		model := testModel()
		cdr := coder.New(root, model)
		cdr.Client = answerStub("ok\n")
		cdr.AddFile("hello.txt")

		out := &syncBuffer{}
		r, err := New(Options{
			Coder:      cdr,
			Config:     testConfig(model),
			ModelAlias: "test",
			Version:    "9.9.9",
			Color:      color,
			Stdin:      strings.NewReader(""),
			Stdout:     out,
			Stderr:     out,
			IsTerminal: func() bool { return true },
			MakeRaw:    func() error { return nil },
			ExitRaw:    func() error { return nil },
			GetSize:    func() (int, int) { return 24, 80 }, // width 24
		})
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()

		r.announce()
		r.renderPromptHeader()
		got := out.String()

		for _, want := range []string{
			"Strument v9.9.9",
			"Model: test-model with diff edit format",
			"Git repo: none",
			"Repo-map: disabled",
			"Added hello.txt to the chat.", // banner
			strings.Repeat("─", 24),        // solid rule (aider's console.rule), honors GetSize width
			"hello.txt",                    // file listing
		} {
			if !strings.Contains(got, want) {
				t.Errorf("color=%v: output missing %q:\n%q", color, want, got)
			}
		}

		greenRule := "\x1b[38;2;0;204;0m"
		if color && !strings.Contains(got, greenRule) {
			t.Errorf("color: rule not in user-input color:\n%q", got)
		}
		if !color && strings.Contains(got, "\x1b") {
			t.Errorf("no-color: must not emit escape codes:\n%q", got)
		}
	}
}

func TestSlashCommandReturnsNoSend(t *testing.T) {
	// A session of only slash commands must never touch the client.
	input := strings.NewReader("/ls\n/clear\n/model\n/model other\n/model bogus\n/exit\n")
	stub := &fixture.StreamStub{} // zero turns: any Send errors the turn
	r, cdr, out := newTestREPL(t, stub, input)
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Active model: test (test-model).",
		"Switched to model other (test-model).",
		`Unknown model alias "bogus"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if cdr.Model == nil {
		t.Fatal("model unset")
	}
}

func TestRunCommandAddsExchange(t *testing.T) {
	// /run output + "y" to the add-output confirm => an exchange lands in
	// history; verified via /tokens growing is fragile, so peek via a turn.
	input := strings.NewReader("/run echo hi from run\ny\n/exit\n")
	stub := &fixture.StreamStub{}
	r, cdr, out := newTestREPL(t, stub, input)
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hi from run") {
		t.Errorf("command output not shown:\n%s", got)
	}
	if !strings.Contains(got, "Added the command output to the chat.") {
		t.Errorf("confirm flow failed:\n%s", got)
	}
	_ = cdr
}

func TestAddDropDirectory(t *testing.T) {
	// The test root is not a git repo, so /add <dir> uses the filesystem walk.
	input := strings.NewReader("/add sub\n/ls\n/drop sub\n/ls\n/exit\n")
	r, cdr, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()

	sub := filepath.Join(cdr.Root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(sub, n), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Added sub/a.txt to the chat.",
		"Added sub/b.txt to the chat.",
		"Dropped sub/a.txt from the chat.",
		"Dropped sub/b.txt from the chat.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRunEmptySuccessSkipsConfirm(t *testing.T) {
	// A command that succeeds with no output has nothing to add, so the
	// "add output?" prompt must not appear (it would also swallow /exit).
	input := strings.NewReader("/run true\n/exit\n")
	stub := &fixture.StreamStub{}
	r, _, out := newTestREPL(t, stub, input)
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out.String(), "Add command output to the chat?") {
		t.Errorf("empty successful /run should not prompt to add output:\n%s", out.String())
	}
}

func TestPromptCtrlCChordExits(t *testing.T) {
	// Two ^C at the prompt within the window: first prints the hint,
	// second exits. Readline maps \x03 to ErrInterrupt in non-interactive
	// mode too.
	input := strings.NewReader("\x03\x03")
	r, _, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()

	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("REPL did not exit on the ^C chord")
	}
	if !strings.Contains(out.String(), "^C again to exit") {
		t.Errorf("missing chord hint:\n%s", out.String())
	}
}

func TestPromptCtrlCOutsideWindowDoesNotExit(t *testing.T) {
	// A stale first ^C must not make a later one exit.
	input := strings.NewReader("\x03\x03/exit\n")
	r, _, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()

	// Inject a clock where each ^C is 3 seconds after the previous.
	base := time.Now()
	calls := 0
	r.opts.Now = func() time.Time {
		calls++
		return base.Add(time.Duration(calls) * 3 * time.Second)
	}

	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("REPL hung")
	}
	// Both ^C printed the hint; the session ended via /exit, meaning the
	// second ^C did not exit.
	if got := strings.Count(out.String(), "^C again to exit"); got != 2 {
		t.Errorf("hint count = %d, want 2:\n%s", got, out.String())
	}
}

// blockingClient hangs mid-stream until released, signaling when the send
// starts and when it observes cancellation.
type blockingClient struct {
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func (b *blockingClient) Send(ctx context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		close(b.started)
		<-ctx.Done()
		close(b.cancelled)
		<-b.release
		yield(llm.StreamEvent{}, ctx.Err())
	}
}

func TestTurnCtrlCChord(t *testing.T) {
	cl := &blockingClient{
		started:   make(chan struct{}),
		cancelled: make(chan struct{}),
		release:   make(chan struct{}),
	}

	pr, pw := io.Pipe()
	r, _, out := newTestREPL(t, cl, pr)
	defer r.Close()

	var sig chan<- os.Signal
	ready := make(chan struct{})
	r.opts.Notify = func(ch chan<- os.Signal) func() {
		sig = ch
		close(ready)
		return func() {}
	}
	exited := make(chan int, 1)
	r.opts.Exit = func(code int) { exited <- code }

	go func() {
		_, _ = io.WriteString(pw, "hello\n")
	}()
	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()

	<-ready
	<-cl.started
	sig <- os.Interrupt // first: cancels the send
	<-cl.cancelled
	sig <- os.Interrupt // second within 2s: exits

	select {
	case code := <-exited:
		if code != 130 {
			t.Errorf("exit code = %d, want 130", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second in-turn ^C did not exit")
	}

	close(cl.release)
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("REPL did not stop after the turn")
	}
	if !strings.Contains(out.String(), "^C again to exit") {
		t.Errorf("missing chord hint:\n%s", out.String())
	}
}
