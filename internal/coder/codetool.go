package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
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
//
// "Empirical" is a standard the list failed once. It named math/re/datetime/json
// and said "other imports" were unavailable, while itertools and collections
// worked unadvertised and os, sys and pathlib imported fine before failing at
// the first attribute — which is exactly the green light that sent a model
// probing os for a filesystem. TestCodeDescriptionMatchesTheModulesThatWork now
// runs the imports it promises, in both directions, so the prose cannot drift
// from the interpreter again without a red test.

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
// symbol fix (doc/experiments/2026-08-symbol-uptake/README.md) established that the
// description that moves uptake is the one that opens by mapping the felt need
// ("I have several lookups to combine") to the tool; a spec sheet selects for
// nobody. The negations are compressed to one sentence and paired with the
// recovery path, because "grep always works; this opens with a failure
// surface" was the risk asymmetry the first version created.
func codeTool(callable []string, arm CodeResult, ns CodeNamespace) llm.ToolDef {
	var b strings.Builder
	b.WriteString("Do several lookups, or a computation, in one call instead of " +
		"several. Use this when one answer needs multiple read/grep/glob/ls " +
		"results combined, or needs arithmetic, counting, sorting, or date " +
		"math.\n\n" +
		codeCallStyleText(ns) +
		fmt.Sprintf(" — up to %d calls, each shown to the user like a direct call. Example:\n\n", maxBridgedCalls) +
		codeExampleText(arm, ns) +
		codeContractText(arm) +
		"The interpreter is Monty, a restricted Python subset. Expressions, " +
		"statements, loops, f-strings, comprehensions, try/except, classes, and " +
		"math, re, datetime, json, itertools and collections all work. Not " +
		"available: with, match, del, eval/exec, open, network access, and other " +
		"imports — os, sys and pathlib import but reach no filesystem, so use " +
		"glob(pattern=\"**/*.py\") to walk the tree and read() to open a file; " +
		"the bash tool, not this one, runs commands. " +
		"A missing construct raises an error naming it — simplify and rerun; a " +
		"failed program costs one cheap retry.")

	// The bridged names come from the caller, which builds one list for the
	// description, the Monty registration, and the bridge's allow check — so
	// the three cannot drift, the exact drift this repository has had three
	// times elsewhere. The example above names two; the authoritative list
	// rides behind it. The code functions (codefuncs.go) are run_code-only and
	// ride in through codeFuncDoc, from the same registry the bridge
	// dispatches on.
	if len(callable) > 0 {
		fmt.Fprintf(&b, "\n\nThe callable functions are exactly: %s.",
			strings.Join(nsQualified(callable, ns), ", "))
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

// codeExampleText is the worked example, in the shape the arm's contract calls
// for. An example that contradicts the paragraph above it would be the loudest
// thing in the description, and models copy the example.
func codeExampleText(arm CodeResult, ns CodeNamespace) string {
	g := nsQualify("grep", ns)
	body := "caps = {}\n" +
		"for name in [\"maxToolOutputBytes\", \"MaxSteps\", \"maxChatHistoryTokens\"]:\n" +
		"    caps[name] = " + g + "(pattern=name + \" =\", glob=\"**/*.go\")\n"
	switch arm {
	case CodeResultMain:
		body = "def main():\n" +
			"    caps = {}\n" +
			"    for name in [\"maxToolOutputBytes\", \"MaxSteps\", \"maxChatHistoryTokens\"]:\n" +
			"        caps[name] = " + g + "(pattern=name + \" =\", glob=\"**/*.go\")\n" +
			"    return caps\n"
	default:
		body += "caps\n"
	}
	return "```python\n" + body + "```\n\n"
}

// nsQualify writes a tool name the way the arm's programs call it.
func nsQualify(tool string, ns CodeNamespace) string {
	if ns == CodeNSBoth || ns == CodeNSOnly {
		return "tools." + tool
	}
	return tool
}

func nsQualified(tools []string, ns CodeNamespace) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, nsQualify(t, ns))
	}
	return out
}

// codeCallStyleText is the sentence the CodeNamespace arms differ in: how a
// program reaches the tools. Kept apart from the rest of the description for the
// same reason the result contract is — so the arms differ in one paragraph and
// its example, rather than in two rewritten descriptions.
func codeCallStyleText(ns CodeNamespace) string {
	switch ns {
	case CodeNSBoth:
		return "The tools live in a tools namespace the program can call directly — " +
			"tools.grep(pattern=\"TODO\", glob=\"**/*.go\"), tools.read(path=\"a.go\", limit=20). " +
			"print(tools) lists them with their arguments. The bare names work too"
	case CodeNSOnly:
		return "The tools live in a tools namespace the program calls through — " +
			"tools.grep(pattern=\"TODO\", glob=\"**/*.go\"), tools.read(path=\"a.go\", limit=20). " +
			"print(tools) lists them with their arguments. The bare names are not defined"
	default:
		return "The program can call the read-only tools directly — " +
			"grep(pattern=\"TODO\", glob=\"**/*.go\"), read(path=\"a.go\", limit=20)"
	}
}

// codeContractText is the one paragraph the CodeResult arms differ in: what the
// model is told comes back. Kept apart from the rest of the description so the
// arms differ in exactly this, which is what makes the trial a comparison of
// the contract rather than of two rewritten descriptions.
func codeContractText(arm CodeResult) string {
	switch arm {
	case CodeResultAll:
		return "Every call the program makes returns its result to you, in order, " +
			"followed by the program's final value. Compute over the results and " +
			"end with a conclusion when you can; the raw results come back either " +
			"way. print() shows anything else.\n\n"
	case CodeResultMain:
		return "Define a function called main and return what you want to see from " +
			"it; the program is run and then main() is called, and its return value " +
			"is what comes back. A program that returns nothing hands you None. " +
			"print() shows intermediate values.\n\n"
	default:
		return "Only the program's last evaluated value comes back to you, so end it " +
			"with what you want to see — two calls on two lines return the second " +
			"one's result and drop the first. print() shows intermediate values.\n\n"
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
	log := bridgeLog{keepEcho: c.CodeResult == CodeResultAll}
	// The main() arm runs the program to define things and then calls main(),
	// so the value comes from a return statement. A program that defines no
	// main raises NameError, which is the whole point: the failure is loud
	// rather than a quiet None.
	source := cc.code
	if c.CodeResult == CodeResultMain && !callsMain(cc.code) {
		source += "\n\nmain()"
	}
	// The namespace arms build `tools` ahead of the model's code, which shifts
	// every traceback line; the offset is subtracted back out below.
	prelude := codePrelude(c.CodeNamespace, c.codeCallableTools())
	source = prelude + source
	result, err := runner.Execute(context.Background(), source, nil,
		append(c.codeOptions(&log), monty.WithPrintFunc(func(s string) { printed.WriteString(s) }))...)
	summary := codeCalledText(codeLines(cc.code), log.names)
	if err != nil {
		// The calls made before the failure still happened, and a program that
		// aborted mid-way is precisely where the summary carries information
		// the value cannot.
		c.Out.Toolf("%s", summary)
		return c.codeFailureText(err, preludeLines(prelude))
	}
	c.Out.Toolf("%s", summary)
	return truncateResult(codeEchoText(&log) + codeResultText(result, printed.String(), &log))
}

// codeArgsText renders a bridged call's arguments the way the model wrote them,
// as keywords rather than as the JSON they crossed the boundary in. The echo is
// meant to read as the program's own lines coming back.
func codeArgsText(argsJSON string) string {
	var m map[string]any
	if json.Unmarshal([]byte(argsJSON), &m) != nil {
		return strings.TrimSpace(argsJSON)
	}
	parts := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		v, err := json.Marshal(m[k])
		if err != nil {
			continue
		}
		parts = append(parts, k+"="+string(v))
	}
	return strings.Join(parts, ", ")
}

// mainCallRE matches a top-level call to main() on its own line.
var mainCallRE = regexp.MustCompile(`(?m)^main\(\s*\)\s*$`)

// callsMain reports whether the program already calls main() itself, so the
// harness does not append a second call.
//
// Measured, not anticipated: 38 of 89 programs under this arm ended in main(),
// having been told the program is run and then main() is called. Appending
// unconditionally ran the whole program twice — every read repeated, every call
// counted twice against the bridge cap, and the arm's token cost roughly
// doubled, which would have been read as a fact about the design.
func callsMain(code string) bool {
	return mainCallRE.MatchString(code)
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
	// keepEcho records every call for CodeResultAll. Off by default so the
	// other arms do not accumulate results nobody will read.
	keepEcho bool
	// echo is one entry per call — name, arguments, result — kept only for
	// CodeResultAll, which hands them all back. Under the other arms it stays
	// nil rather than accumulating megabytes nobody reads.
	echo []bridgedCall
}

// bridgedCall is one call a program made, as CodeResultAll reports it.
type bridgedCall struct {
	name   string
	args   string
	result any
}

// CodeResult selects what a run_code program hands back. It exists to be
// measured: doc/experiments/2026-09-code-result/README.md is the trial, and until it
// reports, CodeResultLast is the shipped behaviour rather than the chosen one.
type CodeResult int

const (
	// CodeResultLast returns the program's final value, the way a Jupyter cell
	// echoes its last expression.
	CodeResultLast CodeResult = iota
	// CodeResultAll returns every bridged call's result and then the final
	// value, the way an interactive interpreter echoes each statement. The
	// hypothesis it tests: a model writing read() reads it as a tool call, and
	// everywhere else in this harness a tool call's result comes back.
	CodeResultAll
	// CodeResultMain runs the program and then calls main(), so the value comes
	// from a return statement rather than from whatever was evaluated last.
	CodeResultMain
)

func (r CodeResult) String() string {
	switch r {
	case CodeResultAll:
		return "all"
	case CodeResultMain:
		return "main"
	default:
		return "last"
	}
}

// ParseCodeResult reads an arm name, reporting whether it is one.
func ParseCodeResult(name string) (CodeResult, bool) {
	switch name {
	case "last":
		return CodeResultLast, true
	case "all":
		return CodeResultAll, true
	case "main":
		return CodeResultMain, true
	}
	return 0, false
}

// CodeResultNames lists the arms, for help text and errors.
var CodeResultNames = []string{"last", "all", "main"}

// codeEchoText renders every call a program made, the way an interactive
// interpreter would have as the program ran. The arguments come along because
// two greps differing only in their glob are otherwise two identical headings.
func codeEchoText(log *bridgeLog) string {
	if log == nil || len(log.echo) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range log.echo {
		fmt.Fprintf(&b, ">>> %s(%s)\n", e.name, codeArgsText(e.args))
		switch v := e.result.(type) {
		case string:
			b.WriteString(strings.TrimRight(v, "\n"))
		default:
			if data, err := json.Marshal(v); err == nil {
				b.Write(data)
			} else {
				fmt.Fprintf(&b, "%v", v)
			}
		}
		b.WriteString("\n\n")
	}
	return b.String()
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

// codeCallableTools is the one list behind the run_code description, the Monty
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

// codeOptions assembles the Execute options: the resource limits, plus the
// read-only bridge. log collects what the program actually did, for the outcome
// line and the result's note.
func (c *Coder) codeOptions(log *bridgeLog) []monty.ExecuteOption {
	opts := make([]monty.ExecuteOption, 0, 2)
	opts = append(opts, monty.WithLimits(codeLimits))

	names := c.codeCallableTools()
	funcs := make([]monty.FuncDef, 0, len(names)+len(codeFuncs))
	// Each tool takes its arguments as keywords; the params list is what lets
	// Monty map a positional call's arguments onto those names. The schemas
	// differ per tool (path, pattern, limit, …), so the program passes
	// everything by name and Monty hands over whatever it got — an argument
	// this side does not recognize is answered by the tool itself, exactly as
	// a direct call with a wrong field would be.
	for _, n := range names {
		funcs = append(funcs, monty.Func(c.CodeNamespace.wireName(n)))
	}
	// Code functions ride the same registration: positional calls map onto
	// their parameter names the same way, and an unregistered one raises
	// NameError inside the program, which is the fail-closed path.
	for _, d := range codeFuncs {
		funcs = append(funcs, monty.Func(d.name))
	}
	opts = append(opts, monty.WithExternalFunc(c.bridgeCall(names, log), funcs...))
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
func (c *Coder) bridgeCall(allowed []string, log *bridgeLog) monty.ExternalFunc {
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
	return func(_ context.Context, call *monty.FunctionCall) (any, error) {
		// Under the tools-only arm the functions are registered with a prefix
		// the program never types; the prelude binds tools.read to it. Strip it
		// before anything else looks at the name.
		call.Name = c.CodeNamespace.toolName(call.Name)
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
			log.names = append(log.names, call.Name)
		}
		log.calls++
		if log.calls > maxBridgedCalls {
			return nil, fmt.Errorf("the program made more than %d tool calls", maxBridgedCalls)
		}

		// Code functions answer with data, not model text, so they bypass
		// Inspector.Run and the prefix classification — their errors are
		// already Go errors and become exceptions with the right traceback.
		// No per-call announcement: every bridged call lands under the program
		// block that caused it (the ‹run_code› the user just read), and the
		// tools' own outcome lines plus the turn's "Ran N lines of code
		// calling …" summary say what happened. A "‹run_code› read" line per
		// call read as a separate action the model initiated — the confusion a
		// live session reported.
		if d := codeFuncByName(call.Name); d != nil {
			v, err := d.fn(c, call)
			log.last = v
			if log.keepEcho && err == nil {
				log.echo = append(log.echo, bridgedCall{name: call.Name, args: call.ArgsJSON(), result: v})
			}
			return v, err
		}

		// The call crosses the boundary as the same Inspector.Run a direct
		// call goes through, so the outcome line and the model's answer are
		// byte-for-byte what a direct call would produce.
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
		log.last = out
		if log.keepEcho {
			log.echo = append(log.echo, bridgedCall{name: call.Name, args: call.ArgsJSON(), result: out})
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
func codeResultText(result any, printed string, log *bridgeLog) string {
	// Under the echoing arm the last call's result has just been printed in
	// full; repeating it as the value doubles the largest thing in the reply.
	if log != nil && log.keepEcho && printed == "" && sameBridgedValue(result, log.last) {
		return ""
	}
	var b strings.Builder
	if printed != "" {
		b.WriteString(strings.TrimRight(printed, "\n"))
		if result != nil {
			b.WriteString("\n")
		}
	}
	switch result.(type) {
	case nil:
		// A print-driven program's None is the tail of its own output, not a
		// value worth naming; with nothing printed either, None is all there is.
		if printed == "" {
			b.WriteString("None")
		}
	case []any, map[string]any:
		if data, err := json.Marshal(result); err == nil {
			b.Write(data)
		} else {
			fmt.Fprintf(&b, "%v", result)
		}
	default:
		fmt.Fprintf(&b, "%v", result)
	}
	if note := codeLostCallsNote(result, printed, log); note != "" {
		// One blank line between the value and the note, whether or not the
		// value already ended in a newline — grep's content mode does, and two
		// blank lines read as a missing paragraph.
		return strings.TrimRight(b.String(), "\n") + "\n\n" + note
	}
	return b.String()
}

// codeLostCallsNote says so when the program's value cannot account for the
// calls the program made. Only the final value comes back, and a program whose
// calls do not reach it looks, from the model's side, exactly like a program
// whose calls found nothing.
//
// Both shapes were observed in the field, and they fail differently. A loop of
// read(path=…) that keeps nothing returns the bare word "None" after four
// successful reads, and the model read that as "the files do not exist" and
// went looking for them again — loud, and wrong in a way that costs a round
// trip. Two grep(…) statements on consecutive lines return the second one's
// output and silently drop the first, so a model verifying two files has
// verified one and neither it nor the user can tell; the screen shows both
// searches happening. The second is the worse of the two for being quiet.
//
// The note is model-facing text and therefore a hypothesis about behaviour
// rather than a style choice: whether it moves anything, and whether it pushes
// models toward printing everything (the counter-metric — result size), is
// measurable and not yet measured.
func codeLostCallsNote(result any, printed string, log *bridgeLog) string {
	if log == nil || log.calls == 0 {
		return ""
	}
	// Nothing is lost when every call's result is handed back, and a note
	// saying otherwise would contradict the contract the model was given.
	if log.keepEcho {
		return ""
	}
	// Nothing came back at all: every result was discarded.
	if result == nil && printed == "" {
		return fmt.Sprintf("The program made %s and returned none of their results. "+
			"Only the program's final value comes back to you, so end it with what you "+
			"want to see, or print() as you go.",
			render.Plural(log.calls, "call", "calls"))
	}
	// The value *is* the last call's output, verbatim, and there were earlier
	// calls. Comparing against what the bridge actually returned is what keeps
	// this from firing on a program that computed over its results: only a bare
	// trailing call reproduces the string exactly.
	if log.calls >= 2 && sameBridgedValue(result, log.last) {
		return fmt.Sprintf("That is the last call's result. The program made %d, and the "+
			"earlier ones stayed inside it — only the final value comes back to you. "+
			"Collect what you need (a list or a dict) and end the program with that.",
			log.calls)
	}
	return ""
}

// sameBridgedValue reports whether a program's value is one bridged call's
// return, unchanged. Strings only: the observation tools answer with text, and
// a code function's map is not a shape a program returns by accident.
func sameBridgedValue(result, last any) bool {
	rs, ok := result.(string)
	if !ok {
		return false
	}
	ls, ok := last.(string)
	return ok && rs == ls
}

// codeErrorText renders an execution failure for the model. The traceback's
// leading file framing is dropped because it is constant — every program is
// "script.py" from Monty's point of view — and the exception itself is what
// the model needs.
// codeFailureText renders a failed program for the model: the exception, with
// the prelude's lines subtracted from the traceback, plus — under the hint arm —
// a pointer to the tool that serves what the program reached for.
func (c *Coder) codeFailureText(err error, offset int) string {
	msg := renumberTraceback(codeErrorText(err), offset)
	if c.CodeNamespace == CodeNSHint && reachedForPython.MatchString(msg) {
		msg += codeReachHint
	}
	return msg
}

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
