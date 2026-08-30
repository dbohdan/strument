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
)

// The code tool: the model writes one small program and it runs in Monty, a
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
		"statements, loops, f-strings, comprehensions, try/except, and " +
		"math/re/datetime/json all work; the last value evaluated is returned, " +
		"and print() shows intermediate values. Not available: classes, with, " +
		"match, eval/exec, open, filesystem or network access, other imports. " +
		"A missing construct raises an error naming it — simplify and rerun; a " +
		"failed program costs one cheap retry.")

	// The bridged names are enumerated from InspectorTools() itself, so the
	// description and the dispatch cannot drift — the exact drift this
	// repository has had three times elsewhere. The example above names two;
	// the authoritative list rides behind it.
	if names := InspectorTools(); len(names) > 0 {
		fmt.Fprintf(&b, "\n\nThe callable functions are exactly: %s.",
			strings.Join(names, ", "))
	}

	return llm.ToolDef{
		Name:        toolCode,
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
// project's observation tools. It is announced instead.
func (c *Coder) runCode(_ context.Context, cc codeCall) string {
	c.Out.Toolf("‹code› %s", oneLine(cc.code))

	runner, err := montyRunner()
	if err != nil {
		return fmt.Sprintf("The Python interpreter failed to start: %v", err)
	}

	// A fresh context rather than the send's: the duration limit is the
	// harness's promise that a runaway loop terminates, and wazero honors a
	// canceled context by closing on it — but MaxDuration is the mechanism the
	// interpreter enforces from inside, which the limit test below pins.
	result, err := runner.Execute(context.Background(), cc.code, nil,
		c.codeOptions()...)
	if err != nil {
		return codeErrorText(err)
	}
	return codeResultText(result)
}

// codeOptions assembles the Execute options: the resource limits, plus the
// read-only bridge.
func (c *Coder) codeOptions() []monty.ExecuteOption {
	opts := make([]monty.ExecuteOption, 0, 2)
	opts = append(opts, monty.WithLimits(codeLimits))

	names := InspectorTools()
	funcs := make([]monty.FuncDef, 0, len(names))
	// Each tool takes its arguments as keywords; the params list is what lets
	// Monty map a positional call's arguments onto those names. The schemas
	// differ per tool (path, pattern, limit, …), so the program passes
	// everything by name and Monty hands over whatever it got — an argument
	// this side does not recognize is answered by the tool itself, exactly as
	// a direct call with a wrong field would be.
	for _, n := range names {
		funcs = append(funcs, monty.Func(n))
	}
	opts = append(opts, monty.WithExternalFunc(c.bridgeCall(names), funcs...))
	return opts
}

// bridgeCall is the ExternalFunc that pauses the program and answers one
// read-only tool call. It is an adapter, not a reimplementation: the call
// crosses the boundary as JSON and is answered by the same Inspector.Run a
// direct tool call goes through, so what the program sees is byte-for-byte
// what the model would have seen.
func (c *Coder) bridgeCall(allowed []string) monty.ExternalFunc {
	isAllowed := make(map[string]bool, len(allowed))
	for _, n := range allowed {
		isAllowed[n] = true
	}

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
		calls++
		if calls > maxBridgedCalls {
			return nil, fmt.Errorf("the program made more than %d tool calls; "+
				"stop and do the rest with direct tool calls", maxBridgedCalls)
		}

		// Announce exactly as a direct call would be, so the review surface
		// looks the same whether the model called read directly or from inside
		// a program.
		c.Out.Toolf("‹code› %s", call.Name)
		tc := llm.ToolCall{Name: call.Name, Arguments: call.ArgsJSON()}
		return c.inspector().Run(call.Name, tc.Arguments), nil
	}
}

// codeResultText renders a program's value. A list or dict the model wants to
// read comes back as JSON rather than Go's `%v` spacing, which a model would
// otherwise have to misread as Python.
func codeResultText(result any) string {
	switch result.(type) {
	case nil:
		return "None"
	case []any, map[string]any:
		b, err := json.Marshal(result)
		if err == nil {
			return string(b)
		}
	}
	return fmt.Sprintf("%v", result)
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
