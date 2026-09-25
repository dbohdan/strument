package coder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
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
	return c.runAndShowTail(ctx, command, requestedTimeout, 0)
}

// runAndShowTail is runAndShow that returns only the output's last tail
// lines, when tail is positive. The user is shown all of it regardless.
//
// This is what `cmd | tail -20` is for, without its two costs. The pipeline's
// exit status is tail's, so a failing test run reads as a pass: the shell has
// no pipefail, and a model that trusts "Exit status: 0" moves on. And the
// user, who reviews what ran, sees only what the model chose to keep. Here
// the status is the command's own and the screen has everything.
//
// Strument's own notices — the timeout, a Ctrl-C, the sandbox hint — come
// after the tail rather than inside it, so asking for 20 lines never costs
// the line that says why the command stopped.
func (c *Coder) runAndShowTail(ctx context.Context, command string, requestedTimeout time.Duration, tail int) (int, string) {
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
	c.shown.commandRan()
	exitCode, output, err := runner.Run(ctx, command, c.Root)
	if err != nil {
		c.Out.Errorf("Could not run the command: %v", err)
	}
	// Said in the output rather than only on screen, because the output is what
	// reaches the model: a command that was killed at two minutes and one that
	// exited on its own are otherwise indistinguishable to it, and the obvious
	// next move after an unexplained failure is to change the code.
	//
	// A denial arrives as a bare errno, and an unexplained failure is the thing
	// a coding model responds to by editing code. Naming the sandbox turns
	// thrashing into a config change.
	var notice string
	if exitCode != 0 && c.Sandbox.Active && looksDenied(output) {
		notice += c.Sandbox.deniedHint()
	}
	// Who stopped it, said in the output rather than only on screen, because
	// the output is what reaches the model: a command killed at two minutes, a
	// command the user stopped, and a command that failed on its own are
	// otherwise indistinguishable to it, and the obvious next move after an
	// unexplained failure is to change the code.
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		notice += fmt.Sprintf("\nThe command was stopped after %s by Strument's shell_timeout.", deadline)
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
		notice += "\nThe user pressed Ctrl-C and stopped this command. The output above is how far the command got."
		if exitCode == 0 {
			exitCode = -1
		}
	}
	if shown := output + notice; shown != "" {
		// Printf adds the trailing newline; trim the runner's so output that
		// already ends in one doesn't print a blank line.
		c.Out.CommandOutput(strings.TrimRight(shown, "\n"))
	}
	return exitCode, lastLines(output, tail) + notice
}

// lastLines keeps the last n lines of output, saying how many it left out.
// n <= 0, or output no longer than n lines, returns output unchanged.
func lastLines(output string, n int) string {
	if n <= 0 {
		return output
	}
	lines := strings.SplitAfter(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) <= n {
		return output
	}
	return fmt.Sprintf("(The last %d of %d lines. The user saw all of them.)\n", n, len(lines)) +
		strings.Join(lines[len(lines)-n:], "") + "\n"
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
	// PWD is set on every child rather than left to inheritance
	// (execWithPWD): an inherited value may name where Strument started
	// rather than this block's cwd, and the filtered set may carry none at
	// all — the allowlist only passes through, and a session started by a
	// non-shell parent has no PWD to pass. Every real shell exports PWD;
	// bash and fish synthesize it even from an empty environment. A suite
	// that reads it — Go's os.Getwd honors an exported PWD naming the same
	// directory — would otherwise pass under the harness and fail in the
	// user's terminal, which is how a symlinked-repo test failure hid
	// behind exactly this absence.
	env := r.Env
	if env == nil {
		env = os.Environ()
	}

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
		interp.Env(expand.ListEnviron(env...)),
		interp.ExecHandlers(func(interp.ExecHandlerFunc) interp.ExecHandlerFunc {
			return execWithPWD
		}),
	}
	runner, err := interp.New(opts...)
	if err != nil {
		return -1, "", err
	}
	err = runner.Run(ctx, file)

	captured := capMiddle(output.String(), maxBytes)

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

// capMiddle keeps the first and last halves of output longer than maxBytes
// and drops the middle, cut at line boundaries where there is one nearby.
//
// It used to keep the head only. The end of a build or test run is where the
// verdict is — the failure summary, the final error, "FAIL" — and a head-only
// cut dropped exactly that from the output most likely to be long. The head
// stays too, because a compiler's first error is the one to fix.
func capMiddle(output string, maxBytes int) string {
	if len(output) <= maxBytes {
		return output
	}
	half := maxBytes / 2
	head, tail := output[:half], output[len(output)-half:]
	if i := strings.LastIndexByte(head, '\n'); i > 0 {
		head = head[:i+1]
	}
	if i := strings.IndexByte(tail, '\n'); i >= 0 && i < len(tail)-1 {
		tail = tail[i+1:]
	}
	omitted := len(output) - len(head) - len(tail)
	return fmt.Sprintf("%s… %d bytes of output omitted …\n%s", head, omitted, tail)
}

// execKillTimeout is how long a cancelled child gets between the interrupt
// and the kill: the interpreter's own default.
const execKillTimeout = 2 * time.Second

// execWithPWD is interp.DefaultExecHandler with one difference: the child's
// environment carries PWD naming the directory it runs in.
//
// The interpreter cannot be trusted to pass it on. mvdan/sh (v3.13.1, and
// master at aebdf2b) assigns its PWD with setVarString on start and on every
// cd, which makes it unexported. At the top level of a block the exported
// PWD from the environment shows through underneath; a pipeline stage is a
// background subshell that flattens the two layers, and the unexported one
// wins. So `env` saw PWD and `env | grep PWD` did not, and a cd dropped it
// everywhere. Setting it where the child is started covers every path at
// once, and hc.Dir is the directory the child actually gets.
//
// The one divergence from a real shell: a block that unexports or unsets PWD
// still hands its children one. Nothing a model writes does that on purpose.
func execWithPWD(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	path, err := interp.LookPathDir(hc.Dir, hc.Env, args[0])
	if err != nil {
		fmt.Fprintln(hc.Stderr, err)
		return interp.ExitStatus(127)
	}
	cmd := exec.Cmd{
		Path:   path,
		Args:   args,
		Env:    withPWD(exportedEnv(hc.Env), hc.Dir),
		Dir:    hc.Dir,
		Stdin:  hc.Stdin,
		Stdout: hc.Stdout,
		Stderr: hc.Stderr,
	}
	err = cmd.Start()
	if err == nil {
		stop := context.AfterFunc(ctx, func() {
			if runtime.GOOS == "windows" {
				_ = cmd.Process.Signal(os.Kill)
				return
			}
			_ = cmd.Process.Signal(os.Interrupt)
			time.Sleep(execKillTimeout)
			_ = cmd.Process.Signal(os.Kill)
		})
		defer stop()
		err = cmd.Wait()
	}
	var exitErr *exec.ExitError
	var startErr *exec.Error
	switch {
	case errors.As(err, &exitErr):
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return exitStatus(128 + int(status.Signal()))
		}
		return exitStatus(exitErr.ExitCode())
	case errors.As(err, &startErr):
		fmt.Fprintf(hc.Stderr, "%v\n", err)
		return interp.ExitStatus(127)
	default:
		return err
	}
}

// exitStatus wraps a process's exit code into the shell's eight bits, as a
// POSIX shell does. Windows codes run to 32 bits.
func exitStatus(code int) interp.ExitStatus {
	return interp.ExitStatus(code & 0xff)
}

// exportedEnv is the environment a child of the interpreter gets, the way
// the interpreter's own (unexported) execEnv builds it: the exported string
// variables, and a variable unset in the block removes an inherited one.
func exportedEnv(env expand.Environ) []string {
	var out []string
	for name, vr := range env.Each {
		if !vr.IsSet() {
			out = slices.DeleteFunc(out, func(kv string) bool {
				return strings.HasPrefix(kv, name+"=")
			})
		}
		if vr.Exported && vr.Kind == expand.String {
			out = append(out, name+"="+vr.String())
		}
	}
	return out
}

// withPWD returns env with every entry named PWD replaced by one naming
// dir. Replace rather than append: an inherited PWD may name a different
// directory than the block runs in, and a stale value a program trusts
// blindly is worse than no value — Go stats the name and falls back to
// the kernel, but not every reader does. The comparison folds only on
// Windows, where environment names are case-insensitive at the API and
// os.Environ may spell the name "pwd" — the same rule envAllowed
// applies, through envNamesFold.
func withPWD(env []string, dir string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if name == "PWD" || (envNamesFold && strings.EqualFold(name, "PWD")) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PWD="+dir)
}
