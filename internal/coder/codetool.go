package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/monty"
	"dbohdan.com/strument/internal/render"
)

// The run_code tool: the model writes one small program and it runs in Monty, a
// restricted Python interpreter compiled to WebAssembly (internal/monty). Two
// measured facts motivate it (doc/plans/code-mode.md): arithmetic costs a
// quarter of the reasoning lines in this repository's own experiments, and
// models spend full request round trips on runs of read-only calls a program
// could make in one step. This file is both halves — pure computation, and
// the read-only bridge.
//
// Monty is a Python *subset*, and the description below is load-bearing: a
// model writing ordinary Python will hit walls, and the description is the
// only thing that can prevent most of them. The list of what is missing is
// empirical — probed against the vendored monty.wasm, not read off upstream's
// docs — and each wall a model hits anyway returns Monty's own error text,
// which names the construct.

// codeLimits bounds a program's resources. Explicit, never zero: the plan's
// point is that a runaway program terminates on a limit rather than hanging
// the turn.
var codeLimits = monty.Limits{
	MaxDuration:       5 * time.Second,
	MaxMemoryBytes:    32 << 20, // 32 MiB
	MaxRecursionDepth: 100,
}

// maxBridgedCalls caps how many read-only tool calls one program may issue.
// The bridge is a tool call, never a per-element helper — a program that loops
// over a list calling read() on each element is a run of observation calls
// with extra steps — and without a cap the number is unbounded.
const maxBridgedCalls = 50

// codeTool describes the tool. The Python-subset caveats live here rather than
// in the system prompt, for the same reason the skill catalog does: prose must
// not promise a tool that is only sometimes offered, and the schema is sent
// with the tool regardless of mode.
//
// The order is the finding, not a style: the first version led with mechanism
// ("Run a short Python program…") and prohibitions, and the bridge — the thing
// that answers the measured 4-removable-round-trips problem — came last. The
// symbol fix (doc/experiments/2026-08-symbol-uptake.md) established that the
// description that moves uptake is the one that opens by mapping the felt need
// ("I have several lookups to combine") to the tool; a spec sheet selects for
// nobody. The negations are compressed to one sentence and paired with the
// recovery path, because "grep always works; this opens with a failure
// surface" was the risk asymmetry the first version created.
func codeTool() llm.ToolDef {
	var b strings.Builder
	b.WriteString("Do several lookups, or a computation, in one call instead of " +
		"several. Use this when one answer needs multiple read/grep/glob/ls " +
		"results combined, or needs arithmetic, counting, sorting, or date " +
		"math.\n\n" +
		"The program can call the read-only tools directly — " +
		"grep(pattern=\"TODO\", glob=\"**/*.go\"), read(path=\"a.go\", limit=20)" +
		fmt.Sprintf(" — up to %d calls, each shown to the user like a direct call. Example:\n\n", maxBridgedCalls) +
		"```python\n" +
		"caps = {}\n" +
		"for name in [\"maxToolOutputBytes\", \"MaxSteps\", \"maxChatHistoryTokens\"]:\n" +
		"    caps[name] = grep(pattern=name + \" =\", glob=\"**/*.go\")\n" +
		"caps\n" +
		"```\n\n" +
		"The interpreter is Monty, a restricted Python subset. Expressions, " +
		"statements, loops, f-strings, comprehensions, try/except, classes, and " +
		"math/re/datetime/json all work; the last value evaluated is returned, " +
		"and print() shows intermediate values. Not available: with, match, " +
		"del, eval/exec, open, filesystem or network access, other imports. " +
		"A missing construct raises an error naming it — simplify and rerun; a " +
		"failed program costs one cheap retry.")

	// The bridged names are enumerated from InspectorTools() itself, so the
	// description and the dispatch cannot drift — the exact drift this
	// repository has had three times elsewhere. The example above names two;
	// the authoritative list rides behind it. The code functions (codefuncs.go)
	// are run_code-only and ride in through codeFuncDoc, from the same
	// registry the bridge dispatches on.
	if names := InspectorTools(); len(names) > 0 {
		fmt.Fprintf(&b, "\n\nThe callable functions are exactly: %s.",
			strings.Join(names, ", "))
	}
	b.WriteString(codeFuncDoc())

	return llm.ToolDef{
		Name:        toolRunCode,
		Description: b.String(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": strProp("The Python program to run. Its last evaluated value is " +
					"returned; use print() for intermediate values."),
			},
			"required": []any{"code"},
		},
	}
}

type codeCall struct {
	callID string
	code   string
}

func parseCodeArgs(tc llm.ToolCall) (codeCall, string) {
	var a struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(tc.Arguments), &a); err != nil {
		return codeCall{}, fmt.Sprintf("The arguments were not valid JSON: %v", err)
	}
	if strings.TrimSpace(a.Code) == "" {
		return codeCall{}, "The required \"code\" argument was missing or empty."
	}
	return codeCall{callID: tc.ID, code: a.Code}, ""
}

// runCode executes the program and returns the value, or the error text for
// the model to act on. Monty's errors carry the failing line and the
// exception — a useful error string is part of the contract, not decoration —
// so they pass through stripped of only the traceback's file framing, which
// says "script.py" regardless of anything the model did.
//
// No confirmation, like the other read-only tools: a program computes and
// reads, it does not touch anything outside its own WASM instance and the
// project's observation tools. It is announced twice, the two renderings of
// one event (the ask_user_question pattern): the shaped source block for the
// screen before the run, and one prose line after — for the transcript and as
// the outcome — naming what the program actually called, collected at the
// bridge rather than scanned from the source.
func (c *Coder) runCode(_ context.Context, cc codeCall) string {
	c.Out.ToolBlock(render.CodeOpen, cc.code)

	runner, err := montyRunner()
	if err != nil {
		return fmt.Sprintf("The Python interpreter failed to start: %v", err)
	}

	// print() output is collected and shipped in the result text. The
	// description tells the model to "use print() for intermediate values",
	// and a program that ends in print(...) rather than a bare expression —
	// most of them, observed live — would otherwise return None with its
	// actual output dropped on the floor. Under the observation force arm
	// print is the primary reporting channel, so this is not cosmetic.
	var printed strings.Builder
	var called []string
	result, err := runner.Execute(context.Background(), cc.code, nil,
		append(c.codeOptions(&called), monty.WithPrintFunc(func(s string) { printed.WriteString(s) }))...)
	summary := codeCalledText(codeLines(cc.code), called)
	if err != nil {
		// The calls made before the failure still happened, and a program that
		// aborted mid-way is precisely where the summary carries information
		// the value cannot.
		c.Out.Toolf("%s", summary)
		return codeErrorText(err)
	}
	c.Out.Toolf("%s", summary)
	return codeResultText(result, printed.String())
}

// codeLines counts the program's lines, discounting the leading and trailing
// blank lines the model's formatting may have added. Named apart from
// scrape.go's lineCount, which counts newline-terminated lines of a body.
func codeLines(code string) int {
	lines := strings.Split(strings.TrimSpace(code), "\n")
	return len(lines)
}

// codeCalledText is the outcome line: how big the program was and which tools
// it actually called. A program with no calls ran pure computation — a
// legitimate program, and said as such rather than left silent.
func codeCalledText(n int, called []string) string {
	size := fmt.Sprintf("%d lines", n)
	if n == 1 {
		size = "1 line"
	}
	if len(called) == 0 {
		return fmt.Sprintf("Ran %s of code.", size)
	}
	return fmt.Sprintf("Ran %s of code calling %s.", size, strings.Join(called, ", "))
}

// codeOptions assembles the Execute options: the resource limits, plus the
// read-only bridge. called collects the tool names the program actually
// invoked, for the outcome line.
func (c *Coder) codeOptions(called *[]string) []monty.ExecuteOption {
	opts := make([]monty.ExecuteOption, 0, 2)
	opts = append(opts, monty.WithLimits(codeLimits))

	names := InspectorTools()
	funcs := make([]monty.FuncDef, 0, len(names)+len(codeFuncs))
	// Each tool takes its arguments as keywords; the params list is what lets
	// Monty map a positional call's arguments onto those names. The schemas
	// differ per tool (path, pattern, limit, …), so the program passes
	// everything by name and Monty hands over whatever it got — an argument
	// this side does not recognize is answered by the tool itself, exactly as
	// a direct call with a wrong field would be.
	for _, n := range names {
		funcs = append(funcs, monty.Func(n))
	}
	// Code functions ride the same registration: positional calls map onto
	// their parameter names the same way, and an unregistered one raises
	// NameError inside the program, which is the fail-closed path.
	for _, d := range codeFuncs {
		funcs = append(funcs, monty.Func(d.name))
	}
	opts = append(opts, monty.WithExternalFunc(c.bridgeCall(names, called), funcs...))
	return opts
}

// bridgeCall is the ExternalFunc that pauses the program and answers one
// read-only tool call. It is an adapter, not a reimplementation: the call
// crosses the boundary as JSON and is answered by the same Inspector.Run a
// direct tool call goes through, so what the program sees is byte-for-byte
// what the model would have seen.
//
// The tools the program actually called are collected as they happen —
// recording at the bridge rather than scanning the source, because a scan
// overreports: a comment naming read, a variable called ls, a call in a branch
// that never runs. The interpreter pauses at every real call, so this side has
// the truth without parsing anything.
func (c *Coder) bridgeCall(allowed []string, called *[]string) monty.ExternalFunc {
	isAllowed := make(map[string]bool, len(allowed)+len(codeFuncs))
	for _, n := range allowed {
		isAllowed[n] = true
	}
	// The code functions are allowed by the same fail-closed check — they are
	// called from the same bridge, counted in the same cap, and announced the
	// same way. Their own dispatch happens below the check.
	for _, d := range codeFuncs {
		isAllowed[d.name] = true
	}

	seen := map[string]bool{}
	calls := 0
	return func(_ context.Context, call *monty.FunctionCall) (any, error) {
		// Fail closed. This runs behind the registration check already — a
		// name outside `allowed` is not registered with Monty at all and
		// raises NameError inside the program — but the check lives here too
		// so the invariant does not depend on the registration list staying
		// in step with it.
		if !isAllowed[call.Name] {
			return nil, fmt.Errorf("unknown function %q: only the read-only tools "+
				"(%s) can be called from a program", call.Name, strings.Join(allowed, ", "))
		}
		if !seen[call.Name] {
			seen[call.Name] = true
			*called = append(*called, call.Name)
		}
		calls++
		if calls > maxBridgedCalls {
			return nil, fmt.Errorf("the program made more than %d tool calls", maxBridgedCalls)
		}

		// Code functions answer with data, not model text, so they bypass
		// Inspector.Run and the prefix classification — their errors are
		// already Go errors and become exceptions with the right traceback.
		// Announced under the run_code tag, same as a bridged tool call: the
		// program, not a direct call, is what caused this either way.
		if d := codeFuncByName(call.Name); d != nil {
			c.Out.Toolf("‹run_code› %s", call.Name)
			return d.fn(c, call)
		}

		// Announce exactly as a direct call would be, so the review surface
		// looks the same whether the model called read directly or from inside
		// a program.
		c.Out.Toolf("‹run_code› %s", call.Name)
		tc := llm.ToolCall{Name: call.Name, Arguments: call.ArgsJSON()}
		out := c.inspector().Run(call.Name, tc.Arguments)

		// A tool failure raises instead of returning. Since the Go wrapper
		// resumes the snapshot with the error (monty_resume_error), Monty
		// raises it at the call site — the traceback names the program line
		// that made the call, and a try/except can catch it like any other
		// exception. An *empty result* is not a failure: "No matches" and a
		// symbol miss are answers a program may legitimately filter on, so
		// those stay values.
		if msg, bad := bridgeToolFailure(out); bad {
			return nil, fmt.Errorf("%s failed: %s", call.Name, msg)
		}
		return out, nil
	}
}

// bridgeToolFailure classifies a tool's returned text as a failure of the
// call itself. It is prefix-matching on the tools' own error sentences — the
// alternative is a parallel error channel through every Inspector method,
// five signatures changed for one caller, which the bridge's byte-for-byte
// contract makes unnecessary: the text the program sees is the text the
// model would see, so the classification reads the same text.
//
// The failures are the calls that could not do what they were asked: missing
// or malformed arguments, unreadable paths, unknown modes. Deliberately not
// failures, because continuing on them is the point of a program: "No
// matches", "No files match", "is empty", and symbol's miss message are
// empty results, not errors.
func bridgeToolFailure(out string) (string, bool) {
	for _, prefix := range []string{
		"Could not read", "Could not list", "Could not match",
		"The required ", "The arguments were not valid JSON",
		"Unknown mode ", "Unknown kind ", "Unknown tool ",
		"context_lines cannot be negative", "context_lines is capped",
		"The language parser is not available",
	} {
		if strings.HasPrefix(out, prefix) {
			return out, true
		}
	}
	return "", false
}

// codeResultText renders a program's value, plus anything it printed. The
// printed section comes first: it is what the model chose to show, and the
// final value (often None in a print-driven program) reads as the tail. A
// list or dict the model wants to read comes back as JSON rather than Go's
// `%v` spacing, which a model would otherwise have to misread as Python.
func codeResultText(result any, printed string) string {
	var b strings.Builder
	if printed != "" {
		b.WriteString(strings.TrimRight(printed, "\n"))
		if result != nil {
			b.WriteString("\n")
		}
	}
	switch result.(type) {
	case nil:
		if printed == "" {
			return "None"
		}
		return strings.TrimRight(b.String(), "\n")
	case []any, map[string]any:
		if data, err := json.Marshal(result); err == nil {
			b.Write(data)
			return b.String()
		}
	}
	fmt.Fprintf(&b, "%v", result)
	return b.String()
}

// codeErrorText renders an execution failure for the model. The traceback's
// leading file framing is dropped because it is constant — every program is
// "script.py" from Monty's point of view — and the exception itself is what
// the model needs.
func codeErrorText(err error) string {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "monty: ")
	// A traceback starts with the file framing and carries the failing line;
	// keep from the first exception name onward.
	if i := strings.Index(msg, "\n"); i >= 0 && strings.Contains(msg[:i], "script.py") {
		msg = msg[i+1:]
	}
	return "The program failed: " + msg
}

// montyRunner lazily builds the one process-wide Runner. Compiling monty.wasm
// costs hundreds of milliseconds, and wazero compiles once and reuses; each
// Execute gets its own isolated instance.
var montyRunner = sync.OnceValues(monty.New)
