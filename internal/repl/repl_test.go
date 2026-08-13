// Automated coverage of the REPL: scripted sessions over a
// pipe in readline's non-interactive mode, the double-Ctrl-C chords, and a
// pty round trip for the interactive path.

package repl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/repomap"
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

func (s *syncBuffer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}

func testModel() *config.Model {
	m := &config.Model{
		Provider:   config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:       "test-model",
		EditFormat: "tool",
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

type testCommandRunner func(context.Context, string, string) (int, string, error)

func (r testCommandRunner) Run(ctx context.Context, command, cwd string) (int, string, error) {
	return r(ctx, command, cwd)
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
	if runtime.GOOS == "windows" {
		t.Skip("Windows requires a real console for ANSI mode")
	}
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
			"Model: openrouter/test-model",
			"Git repo: none",
			"Language parser: off",
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
		"Active model: test (openrouter/test-model).",
		"Switched to model other (openrouter/test-model).",
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
	// The command output is auto-confirmed, then the next turn verifies that
	// the exchange was added to the conversation.
	input := strings.NewReader("/run echo hi from run\ny\nshow me the command output\n/exit\n")
	stub := answerStub("ok\n")
	var reqText string
	stub.OnRequest = func(_ int, req llm.Request, _ *fixture.Request) error {
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Text())
		}
		reqText = b.String()
		return nil
	}
	r, cdr, out := newTestREPL(t, stub, input)
	defer r.Close()
	cdr.Runner = testCommandRunner(func(context.Context, string, string) (int, string, error) {
		return 0, "hi from run\n", nil
	})

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
	if !strings.Contains(reqText, "Command: echo hi from run") || !strings.Contains(reqText, "hi from run") {
		t.Errorf("command output was not added to the next request:\n%s", reqText)
	}
}

func TestReloadConfig(t *testing.T) {
	// A reload that adds a model makes it selectable without a restart.
	input := strings.NewReader("/reload\n/model newmodel\n/exit\n")
	r, _, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()
	r.opts.ReloadConfig = func() (*config.Config, error) {
		cfg := testConfig(testModel())
		cfg.Models["newmodel"] = testModel()
		return cfg, nil
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Config reloaded.") {
		t.Errorf("no reload confirmation:\n%s", got)
	}
	if !strings.Contains(got, "Switched to model newmodel") {
		t.Errorf("model added by reload should be selectable:\n%s", got)
	}
}

func TestReloadConfigErrorKeepsConfig(t *testing.T) {
	// A failed reload reports the error and leaves the session usable.
	input := strings.NewReader("/reload\n/model other\n/exit\n")
	r, _, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()
	r.opts.ReloadConfig = func() (*config.Config, error) {
		return nil, errors.New("boom in config.star")
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Config not reloaded") {
		t.Errorf("error path not reported:\n%s", got)
	}
	if !strings.Contains(got, "Switched to model other") {
		t.Errorf("session should stay usable on the old config after a failed reload:\n%s", got)
	}
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

// TestShellConfirmationShowsThePurpose renders what the user actually reads
// before answering: the model's claim about the command, above the command
// itself. An absent purpose is shown rather than passed over — the model was
// asked for one, and its silence is part of the decision.
func TestShellConfirmationShowsThePurpose(t *testing.T) {
	for _, tc := range []struct{ name, purpose, want string }{
		{"stated", "re-run the suite after the parser fix", "re-run the suite after the parser fix"},
		{"absent", "", "(no purpose given)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, out := newTestREPL(t, answerStub("ok"), strings.NewReader("y\n"))
			res := r.Confirmer().Confirm(coder.ConfirmRequest{
				Prompt:  "Run shell command?",
				Command: "go test ./...",
				Purpose: tc.purpose,
			})
			if !res.Yes {
				t.Fatal("y did not approve")
			}
			got := out.String()
			if !strings.Contains(got, tc.want) {
				t.Errorf("prompt did not show %q:\n%s", tc.want, got)
			}
			// "$ " is the shape runChecks prints an argv in, so the two shell
			// surfaces read alike.
			if !strings.Contains(got, "$ go test ./...") {
				t.Errorf("command not shown as a command:\n%s", got)
			}
		})
	}
}

// TestShellPromptDefaultsToYes pins the answer line, which nothing did before:
// Enter approves, and the shell gate offers no blanket "all this turn". A
// grouped prompt still does — a message naming five URLs asks five identical
// questions, which is the repetition "a" was for.
func TestShellPromptDefaultsToYes(t *testing.T) {
	shell := coder.ConfirmRequest{
		Prompt:           "Run shell command?",
		Command:          "go test ./...",
		Purpose:          "re-run the suite",
		RequiresYesShell: true,
	}
	grouped := coder.ConfirmRequest{
		Prompt:  "Add URL to the chat?",
		Subject: "https://example.com",
		Group:   "add-url",
	}
	if got := confirmSuffix(shell); got != " (Y/n) " {
		t.Errorf("shell suffix = %q, want the default-yes form with no blanket option", got)
	}
	if got := confirmSuffix(grouped); got != " (Y/n/a=all turn) " {
		t.Errorf("grouped suffix = %q", got)
	}

	// And the answers those hints promise.
	r, _, _ := newTestREPL(t, answerStub("ok"), strings.NewReader("\n"))
	if !r.Confirmer().Confirm(shell).Yes {
		t.Error("a bare Enter did not approve")
	}
	r2, _, _ := newTestREPL(t, answerStub("ok"), strings.NewReader("a\n"))
	if !r2.Confirmer().Confirm(grouped).AlwaysThisTurn {
		t.Error(`"a" did not mean all this turn`)
	}
}

// TestNonShellConfirmationShowsOnlyItsSubject: a URL has no purpose slot, so
// the absence marker must not leak onto prompts that never asked for one.
func TestNonShellConfirmationShowsOnlyItsSubject(t *testing.T) {
	r, _, out := newTestREPL(t, answerStub("ok"), strings.NewReader("y\n"))
	r.Confirmer().Confirm(coder.ConfirmRequest{
		Prompt:  "Add URL to the chat?",
		Subject: "https://example.com/page",
	})
	got := out.String()
	if !strings.Contains(got, "https://example.com/page") {
		t.Errorf("subject not shown:\n%s", got)
	}
	if strings.Contains(got, "no purpose given") || strings.Contains(got, "$ ") {
		t.Errorf("shell chrome leaked onto a non-shell prompt:\n%s", got)
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

// TestWebCommandScrapesAndStages: /web scrapes the URL once and the page reaches
// the model as context on the next real message.
func TestWebCommandScrapesAndStages(t *testing.T) {
	stub := answerStub("Summary.")
	var reqText string
	stub.OnRequest = func(_ int, req llm.Request, _ *fixture.Request) error {
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Text() + "\n")
		}
		reqText = b.String()
		return nil
	}

	input := strings.NewReader("/web https://example.com/page\nsummarize\n/exit\n")
	r, cdr, out := newTestREPL(t, stub, input)
	defer r.Close()
	var scraped []string
	cdr.Scrape = func(_ context.Context, url string) (string, error) {
		scraped = append(scraped, url)
		return "SCRAPED PAGE BODY", nil
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(scraped) != 1 || scraped[0] != "https://example.com/page" {
		t.Errorf("scraped = %v, want one call to the /web URL", scraped)
	}
	if !strings.Contains(reqText, "SCRAPED PAGE BODY") {
		t.Errorf("staged page did not reach the model request:\n%s", reqText)
	}
	if !strings.Contains(out.String(), "Added https://example.com/page to the chat.") {
		t.Errorf("missing /web confirmation:\n%s", out.String())
	}
}

// /symbol is the human's door to the tag layer, and it replaced /map: the
// ranked digest was a thing to read once, "where is this defined" is a thing to
// ask constantly. These pin the four answers it can give.
func TestSymbolCommand(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("ok\n"), strings.NewReader("/exit\n"))
	defer r.Close()
	if err := os.WriteFile(filepath.Join(cdr.Root, "lib.go"),
		[]byte("package lib\n\nfunc VerySpecificName() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No parser: the command says so rather than pretending there are no hits.
	cmdSymbol(context.Background(), r, "VerySpecificName")
	if got := out.String(); !strings.Contains(got, "language parser is not available") {
		t.Errorf("without a parser:\n%s", got)
	}

	cdr.RepoMap = repomap.New(cdr.Root)
	out.Reset()

	cmdSymbol(context.Background(), r, "VerySpecificName")
	if got := out.String(); !strings.Contains(got, "lib.go:3") || !strings.Contains(got, "is defined in") {
		t.Errorf("definition lookup:\n%s", got)
	}

	out.Reset()
	cmdSymbol(context.Background(), r, "NoSuchName")
	if got := out.String(); !strings.Contains(got, "No place where NoSuchName is defined") {
		t.Errorf("miss should say so plainly:\n%s", got)
	}

	// A bare /symbol is a usage error, not an empty search over the project.
	out.Reset()
	cmdSymbol(context.Background(), r, "")
	if got := out.String(); !strings.Contains(got, "Usage: /symbol") {
		t.Errorf("bare /symbol:\n%s", got)
	}
}
