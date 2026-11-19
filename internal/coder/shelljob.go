package coder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// The interpreter runs `cmd &` as a goroutine: $! is the goroutine's index
// ("g1"), not a process ID, nothing stops one job without the rest, and its
// kill builtin is unimplemented. Models write `server & ... ; kill $!` out of
// habit; refused, MiMo spent a second command on pkill in three runs of
// three. So a command that starts exactly one background job, once, gets a
// kill that works on it: the job's statement is marked, the processes it
// starts are recorded, and `kill $!` signals them. Any other command keeps
// the refusal, because with two jobs or a job in a loop, which one $! names
// is a guess.
//
// It is half a kill. $! is still "g1", so a process ID written to a file is
// that label too, and it signals the job's direct processes, as the stop at
// return does, not their descendants.

// jobMarker is an unexported shell variable set at the start of the one
// job, so every process the job starts, a pipeline stage or a function's
// included, can be told from the command's own. Being unexported, it reaches
// no child's environment.
const jobMarker = "__strument_bg"

// The commands routeKill rewrites a kill into, for the exec handler to
// answer. Their names cannot be typed as a command: they hold a NUL.
const (
	killJobCommand     = "kill\x00job"
	killMissingCommand = "kill\x00missing"
)

// jobRef matches a job as the interpreter names it ($!) and as a shell's job
// control does (%1, %%, %+, %-).
var jobRef = regexp.MustCompile(`^(g\d+|%.*)$`)

// oneJob is the one background job of a command, when it has exactly one.
// Its zero value, with marked false, is a command that has none or several.
type oneJob struct {
	marked bool

	mu      sync.Mutex
	procs   map[*os.Process]bool
	started bool
	pending syscall.Signal // a kill that came before the job started a process
}

// markOneJob reports whether file starts exactly one background job, once,
// and marks that job's statement if so. A job in a loop or a function can
// start many times; one in a subshell or a substitution is not the job $!
// names in the command; and a process substitution is a background job to
// the interpreter too, which moves $! along.
func markOneJob(file *syntax.File) *oneJob {
	var (
		found      []*syntax.Stmt
		disqualify bool
		stack      []syntax.Node
	)
	syntax.Walk(file, func(n syntax.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		switch n := n.(type) {
		case *syntax.Stmt:
			if n.Background || n.Disown || n.Coprocess {
				found = append(found, n)
				for _, a := range stack {
					switch a.(type) {
					case *syntax.ForClause, *syntax.WhileClause, *syntax.FuncDecl,
						*syntax.CmdSubst, *syntax.ProcSubst, *syntax.Subshell:
						disqualify = true
					}
				}
			}
		case *syntax.ProcSubst:
			disqualify = true
		}
		stack = append(stack, n)
		return true
	})
	if len(found) != 1 || disqualify || found[0].Cmd == nil || found[0].Disown || found[0].Coprocess {
		return &oneJob{}
	}
	marker, err := syntax.NewParser().Parse(strings.NewReader(jobMarker+"=1"), "")
	if err != nil || len(marker.Stmts) != 1 {
		return &oneJob{}
	}
	st := found[0]
	st.Cmd = &syntax.Block{Lbrace: st.Cmd.Pos(), Rbrace: st.Cmd.End(),
		Stmts: []*syntax.Stmt{marker.Stmts[0], {Cmd: st.Cmd, Position: st.Cmd.Pos()}}}
	return &oneJob{marked: true, procs: map[*os.Process]bool{}}
}

// started records a process the command started, and returns what to call
// when it exits. A process of the job is signalled at once when a kill came
// before it started.
func (j *oneJob) start(hc interp.HandlerContext, p *os.Process) (done func()) {
	if j == nil || !j.marked || !hc.Env.Get(jobMarker).IsSet() {
		return func() {}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.procs[p] = true
	j.started = true
	if j.pending != 0 {
		signal(p, j.pending)
	}
	return func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		delete(j.procs, p)
	}
}

// route is the call handler: a kill of a job goes to the job, any other kill
// to the system's.
func (j *oneJob) route(_ context.Context, args []string) ([]string, error) {
	if args[0] != "kill" {
		return args, nil
	}
	if slices.ContainsFunc(args[1:], jobRef.MatchString) {
		return append([]string{killJobCommand}, args[1:]...), nil
	}
	if path, err := lookKill(); err == nil {
		return append([]string{path}, args[1:]...), nil
	}
	return append([]string{killMissingCommand}, args[1:]...), nil
}

// answer runs the two rewritten kills, reporting whether name was one.
func (j *oneJob) answer(hc interp.HandlerContext, name string, args []string) (bool, error) {
	switch name {
	case killMissingCommand:
		fmt.Fprintln(hc.Stderr, "kill: there is no kill command on this system.")
		return true, interp.ExitStatus(1)
	case killJobCommand:
		if msg := j.kill(args); msg != "" {
			fmt.Fprintln(hc.Stderr, msg)
			return true, interp.ExitStatus(1)
		}
		return true, nil
	}
	return false, nil
}

// lookKill finds the system's kill.
func lookKill() (string, error) { return exec.LookPath("kill") }

// kill signals the one job, or says why it will not.
func (j *oneJob) kill(args []string) string {
	sig, operands, msg := parseKill(args)
	if msg != "" {
		return "kill: " + msg
	}
	if j == nil || !j.marked {
		return "kill: a job started with & here is not a process with an ID: $! is the shell's own " +
			"handle for it, and this command starts more than one job, or starts one in a loop or a " +
			"function, so which one it names is not known. Every job a command starts is stopped when " +
			"the command returns. To stop one sooner, signal its process: pkill -f <pattern>, or " +
			"kill $(pgrep -f <pattern>)."
	}
	for _, o := range operands {
		switch o {
		case "g1", "%1", "%%", "%+":
		default:
			if !jobRef.MatchString(o) {
				return "kill: give either the job or process IDs, not both."
			}
			return "kill: " + o + ": no such job; this command starts one, which is %1."
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case len(j.procs) > 0:
		for p := range j.procs {
			signal(p, sig)
		}
	case !j.started:
		// Asked before the job got as far as starting its process: it is
		// signalled when it does, as a shell's kill reaches the subshell.
		j.pending = sig
	default:
		return "kill: the job has already finished."
	}
	return ""
}

// parseKill reads kill's options: -SIG, -s SIG, -n NUM, and --. The default
// signal is TERM.
func parseKill(args []string) (syscall.Signal, []string, string) {
	sig := syscall.SIGTERM
	for len(args) > 0 && strings.HasPrefix(args[0], "-") && args[0] != "-" {
		a := args[0]
		args = args[1:]
		var name string
		switch a {
		case "--":
			return sig, args, ""
		case "-s", "-n":
			if len(args) == 0 {
				return 0, nil, a + " needs a signal"
			}
			name, args = args[0], args[1:]
		case "-l":
			return 0, nil, "-l lists signals; run it without a job."
		default:
			name = a[1:]
		}
		s, ok := signalByName(name)
		if !ok {
			return 0, nil, "unknown signal " + name
		}
		sig = s
	}
	if len(args) == 0 {
		return 0, nil, "no job or process ID given"
	}
	return sig, args, ""
}

func signalByName(name string) (syscall.Signal, bool) {
	if n, err := strconv.Atoi(name); err == nil && n > 0 && n < 65 {
		return syscall.Signal(n), true
	}
	switch strings.TrimPrefix(strings.ToUpper(name), "SIG") {
	case "HUP":
		return syscall.SIGHUP, true
	case "INT":
		return syscall.SIGINT, true
	case "QUIT":
		return syscall.SIGQUIT, true
	case "KILL":
		return syscall.SIGKILL, true
	case "TERM":
		return syscall.SIGTERM, true
	}
	return 0, false
}

// signal sends sig to p. Windows has no signals to send but the kill.
func signal(p *os.Process, sig syscall.Signal) {
	if runtime.GOOS == "windows" {
		_ = p.Kill()
		return
	}
	_ = p.Signal(sig)
}
