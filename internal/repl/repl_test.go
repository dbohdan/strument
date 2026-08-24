// Automated coverage of the REPL: scripted sessions over a
// pipe in readline's non-interactive mode, the double-Ctrl-C chords, and a
// pty round trip for the interactive path.

package repl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	m.SideModel = m
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
		"Pinned hello.txt for editing.",
		"Pinned for editing:",
		"hello.txt",
		"read-only files", // /tokens section (chat files no
		//                                    longer have one: pinned files are
		//                                    named in the system prompt and
		//                                    their contents arrive as tool
		//                                    results, landing in the history)
		"Hello! Some bold and code here.", // rendered plain (no color)
		"Invalid command: /nonsense.",
		"Unpinned everything.",
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

// /context is a pure view: it sends nothing and mutates no state. The bare form
// renders everything; an [n] caps the number of summaries; a non-numeric [n] is
// a usage error.
func TestContextCommandSendsNothingAndMutatesNothing(t *testing.T) {
	input := strings.NewReader("/context\n/context 2\n/context bogus\n/exit\n")
	stub := &fixture.StreamStub{} // whatever Send reaches is an error
	r, _, out := newTestREPL(t, stub, input)
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if n := strings.Count(got, "Context as the model sees it:"); n != 2 {
		t.Errorf("bare and [n] should each render, got %d render(s):\n%s", n, got)
	}
	if !strings.Contains(got, "Usage: /context [n]") {
		t.Errorf("bad [n] should give usage:\n%s", got)
	}
	if stub.Remaining() != 0 {
		t.Errorf("/context must never reach the client: %d turn(s) consumed", -stub.Remaining())
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
			"Pinned hello.txt for editing.", // banner
			strings.Repeat("─", 24),         // solid rule (aider's console.rule), honors GetSize width
			"hello.txt",                     // file listing
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
	r, cdr, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()
	r.opts.ReloadConfig = func() (*config.Config, error) {
		cfg := testConfig(testModel())
		cfg.MaxSteps = 50
		cfg.MaxErrorReflections = 7
		cfg.Models["newmodel"] = testModel()
		return cfg, nil
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cdr.MaxSteps != 50 {
		t.Errorf("MaxSteps = %d, want 50 after reload", cdr.MaxSteps)
	}
	if cdr.MaxErrorReflections != 7 {
		t.Errorf("MaxErrorReflections = %d, want 7 after reload", cdr.MaxErrorReflections)
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
		"Pinned sub/a.txt for editing.",
		"Pinned sub/b.txt for editing.",
		"Unpinned sub/a.txt.",
		"Unpinned sub/b.txt.",
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

func TestCheckCommandAddsExchange(t *testing.T) {
	// The check output is auto-confirmed, then the next turn verifies that
	// the exchange was added to the conversation.
	input := strings.NewReader("/check echo-check\ny\nshow me the check output\n/exit\n")
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
	cdr.Check = []config.Check{
		{Name: "echo-check", Argv: []string{"echo", "hi from check"}},
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hi from check") {
		t.Errorf("check output not shown:\n%s", got)
	}
	if !strings.Contains(got, "Added the check output to the chat.") {
		t.Errorf("confirm flow failed:\n%s", got)
	}
	if !strings.Contains(reqText, "echo-check") || !strings.Contains(reqText, "hi from check") {
		t.Errorf("check output was not added to the next request:\n%s", reqText)
	}
}

func TestCheckAllRunsInOrder(t *testing.T) {
	// With no argument, /check runs all configured checks in order.
	input := strings.NewReader("/check\ny\n/exit\n")
	r, cdr, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()
	cdr.Check = []config.Check{
		{Name: "first", Argv: []string{"echo", "one"}},
		{Name: "second", Argv: []string{"echo", "two"}},
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "one") || !strings.Contains(got, "two") {
		t.Errorf("both checks should run:\n%s", got)
	}
}

func TestCheckStopsAtFirstFailure(t *testing.T) {
	// A failing check stops the run; later checks are not attempted.
	input := strings.NewReader("/check\n/exit\n")
	r, cdr, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()
	cdr.Check = []config.Check{
		{Name: "fail", Argv: []string{"false"}},
		{Name: "never", Argv: []string{"echo", "should not run"}},
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "should not run") {
		t.Errorf("later check should not run after a failure:\n%s", got)
	}
	if !strings.Contains(got, "Stopped here") {
		t.Errorf("should say it stopped:\n%s", got)
	}
}

func TestCheckInvalidName(t *testing.T) {
	input := strings.NewReader("/check nope\n/exit\n")
	r, cdr, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()
	cdr.Check = []config.Check{
		{Name: "real", Argv: []string{"true"}},
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `There is no check named "nope"`) {
		t.Errorf("should report unknown check name:\n%s", got)
	}
	if !strings.Contains(got, "real") {
		t.Errorf("should list configured checks:\n%s", got)
	}
}

func TestCheckNoneConfigured(t *testing.T) {
	input := strings.NewReader("/check\n/exit\n")
	r, _, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "No checks are configured") {
		t.Errorf("should report no checks:\n%s", out.String())
	}
}

func TestCheckEmptySuccessSkipsConfirm(t *testing.T) {
	// A check that succeeds with no output should not prompt.
	input := strings.NewReader("/check silent\n/exit\n")
	r, cdr, out := newTestREPL(t, &fixture.StreamStub{}, input)
	defer r.Close()
	cdr.Check = []config.Check{
		{Name: "silent", Argv: []string{"true"}},
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out.String(), "Add check output to the chat?") {
		t.Errorf("empty successful /check should not prompt:\n%s", out.String())
	}
}

// TestShellConfirmationShowsThePurpose renders what the user actually reads
// before answering: the model's claim about the command, above the command
// itself. An absent purpose is shown rather than passed over — the model was
// asked for one, and its silence is part of the decision.
func TestShellConfirmationShowsThePurpose(t *testing.T) {
	for _, tc := range []struct{ name, purpose, want string }{
		{"stated", "re-run the suite after the parser fix", "‹shell› re-run the suite after the parser fix"},
		{"absent", "", "‹shell› (no purpose given)"},
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

// TestAskerRendersTheQuestion drives rlAsker directly, the way the
// confirmation tests drive the confirmer: the question, the numbered options
// with their descriptions, and the implicit "Other" row print as ordinary
// scroll, then one line is read — an index selects the label, and free text
// is the whole answer.
func TestAskerRendersTheQuestion(t *testing.T) {
	req := coder.AskRequest{
		Question: "Which timestamp format should the new log lines use?",
		Options: []coder.AskOption{
			{Label: "RFC 3339", Description: "matches every other logger in this repo"},
			{Label: "Unix epoch", Description: "compact, but needs conversion to read"},
		},
	}

	r, _, out := newTestREPL(t, answerStub("ok"), strings.NewReader("1\n"))
	got1 := r.Asker().Ask(req)
	if strings.Join(got1, ",") != "RFC 3339" {
		t.Errorf("answer = %v, want the selected label", got1)
	}
	got := out.String()
	for _, want := range []string{
		"‹question› Which timestamp format should the new log lines use?",
		"1. RFC 3339 — matches every other logger in this repo",
		"2. Unix epoch — compact, but needs conversion to read",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if s := askHint(2, false); s != "Answer (1-2, or your own text): " {
		t.Errorf("single-select hint = %q", s)
	}

	// Free text is the whole answer, not a correction the harness interprets.
	out.Reset()
	r2, _, _ := newTestREPL(t, answerStub("ok"), strings.NewReader("rfc3339, but with milliseconds\n"))
	got2 := r2.Asker().Ask(req)
	if strings.Join(got2, "|") != "rfc3339, but with milliseconds" {
		t.Errorf("free-text answer = %v, want the raw line", got2)
	}
}

// TestAskerMultiSelectPrompt pins the comma-separated hint, which is the only
// user-visible difference a multiSelect question carries.
func TestAskerMultiSelectPrompt(t *testing.T) {
	r, _, _ := newTestREPL(t, answerStub("ok"), strings.NewReader("1,2\n"))
	got := r.Asker().Ask(coder.AskRequest{
		Question:    "Which sections?",
		Options:     []coder.AskOption{{Label: "Introduction"}, {Label: "Conclusion"}},
		MultiSelect: true,
	})
	if strings.Join(got, ",") != "Introduction,Conclusion" {
		t.Errorf("answer = %v, want both labels", got)
	}
	if s := askHint(2, true); s != "Answer (numbers separated by commas, or your own text): " {
		t.Errorf("multiSelect hint = %q", s)
	}
}

// TestAskerEmptyLineRepromptsOnce: an empty answer re-prompts one more time,
// and a second empty line falls through as an empty free-text answer rather
// than looping forever.
func TestAskerEmptyLineRepromptsOnce(t *testing.T) {
	r, _, _ := newTestREPL(t, answerStub("ok"), strings.NewReader("\n\n"))
	got := r.Asker().Ask(coder.AskRequest{
		Question: "Which?",
		Options:  []coder.AskOption{{Label: "a"}, {Label: "b"}},
	})
	if len(got) != 0 {
		t.Errorf("answer = %v, want the empty fall-through", got)
	}
	// The prompt itself is written by readline and invisible to the captured
	// output (see askHint); the two lines of input consumed are the count.
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

// /submit reads a file and sends its contents as the message for the turn:
// the file text reaches the model request exactly once, and nothing is pinned.
func TestSubmitCommandSendsFileContents(t *testing.T) {
	stub := answerStub("ok\n")
	var reqText string
	stub.OnRequest = func(_ int, req llm.Request, _ *fixture.Request) error {
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Text() + "\n")
		}
		reqText = b.String()
		return nil
	}

	input := strings.NewReader(`/submit "hello.txt"` + "\n/exit\n")
	r, cdr, out := newTestREPL(t, stub, input)
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(reqText, "hello") {
		t.Errorf("file contents did not reach the model request:\n%s", reqText)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("/submit did not print the file contents:\n%s", out.String())
	}
	if len(cdr.ChatFiles()) != 0 {
		t.Errorf("files pinned after /submit: %v", cdr.ChatFiles())
	}
}

// /submit takes one path from the project root, but outside paths are allowed
// like /read-only's — a drafted prompt often lives outside the project.
func TestSubmitCommandOutsidePath(t *testing.T) {
	stub := answerStub("ok\n")
	var reqText string
	stub.OnRequest = func(_ int, req llm.Request, _ *fixture.Request) error {
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Text() + "\n")
		}
		reqText = b.String()
		return nil
	}

	outside := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(outside, []byte("OUTSIDE PROMPT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader(fmt.Sprintf("/submit %q\n/exit\n", outside))
	r, _, _ := newTestREPL(t, stub, input)
	defer r.Close()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(reqText, "OUTSIDE PROMPT") {
		t.Errorf("outside path did not reach the model request:\n%s", reqText)
	}
}

// Everything that should not produce a message refuses with an error instead:
// oversize files are not truncated, and directories, non-UTF-8, and empty
// files are not sent.
func TestSubmitCommandRefusals(t *testing.T) {
	r, _, out := newTestREPL(t, answerStub("ok\n"), strings.NewReader("/exit\n"))
	defer r.Close()

	big := make([]byte, submitLimit+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(r.coder.Root, "big.txt"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.coder.Root, "bin.dat"), []byte{0xff, 0xfe, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.coder.Root, "empty.txt"), []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ args, want string }{
		{"", "Usage: /submit"},
		{"a.txt b.txt", "Usage: /submit"},
		{"big.txt", "over the 100.0 KiB /submit limit"},
		{".", "is a directory"},
		{"bin.dat", "not valid UTF-8"},
		{"empty.txt", "is empty"},
		{"no-such.txt", "Could not read no-such.txt"},
	} {
		out.Reset()
		if msg := cmdSubmit(context.Background(), r, tc.args); msg != "" {
			t.Errorf("/submit %q returned %q, want \"\"", tc.args, msg)
		}
		if got := out.String(); !strings.Contains(got, tc.want) {
			t.Errorf("/submit %q output missing %q:\n%s", tc.args, tc.want, got)
		}
	}

	// The happy path prints exactly the trimmed contents: the echo must be
	// what gets sent, with the framing whitespace gone.
	trimmed := "  \ntrims fine\n\t"
	if err := os.WriteFile(filepath.Join(r.coder.Root, "trimmed.txt"), []byte(trimmed), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if msg := cmdSubmit(context.Background(), r, "trimmed.txt"); msg != "trims fine" {
		t.Errorf("/submit trimmed.txt returned %q, want %q", msg, "trims fine")
	}
	if got := out.String(); got != "trims fine\n" {
		t.Errorf("/submit trimmed.txt printed %q, want the trimmed contents", got)
	}
}

// TestConfirmDeclinesWithoutATerminal is the regression for a turn that
// vanished. Piped stdin is one stream, so a confirm that reads consumes the
// user's next message as its y/n answer — and anything that is not "y" is a
// no, so the command is declined *and* the turn is lost, with no output, no
// error and exit 0.
func TestConfirmDeclinesWithoutATerminal(t *testing.T) {
	// The scripted input is what a user would type next, not an answer.
	r, _, out := newTestREPL(t, answerStub("ok"), strings.NewReader("Change defaultTimeout from 30 to 45.\n"))
	r.opts.StdinIsTerminal = func() bool { return false }

	res := r.Confirmer().Confirm(coder.ConfirmRequest{
		Command:          "go build ./poll/",
		Purpose:          "Verify the poll package compiles.",
		Prompt:           "Run it?",
		RequiresYesShell: true,
	})
	if res.Yes || res.AlwaysThisTurn {
		t.Fatal("a prompt with nobody at the keyboard was answered yes")
	}

	got := out.String()
	// The request is still shown: what was proposed is worth reading even when
	// the answer is a foregone no.
	if !strings.Contains(got, "go build ./poll/") {
		t.Errorf("the command was not shown:\n%s", got)
	}
	// And the decline says which flag would have answered it, naming the shell
	// one — --yes deliberately does not cover a shell command.
	if !strings.Contains(got, "--yes-shell") {
		t.Errorf("the decline does not name the flag that answers it:\n%s", got)
	}

	// The load-bearing assertion: the scripted line was not eaten, so the REPL
	// still has it to run as a turn.
	line, err := r.rl.ReadLine()
	if err != nil {
		t.Fatalf("the confirm consumed the user's next message: %v", err)
	}
	if !strings.Contains(line, "defaultTimeout") {
		t.Errorf("next line = %q, want the user's message", line)
	}
}

// TestConfirmStillAsksWhenOnlyOutputIsRedirected pins that the guard reads
// stdin and not both ends. `strument | tee log` has a human at the keyboard,
// and folding rendering-terminal-ness into this question would refuse to ask.
func TestConfirmStillAsksWhenOnlyOutputIsRedirected(t *testing.T) {
	r, _, _ := newTestREPL(t, answerStub("ok"), strings.NewReader("y\n"))
	r.opts.IsTerminal = func() bool { return false } // output is a file
	r.opts.StdinIsTerminal = func() bool { return true }

	if !r.Confirmer().Confirm(coder.ConfirmRequest{Subject: "Do it?", Prompt: "Ok?"}).Yes {
		t.Error("a redirected stdout stopped the harness asking a human who was there")
	}
}

// TestAskReturnsUnansweredWithoutATerminal is the same guard on the question
// path, which has no flag to answer it and so has only one honest outcome.
func TestAskReturnsUnansweredWithoutATerminal(t *testing.T) {
	r, _, out := newTestREPL(t, answerStub("ok"), strings.NewReader("Add a Ping function.\n"))
	r.opts.StdinIsTerminal = func() bool { return false }

	if got := r.Asker().Ask(coder.AskRequest{
		Question: "Which signature?",
		Options:  []coder.AskOption{{Label: "error"}, {Label: "nothing"}},
	}); got != nil {
		t.Errorf("answered without a terminal: %q", got)
	}
	if !strings.Contains(out.String(), "Which signature?") {
		t.Errorf("the question was not shown:\n%s", out.String())
	}
	line, err := r.rl.ReadLine()
	if err != nil || !strings.Contains(line, "Ping") {
		t.Errorf("the question consumed the user's next message: %q %v", line, err)
	}
}
