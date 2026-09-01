package coder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// runAndShow runs a confirmed command through the configured runner, echoing
// "Running <cmd>" and then the captured output to the user — the output
// otherwise only reaches the model (as a tool result or chat addition), never
// the terminal. requestedTimeout is what the model asked for on this call,
// zero when it gave none; it can only narrow the configured deadline, never
// widen it (see shellTimeout). Returns the exit code and captured output.
func (c *Coder) runAndShow(ctx context.Context, command string, requestedTimeout time.Duration) (int, string) {
	c.Out.Printf("")
	c.Out.Toolf("Running %s", quoteToolArg(command))

	// A required sandbox that is not enforcing stops the command here rather
	// than running it and mentioning the fact. /run does not come through this
	// function, so the user keeps their own escape hatch.
	if c.Sandbox.blocksExecution() {
		refusal := c.Sandbox.refusal()
		c.Out.Errorf("%s", refusal)
		return -1, refusal
	}

	// A model-caused block gets a deadline. The turn context is cancellable but
	// carries none, so a command that never returns — a dev server, a `read`, a
	// test waiting on a socket it will not get — hangs the session until a human
	// notices and presses Ctrl-C. That is survivable while every command is
	// confirmed one at a time, and stops being survivable the moment a turn can
	// be approved in a batch, which is why this lands before that does.
	//
	// It is also the timeout doc/experimenting.md has promised all along.
	//
	// /run gets no deadline: the user typed that command and may well have
	// meant the twenty-minute build.
	deadline := c.shellTimeout(requestedTimeout)
	if deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}

	runner := c.Runner
	if runner == nil {
		// Model-run, so the allowlist applies: the block came from the model,
		// and its output goes back to the model as a tool result. /run builds
		// its own PipeRunner without an Env, keeping the full environment.
		runner = PipeRunner{Env: FilterEnv(nil, c.EnvAllow)}
	}
	exitCode, output, err := runner.Run(ctx, command, c.Root)
	if err != nil {
		c.Out.Errorf("Error running command: %v", err)
	}
	// Said in the output rather than only on screen, because the output is what
	// reaches the model: a command that was killed at two minutes and one that
	// exited on its own are otherwise indistinguishable to it, and the obvious
	// next move after an unexplained failure is to change the code.
	//
	// A denial arrives as a bare errno, and an unexplained failure is the thing
	// a coding model responds to by editing code. Naming the sandbox turns
	// thrashing into a config change.
	if exitCode != 0 && c.Sandbox.Active && looksDenied(output) {
		output += c.Sandbox.deniedHint()
	}
	// Who stopped it, said in the output rather than only on screen, because
	// the output is what reaches the model: a command killed at two minutes, a
	// command the user stopped, and a command that failed on its own are
	// otherwise indistinguishable to it, and the obvious next move after an
	// unexplained failure is to change the code.
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		output += fmt.Sprintf("\nThe command was stopped after %s by Strument's shell_timeout.", deadline)
		if exitCode == 0 {
			exitCode = -1
		}
	case errors.Is(ctx.Err(), context.Canceled):
		// This used to say nothing, on the reasoning that a Ctrl-C is the
		// user's own decision and needs no explaining back to them. That held
		// while a Ctrl-C ended the turn. It no longer does — the turn asks the
		// user what they meant and can carry on — so the model is left with a
		// command that died for no stated reason, which is exactly what it
		// answers by editing code.
		output += "\nThe user pressed Ctrl-C and stopped this command. The output above is how far the command got."
		if exitCode == 0 {
			exitCode = -1
		}
	}
	if output != "" {
		// Printf adds the trailing newline; trim the runner's so output that
		// already ends in one doesn't print a blank line.
		c.Out.Printf("%s", strings.TrimRight(output, "\n"))
	}
	return exitCode, output
}

// defaultShellTimeout bounds a model-caused command. Two minutes is what
// doc/experimenting.md has documented since before any such timeout existed;
// it is long enough for a test suite and short enough that a hang is a pause
// rather than the end of the session.
const defaultShellTimeout = 2 * time.Minute

// shellTimeout resolves the effective deadline for one model-caused command.
// requestedTimeout is what the model asked for on the call, zero when it gave
// none. Zero on the coder takes the default; a negative one means no deadline
// at all, which is what `shell_timeout = 0` in a config asks for.
//
// The model's request is a narrowing, never a widening: it can spend less than
// the configured ceiling by saying so (a command it knows finishes in a second
// fails fast instead of hanging the turn), but it cannot buy more time than the
// user configured, because the ceiling exists so that a hang is a pause the
// user notices rather than a session that stopped answering (see
// defaultShellTimeout). A silent clamp would look to the model like the command
// itself failing; runShellTool reports the ceiling in that case.
func (c *Coder) shellTimeout(requested time.Duration) time.Duration {
	var ceiling time.Duration
	if c.ShellTimeout == 0 {
		ceiling = defaultShellTimeout
	} else {
		ceiling = c.ShellTimeout
	}
	if requested > 0 && (ceiling <= 0 || requested < ceiling) {
		return requested
	}
	return ceiling
}

// syncWriter serializes writes to the capture buffer. The interpreter's
// documentation warns that "writes to Stdout and Stderr may be concurrent if
// background commands are used" (api.go) — `a & b &` has both jobs writing to
// the same buffer while the foreground continues — and a bytes.Buffer is not
// safe for concurrent use. Serialize rather than race.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// PipeRunner is the default deterministic CommandRunner: the whole
// block through one shell, stdout+stderr merged. PTY execution is opt-in
// elsewhere.
type PipeRunner struct {
	// MaxBytes caps captured output; 0 means the default (64 KiB).
	MaxBytes int
	// Env is the environment the block runs under: model-run commands get the
	// allowlist-filtered set (see FilterEnv), while the zero value inherits
	// the whole process environment — the behavior /run wants, since the user
	// typed that command themselves.
	Env []string
}

func (r PipeRunner) Run(ctx context.Context, block string, cwd string) (int, string, error) {
	maxBytes := r.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}

	var output bytes.Buffer
	capture := &syncWriter{w: &output}
	file, err := syntax.NewParser().Parse(strings.NewReader(block), "")
	if err != nil {
		return -1, "", err
	}

	// The allowlist, when one was given. A nil Env inherits the whole process
	// environment — the caller decides, because the same runner serves
	// model-run commands (filtered) and user-run ones (/run, unfiltered);
	// ListEnviron(nil...) would instead give the block an empty environment.
	//
	// The stdin nil below is deliberate rather than the output buffer: that
	// wiring was self-referential (a command reading stdin reads what the block
	// has printed so far, and reading a bytes.Buffer drains it), and a
	// tool-invoked command has no user at a keyboard, so empty stdin is the
	// honest state.
	opts := []interp.RunnerOption{
		// nil stdin rather than the output buffer, which was also passed as
		// stdin. That wiring is self-referential: a command reading stdin reads
		// what the block has printed so far, and reading a bytes.Buffer drains
		// it. A standalone reproduction does exactly that — `echo one; echo two;
		// cat -n` comes back as the numbered version of its own output — though
		// it does not reproduce through this function, and I could not isolate
		// what differs. So this is a correctness change rather than a fix for
		// observed misbehavior: a tool-invoked command has no user at a keyboard,
		// exec.Command gave it an empty stdin before the interpreter replaced it,
		// and nil says that unambiguously instead of relying on whatever keeps
		// the self-reference from biting.
		interp.StdIO(nil, capture, capture),
		interp.Dir(cwd),
	}
	if r.Env != nil {
		opts = append(opts, interp.Env(expand.ListEnviron(r.Env...)))
	}
	runner, err := interp.New(opts...)
	if err != nil {
		return -1, "", err
	}
	err = runner.Run(ctx, file)

	captured := output.String()
	if len(captured) > maxBytes {
		captured = captured[:maxBytes] + "\n… output truncated"
	}

	exitCode := 0
	if err != nil {
		var status interp.ExitStatus
		if errors.As(err, &status) {
			exitCode = int(status)
			err = nil
		} else {
			exitCode = -1
		}
	}
	return exitCode, captured, err
}
