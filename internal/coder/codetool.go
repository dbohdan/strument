package coder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"runtime/metrics"
	"strings"
	"time"

	"github.com/dop251/goja"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/render"
)

// The run_code tool: the model writes one small JavaScript program and it runs
// in goja, a JavaScript engine written in Go. Two measured facts motivate the
// tool (doc/plans/code-mode.md): arithmetic costs a quarter of the reasoning
// lines in this repository's own experiments, and models spend full request
// round trips on runs of read-only calls a program could make in one step.
// This file is both halves — pure computation, and the read-only bridge.
//
// It was Monty, a restricted Python interpreter compiled to WebAssembly, until
// doc/experiments/2026-09-run-code-arms. Models took Monty for full Python and
// reached for os, open and glob in their first program; described as
// JavaScript, the same tasks drew no reach for Node's equivalents, and
// correctness was the same. The trial did not show JavaScript better by a
// significant margin. The switch rests on its being no worse on anything
// measured while removing a Rust shim, a vendored 5 MB WebAssembly blob and a
// WebAssembly runtime: goja is ordinary Go.
//
// JavaScript is the whole language here, not a subset, so the description no
// longer carries a list of missing constructs to keep true. What is missing is
// the host — Node's require, fs and process, a browser's fetch — and each reach
// for it is answered in the error channel, where it happens.

// codeLimits bounds a program's resources. Explicit, never zero: a runaway
// program terminates on a limit rather than hanging the turn.
var codeLimits = struct {
	MaxDuration time.Duration
	// MaxHeapGrowth is how far the process's heap may grow while a program
	// runs. goja has no memory limit of its own, and Monty's was 32 MiB of its
	// own linear memory; this is the nearest equivalent a Go-hosted engine
	// allows. Process-wide, so it is generous: other goroutines allocate too.
	MaxHeapGrowth uint64
	// MaxCallStackSize is goja's frame limit: deep recursion raises a
	// RangeError rather than exhausting the Go stack.
	MaxCallStackSize int
}{
	MaxDuration:      5 * time.Second,
	MaxHeapGrowth:    256 << 20,
	MaxCallStackSize: 1000,
}

// maxBridgedCalls caps how many read-only tool calls one program may issue.
// The bridge is a tool call, never a per-element helper — a program that loops
// over a list calling read() on each element is a run of observation calls
// with extra steps — and without a cap the number is unbounded.
const maxBridgedCalls = 50

// codeTool describes the tool. The caveats live here rather than in the system
// prompt, for the same reason the skill catalog does: prose must not promise a
// tool that is only sometimes offered, and the schema is sent with the tool
// regardless of mode.
//
// The order is the finding, not a style: the first version led with mechanism
// ("Run a short Python program…") and prohibitions, and the bridge — the thing
// that answers the measured 4-removable-round-trips problem — came last. The
// symbol fix (doc/experiments/2026-08-symbol-uptake/README.md) established that the
// description that moves uptake is the one that opens by mapping the felt need
// ("I have several lookups to combine") to the tool; a spec sheet selects for
// nobody. The text is the one doc/experiments/2026-09-run-code-js and
// 2026-09-run-code-arms measured, sentence for sentence against the Monty
// description it replaced.
func codeTool(callable []string) llm.ToolDef {
	var b strings.Builder
	b.WriteString("Do several lookups, or a computation, in one call instead of " +
		"several. Use this when one answer needs multiple read/grep/glob/ls " +
		"results combined, or needs arithmetic, counting, sorting, or date " +
		"math.\n\n" +
		"The program can call the read-only tools directly — " +
		"grep({pattern: \"TODO\", glob: \"**/*.go\"}), read({path: \"a.go\", limit: 20})" +
		fmt.Sprintf(" — up to %d calls, each shown to the user like a direct call. ", maxBridgedCalls) +
		"Each function takes an options object as shown; a single leading argument may also " +
		"be passed on its own, as in read(\"a.go\"). Example:\n\n" +
		codeExampleText() +
		"Only the program's last evaluated value comes back to you, so end it " +
		"with what you want to see — two calls on two lines return the second " +
		"one's result and drop the first. console.log() shows intermediate values.\n\n" +
		"The interpreter is goja, a JavaScript engine embedded in the harness. Standard " +
		"JavaScript works: let and const, arrow functions, classes, template literals, " +
		"destructuring, spread, try/catch, Map and Set, and JSON, Math, RegExp and Date. " +
		"This is not Node or a browser: require, import, fs, path, process, child_process, " +
		"fetch, timers and network access do not exist, so use glob({pattern: \"**/*.py\"}) " +
		"to walk the tree and read() to open a file; the bash tool, not this one, runs " +
		"commands. A missing name raises an error naming it — simplify and rerun; a failed " +
		"program costs one cheap retry.")

	// The bridged names come from the caller, which builds one list for the
	// description, the registration, and the bridge's allow check — so the
	// three cannot drift, the exact drift this repository has had three times
	// elsewhere. The example above names two; the authoritative list rides
	// behind it. The code functions (codefuncs.go) are run_code-only and ride
	// in through codeFuncDoc, from the same registry the bridge dispatches on.
	if len(callable) > 0 {
		fmt.Fprintf(&b, "\n\nThe callable functions are exactly: %s.", strings.Join(callable, ", "))
	}
	b.WriteString(codeDataFuncDoc())
	b.WriteString(codeFuncDoc())

	return llm.ToolDef{
		Name:        toolRunCode,
		Description: b.String(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": strProp("The JavaScript program to run. Its last evaluated value is " +
					"returned; use console.log() for intermediate values."),
			},
			"required": []any{"code"},
		},
	}
}

// codeExampleText is the worked example. It is kept out of the string above
// because models copy the example, so it is the part most worth being able to
// read on its own: it has to agree with the contract paragraph beside it.
func codeExampleText() string {
	return "```javascript\n" +
		"const caps = {};\n" +
		"for (const name of [\"maxToolOutputBytes\", \"MaxSteps\", \"maxChatHistoryTokens\"]) {\n" +
		"  caps[name] = grep({pattern: name + \" =\", glob: \"**/*.go\"});\n" +
		"}\n" +
		"caps\n" +
		"```\n\n"
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

// bridgedCall is one call a program made to a bridged function: its name and
// its arguments by name, however the program wrote them (jsArgs).
type bridgedCall struct {
	Name string
	Args map[string]any
}

// ArgsJSON is Args as the JSON a direct tool call carries, so a bridged call
// is answered by the same Inspector.Run a direct call goes through.
func (b *bridgedCall) ArgsJSON() string {
	if len(b.Args) == 0 {
		return "{}"
	}
	data, err := json.Marshal(b.Args)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// bridgeFunc answers one bridged call with data, or an error the program sees
// as a thrown Error.
type bridgeFunc func(ctx context.Context, call *bridgedCall) (any, error)

// runCode executes the program and returns the value, or the error text for
// the model to act on.
//
// No confirmation, like the other read-only tools: a program computes and
// reads, it does not touch anything outside its own interpreter and the
// project's observation tools. It is announced twice, the two renderings of
// one event (the ask_user_question pattern): the shaped source block for the
// screen before the run, and one prose line after — for the transcript and as
// the outcome — naming what the program actually called, collected at the
// bridge rather than scanned from the source.
func (c *Coder) runCode(ctx context.Context, cc codeCall) string {
	c.Out.ToolBlock(render.CodeOpen, cc.code)

	vm := goja.New()
	vm.SetMaxCallStackSize(codeLimits.MaxCallStackSize)

	var printed strings.Builder
	var log bridgeLog
	names := c.codeCallableTools()
	bridge := c.bridgeCall(names, &log)

	stringifyV, err := vm.RunString(jsStringify)
	if err != nil {
		return fmt.Sprintf("The JavaScript interpreter failed to start: %v", err)
	}
	stringify, _ := goja.AssertFunction(stringifyV)
	show := func(v goja.Value) string {
		if v == nil || goja.IsUndefined(v) {
			return "undefined"
		}
		if goja.IsNull(v) {
			return "null"
		}
		if s, ok := v.Export().(string); ok {
			return s
		}
		if _, ok := v.(*goja.Object); ok {
			if out, err := stringify(goja.Undefined(), v); err == nil && !goja.IsUndefined(out) {
				return out.String()
			}
		}
		return v.String()
	}
	throw := func(msg string) {
		errCtor, _ := goja.AssertConstructor(vm.Get("Error"))
		obj, err := errCtor(nil, vm.ToValue(msg))
		if err != nil {
			panic(vm.ToValue(msg))
		}
		panic(obj)
	}

	register := func(name string, params []string) {
		_ = vm.Set(name, func(call goja.FunctionCall) goja.Value {
			v, err := bridge(ctx, &bridgedCall{Name: name, Args: jsArgs(call.Arguments, params)})
			if err != nil {
				throw(err.Error())
			}
			return vm.ToValue(v)
		})
	}
	for _, n := range names {
		register(n, codeToolParams[n])
	}
	for _, d := range codeDataFuncs {
		register(d.name, d.params)
	}
	for _, d := range codeFuncs {
		register(d.name, d.params)
	}

	console := vm.NewObject()
	logf := func(call goja.FunctionCall) goja.Value {
		parts := make([]string, len(call.Arguments))
		for i, a := range call.Arguments {
			parts[i] = show(a)
		}
		printed.WriteString(strings.Join(parts, " ") + "\n")
		return goja.Undefined()
	}
	for _, m := range []string{"log", "info", "warn", "error", "debug"} {
		_ = console.Set(m, logf)
	}
	_ = vm.Set("console", console)

	stop := watchProgram(ctx, vm)
	var value goja.Value
	prog, err := compileProgram(cc.code)
	if err == nil {
		value, err = vm.RunProgram(prog)
	}
	stop()

	// The calls made before a failure still happened, and a program that
	// aborted mid-way is precisely where the summary carries information the
	// value cannot.
	c.Out.Toolf("%s", codeCalledText(codeLines(cc.code), log.names))
	if err != nil {
		return jsErrorText(err, cc.code)
	}

	var result any
	if value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		result = value.Export()
	}
	// console.log output is collected and shipped in the result text. The
	// description tells the model to use it for intermediate values, and a
	// program that ends in console.log(...) rather than a bare expression —
	// most of them — would otherwise return undefined with its actual output
	// dropped on the floor. The printed section comes first: it is what the
	// model chose to show, and the final value reads as the tail.
	var b strings.Builder
	if printed.Len() > 0 {
		b.WriteString(strings.TrimRight(printed.String(), "\n"))
		if result != nil {
			b.WriteString("\n")
		}
	}
	if result != nil || printed.Len() == 0 {
		b.WriteString(show(value))
	}
	if note := codeLostCallsNote(result, printed.String(), &log); note != "" {
		// One blank line between the value and the note, whether or not the
		// value already ended in a newline — grep's content mode does, and two
		// blank lines read as a missing paragraph.
		return truncateResult(strings.TrimRight(b.String(), "\n") + "\n\n" + note)
	}
	return truncateResult(b.String())
}

// errHeapLimit is the interrupt value for a program that grew the heap past
// codeLimits.MaxHeapGrowth.
var errHeapLimit = errors.New("memory limit")

// watchProgram interrupts the program on the time limit, on the turn being
// cancelled, and on the heap growing past its limit, and returns the function
// that stops watching. The heap is sampled rather than metered — goja offers
// no allocation hook — through runtime/metrics, which reads without stopping
// the world.
func watchProgram(ctx context.Context, vm *goja.Runtime) (stop func()) {
	done := make(chan struct{})
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	heap := func() uint64 {
		metrics.Read(sample)
		if sample[0].Value.Kind() != metrics.KindUint64 {
			return 0
		}
		return sample[0].Value.Uint64()
	}
	base := heap()
	go func() {
		deadline := time.NewTimer(codeLimits.MaxDuration)
		defer deadline.Stop()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				vm.Interrupt(ctx.Err())
				return
			case <-deadline.C:
				vm.Interrupt("time limit")
				return
			case <-tick.C:
				if h := heap(); h > base && h-base > codeLimits.MaxHeapGrowth {
					vm.Interrupt(errHeapLimit)
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

// jsStringify renders a value the way the result and console.log show it:
// JSON, with Map and Set turned into what they hold rather than the {} that
// JSON.stringify gives them. A list or object the model wants to read comes
// back as JSON rather than Go's %v spacing.
const jsStringify = `(function (v) {
  return JSON.stringify(v, function (k, x) {
    if (x instanceof Map) return Object.fromEntries(x);
    if (x instanceof Set) return Array.from(x);
    return x;
  });
})`

// jsArgs maps a call's arguments onto the parameter names. An options object —
// the convention the description shows — supplies them by name; leading
// positionals fill params in order, and a trailing options object after them
// is merged in, which is how read("a.go", {limit: 20}) is written.
func jsArgs(args []goja.Value, params []string) map[string]any {
	out := map[string]any{}
	opts := func(v goja.Value) (map[string]any, bool) {
		obj, ok := v.(*goja.Object)
		if !ok {
			return nil, false
		}
		m, ok := obj.Export().(map[string]any)
		return m, ok
	}
	if n := len(args); n > 0 {
		if m, ok := opts(args[n-1]); ok {
			maps.Copy(out, m)
			args = args[:n-1]
		}
	}
	for i, a := range args {
		if i < len(params) {
			out[params[i]] = a.Export()
		}
	}
	return out
}

// RunCode answers one run_code call and returns the text a model would
// receive — the door `strument tool run_code` drives, on the same terms
// Inspector.Run backs the other tools' command line: the program the model
// would send, the result it would get, byte for byte.
//
// It wraps the unexported runCode rather than being the unexported runCode for
// the same reason the Inspector exists: the chat loop's dispatch (tools.go)
// works on codeCall and thread-safety assumptions a command line does not
// carry, and this entry point owns the one difference — an Out that is
// required here, because the program block and the outcome line are the
// command's visible behavior.
func (c *Coder) RunCode(code string) string {
	return c.runCode(context.Background(), codeCall{code: code})
}

// bridgeLog is what the bridge records about one program's calls: which tools
// it reached for, how many calls it made in total, and what the last one
// answered. The first feeds the outcome line on screen; the other two feed the
// note in the result, which is the only place a discarded result can be
// mentioned.
type bridgeLog struct {
	names []string // distinct tool names, in first-call order
	calls int
	last  any // what the last bridged call returned
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

// codeCallableTools is the one list behind the run_code description, the
// registration, and the bridge's allow check.
//
// It is InspectorTools() minus symbol when there is no repo map, because that
// is exactly the condition under which toolDefs offers the symbol tool: symbol
// reads the tree-sitter layer the repo map is built from, and without it every
// call answers "The language parser is not available".
//
// Naming it unconditionally was the mirror image of a bug this repository has
// already reasoned about once. internal/prompts leaves symbol out of the
// run_code bullet on purpose — "prose promising a conditional tool is the bug
// this comment is about" — while the tool description, which is prose the model
// reads just as surely, promised it in every session. The prompt was right and
// the schema was wrong, which is the opposite of how that drift usually runs.
func (c *Coder) codeCallableTools() []string {
	names := InspectorTools()
	if c.RepoMap != nil {
		return names
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n != toolSymbol {
			out = append(out, n)
		}
	}
	return out
}

// codeToolParams is the positional order of each bridged tool's arguments,
// for a program that passes them positionally rather than in an options
// object (jsArgs).
//
// It has to be written out. A ToolDef's Parameters is a map, so it carries no
// order at all — and what the model is shown is Go's JSON marshalling of that
// map, which sorts the keys alphabetically. `grep` reaches the model as
// context_lines, glob, ignore_case, mode, path, pattern. No order a program
// could follow exists until one is chosen here.
//
// Chosen for the strongest prior a model has, not for the schema's source
// order, and the second slot is the only one where that took any thinking:
//
//   - grep's is `path`, following the shell's `grep PATTERN FILE`. The
//     alternative, glob, is what the tool's own description warns against —
//     "This, not glob, is how to restrict a search to a subtree" — and it is
//     also the safer error: `grep("foo", "src/")` meaning glob searches a
//     subtree instead of the tree, while the reverse treats a directory as a
//     glob and matches nothing.
//   - read's follows read_bin's documented signature, which is already in the
//     description the model reads.
//
// A positional past the end of this list is dropped, so the list is every
// parameter the schema has, not just the required one. codeparams_test.go
// holds it to the schemas.
var codeToolParams = map[string][]string{
	toolRead:   {"path", "offset", "limit"},
	toolGrep:   {"pattern", "path", "glob", "mode", "ignore_case", "context_lines"},
	toolGlob:   {"pattern"},
	toolLS:     {"path"},
	toolSymbol: {"name", "kind"},
}

// bridgeCall is the function that answers one bridged call. It is an adapter,
// not a reimplementation: the call is answered by the same Inspector.Run a
// direct tool call goes through, so what the program sees is byte-for-byte
// what the model would have seen.
//
// The tools the program actually called are collected as they happen —
// recording at the bridge rather than scanning the source, because a scan
// overreports: a comment naming read, a variable called ls, a call in a branch
// that never runs. Every real call passes through here, so this side has the
// truth without parsing anything.
func (c *Coder) bridgeCall(allowed []string, log *bridgeLog) bridgeFunc {
	funcs := codeFuncs
	isAllowed := make(map[string]bool, len(allowed)+len(funcs)+len(codeDataFuncs))
	for _, n := range allowed {
		isAllowed[n] = true
	}
	// The code functions are allowed by the same fail-closed check — they are
	// called from the same bridge, counted in the same cap, and announced the
	// same way. Their own dispatch happens below the check.
	for _, d := range funcs {
		isAllowed[d.name] = true
	}
	for _, d := range codeDataFuncs {
		isAllowed[d.name] = true
	}

	seen := map[string]bool{}
	return func(_ context.Context, call *bridgedCall) (any, error) {
		// Fail closed. A name outside `allowed` is not registered at all and
		// raises a ReferenceError inside the program — but the check lives
		// here too so the invariant does not depend on the registration list
		// staying in step with it.
		if !isAllowed[call.Name] {
			return nil, fmt.Errorf("unknown function %q: only the read-only tools "+
				"(%s) can be called from a program", call.Name, strings.Join(allowed, ", "))
		}
		if !seen[call.Name] {
			seen[call.Name] = true
			log.names = append(log.names, call.Name)
		}
		log.calls++
		if log.calls > maxBridgedCalls {
			return nil, fmt.Errorf("the program made more than %d tool calls", maxBridgedCalls)
		}

		// Code functions answer with data, not model text, so they bypass
		// Inspector.Run and the prefix classification — their errors are
		// already Go errors and become thrown Errors at the calling line.
		// No per-call announcement: every bridged call lands under the program
		// block that caused it (the ‹run_code› the user just read), and the
		// tools' own outcome lines plus the turn's "Ran N lines of code
		// calling …" summary say what happened. A "‹run_code› read" line per
		// call read as a separate action the model initiated — the confusion a
		// live session reported.
		if d := codeFuncByName(call.Name); d != nil {
			v, err := d.fn(c, call)
			log.last = v
			return v, err
		}
		// The data shapes go before Inspector.Run: they are the same tool
		// names, answered as data inside a program. The prose shapes stay
		// behind them untouched, for the model's direct calls.
		if d := codeDataFuncByName(call.Name); d != nil {
			v, err := d.fn(c, call)
			log.last = v
			return v, err
		}

		// The call is answered by the same Inspector.Run a direct call goes
		// through, so the outcome line and the model's answer are byte-for-byte
		// what a direct call would produce — for the tools whose result *is*
		// prose. The data-shaped ones (glob, ls) answered above, because a
		// program that computes over a result needs the result, not the report
		// about it: glob's prose names the pattern and explains syntax, and
		// sorting that prose sorts its characters.
		out := c.inspector().Run(call.Name, call.ArgsJSON())

		// A tool failure throws instead of returning, at the line that made
		// the call, where a try/catch can catch it like any other exception.
		// An *empty result* is not a failure: "No matches" and a symbol miss
		// are answers a program may legitimately filter on, so those stay
		// values.
		if msg, bad := bridgeToolFailure(out); bad {
			return nil, fmt.Errorf("%s failed: %s", call.Name, msg)
		}
		log.last = out
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

// codeLostCallsNote says so when the program's value cannot account for the
// calls the program made. Only the final value comes back, and a program whose
// calls do not reach it looks, from the model's side, exactly like a program
// whose calls found nothing.
//
// Both shapes were observed in the field, and they fail differently. A loop of
// read calls that keeps nothing returns a bare empty value after four
// successful reads, and the model read that as "the files do not exist" and
// went looking for them again — loud, and wrong in a way that costs a round
// trip. Two grep calls on consecutive lines return the second one's output and
// silently drop the first, so a model verifying two files has verified one and
// neither it nor the user can tell; the screen shows both searches happening.
// The second is the worse of the two for being quiet.
func codeLostCallsNote(result any, printed string, log *bridgeLog) string {
	if log == nil || log.calls == 0 {
		return ""
	}
	// Nothing came back at all: every result was discarded.
	if result == nil && printed == "" {
		return fmt.Sprintf("The program made %s and returned none of their results. "+
			"Only the program's final value comes back to you, so end it with what you "+
			"want to see, or console.log() as you go.",
			render.Plural(log.calls, "call", "calls"))
	}
	// The value *is* the last call's output, verbatim, and there were earlier
	// calls. Comparing against what the bridge actually returned is what keeps
	// this from firing on a program that computed over its results: only a bare
	// trailing call reproduces the string exactly.
	if log.calls >= 2 && sameBridgedValue(result, log.last) {
		return fmt.Sprintf("That is the last call's result. The program made %d, and the "+
			"earlier ones stayed inside it — only the final value comes back to you. "+
			"Collect what you need (an array or an object) and end the program with that.",
			log.calls)
	}
	return ""
}

// sameBridgedValue reports whether a program's value is one bridged call's
// return, unchanged. Compared as JSON, because a call now answers with data as
// often as with text — glob, grep and ls return arrays in a program — and the
// program's value has crossed goja on its way here, which changes Go types
// (an int comes back an int64) without changing what the model sees.
func sameBridgedValue(result, last any) bool {
	if result == nil || last == nil {
		return false
	}
	if rs, ok := result.(string); ok {
		ls, ok := last.(string)
		return ok && rs == ls
	}
	rj, err1 := json.Marshal(result)
	lj, err2 := json.Marshal(last)
	return err1 == nil && err2 == nil && bytes.Equal(rj, lj)
}

// jsNativeFrame is the stack frame goja appends for a Go function, which
// names the closure — "at dbohdan.com/…runCode.func7 (native)" — rather than
// anything the program did.
var jsNativeFrame = regexp.MustCompile(` at [^ ]+ \(native\)`)

// jsPosition finds the program line goja's message points at.
var jsPosition = regexp.MustCompile(`program\.js:(\d+):`)

// jsImport is an import statement, which a script cannot hold.
var jsImport = regexp.MustCompile(`(?m)^\s*import\s`)

// jsHostName matches the ReferenceError of a Node or browser global — the
// wrong reach JavaScript invites, as os and open were Python's.
var jsHostName = regexp.MustCompile(`\b(require|process|fs|path|fetch|module|exports|__dirname|__filename|Deno|Bun|window|document|setTimeout|setInterval|Buffer)\b is not defined`)

// jsKeywordCall is Python's keyword-argument habit in a JavaScript program:
// grep(pattern="x") assigns to an undeclared name, which strict mode rejects.
var jsKeywordCall = regexp.MustCompile(`\b(read|read_text|read_bin|grep|glob|ls|symbol)\s*\(\s*[a-z_]+\s*=[^=]`)

// jsErrorText renders a failure for the model: goja's exception, which names
// the error and the position, plus the line it points at — the line a
// traceback would have shown.
//
// Hints ride the error classes the model would otherwise have to work out for
// itself. They are the wrong-reach family, whose measured cost is one step per
// session (doc/experiments/2026-09-code-namespace/README.md) and whose
// description-side fixes all failed: the mistake is the first program of a
// session, written before any description is consulted. The error channel is
// the one that fires exactly when the mistake did, and costs nothing on
// correct programs; a hint never rides a wall the model could not have
// avoided.
// compileProgram compiles a program, accepting a top-level return.
//
// A program's value is its last expression, and the description says so, but
// models write `return x` anyway: many code-execution tools run a program as a
// function body, and a return at the top level can only mean "this is the
// result". MiMo did it with the description in front of it and spent a step on
// goja's "Illegal return statement". So a program that fails on exactly that is
// compiled again as the body of a function called at once, and its value is
// what it returns.
//
// The wrapper opens on the program's first line, so every line number an
// error reports is still the program's own; only columns on line 1 move. A
// program with a return and another syntax error fails on the other one,
// which, once return is legal, is the error worth reporting.
func compileProgram(code string) (*goja.Program, error) {
	prog, err := goja.Compile("program.js", code, true)
	if err == nil || !strings.Contains(err.Error(), "Illegal return statement") {
		return prog, err
	}
	return goja.Compile("program.js", "(function () {"+code+"\n})()", true)
}

func jsErrorText(err error, code string) string {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if v, ok := interrupted.Value().(error); ok && errors.Is(v, errHeapLimit) {
			return fmt.Sprintf("The program failed: it used more than %d MiB of memory.",
				codeLimits.MaxHeapGrowth>>20)
		}
		if _, ok := interrupted.Value().(string); ok {
			return fmt.Sprintf("The program failed: it ran past the %s time limit.", codeLimits.MaxDuration)
		}
		return "The program was stopped: the turn was interrupted."
	}
	msg := err.Error()
	line := 0
	var ex *goja.Exception
	if errors.As(err, &ex) {
		msg = ex.Error()
		// An error thrown by a bridged function names the Go closure, not the
		// program; the program line that made the call is the first frame of
		// the program's own on the stack.
		for _, f := range ex.Stack() {
			if f.SrcName() == "program.js" {
				line = f.Position().Line
				break
			}
		}
	}
	msg = jsNativeFrame.ReplaceAllString(msg, "")
	if m := jsPosition.FindStringSubmatch(msg); line == 0 && m != nil {
		_, _ = fmt.Sscan(m[1], &line)
	}
	full := "The program failed: " + msg
	if lines := strings.Split(code, "\n"); line >= 1 && line <= len(lines) {
		full += fmt.Sprintf("\n  line %d: %s", line, strings.TrimSpace(lines[line-1]))
	}
	if strings.Contains(msg, "is not defined") && jsKeywordCall.MatchString(code) {
		full += "\n\nArguments go in an options object: grep({pattern: \"TODO\", glob: \"**/*.go\"}), " +
			"not grep(pattern=\"TODO\")."
	}
	if strings.Contains(msg, "print is not defined") {
		full += "\n\nconsole.log() is how a program prints here."
	}
	if jsHostName.MatchString(msg) || (jsImport.MatchString(code) && strings.Contains(msg, "SyntaxError")) {
		full += "\n\nThis is not Node or a browser: require, import, fs, path, process, fetch and " +
			"timers do not exist. Use the glob, ls, read, read_text, and grep functions instead."
	}
	return full
}
