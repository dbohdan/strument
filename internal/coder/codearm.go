package coder

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/monty"
	"dbohdan.com/strument/internal/render"
)

// The arms of doc/experiments/2026-09-run-code-arms: three ways of answering
// the first-program reach for a filesystem that is not there.
//
// A text-only probe (doc/experiments/2026-09-run-code-js) found that describing
// run_code as JavaScript took that reach from 11 of 97 first programs to 0 of
// 100, almost all of it MiMo's, and seven of the eleven the builtin open(). Two
// answers followed, and this file holds both behind one hidden flag so the
// trial runs one binary:
//
//   - monty-open: make the reach succeed. open(path) for reading and
//     Path(path).read_text() are answered through read_text, the function a
//     program is told to use instead, so the same containment binds them.
//   - js: take the reach away. goja runs the program, with the same bridge,
//     the same data shapes, the same caps, and hints in the same places.
//
// monty is the shipped behaviour and the default. Whichever arm loses is
// deleted with this comment when the trial reports.
const (
	codeArmMonty     = "monty"
	codeArmMontyOpen = "monty-open"
	codeArmJS        = "js"
)

func (c *Coder) codeArm() string {
	switch c.CodeArm {
	case codeArmMontyOpen, codeArmJS:
		return c.CodeArm
	}
	return codeArmMonty
}

// codeToolDef is the run_code schema for this session's arm.
func (c *Coder) codeToolDef() llm.ToolDef {
	callable := c.codeCallableTools()
	switch c.codeArm() {
	case codeArmJS:
		return codeToolJS(callable)
	case codeArmMontyOpen:
		def := codeTool(callable)
		def.Description = strings.Replace(def.Description, codeMissingSentence, codeMissingSentenceOpen, 1)
		return def
	}
	return codeTool(callable)
}

// codeMissingSentence is the shipped description's list of what Monty lacks,
// and codeMissingSentenceOpen the monty-open arm's. The arm drops open and
// with from the list: open is what it adds, and with works in the vendored
// Monty — probed, `with open(...) as f` reaches the open call — so the list
// was wrong about it, and a model told `with` is missing would not write the
// most ordinary way to open a file.
const (
	codeMissingSentence = "Not available: with, match, del, eval/exec, open, subprocess, network " +
		"access, and other imports — os, sys and pathlib import but reach no " +
		"filesystem, so use glob(pattern=\"**/*.py\") to walk the tree and " +
		"read() to open a file; the bash tool, not this one, runs commands."
	codeMissingSentenceOpen = "Project files can be read the ordinary way, read-only: open(path) — with " +
		"or without with — and Path(path).read_text() return a file's text, as read_text() does. " +
		"Not available: match, del, eval/exec, writing files, subprocess, network access, and " +
		"other imports — os and sys import but reach no filesystem, so use " +
		"glob(pattern=\"**/*.py\") to walk the tree; the bash tool, not this one, runs commands."
)

// codeToolsBullet renders the {code_tools} slot's bullet for the arm. Only the
// language's name differs: the probe's JS arm changed exactly this word in the
// system prompt, and the trial keeps what the probe measured.
func (c *Coder) codeToolsBullet(s string) string {
	if c.codeArm() == codeArmJS {
		return strings.Replace(s, "a short Python program", "a short JavaScript program", 1)
	}
	return s
}

// codeOsCallFunc is the OS-call handler for the arm. Only monty-open answers
// anything; the others refuse every call, as the shipped handler does.
func (c *Coder) codeOsCallFunc(bridge monty.ExternalFunc) monty.OsCallFunc {
	if c.codeArm() != codeArmMontyOpen {
		return codeOsCall
	}
	return func(ctx context.Context, call *monty.OsCall) (any, error) {
		switch call.Function {
		case "open":
			return c.codeOpen(call)
		case "Path.read_text":
			// Also where a file object's first read arrives: Monty buffers the
			// whole file on it and slices in the program. Answered as a
			// read_text call through the bridge, so it counts against the same
			// cap and names read_text in the outcome line.
			path, _ := osCallArg(call, 0)
			return bridge(ctx, &monty.FunctionCall{Name: readTextFunc.name, Args: map[string]any{"path": path}})
		}
		return codeOsCall(ctx, call)
	}
}

// codeOpen answers open(path, mode). Reading is the only mode: a program
// changes nothing, and the edit tools are how a file changes. Binary reads are
// refused towards read_bin, whose windowed shape is what binary data gets here.
// The file is checked now, so a missing one raises at the open line rather than
// at the first read.
func (c *Coder) codeOpen(call *monty.OsCall) (any, error) {
	path, _ := osCallArg(call, 0)
	mode, ok := osCallArg(call, 1)
	if !ok || mode == "" {
		mode = "r"
	}
	switch mode {
	case "r", "rt":
	case "rb":
		return nil, errors.New("binary files are read with read_bin(path, offset, limit), not open")
	default:
		return nil, fmt.Errorf("files open for reading only here (mode %q would change the file); "+
			"a program changes nothing — the edit and write tools do that", mode)
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("open requires a path")
	}
	if _, err := c.Files.Read(path, 0, 1); err != nil {
		//nolint:staticcheck // ST1005: continues Monty's "OS call … failed:" frame.
		return nil, fmt.Errorf("Could not read %s: %w", quoteToolArg(path), err)
	}
	return map[string]any{"$file": map[string]any{"path": path, "mode": "r"}}, nil
}

// osCallArg returns an OS call's i-th positional argument as a string.
func osCallArg(call *monty.OsCall, i int) (string, bool) {
	if i >= len(call.Args) {
		return "", false
	}
	s, ok := call.Args[i].(string)
	return s, ok
}

// codeHintFor is codeHintText for the arm: monty-open's hint says files can be
// read, which the shipped hint denies.
func (c *Coder) codeHintFor(full, msg string) string {
	if c.codeArm() == codeArmMontyOpen && strings.Contains(msg, "No module named '") {
		return full + "\n\nMonty has no subprocess and only a few modules (math, re, datetime, " +
			"json, itertools, collections); os and sys import but reach no filesystem. " +
			"open(path) and Path(path).read_text() read a file; the glob and ls functions " +
			"walk the tree."
	}
	return codeHintText(full, msg)
}

// --- The js arm ---------------------------------------------------------

// codeToolJS is the JavaScript description: the probe's text, sentence for
// sentence against the Monty one, with the callable list and the function
// summaries rendered from the same registries.
func codeToolJS(callable []string) llm.ToolDef {
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
		"```javascript\n" +
		"const caps = {};\n" +
		"for (const name of [\"maxToolOutputBytes\", \"MaxSteps\", \"maxChatHistoryTokens\"]) {\n" +
		"  caps[name] = grep({pattern: name + \" =\", glob: \"**/*.go\"});\n" +
		"}\n" +
		"caps\n" +
		"```\n\n" +
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
	if len(callable) > 0 {
		fmt.Fprintf(&b, "\n\nThe callable functions are exactly: %s.", strings.Join(callable, ", "))
	}
	b.WriteString("\n\nInside a program, glob and ls return data rather than the tools' " +
		"prose, and override them:\n")
	for _, d := range codeDataFuncs {
		fmt.Fprintf(&b, "- %s\n", jsSummaries[d.name])
	}
	b.WriteString("\n\nAlso callable, but only from inside a program (they return data for " +
		"the program, not text for you):\n")
	for _, d := range codeFuncs {
		fmt.Fprintf(&b, "- %s\n", jsSummaries[d.name])
	}
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

// jsSummaries are the registry's summaries in JavaScript's terms. Keyed by
// name so a function added to a registry without one fails
// TestJSSummariesCoverTheRegistries rather than rendering a blank line.
var jsSummaries = map[string]string{
	"glob": "glob({pattern}) returns the matching project-relative paths as an array of strings — " +
		"data for the program, unlike the glob tool's prose. Empty array when nothing matches. " +
		"The pattern is matched against the whole path, segment by segment; \"**/*.go\" reaches " +
		"every directory, \"*.go\" only the root, and a bare directory name matches nothing.",
	"ls": "ls({path: \"\"}) returns one directory's entries as an array of objects {path, is_dir, link} " +
		"sorted by path — data for the program, unlike the ls tool's prose. Empty path is the " +
		"project root; a directory under the standard temp directory is allowed too. " +
		"link is the symlink target, present only on symlinks.",
	"read_bin": "read_bin({path, offset: 0, limit: 4096}) reads a window of a file's raw bytes as " +
		"{size, offset, truncated, data} where data is an array of 0-255 numbers. For computing " +
		"over binary files (magic numbers, entropy, embedded strings); read is the " +
		"text-shaped one and refuses binaries.",
	"read_text": "read_text({path, offset: 0, limit: 0}) returns a file's text exactly as stored — no " +
		"line numbers, no header — for computing over contents (lengths, parsing, hashing, " +
		"counting). It keeps the file's final newline, so text.split(\"\\n\") ends with an empty " +
		"string; drop it before counting lines. read is the one to use when the answer cites " +
		"a line number.",
}

// jsStringify renders a value the way the result and console.log show it:
// JSON, with Map and Set turned into what they hold rather than the {} that
// JSON.stringify gives them.
const jsStringify = `(function (v) {
  return JSON.stringify(v, function (k, x) {
    if (x instanceof Map) return Object.fromEntries(x);
    if (x instanceof Set) return Array.from(x);
    return x;
  });
})`

// runCodeJS is runCode for the js arm. The shape is the same — announce the
// block, run with the bridge, render printed output then the value, append
// the lost-calls note, truncate — so the arms differ in the language and in
// nothing a model could notice about the harness.
func (c *Coder) runCodeJS(cc codeCall) string {
	c.Out.ToolBlock(render.CodeOpen, cc.code)

	vm := goja.New()
	// Deep recursion is an error, not a crashed process; the same bound Monty
	// has, in frames.
	vm.SetMaxCallStackSize(int(codeLimits.MaxRecursionDepth) * 10)

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
			args := jsArgs(call.Arguments, params)
			v, err := bridge(context.Background(), &monty.FunctionCall{Name: name, Args: args})
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

	timer := time.AfterFunc(codeLimits.MaxDuration, func() { vm.Interrupt("time limit") })
	defer timer.Stop()

	var value goja.Value
	prog, err := goja.Compile("program.js", cc.code, true)
	if err == nil {
		value, err = vm.RunProgram(prog)
	}
	summary := codeCalledText(codeLines(cc.code), log.names)
	c.Out.Toolf("%s", summary)
	if err != nil {
		return jsErrorText(err, cc.code)
	}

	var result any
	if value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		result = value.Export()
	}
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
		note = strings.ReplaceAll(note, "print()", "console.log()")
		return truncateResult(strings.TrimRight(b.String(), "\n") + "\n\n" + note)
	}
	return truncateResult(b.String())
}

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

// jsHostName matches the ReferenceError of a Node or browser global — the JS
// arm's version of the wrong reach, answered in the same channel and the same
// words as Monty's ModuleNotFoundError hint.
var jsHostName = regexp.MustCompile(`\b(require|process|fs|path|fetch|module|exports|__dirname|__filename|Deno|Bun|window|document|setTimeout|setInterval|Buffer)\b is not defined`)

// jsNativeFrame is the stack frame goja appends for a Go function.
var jsNativeFrame = regexp.MustCompile(` at [^ ]+ \(native\)`)

// jsKeywordCall is Python's keyword-argument habit in a JavaScript program:
// grep(pattern="x") assigns to an undeclared name, which strict mode rejects.
var jsKeywordCall = regexp.MustCompile(`\b(read|read_text|read_bin|grep|glob|ls|symbol)\s*\(\s*[a-z_]+\s*=[^=]`)

// jsErrorText renders a failure for the model: goja's exception, which names
// the error and the position, plus the line it points at.
func jsErrorText(err error, code string) string {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		return fmt.Sprintf("The program failed: it ran past the %s time limit.", codeLimits.MaxDuration)
	}
	msg := err.Error()
	var ex *goja.Exception
	if errors.As(err, &ex) {
		msg = ex.Error()
	}
	// An error thrown from a bridged function carries the Go closure as its
	// frame — "at dbohdan.com/…runCodeJS.func7 (native)" — which names nothing
	// the program did.
	msg = jsNativeFrame.ReplaceAllString(msg, "")
	full := "The program failed: " + msg
	if m := regexp.MustCompile(`program\.js:(\d+):`).FindStringSubmatch(msg); m != nil {
		var n int
		_, _ = fmt.Sscan(m[1], &n)
		if lines := strings.Split(code, "\n"); n >= 1 && n <= len(lines) {
			full += fmt.Sprintf("\n  line %d: %s", n, strings.TrimSpace(lines[n-1]))
		}
	}
	importStmt := regexp.MustCompile(`(?m)^\s*import\s`).MatchString(code)
	if strings.Contains(msg, "is not defined") && jsKeywordCall.MatchString(code) {
		full += "\n\nArguments go in an options object: grep({pattern: \"TODO\", glob: \"**/*.go\"}), " +
			"not grep(pattern=\"TODO\")."
	}
	if strings.Contains(msg, "print is not defined") {
		full += "\n\nconsole.log() is how a program prints here."
	}
	if jsHostName.MatchString(msg) || (importStmt && strings.Contains(msg, "SyntaxError")) {
		full += "\n\nThis is not Node or a browser: require, import, fs, path, process, fetch and " +
			"timers do not exist. Use the glob, ls, read, read_text, and grep functions instead."
	}
	return full
}
