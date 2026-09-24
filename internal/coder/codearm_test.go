package coder

import (
	"context"
	"strings"
	"testing"
)

// The run_code trial arms (codearm.go). Each program was run by hand against
// the arm before it went in here; the assertions are what the model sees.

var armFiles = map[string]string{
	"notes.md":       "alpha\nbeta\n\ngamma\n",
	"data/sizes.csv": "name,bytes\na,10\nb,9\nc,100\n",
}

func armCoder(t *testing.T, arm string) *Coder {
	t.Helper()
	c, _ := observeEnv(t, armFiles)
	c.CodeArm = arm
	return c
}

func runArm(c *Coder, code string) string {
	return c.runCode(context.Background(), codeCall{code: code})
}

// The idioms the probe caught, in the form each was written, now answered.
func TestMontyOpenReadsTheWaysModelsWrite(t *testing.T) {
	c := armCoder(t, codeArmMontyOpen)
	for _, tc := range []struct{ code, want string }{
		{`open("notes.md").read()`, "alpha\nbeta\n\ngamma\n"},
		{`len(open("notes.md").read().splitlines())`, "4"},
		{"with open(\"data/sizes.csv\") as f:\n    rows = f.readlines()\nlen(rows)", "4"},
		{"from pathlib import Path\nPath(\"notes.md\").read_text().count(\"a\")", "5"},
		{"from pathlib import Path\n(Path(\"data\") / \"sizes.csv\").read_text().splitlines()[1]", "a,10"},
		{`open("notes.md", "r").read(5)`, "alpha"},
	} {
		if got := runArm(c, tc.code); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.code, got, tc.want)
		}
	}
}

// Reading only, inside the project only, and a read counts as a bridged call.
func TestMontyOpenStaysReadOnlyAndContained(t *testing.T) {
	c := armCoder(t, codeArmMontyOpen)
	for _, tc := range []struct{ code, want string }{
		{`open("notes.md", "w")`, "for reading only"},
		{`open("notes.md", "a")`, "for reading only"},
		{`open("notes.md", "rb")`, "read_bin"},
		{`open("../outside.txt")`, "outside the project"},
		{`open("missing.txt")`, "Could not read"},
	} {
		if got := runArm(c, tc.code); !strings.Contains(got, tc.want) {
			t.Errorf("%q: got %q, want it to mention %q", tc.code, got, tc.want)
		}
	}
	out := &captureOut{}
	c.Out = out
	runArm(c, `open("notes.md").read()`)
	if !strings.Contains(strings.Join(out.lines, "\n"), "calling read_text") {
		t.Errorf("the outcome line does not name the read: %q", out.lines)
	}
}

// The baseline arm is the shipped behaviour: open is refused.
func TestMontyArmStillRefusesOpen(t *testing.T) {
	c := armCoder(t, codeArmMonty)
	if got := runArm(c, `open("notes.md").read()`); !strings.Contains(got, "a program has no filesystem") {
		t.Errorf("the monty arm answered open: %q", got)
	}
}

// The monty-open description differs from the shipped one in exactly the
// sentence it replaces; the replacement has to find its target, or the arm
// would silently be the baseline.
func TestMontyOpenDescriptionReplacesOneSentence(t *testing.T) {
	base := armCoder(t, codeArmMonty).codeToolDef().Description
	open := armCoder(t, codeArmMontyOpen).codeToolDef().Description
	if !strings.Contains(base, codeMissingSentence) {
		t.Fatal("the shipped description no longer carries the sentence the arm replaces")
	}
	if strings.Replace(base, codeMissingSentence, codeMissingSentenceOpen, 1) != open {
		t.Error("the monty-open description differs by more than the one sentence")
	}
}

func TestJSRunsProgramsAgainstTheBridge(t *testing.T) {
	c := armCoder(t, codeArmJS)
	for _, tc := range []struct{ code, want string }{
		{"1 + 2", "3"},
		{`read_text({path: "notes.md"}).length`, "18"},
		{`read_text("notes.md").split("\n").length`, "5"},
		{`glob({pattern: "data/*"})`, `["data/sizes.csv"]`},
		{`ls("data").map(e => e.path)`, `["data/sizes.csv"]`},
		{`const rows = read_text("data/sizes.csv").trim().split("\n").slice(1).map(l => +l.split(",")[1]); rows.sort((a, b) => b - a)`, "[100,10,9]"},
		{`console.log("x", [1, 2], new Map([["k", 1]])); new Set([3])`, "x [1,2] {\"k\":1}\n[3]"},
		{`console.log("only")`, "only"},
		{`let x`, "undefined"},
	} {
		if got := runArm(c, tc.code); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.code, got, tc.want)
		}
	}
}

// The wrong reaches the JS arm can make are answered where they happen, as
// Monty's are.
func TestJSHintsRideTheRightErrors(t *testing.T) {
	c := armCoder(t, codeArmJS)
	for _, tc := range []struct{ code, want string }{
		{`const fs = require("fs")`, "This is not Node"},
		{`import fs from "fs"`, "This is not Node"},
		{`process.cwd()`, "This is not Node"},
		{`print(1)`, "console.log()"},
		{`grep(pattern="x")`, "options object"},
		{`read({path: "missing.txt"})`, "Could not read"},
		{"const a = 1;\nundefinedName + 1", "line 2: undefinedName + 1"},
	} {
		if got := runArm(c, tc.code); !strings.Contains(got, tc.want) {
			t.Errorf("%q: got %q, want it to contain %q", tc.code, got, tc.want)
		}
	}
	got := runArm(c, `read({path: "missing.txt"})`)
	if strings.Contains(got, "(native)") || strings.Contains(got, "runCodeJS") {
		t.Errorf("a Go frame leaked into the error: %q", got)
	}
	if got := runArm(c, `undefinedName`); strings.Contains(got, "This is not Node") {
		t.Errorf("the Node hint rode an unrelated error: %q", got)
	}
}

func TestJSTimeLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the five-second limit")
	}
	c := armCoder(t, codeArmJS)
	if got := runArm(c, "while (true) {}"); !strings.Contains(got, "time limit") {
		t.Errorf("a runaway loop returned %q", got)
	}
}

// The lost-calls note is shared with Monty; in the JS arm it names console.log.
func TestJSLostCallsNoteNamesConsoleLog(t *testing.T) {
	c := armCoder(t, codeArmJS)
	got := runArm(c, "read_text('notes.md'); read_text('data/sizes.csv'); undefined")
	if !strings.Contains(got, "console.log()") || strings.Contains(got, "print()") {
		t.Errorf("the note does not speak JavaScript: %q", got)
	}
}

func TestJSSummariesCoverTheRegistries(t *testing.T) {
	for _, d := range append(append([]codeFuncDef{}, codeFuncs...), codeDataFuncs...) {
		if jsSummaries[d.name] == "" {
			t.Errorf("%s has no JavaScript summary", d.name)
		}
	}
}

// The system prompt's bullet names the language, and only in the JS arm does
// it change.
func TestCodeBulletNamesTheArmLanguage(t *testing.T) {
	for arm, want := range map[string]string{
		codeArmMonty:     "a short Python program",
		codeArmMontyOpen: "a short Python program",
		codeArmJS:        "a short JavaScript program",
	} {
		c := armCoder(t, arm)
		c.OfferCode = true
		if got := c.codeToolsText(); !strings.Contains(got, want) {
			t.Errorf("%s: the bullet does not say %q: %q", arm, want, got)
		}
	}
}
