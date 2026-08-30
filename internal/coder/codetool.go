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
// could make in one step. Part 2 is pure computation; Part 3 bridges the
// read-only tools in.
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

// codeTool describes the tool. The Python-subset caveats live here rather than
// in the system prompt, for the same reason the skill catalog does: prose must
// not promise a tool that is only sometimes offered, and the schema is sent
// with the tool regardless of mode.
func codeTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolCode,
		Description: "Run a short Python program and return its value: a calculator, " +
			"a formatter, or one program that computes over several pieces of data at once. " +
			"The interpreter is Monty, a restricted Python subset — not full Python.\n\n" +
			"NOT available: class definitions, `with`, `match`, `eval`/`exec`, `open`, " +
			"imports beyond math/re/datetime/json, and third-party libraries. There is " +
			"no filesystem or network access.\n\n" +
			"Available: f-strings, while, try/except, comprehensions, generators, " +
			"lambda, round(), sum/min/max/sorted/enumerate/zip/abs, and all of math. " +
			"Write the expression or statements whose last value is the answer; " +
			"`round(x, 2)` rounds to 2 decimal places. Format specs work in " +
			"f-strings (`f'{x:.2f}'`, `f'{n:5d}'`) but zero-padding applies to " +
			"decimal only — use `s.zfill(n)` for other bases; there is no " +
			"%-formatting and no .format().",
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
// No confirmation, like the other read-only tools: a program computes, it does
// not touch anything outside its own WASM instance. It is announced instead.
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
		monty.WithLimits(codeLimits))
	if err != nil {
		return codeErrorText(err)
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
