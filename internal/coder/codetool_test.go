package coder

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"dbohdan.com/strument/internal/repomap"
)

// The run_code tool's tests. Monty's own behavior is pinned here at the level the
// model sees — the returned value and the error text — because that is the
// contract the tool description promises. The security claims (no filesystem,
// no network) are tested, not assumed: each program below was verified to
// fail, and stays red if a Monty upgrade ever makes it pass.

func TestCodeArithmetic(t *testing.T) {
	c, _ := observeEnv(t, nil)

	for _, tc := range []struct {
		code, want string
	}{
		{"1 + 2", "3"},
		{"2.5 * 4", "10"},
		{"round(3.14159, 2)", "3.14"},
		{"sum(x * x for x in range(10))", "285"},
		{"f'{42:08d}'", "00000042"},
		{"'5'.zfill(3)", "005"},
		{"import math\nmath.sqrt(1764)", "42"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			got := c.runCode(context.Background(), codeCall{code: tc.code})
			if got != tc.want {
				t.Errorf("code %q: got %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// TestCodePrintOutputIsDelivered pins the fix the CO smoke run caught: the
// description says "use print() for intermediate values", most model programs
// end in print(...) rather than a bare expression, and without a print
// handler every such program returned "None" with its actual output dropped.
// A print-driven program must see its output, printed section before the
// final value; a bare-expression program is unchanged.
func TestCodePrintOutputIsDelivered(t *testing.T) {
	c, _ := observeEnv(t, nil)

	if got := c.runCode(context.Background(), codeCall{code: "print(\"hello\")\nprint(\"world\")"}); got != "hello\nworld" {
		t.Errorf("a print-driven program returned %q, want its printed output", got)
	}
	if got := c.runCode(context.Background(), codeCall{code: "x = 6\nprint(x)\nx * 7"}); got != "6\n42" {
		t.Errorf("print plus a final value returned %q, want \"6\\n42\"", got)
	}
	if got := c.runCode(context.Background(), codeCall{code: "1 + 2"}); got != "3" {
		t.Errorf("a bare expression returned %q, want \"3\"", got)
	}
}

// TestCodeRoundExists pins the probe result that decided the tool
// description: round() is available. It is the single most likely call in
// model-written number formatting, and its absence would have to be said so.
func TestCodeRoundExists(t *testing.T) {
	c, _ := observeEnv(t, nil)
	got := c.runCode(context.Background(), codeCall{code: "round(3.7)"})
	if got != "4" {
		t.Errorf("round(3.7): got %q, want \"4\" — the tool description says round() is available", got)
	}
}

// TestCodeInfiniteLoopTerminates is the duration limit's whole point: a
// runaway loop ends on the limit rather than hanging the turn.
func TestCodeInfiniteLoopTerminates(t *testing.T) {
	c, _ := observeEnv(t, nil)

	start := time.Now()
	got := c.runCode(context.Background(), codeCall{code: "while True: pass"})
	elapsed := time.Since(start)

	if !strings.Contains(got, "failed") && !strings.Contains(got, "limit") {
		t.Errorf("an infinite loop must return an error text, got: %q", got)
	}
	if elapsed > 30*time.Second {
		t.Errorf("the duration limit did not fire; ran %v", elapsed)
	}
}

// TestCodeMemoryBombTerminates is the memory limit: an allocation loop ends on
// the limit rather than taking the process with it.
func TestCodeMemoryBombTerminates(t *testing.T) {
	c, _ := observeEnv(t, nil)

	got := c.runCode(context.Background(), codeCall{code: "x = []\nwhile True:\n    x = x + [0] * 1000"})
	if !strings.Contains(got, "failed") && !strings.Contains(got, "limit") {
		t.Errorf("a memory bomb must return an error text, got: %q", got)
	}
}

// TestCodeUnsupportedSyntaxIsUsefulError is the contract the tool description
// leans on: a wall returns Monty's own error, which names the construct, and
// the turn survives it.
func TestCodeUnsupportedSyntaxIsUsefulError(t *testing.T) {
	c, _ := observeEnv(t, nil)

	got := c.runCode(context.Background(), codeCall{code: "match x:\n    case 1: pass"})
	if !strings.Contains(got, "match") {
		t.Errorf("a match statement must name itself in the error, got: %q", got)
	}
}

// TestCodeToolOfferedInAskMode: computing mutates nothing, so a discussion
// turn gets the calculator too.
func TestCodeToolOfferedInAskMode(t *testing.T) {
	c, _ := observeEnv(t, nil)
	c.OfferCode = true
	c.editFormat = "ask"

	found := false
	for _, d := range c.toolDefs() {
		if d.Name == toolRunCode {
			found = true
		}
	}
	if !found {
		t.Error("the run_code tool must be offered in ask mode")
	}

	c.editFormat = "tool"
	found = false
	for _, d := range c.toolDefs() {
		if d.Name == toolRunCode {
			found = true
		}
	}
	if !found {
		t.Error("the run_code tool must be offered in tool mode")
	}
}

// TestCodeDescriptionNamesTheLimits checks the description stays honest about
// the subset. A line that stops describing a real wall is a lie to the model;
// each substring here corresponds to a probe in the tests below.
func TestCodeDescriptionNamesTheLimits(t *testing.T) {
	desc := codeTool(InspectorTools(), CodeResultLast).Description
	for _, want := range []string{"class", "with", "match", "math", "re", "datetime", "json"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the description must mention %q:\n%s", want, desc)
		}
	}
}

// TestCodeNoFilesystemAccess is the security claim, tested not assumed. Monty
// has no `open` and routes os/pathlib through an OsCallFunc this tool never
// registers, so the calls must fail. If a Monty upgrade makes any of these
// succeed, that upgrade must not ship.
func TestCodeNoFilesystemAccess(t *testing.T) {
	c, _ := observeEnv(t, nil)

	for _, code := range []string{
		"open('/etc/passwd')",
		"import os\nos.getcwd()",
		"import os\nos.listdir('/')",
		"import os\nos.getenv('HOME')",
		"import pathlib\npathlib.Path('/').exists()",
	} {
		t.Run(code, func(t *testing.T) {
			got := c.runCode(context.Background(), codeCall{code: code})
			if !strings.Contains(got, "The program failed") {
				t.Errorf("filesystem access must fail and did not:\n%s", got)
			}
		})
	}
}

// TestCodeNoNetworkAccess: the same claim for the network. There is no socket
// module and no OsCallFunc to route anything through.
func TestCodeNoNetworkAccess(t *testing.T) {
	c, _ := observeEnv(t, nil)

	for _, code := range []string{
		"import socket\nsocket.socket()",
		"import urllib.request\nurllib.request.urlopen('http://localhost:1')",
	} {
		t.Run(code, func(t *testing.T) {
			got := c.runCode(context.Background(), codeCall{code: code})
			if !strings.Contains(got, "The program failed") {
				t.Errorf("network access must fail and did not:\n%s", got)
			}
		})
	}
}

// --- the read-only bridge -------------------------------------------------

// TestCodeBridgeReadReturnsFileContents: a program calling read() gets the
// file's contents — the same text a direct call returns.
func TestCodeBridgeReadReturnsFileContents(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"a.go": "package a\n\nfunc F() {}\n",
	})

	got := c.runCode(context.Background(), codeCall{
		code: `read(path="a.go")`,
	})
	if !strings.Contains(got, "package a") {
		t.Errorf("a bridged read must return the file's contents, got:\n%s", got)
	}
}

// TestCodeBridgeGrepFilterEndToEnd is the phenomenon the bridge exists for:
// search inside the program, filter the results in Python, return the
// computed answer — one round trip instead of two. A tool result crosses the
// boundary as the same text the model would see, so the program parses it;
// that coarseness is the design (a bridged call is a tool call, not a
// per-element helper).
func TestCodeBridgeGrepFilterEndToEnd(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"a.go":  "package a\n\nfunc Target() {}\n",
		"b.go":  "package b\n\n// Target again\n",
		"c.txt": "Target here too\n",
	})

	got := c.runCode(context.Background(), codeCall{
		code: `out = grep(pattern="Target", mode="files")
lines = [l for l in out.splitlines() if l.endswith(".go")]
(lines, "c.txt" in lines)`,
	})
	if !strings.Contains(got, `"a.go"`) || !strings.Contains(got, `"b.go"`) {
		t.Errorf("expected both .go files in the filtered result, got:\n%s", got)
	}
	if !strings.Contains(got, "false") {
		t.Errorf("the Python filter must have excluded c.txt, got:\n%s", got)
	}
}

// TestCodeBridgeForbiddenToolsFail is the fail-closed claim, one test per
// forbidden tool. Two layers both hold: a mutating tool is not registered
// with Monty, so the interpreter itself raises NameError — nothing this side
// can answer — and even a name that were registered cannot pass the
// allowlist check in bridgeCall. The plan's "unknown-function error" is the
// NameError shape; the point is that the call never reaches a mutating tool.
func TestCodeBridgeForbiddenToolsFail(t *testing.T) {
	c, _ := observeEnv(t, nil)

	for _, name := range []string{"bash", "edit", "write", "commit", "check"} {
		t.Run(name, func(t *testing.T) {
			code := name + `(command="rm -rf /")`
			if name == "check" {
				code = `check(name="build")`
			}
			got := c.runCode(context.Background(), codeCall{code: code})
			if !strings.Contains(got, "The program failed") {
				t.Errorf("calling %s from a program must fail and did not:\n%s", name, got)
			}
			if !strings.Contains(got, "not defined") {
				t.Errorf("%s is not registered with Monty, so the error must be a NameError, got:\n%s", name, got)
			}
		})
	}
}

// TestCodeBridgeCapFires: one program cannot issue unbounded work. The cap is
// maxBridgedCalls; a program that loops past it is stopped. Uncaught on
// purpose: since tool-call errors became resumable (raised at the call site),
// a try/except can swallow the cap error and keep calling — that is correct
// Python semantics, and the 5s duration limit bounds it — so the cap's own
// error is pinned in its uncaught form, where it stops the program.
func TestCodeBridgeCapFires(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"f.txt": "x\n"})

	// One over the cap. If the cap is maxBridgedCalls, the loop fails on the
	// call after the last allowed one.
	code := "while True:\n    ls()"
	got := c.runCode(context.Background(), codeCall{code: code})

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

	c.runCode(context.Background(), codeCall{code: `ls()`})

	joined := strings.Join(out.lines, "\n")
	// "‹run_code› ls()" is the program being announced — one line, no closing
	// marker, thinking's one-line shape. The outcome line names the tools the
	// program actually called, collected at the bridge.
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
// one line; a multi-line program is bracketed, its lines preserved — the
// flattening this replaced made Python's indentation-meaningful source
// unreadable) and one prose line for the transcript.
func TestCodeProgramShapes(t *testing.T) {
	c, out := observeEnv(t, map[string]string{"f.txt": "x\n"})

	// Multi-line program, no tool calls: pure computation is said as such.
	c.runCode(context.Background(), codeCall{code: "x = 40\ny = 2\nx + y"})

	joined := strings.Join(out.lines, "\n")
	if !strings.Contains(joined, "‹run_code›\nx = 40\ny = 2\nx + y\n‹/›") {
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

	c.runCode(context.Background(), codeCall{code: "a = read(path='f.txt')\nb = read(path='missing')\nb"})

	joined := strings.Join(out.lines, "\n")
	if !strings.Contains(joined, "Ran 3 lines of code calling read.") {
		t.Errorf("the outcome line must survive a failed program, got:\n%s", joined)
	}
	// The error text travels in the tool result (to the model), not the
	// user-facing lines; runCode's return is what carries it.
	if got := c.runCode(context.Background(), codeCall{code: "b = read(path='missing')\nb"}); !strings.Contains(got, "The program failed:") {
		t.Errorf("the error text must still reach the model, got:\n%s", got)
	}
}

// --- results the program did not keep -------------------------------------

// TestCodeDiscardedResultsAreNamed covers the two field reports that motivated
// the note, and — in the same table — the shapes it must stay quiet for.
//
// Both failures come from the same fact: only the final value reaches the
// model. A loop of reads that keeps nothing returns the bare word "None" after
// reading four files successfully, which one model read as "the files do not
// exist" before going to look for them again. Two calls on two lines return the
// second one's result and drop the first, which is quieter and worse: the
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
		{
			name:     "loop keeps nothing",
			code:     "for p in [\"a.py\", \"b.py\"]:\n    read(path=p)",
			wantNote: true,
		},
		{
			name:     "two bare calls, first dropped",
			code:     "grep(pattern=\"NEEDLE\", glob=\"a.py\")\ngrep(pattern=\"NEEDLE\", glob=\"b.py\")",
			wantNote: true,
		},
		{
			name:     "results collected",
			code:     "hits = {}\nfor p in [\"a.py\", \"b.py\"]:\n    hits[p] = read(path=p)\nhits",
			wantNote: false,
		},
		{
			name:     "results printed",
			code:     "for p in [\"a.py\", \"b.py\"]:\n    print(read(path=p))",
			wantNote: false,
		},
		{
			name:     "one call, returned",
			code:     "read(path=\"a.py\")",
			wantNote: false,
		},
		{
			name:     "no calls at all",
			code:     "x = 1",
			wantNote: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := observeEnv(t, files)
			got := c.runCode(context.Background(), codeCall{code: tc.code})
			noted := strings.Contains(got, "comes back to you")
			if noted != tc.wantNote {
				t.Errorf("note present = %v, want %v:\n%s", noted, tc.wantNote, got)
			}
			// The value itself is never replaced by the note.
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

	none := c.runCode(context.Background(), codeCall{code: "for p in [\"a.py\", \"b.py\"]:\n    read(path=p)"})
	if !strings.Contains(none, "returned none of their results") {
		t.Errorf("a program that kept nothing must be told so:\n%s", none)
	}
	if !strings.Contains(none, "2 calls") {
		t.Errorf("the note must say how many calls were made:\n%s", none)
	}

	last := c.runCode(context.Background(), codeCall{code: "grep(pattern=\"NEEDLE\", glob=\"a.py\")\ngrep(pattern=\"NEEDLE\", glob=\"b.py\")"})
	if !strings.Contains(last, "That is the last call's result") {
		t.Errorf("a program that returned only its last call must be told so:\n%s", last)
	}
}

// The description's module list is a claim about the vendored monty.wasm, and
// it has been wrong: it named math/re/datetime/json only, while itertools and
// collections work and os/sys/pathlib import but reach nothing — which is what
// invites a model to probe os for a filesystem it will not find. Probed here
// against the interpreter itself rather than asserted, so the list cannot drift
// from what a program can actually import.
func TestCodeDescriptionMatchesTheModulesThatWork(t *testing.T) {
	c, _ := observeEnv(t, nil)
	desc := codeTool(InspectorTools(), CodeResultLast).Description

	for _, m := range []string{"math", "re", "datetime", "json", "itertools", "collections"} {
		if got := c.runCode(context.Background(), codeCall{code: "import " + m + "\n1"}); got != "1" {
			t.Errorf("the description promises %q, which does not import: %s", m, got)
		}
		if !strings.Contains(desc, m) {
			t.Errorf("module %q works and the description does not name it", m)
		}
	}
	// The other direction: a module the description tells the model not to
	// reach for must really be missing, or the advice is a lie that costs a
	// retry. functools is representative of the batch that is absent.
	if got := c.runCode(context.Background(), codeCall{code: "import functools\n1"}); !strings.Contains(got, "ModuleNotFoundError") {
		t.Errorf("functools now imports; the description's list needs re-probing: %s", got)
	}
	// os/sys/pathlib import and reach nothing, and the description says exactly
	// that rather than claiming they are unavailable.
	if got := c.runCode(context.Background(), codeCall{code: "import os\n1"}); got != "1" {
		t.Errorf("os no longer imports; the description's wording needs revisiting: %s", got)
	}
	for _, want := range []string{"os, sys and pathlib import but reach no filesystem", "bash tool"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the description must say %q:\n%s", want, desc)
		}
	}
}

// symbol is offered exactly where a repo map is, and everything that tells the
// model about the run_code bridge must follow that condition — the schema's
// callable list, the Monty registration behind it, and the prompt bullet.
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

	// The description the model reads follows, in both directions.
	if desc := codeTool(withMap.codeCallableTools(), CodeResultLast).Description; !strings.Contains(desc, "ls, symbol") {
		t.Errorf("the description must name symbol where it works:\n%s", desc)
	}
	if desc := codeTool(without.codeCallableTools(), CodeResultLast).Description; strings.Contains(desc, "symbol") {
		t.Errorf("the description must not name symbol where every call fails:\n%s", desc)
	}

	// And the prompt bullet, which used to name four tools whatever the session
	// had.
	if got := withMap.codeToolsText(); !strings.Contains(got, "glob, ls, and symbol") {
		t.Errorf("the bullet must name symbol where it works:\n%s", got)
	}
	if got := without.codeToolsText(); !strings.Contains(got, "glob, and ls") || strings.Contains(got, "symbol") {
		t.Errorf("the bullet must stop at ls where symbol does not work:\n%s", got)
	}

	// Registration follows too: a program calling symbol without a repo map
	// meets a NameError, the fail-closed path, rather than a tool that answers
	// only to say it cannot.
	got := without.runCode(context.Background(), codeCall{code: `symbol(name="Target")`})
	if !strings.Contains(got, "not defined") {
		t.Errorf("symbol must be unregistered without a repo map, got:\n%s", got)
	}
	if got := withMap.runCode(context.Background(), codeCall{code: `symbol(name="Target")`}); strings.Contains(got, "not defined") {
		t.Errorf("symbol must be registered with a repo map, got:\n%s", got)
	}
}

// --- what a program hands back --------------------------------------------

// The three CodeResult arms, pinned at the level the model sees. They exist to
// be measured (doc/experiments/2026-09-code-result.md), and a trial whose arms
// do not actually differ measures nothing — so the difference is asserted here
// rather than assumed from the flag having been passed.
func TestCodeResultArmsDiffer(t *testing.T) {
	files := map[string]string{"a.py": "x = 1  # NEEDLE\n", "b.py": "y = 2  # NEEDLE\n"}
	twoCalls := "grep(pattern=\"NEEDLE\", glob=\"a.py\")\ngrep(pattern=\"NEEDLE\", glob=\"b.py\")"

	last, _ := observeEnv(t, files)
	got := last.runCode(context.Background(), codeCall{code: twoCalls})
	if strings.Contains(got, "a.py") {
		t.Errorf("the default arm must return the last call only, got:\n%s", got)
	}
	if !strings.Contains(got, "That is the last call's result") {
		t.Errorf("the default arm must say what it dropped, got:\n%s", got)
	}

	all, _ := observeEnv(t, files)
	all.CodeResult = CodeResultAll
	got = all.runCode(context.Background(), codeCall{code: twoCalls})
	if !strings.Contains(got, "a.py") || !strings.Contains(got, "b.py") {
		t.Errorf("the echoing arm must return every call's result, got:\n%s", got)
	}
	// Nothing was lost, so the note that says something was would contradict
	// the contract this arm gave the model.
	if strings.Contains(got, "last call's result") || strings.Contains(got, "returned none") {
		t.Errorf("the echoing arm must not claim results were lost, got:\n%s", got)
	}
	// And the last result is not repeated once as an echo and again as the
	// value — the largest thing in the reply, twice.
	if n := strings.Count(got, "1 match in 1 file for NEEDLE matching b.py"); n != 1 {
		t.Errorf("the last result appears %d times, want 1:\n%s", n, got)
	}
	// The calls come back as the program wrote them, not as wire JSON.
	if !strings.Contains(got, `>>> grep(glob="a.py", pattern="NEEDLE")`) {
		t.Errorf("the echo must render keywords, not JSON, got:\n%s", got)
	}
	// A program that keeps nothing is the case the note was written for, and
	// the case where this arm must stay quiet: the results did come back. The
	// check above cannot see this, because a program whose value is the last
	// call's result never reaches the note at all.
	got = all.runCode(context.Background(), codeCall{
		code: "for g in [\"a.py\", \"b.py\"]:\n    grep(pattern=\"NEEDLE\", glob=g)"})
	if strings.Contains(got, "returned none of their results") {
		t.Errorf("the echoing arm handed back every result and must not say otherwise:\n%s", got)
	}
	if !strings.Contains(got, "a.py") || !strings.Contains(got, "b.py") {
		t.Errorf("the echoing arm must return both results even when the program keeps none:\n%s", got)
	}

	main, _ := observeEnv(t, files)
	main.CodeResult = CodeResultMain
	got = main.runCode(context.Background(), codeCall{
		code: "def main():\n    return [grep(pattern=\"NEEDLE\", glob=g) for g in [\"a.py\", \"b.py\"]]"})
	if !strings.Contains(got, "a.py") || !strings.Contains(got, "b.py") {
		t.Errorf("the main arm must return what main returned, got:\n%s", got)
	}
	// A program that defines no main fails loudly rather than handing back a
	// quiet None, which is the whole argument for this arm.
	got = main.runCode(context.Background(), codeCall{code: twoCalls})
	if !strings.Contains(got, "'main' is not defined") {
		t.Errorf("the main arm must fail loudly with no main defined, got:\n%s", got)
	}
}

// Each arm's description states its own contract and shows an example obeying
// it. An example that contradicts the paragraph above it is the loudest thing
// in a tool description, and models copy examples.
func TestCodeResultDescriptionsMatchTheirArm(t *testing.T) {
	for _, tt := range []struct {
		arm               CodeResult
		contract, example string
	}{
		{CodeResultLast, "Only the program's last evaluated value", "\ncaps\n"},
		{CodeResultAll, "Every call the program makes returns its result", "\ncaps\n"},
		{CodeResultMain, "Define a function called main", "    return caps\n"},
	} {
		t.Run(tt.arm.String(), func(t *testing.T) {
			desc := codeTool(InspectorTools(), tt.arm).Description
			if !strings.Contains(desc, tt.contract) {
				t.Errorf("arm %s does not state its contract (%q):\n%s", tt.arm, tt.contract, desc)
			}
			if !strings.Contains(desc, tt.example) {
				t.Errorf("arm %s's example does not obey it (%q):\n%s", tt.arm, tt.example, desc)
			}
		})
	}
}

func TestParseCodeResultRoundTrips(t *testing.T) {
	for _, name := range CodeResultNames {
		arm, ok := ParseCodeResult(name)
		if !ok {
			t.Fatalf("ParseCodeResult(%q) was not recognized", name)
		}
		if got := arm.String(); got != name {
			t.Errorf("%q parsed to an arm that renders as %q", name, got)
		}
	}
	if _, ok := ParseCodeResult("every"); ok {
		t.Error("ParseCodeResult accepted a name that is not an arm")
	}
}

// The main arm appends main() only when the program has not called it already.
// Told "the program is run and then main() is called", 38 of 89 programs in the
// trial ended in main() anyway — and appending a second call ran everything
// twice, which showed up as that arm costing more.
func TestCodeMainArmDoesNotRunTwice(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{"a.py": "x = 1  # NEEDLE\n"})
	c.CodeResult = CodeResultMain
	out := &captureOut{}
	c.Out = out

	c.runCode(context.Background(), codeCall{
		code: "def main():\n    return read(path=\"a.py\")\n\nmain()"})
	if n := strings.Count(strings.Join(out.lines, "\n"), "Read a.py"); n != 1 {
		t.Errorf("a program that calls main() itself read the file %d times, want 1", n)
	}

	// And a program that does not call main still gets the appended call.
	out.lines = nil
	got := c.runCode(context.Background(), codeCall{
		code: "def main():\n    return read(path=\"a.py\")"})
	if !strings.Contains(got, "NEEDLE") {
		t.Errorf("a program that leaves main uncalled must still be run:\n%s", got)
	}
}
