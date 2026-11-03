package coder

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A file this turn created can be removed without asking, however the path is
// spelled, and leaves the snapshot so neither the commit nor /undo sees it.
// A file that existed before the turn, a flag other than -f, or anything
// chained still goes through the gate.
func TestRemovingOwnFilesIsANoOp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	rc := &recordingConfirmer{answer: false}
	c.Confirm = rc
	c.SuggestShellCommands = true
	c.initBeforeMessage()
	matchFailure := false
	c.applyToolEdits([]plannedEdit{
		wholeFileWrite("w1", "sim/scratch_test.go", "package sim\n"),
		wholeFileWrite("w2", "keep.go", "package x\n"),
		wholeFileWrite("w3", "old.go", "package x // changed\n"),
	}, toolResults{}, &matchFailure)

	for _, cmd := range []string{
		"rm old.go",                    // existed before the turn
		"rm -r sim",                    // not a plain file removal
		"rm sim/scratch_test.go && ls", // chained
		"rm keep.go missing.go",        // one argument is not the turn's
	} {
		if _, ok := c.removesOnlyOwnFiles(cmd); ok {
			t.Errorf("%q counted as removing only this turn's files", cmd)
		}
	}

	out, ran := c.runShell(context.Background(), toolCommand{command: "rm -f -- ./sim/scratch_test.go", purpose: "tidy"})
	if !ran || len(rc.got) != 0 {
		t.Fatalf("ran=%v, prompts=%d, out %q; want it run unasked", ran, len(rc.got), out)
	}
	if _, err := os.Stat(filepath.Join(dir, "sim", "scratch_test.go")); !os.IsNotExist(err) {
		t.Errorf("the file is still there: %v", err)
	}
	for _, p := range c.turnSnap.paths() {
		if p == "sim/scratch_test.go" {
			t.Error("the removed file is still in the turn's snapshot, so a commit or /undo would see it")
		}
	}
	if !c.turnSnap.wrote("keep.go") || !c.turnSnap.wrote("old.go") {
		t.Error("removing one file dropped others from the snapshot")
	}

	if _, ran := c.runShell(context.Background(), toolCommand{command: "rm old.go", purpose: "tidy"}); ran || len(rc.got) != 1 {
		t.Errorf("rm of a file older than the turn: ran=%v prompts=%d; want it asked about and declined", ran, len(rc.got))
	}
}

// commandDecider answers approve_model and keeps the commands it was shown.
type commandDecider struct {
	p     float64
	shown []string
}

func (d *commandDecider) decide(_ context.Context, in DecisionInput) (Decision, error) {
	d.shown = append(d.shown, in.Command)
	return Decision{PSafe: d.p, Model: "laya:en"}, nil
}

// A removal of the turn's own files chained to other work is judged without
// the removal: the decision model sees the rest, an approval runs the whole,
// and the removed file leaves the snapshot. What can make an rm name another
// file, or isn't a plain chain, keeps the whole command in front of it.
func TestChainedOwnRemovalIsJudgedWithoutIt(t *testing.T) {
	dir := t.TempDir()
	c := toolCoder(t, dir)
	c.Out = &captureOut{}
	rc := &recordingConfirmer{answer: false}
	c.Confirm = rc
	c.SuggestShellCommands = true
	c.Sandbox = SandboxState{Active: true}
	d := &commandDecider{p: 0.95}
	c.Approve = &ApproveModel{Decide: d.decide, Slug: "laya", Threshold: 0.9}
	c.initBeforeMessage()
	matchFailure := false
	c.applyToolEdits([]plannedEdit{
		wholeFileWrite("w1", "probe.ts", "console.log(1)\n"),
		wholeFileWrite("w2", "keep.ts", "export {}\n"),
	}, toolResults{}, &matchFailure)

	for cmd, want := range map[string]string{
		"rm -f probe.ts && git status --short":      "git status --short",
		"cat probe.ts 2>&1 | tail -20; rm probe.ts": "cat probe.ts 2>&1 | tail -20",
		"rm -f probe.ts; echo ---; sed -n 1,2p x":   "echo ---\nsed -n 1,2p x",
	} {
		rest, keys, ok := c.splitOwnRemovals(cmd)
		if !ok || rest != want || len(keys) != 1 || keys[0] != "probe.ts" {
			t.Errorf("%q split as (%q, %v, %v), want (%q, [probe.ts], true)", cmd, rest, keys, ok, want)
		}
	}
	for _, cmd := range []string{
		"rm -f probe.ts",                       // nothing else: removesOnlyOwnFiles' case
		"ls && rm -f other.ts",                 // not the turn's file
		"cd sub && rm -f probe.ts",             // a later rm would name another file
		"eval x; rm -f probe.ts",               // runs code it is handed
		"rm -f probe.ts & ls",                  // in the background
		"if true; then ls; fi; rm -f probe.ts", // not a simple command or a pipeline
		"$CMD; rm -f probe.ts",                 // a command name not known until it runs
		"(rm -f probe.ts) && ls",               // the rm is not a plain statement
	} {
		if _, _, ok := c.splitOwnRemovals(cmd); ok {
			t.Errorf("%q was split", cmd)
		}
	}

	out, ran := c.runShell(context.Background(), toolCommand{command: "echo checked; rm -f probe.ts", purpose: "tidy"})
	if !ran || len(rc.got) != 0 {
		t.Fatalf("ran=%v, prompts=%d, out %q; want it run on the model's approval", ran, len(rc.got), out)
	}
	if len(d.shown) != 1 || d.shown[0] != "echo checked" {
		t.Errorf("the decision model was shown %q, want only the rest", d.shown)
	}
	if _, err := os.Stat(filepath.Join(dir, "probe.ts")); !os.IsNotExist(err) {
		t.Errorf("the probe is still there: %v", err)
	}
	if c.turnSnap.wrote("probe.ts") || !c.turnSnap.wrote("keep.ts") {
		t.Error("the removed probe must leave the snapshot, and only it")
	}

	// Below the threshold the prompt shows the whole command, rm included.
	d.p = 0.1
	c.applyToolEdits([]plannedEdit{wholeFileWrite("w3", "probe2.ts", "1\n")}, toolResults{}, &matchFailure)
	c.runShell(context.Background(), toolCommand{command: "echo again && rm probe2.ts", purpose: "tidy"})
	if len(rc.got) != 1 || rc.got[0].Command != "echo again && rm probe2.ts" {
		t.Errorf("prompted with %+v, want the whole command once", rc.got)
	}
}
