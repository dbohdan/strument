package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/repomap"
)

// The run_code tool's tests. The interpreter's behavior is pinned here at the
// level the model sees — the returned value and the error text — because that
// is the contract the tool description promises. The security claims (no
// filesystem, no network) are tested, not assumed: each program below was
// verified to fail, and stays red if a goja upgrade ever makes it pass.

func run(c *Coder, code string) string {
	return c.runCode(context.Background(), codeCall{code: code})
}

func TestCodeArithmetic(t *testing.T) {
	c, _ := observeEnv(t, nil)

	for _, tc := range []struct {
		code, want string
	}{
		{"1 + 2", "3"},
		{"2.5 * 4", "10"},
		{"Math.round(3.14159 * 100) / 100", "3.14"},
		{"[...Array(10).keys()].reduce((s, x) => s + x * x, 0)", "285"},
		{"String(42).padStart(8, '0')", "00000042"},
		{"Math.sqrt(1764)", "42"},
		{"(0.1 + 0.2).toFixed(2)", "0.30"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			if got := run(c, tc.code); got != tc.want {
				t.Errorf("code %q: got %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// TestCodePrintOutputIsDelivered: most model programs end in console.log(...)
// rather than a bare expression, and a program's printed output must reach the
// model, printed section before the final value; a bare-expression program is
// unchanged. The same fix was made for Monty's print(), whose missing handler
// returned "None" for every print-driven program.
func TestCodePrintOutputIsDelivered(t *testing.T) {
	c, _ := observeEnv(t, nil)

	if got := run(c, "console.log(\"hello\")\nconsole.log(\"world\")"); got != "hello\nworld" {
		t.Errorf("a console.log-driven program returned %q, want its printed output", got)
	}
	if got := run(c, "const x = 6\nconsole.log(x)\nx * 7"); got != "6\n42" {
		t.Errorf("console.log plus a final value returned %q, want \"6\\n42\"", got)
	}
	if got := run(c, "1 + 2"); got != "3" {
		t.Errorf("a bare expression returned %q, want \"3\"", got)
	}
	// Objects print as JSON, Maps and Sets as what they hold, and several
	// arguments on one line.
	if got := run(c, `console.log("x", [1, 2], new Map([["k", 1]])); new Set([3])`); got != "x [1,2] {\"k\":1}\n[3]" {
		t.Errorf("mixed console.log arguments returned %q", got)
	}
	if got := run(c, "let x"); got != "undefined" {
		t.Errorf("a program with no value returned %q, want \"undefined\"", got)
	}
}

// TestCodeInfiniteLoopTerminates is the duration limit's whole point: a
// runaway loop ends on the limit rather than hanging the turn.
func TestCodeInfiniteLoopTerminates(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the time limit")
	}
	c, _ := observeEnv(t, nil)

	start := time.Now()
	got := run(c, "while (true) {}")
	elapsed := time.Since(start)

	if !strings.Contains(got, "time limit") {
		t.Errorf("an infinite loop must return the time-limit error, got: %q", got)
	}
	if elapsed > 30*time.Second {
		t.Errorf("the duration limit did not fire; ran %v", elapsed)
	}
}

// TestCodeMemoryBombTerminates is the memory limit: an allocation loop ends on
// the limit rather than taking the process with it. goja has no memory limit
// of its own; watchProgram's heap sampling is the one this pins.
func TestCodeMemoryBombTerminates(t *testing.T) {
	c, _ := observeEnv(t, nil)

	got := run(c, "const xs = [];\nwhile (true) { xs.push(new Array(100000).fill(0)); }")
	if !strings.Contains(got, "memory") && !strings.Contains(got, "time limit") {
		t.Errorf("a memory bomb must end on a limit, got: %q", got)
	}
}

// TestCodeDeepRecursionIsAnError: recursion ends in an error the program's
// author can read, not a crashed process.
func TestCodeDeepRecursionIsAnError(t *testing.T) {
	c, _ := observeEnv(t, nil)
	got := run(c, "function f(n) { return f(n + 1) }\nf(0)")
	if !strings.Contains(got, "The program failed") {
		t.Errorf("unbounded recursion must fail, got: %q", got)
	}
}

// TestCodeSyntaxErrorIsUseful: a syntax error names itself and the line, and
// the turn survives it.
func TestCodeSyntaxErrorIsUseful(t *testing.T) {
	c, _ := observeEnv(t, nil)
	got := run(c, "const a = 1;\nconst = 2;")
	if !strings.Contains(got, "SyntaxError") {
		t.Errorf("a syntax error must name itself, got: %q", got)
	}
}

// TestCodeToolOfferedInAskMode: computing mutates nothing, so a discussion
// turn gets the calculator too.
func TestCodeToolOfferedInAskMode(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.OfferCode = true
	for _, format := range []string{"ask", "tool"} {
		c.editFormat = format
		if !slices.ContainsFunc(c.toolDefs(), func(d llm.ToolDef) bool { return d.Name == toolRunCode }) {
			t.Errorf("the run_code tool must be offered in %s mode", format)
		}
	}
}

// TestCodeDescriptionNamesTheHost checks the description stays honest about
// what is missing. JavaScript is the whole language, so what it names is the
// host, and each name here corresponds to a probe in TestCodeHasNoHost.
func TestCodeDescriptionNamesTheHost(t *testing.T) {
	desc := codeTool(InspectorTools()).Description
	for _, want := range []string{"goja", "not Node or a browser", "require", "fs", "process", "fetch", "bash tool", "console.log()"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the description must mention %q:\n%s", want, desc)
		}
	}
	for _, gone := range []string{"Python", "Monty", "print()"} {
		if strings.Contains(desc, gone) {
			t.Errorf("the description still says %q:\n%s", gone, desc)
		}
	}
}

// TestCodeHasNoHost is the security claim, tested not assumed: no filesystem,
// no processes, no network, no timers. goja provides none of them, and a reach
// for any is a ReferenceError the hint then answers. If an upgrade makes any of
// these defined, that upgrade must not ship without a look.
func TestCodeHasNoHost(t *testing.T) {
	c, _ := observeEnv(t, nil)

	for _, code := range []string{
		`require("fs")`,
		`fs.readFileSync("/etc/passwd")`,
		`process.cwd()`,
		`process.env.HOME`,
		`fetch("http://localhost:1")`,
		`new XMLHttpRequest()`,
		`setTimeout(() => 1, 0)`,
		`Deno.readTextFile("/etc/passwd")`,
		`import fs from "fs"`,
		`import("fs")`,
	} {
		t.Run(code, func(t *testing.T) {
			if got := run(c, code); !strings.Contains(got, "The program failed") {
				t.Errorf("host access must fail and did not:\n%s", got)
			}
		})
	}
}

// TestCodeHostHintRidesTheRightError checks the error-channel hint lands on the
// wrong-reach failures and on nothing else: a reach for Node or the browser
// gets the substitutes, while a wall the model could not have avoided — a
// typo'd name, a syntax error — is returned bare. The hint is model-facing
// text; on the wrong failure it would be noise on every legitimate retry.
func TestCodeHostHintRidesTheRightError(t *testing.T) {
	c, _ := observeEnv(t, nil)
	const hint = "This is not Node or a browser"

	for _, code := range []string{`const fs = require("fs")`, `process.cwd()`, `import fs from "fs"`, `fetch("x")`} {
		t.Run(code, func(t *testing.T) {
			if got := run(c, code); !strings.Contains(got, hint) {
				t.Errorf("the hint must ride the wrong-reach failure:\n%s", got)
			}
		})
	}
	for _, code := range []string{"undefinedName + 1", "const = 1", "null.x"} {
		t.Run(code, func(t *testing.T) {
			if got := run(c, code); strings.Contains(got, hint) {
				t.Errorf("the hint must not ride an unrelated failure:\n%s", got)
			}
		})
	}
	// Python's two habits get their own answers.
	if got := run(c, "print(1)"); !strings.Contains(got, "console.log()") {
		t.Errorf("print() must be answered with console.log:\n%s", got)
	}
	if got := run(c, `grep(pattern="x")`); !strings.Contains(got, "options object") {
		t.Errorf("a keyword-argument call must be answered with the options object:\n%s", got)
	}
}

// TestCodeErrorNamesTheLine: the error carries the line it points at — what a
// traceback would have shown — and not the Go closure goja names for a bridged
// function.
func TestCodeErrorNamesTheLine(t *testing.T) {
	c, _ := observeEnv(t, nil)
	if got := run(c, "const a = 1;\nundefinedName + 1"); !strings.Contains(got, "line 2: undefinedName + 1") {
		t.Errorf("the error must quote the failing line:\n%s", got)
	}
	got := run(c, `read({path: "missing.txt"})`)
	if !strings.Contains(got, "Could not read") {
		t.Errorf("a bridged failure must carry the tool's sentence:\n%s", got)
	}
	if strings.Contains(got, "(native)") || strings.Contains(got, "runCode") {
		t.Errorf("a Go frame leaked into the error:\n%s", got)
	}
}

// --- the read-only bridge -------------------------------------------------

// TestCodeBridgeReadReturnsFileContents: a program calling read() gets the
// file's contents — the same text a direct call returns.
func TestCodeBridgeReadReturnsFileContents(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"a.go": "package a\n\nfunc F() {}\n",
	})
	if got := run(c, `read({path: "a.go"})`); !strings.Contains(got, "package a") {
		t.Errorf("a bridged read must return the file's contents, got:\n%s", got)
	}
}

// TestCodeBridgeGrepFilterEndToEnd is the phenomenon the bridge exists for:
// search inside the program, filter the results, return the computed answer —
// one round trip instead of two. grep answers a program with data, so the
// filter works on paths rather than on the tool's prose.
func TestCodeBridgeGrepFilterEndToEnd(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"a.go":  "package a\n\nfunc Target() {}\n",
		"b.go":  "package b\n\n// Target again\n",
		"c.txt": "Target here too\n",
	})

	got := run(c, `const out = grep({pattern: "Target", mode: "files"});
const lines = out.filter(l => l.endsWith(".go"));
[lines, lines.includes("c.txt")]`)
	if !strings.Contains(got, `"a.go"`) || !strings.Contains(got, `"b.go"`) {
		t.Errorf("expected both .go files in the filtered result, got:\n%s", got)
	}
	if !strings.Contains(got, "false") {
		t.Errorf("the filter must have excluded c.txt, got:\n%s", got)
	}
}

// TestCodeGlobReturnsData pins the fix for the incident this shape exists for:
// a program calling glob() gets the paths as an array, not the tool's prose.
// The live failure was sorting the prose — which sorts its characters —
// turning one call into 49 junk tool calls under the cap. An array cannot be
// mistaken for prose, and an empty match is an empty array, a value the
// program filters on, not an error.
func TestCodeGlobReturnsData(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"a.go":         "package a\n",
		"sub/b.go":     "package b\n",
		"sub/deep.txt": "x\n",
	})

	if got := run(c, `glob("*.go")`); !strings.Contains(got, `["a.go"]`) {
		t.Errorf("glob must return the paths as a JSON array, got:\n%s", got)
	}
	got := run(c, `glob({pattern: "**/*.go"}).map(p => p)`)
	if !strings.Contains(got, `"a.go"`) || !strings.Contains(got, `"sub/b.go"`) {
		t.Errorf("glob must return every matching path, got:\n%s", got)
	}
	if got := run(c, `glob("*.rs")`); strings.TrimSpace(got) != `[]` {
		t.Errorf("an empty glob must be an empty array, got:\n%s", got)
	}
	// The failure mode the shape removed, pinned as the program that once
	// mangled it: sorting a glob result sorts paths, not characters.
	got = run(c, `glob("**/*.go").sort()`)
	if !strings.Contains(got, `"a.go"`) || !strings.Contains(got, `"sub/b.go"`) {
		t.Errorf("sorting the glob result must sort paths, got:\n%s", got)
	}
}

// TestCodeLSReturnsData covers the data shape's other half: entries as
// objects, the is_dir flag a program needs to walk a tree, and link only on
// symlinks — the same three facts the tool's prose renders, in the shape a
// program computes over. The temp-directory exemption is asserted too, because
// it is why the function exists at all: /tmp clones were what the misbehaving
// program was actually trying to enumerate.
func TestCodeLSReturnsData(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"a.go": "package a\n"})

	entries := run(c, `ls()`)
	if !strings.Contains(entries, `"path":"a.go"`) || !strings.Contains(entries, `"is_dir":false`) {
		t.Errorf("ls must return entries as objects, got:\n%s", entries)
	}

	temp := t.TempDir()
	if err := os.WriteFile(filepath.Join(temp, "note.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries = run(c, fmt.Sprintf("ls({path: %q})", temp))
	if !strings.Contains(entries, "note.txt") || !strings.Contains(entries, `"is_dir":false`) {
		t.Errorf("ls must reach a temp directory like the tool does, got:\n%s", entries)
	}

	// A missing directory is a failure the program can catch.
	got := run(c, "try { ls({path: \"missing\"}) } catch (e) { console.log(\"caught\") }")
	if !strings.Contains(got, "caught") {
		t.Errorf("a failed ls must throw inside the program, got:\n%s", got)
	}
}

// TestCodeGlobLSOverrideTheToolNames: inside a program the data shape answers
// the same names the tools answer, so a program written against the tool
// description — glob({pattern: "..."}) — gets data, not the tool's report
// about the search.
func TestCodeGlobLSOverrideTheToolNames(t *testing.T) {
	c, out := observeEnv(t, map[string]string{"a.go": "package a\n"})

	if got := run(c, `glob({pattern: "*.go"})`); !strings.Contains(got, `["a.go"]`) {
		t.Errorf("glob({pattern}) must return data like glob(...), got:\n%s", got)
	}
	if joined := strings.Join(out.lines, "\n"); strings.Contains(joined, "Matched") {
		t.Errorf("the data shape must not run the tool's outcome line, got:\n%s", joined)
	}
}

// TestCodeBridgeGrepReturnsData pins grep's data shape in each mode, and the
// two results that raise rather than return: a scope that admitted nothing,
// and a cut result. It replaced a test pinning grep's prose, after a live
// program took count mode's header line for a file. read still crosses as the
// text a direct call produces.
func TestCodeBridgeGrepReturnsData(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"a.go": "// Target one\nx\n// Target two\n",
		"b.go": "// Target\n",
	})
	for _, tc := range []struct{ code, want string }{
		{`grep({pattern: "Target"})`, `["a.go","b.go"]`},
		// Fields read out explicitly: the objects' key order is Go's map order.
		{`grep({pattern: "Target", mode: "count"}).map(r => r.path + "=" + r.count)`, `["a.go=2","b.go=1"]`},
		{`grep({pattern: "Target", glob: "b.go", mode: "content"}).map(r => [r.path, r.line, r.text, r.match].join("|"))`,
			`["b.go|1|// Target|true"]`},
		{`grep({pattern: "one", glob: "a.go", context_lines: 1}).map(r => r.line + ":" + r.match)`, `["1:true","2:false"]`},
		{`grep("Target", "", "b.go")`, `["b.go"]`}, // positional, in the tool's order
	} {
		got := strings.ReplaceAll(run(c, "JSON.stringify("+tc.code+")"), `\"`, `"`)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s\n got %s\nwant %s", tc.code, got, tc.want)
		}
	}

	if got := run(c, `grep({pattern: "Target", glob: "*.rs"})`); !strings.Contains(got, "never tested") {
		t.Errorf("a scope that admits no files must raise, not return an empty array:\n%s", got)
	}
	c.Files.Limits.MaxMatches = 1
	if got := run(c, `grep({pattern: "Target", mode: "content"})`); !strings.Contains(got, "past its limit") {
		t.Errorf("a cut result must raise:\n%s", got)
	}

	if got := run(c, `read({path: "b.go"})`); !strings.Contains(got, "1\t// Target") {
		t.Errorf("read must still return the tool's text, got:\n%s", got)
	}
}

// TestCodeBridgeForbiddenToolsFail is the fail-closed claim, one test per
// forbidden tool. Two layers both hold: a mutating tool is not registered in
// the interpreter, so the program itself raises a ReferenceError — nothing
// this side can answer — and even a name that were registered cannot pass the
// allowlist check in bridgeCall.
func TestCodeBridgeForbiddenToolsFail(t *testing.T) {
	c, _ := observeEnv(t, nil)

	for _, name := range []string{"bash", "edit", "write", "commit", "check"} {
		t.Run(name, func(t *testing.T) {
			got := run(c, name+`({command: "rm -rf /"})`)
			if !strings.Contains(got, "The program failed") || !strings.Contains(got, "not defined") {
				t.Errorf("calling %s from a program must be a ReferenceError, got:\n%s", name, got)
			}
		})
	}
}

// TestCodeBridgeCapFires: one program cannot issue unbounded work. The cap is
// maxBridgedCalls; a program that loops past it is stopped, at the line that
// made the call. Uncaught on purpose: a try/catch can swallow the cap error
// and keep calling — correct JavaScript, and the time limit bounds it — so the
// cap's own error is pinned in its uncaught form, where it stops the program.
func TestCodeBridgeCapFires(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"f.txt": "x\n"})
	got := run(c, "while (true) {\n  ls()\n}")
	if !strings.Contains(got, "more than") || !strings.Contains(got, "line 2") {
		t.Errorf("the bridged-call cap must fire with line attribution, got:\n%s", got)
	}
}

// TestCodeBridgeCallsAreNotAnnounced pins the shape the user reads: a bridged
// call prints only the tool's own outcome line, under the ‹run_code› program
// block that caused it. The per-call "‹run_code› ls" announcement was removed
// after a live session: it read as a separate action the model initiated, when
// it was downstream of the program already on screen, and the turn's "Ran N
// lines of code calling …" summary already attributes the run.
func TestCodeBridgeCallsAreNotAnnounced(t *testing.T) {
	c, out := observeEnv(t, map[string]string{"f.txt": "x\n"})

	run(c, `ls()`)

	joined := strings.Join(out.lines, "\n")
	if !strings.Contains(joined, "‹run_code› ls()\n") {
		t.Errorf("the program must be announced, got:\n%s", joined)
	}
	if strings.Contains(joined, "‹run_code› ls\n") {
		t.Errorf("a bridged call must not be announced separately, got:\n%s", joined)
	}
	if !strings.Contains(joined, "Ran 1 line of code calling ls.") {
		t.Errorf("the outcome line must name the called tools, got:\n%s", joined)
	}
}

// TestCodeProgramShapes pins the two renderings of one run_code call, the
// ask_user_question pattern: a shaped block for the screen (one line stays
// one line; a multi-line program is bracketed, its lines preserved) and one
// prose line for the transcript.
func TestCodeProgramShapes(t *testing.T) {
	c, out := observeEnv(t, map[string]string{"f.txt": "x\n"})

	run(c, "const x = 40\nconst y = 2\nx + y")

	joined := strings.Join(out.lines, "\n")
	if !strings.Contains(joined, "‹run_code›\nconst x = 40\nconst y = 2\nx + y\n‹/›") {
		t.Errorf("a multi-line program must render as a bracketed block with its lines preserved, got:\n%s", joined)
	}
	if !strings.Contains(joined, "Ran 3 lines of code.") {
		t.Errorf("a call-free program must be summarized without tools, got:\n%s", joined)
	}
}

// TestCodeSummaryAfterFailure pins the outcome line on the error path: a
// program that aborted mid-way still called what it called, and the summary is
// precisely where that survives — the value cannot report it.
func TestCodeSummaryAfterFailure(t *testing.T) {
	c, out := observeEnv(t, map[string]string{"f.txt": "x\n"})

	run(c, "const a = read({path: 'f.txt'})\nconst b = read({path: 'missing'})\nb")

	joined := strings.Join(out.lines, "\n")
	if !strings.Contains(joined, "Ran 3 lines of code calling read.") {
		t.Errorf("the outcome line must survive a failed program, got:\n%s", joined)
	}
	if got := run(c, "read({path: 'missing'})"); !strings.Contains(got, "The program failed:") {
		t.Errorf("the error text must still reach the model, got:\n%s", got)
	}
}

// --- results the program did not keep -------------------------------------

// TestCodeDiscardedResultsAreNamed covers the two field reports that motivated
// the note, and — in the same table — the shapes it must stay quiet for.
//
// Both failures come from the same fact: only the final value reaches the
// model. A loop of reads that keeps nothing returns an empty value after
// reading four files successfully, which one model read as "the files do not
// exist" before going to look for them again. Two calls on two lines return
// the second one's result and drop the first, which is quieter and worse: the
// screen shows both searches happening, so neither the model nor the user can
// see that half the evidence never arrived.
//
// The negative rows are the half that makes this a measurement. A program that
// collects its results, one that prints them, and one that makes a single call
// have lost nothing, and a note on those would be noise on every correct
// program.
func TestCodeDiscardedResultsAreNamed(t *testing.T) {
	files := map[string]string{
		"a.py": "import x  # NEEDLE\n",
		"b.py": "import y  # NEEDLE\n",
	}

	for _, tc := range []struct {
		name, code string
		wantNote   bool
	}{
		{"loop keeps nothing", `["a.py", "b.py"].forEach(p => read({path: p}))`, true},
		{"for loop keeps only the last", `for (const p of ["a.py", "b.py"]) { read({path: p}) }`, true},
		{"two bare calls, first dropped", "grep({pattern: \"NEEDLE\", glob: \"a.py\"})\ngrep({pattern: \"NEEDLE\", glob: \"b.py\"})", true},
		{"results collected", `const hits = {}; for (const p of ["a.py", "b.py"]) { hits[p] = read({path: p}) } hits`, false},
		{"results printed", `for (const p of ["a.py", "b.py"]) { console.log(read({path: p})) }`, false},
		{"one call, returned", `read({path: "a.py"})`, false},
		{"no calls at all", "const x = 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := observeEnv(t, files)
			got := run(c, tc.code)
			if noted := strings.Contains(got, "comes back to you"); noted != tc.wantNote {
				t.Errorf("note present = %v, want %v:\n%s", noted, tc.wantNote, got)
			}
			if tc.wantNote && strings.HasPrefix(tc.code, "grep") && !strings.Contains(got, "b.py") {
				t.Errorf("the note swallowed the value it annotates:\n%s", got)
			}
		})
	}
}

// The two notes say different things, because the two mistakes have different
// repairs: one program kept nothing, the other kept all but one.
func TestCodeDiscardedResultsSayWhichShape(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"a.py": "x = 1  # NEEDLE\n", "b.py": "y = 2  # NEEDLE\n"})

	// forEach is the JavaScript loop that keeps nothing: a for loop's value is
	// its last statement's, so it keeps the last call — the other shape.
	none := run(c, `["a.py", "b.py"].forEach(p => read({path: p}))`)
	if !strings.Contains(none, "returned none of their results") || !strings.Contains(none, "2 calls") {
		t.Errorf("a program that kept nothing must be told so, with the count:\n%s", none)
	}
	if !strings.Contains(none, "console.log()") || strings.Contains(none, "print()") {
		t.Errorf("the note must name the language's printing:\n%s", none)
	}

	last := run(c, "grep({pattern: \"NEEDLE\", glob: \"a.py\"})\ngrep({pattern: \"NEEDLE\", glob: \"b.py\"})")
	if !strings.Contains(last, "That is the last call's result") {
		t.Errorf("a program that returned only its last call must be told so:\n%s", last)
	}
}

// TestCodeDataFuncsOverrideTheBridge pins the two-shape contract from both
// sides: inside a program glob and ls return data (the bridge dispatches them
// before Inspector.Run), and the description the model reads says so, because
// a program written expecting the tool's prose — the failure that motivated
// the change — must meet an array, and a model reading the description must be
// told the names are overridden.
func TestCodeDataFuncsOverrideTheBridge(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"a.go": "package a\n"})
	desc := codeTool(InspectorTools()).Description

	if !strings.Contains(desc, "return data rather than the tools' prose") {
		t.Errorf("the description must state the override:\n%s", desc)
	}
	for _, want := range []string{"glob({pattern})", "array of strings", "{path, is_dir, link}"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the description must name the data shape (%q):\n%s", want, desc)
		}
	}
	for _, tc := range []struct{ code, want string }{
		{`glob("*.go")`, `["a.go"]`},
		{`ls().filter(e => e.is_dir).map(e => e.path)`, `[]`},
	} {
		if got := run(c, tc.code); !strings.Contains(got, tc.want) {
			t.Errorf("%s must return %s-shaped data, got:\n%s", tc.code, tc.want, got)
		}
	}
}

// symbol is offered exactly where a repo map is, and everything that tells the
// model about the run_code bridge must follow that condition — the schema's
// callable list, the registration behind it, and the prompt bullet.
//
// The asymmetry this fixes ran the unusual way round. internal/prompts leaves
// symbol out of the run_code bullet deliberately, on the rule that prose must
// not promise a conditional tool; the tool *description*, which is prose the
// model reads just as surely, named it in every session including the ones
// where every call would answer "The language parser is not available". So the
// prompt was right and the schema was wrong.
//
// Both directions are asserted, because a check that only looks for symbol's
// presence passes for a list that always includes it — which is the bug.
func TestCodeCallableListFollowsTheRepoMap(t *testing.T) {
	withMap, _ := observeEnv(t, map[string]string{"a.go": "package a\n\nfunc Target() {}\n"})
	withMap.RepoMap = repomap.New(withMap.Root)
	withMap.OfferCode = true
	without, _ := observeEnv(t, map[string]string{"a.go": "package a\n\nfunc Target() {}\n"})
	without.OfferCode = true

	if got := withMap.codeCallableTools(); !slices.Contains(got, toolSymbol) {
		t.Errorf("with a repo map the callable list must offer symbol, got %v", got)
	}
	if got := without.codeCallableTools(); slices.Contains(got, toolSymbol) {
		t.Errorf("with no repo map the callable list must not offer symbol, got %v", got)
	}

	if desc := codeTool(withMap.codeCallableTools()).Description; !strings.Contains(desc, "ls, symbol") {
		t.Errorf("the description must name symbol where it works:\n%s", desc)
	}
	if desc := codeTool(without.codeCallableTools()).Description; strings.Contains(desc, "symbol") {
		t.Errorf("the description must not name symbol where every call fails:\n%s", desc)
	}

	if got := withMap.codeToolsText(); !strings.Contains(got, "glob, ls, and symbol") {
		t.Errorf("the bullet must name symbol where it works:\n%s", got)
	}
	if got := without.codeToolsText(); !strings.Contains(got, "glob, and ls") || strings.Contains(got, "symbol") {
		t.Errorf("the bullet must stop at ls where symbol does not work:\n%s", got)
	}

	// Registration follows too: a program calling symbol without a repo map
	// meets a ReferenceError, the fail-closed path, rather than a tool that
	// answers only to say it cannot.
	if got := run(without, `symbol({name: "Target"})`); !strings.Contains(got, "not defined") {
		t.Errorf("symbol must be unregistered without a repo map, got:\n%s", got)
	}
	if got := run(withMap, `symbol({name: "Target"})`); strings.Contains(got, "not defined") {
		t.Errorf("symbol must be registered with a repo map, got:\n%s", got)
	}
}

// TestCodeStopsOnTurnCancellation: a Ctrl-C during a program stops it, as it
// stops a shell command, rather than waiting out the time limit.
func TestCodeStopsOnTurnCancellation(t *testing.T) {
	c, _ := observeEnv(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	got := c.runCode(ctx, codeCall{code: "while (true) {}"})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("a cancelled turn waited %v for the program", elapsed)
	}
	if !strings.Contains(got, "interrupted") {
		t.Errorf("a cancelled program must say so, got: %q", got)
	}
}

// A top-level return is taken as the program's result, since that is the only
// thing it can mean, rather than failing on "Illegal return statement" — MiMo
// wrote one with the description in front of it and spent a step on the error.
// The wrapper that makes it legal keeps the program's line numbers.
func TestCodeTopLevelReturnIsTheResult(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"a.txt": "x\n"})

	for _, tc := range []struct{ name, code, want string }{
		{"plain", "const x = 1 + 1;\nreturn x * 3", "6"},
		{"in a branch", "const n = 2;\nif (n > 1) {\n  return \"many\";\n}\nreturn \"few\"", "many"},
		{"with console.log", "console.log(\"seen\");\nreturn 7", "seen\n7"},
		{"no return, unchanged", "const y = 4;\ny * 2", "8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := run(c, tc.code); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	// A runtime error in a wrapped program names the program's own line.
	got := run(c, "const a = 1;\nconst b = 2;\nreturn missing.x")
	if !strings.Contains(got, "line 3: return missing.x") {
		t.Errorf("the error lost its line under the wrapper:\n%s", got)
	}
	// And a program with a return and another syntax error fails on the
	// other one, which is the one left to fix.
	got = run(c, "const a = ;\nreturn a")
	if strings.Contains(got, "Illegal return") || !strings.Contains(got, "SyntaxError") {
		t.Errorf("want the remaining syntax error, not the return:\n%s", got)
	}
}
