package coder

import (
	"strings"
	"testing"
)

// TestInspectorAnswersExactlyAsTheTurnDoes is the whole promise of `strument
// tool …`: what the command line prints is the string the model was handed,
// not a second rendering of the same search. The two paths share one
// implementation, so this pins the delegation rather than the formatting — if
// Coder ever grows its own copy of a result, this is what notices.
func TestInspectorAnswersExactlyAsTheTurnDoes(t *testing.T) {
	c, _ := observeEnv(t, map[string]string{
		"a.go":   "package a\nfunc Target() {}\n",
		"b.go":   "package b\n// Target again\n",
		"c.txt":  "Target here too\n",
		"d/e.go": "package d\n// nothing\n",
	})
	insp := &Inspector{Root: c.Root, Files: c.Files, RepoMap: c.RepoMap, Out: DiscardReporter{}}

	for _, tc := range []struct{ name, args string }{
		{"read", `{"path":"a.go"}`},
		{"read", `{"path":"nope.go"}`}, // a refusal is an answer too
		{"grep", `{"pattern":"Target"}`},
		{"grep", `{"pattern":"Target","mode":"content"}`},
		{"grep", `{"pattern":"Target","mode":"count"}`},
		{"grep", `{"pattern":"("}`}, // and so is a bad pattern
		{"grep", `{"pattern":"Target","glob":"*.go","mode":"files"}`},
		{"glob", `{"pattern":"**/*.go"}`},
		{"glob", `{"pattern":"**/*.rs"}`},
		{"ls", `{}`},
		{"ls", `{"path":"d"}`},
		{"symbol", `{"name":"Target"}`}, // no RepoMap: the "no parser" answer
	} {
		t.Run(tc.name+" "+tc.args, func(t *testing.T) {
			viaTurn := c.runObservation(tc.name, tc.args)
			viaCLI := insp.Run(tc.name, tc.args)
			if viaTurn != viaCLI {
				t.Errorf("the two paths disagree:\n turn %q\n cli  %q", viaTurn, viaCLI)
			}
			if strings.TrimSpace(viaCLI) == "" {
				t.Error("an observation must always answer with something")
			}
		})
	}
}

// runObservation reaches the Coder's own methods the way the tool dispatch
// does, so the comparison above goes through the call sites that matter.
func (c *Coder) runObservation(name, args string) string {
	tc := call(name, args)
	switch name {
	case toolRead:
		return c.runRead(tc)
	case toolGrep:
		return c.runGrep(tc)
	case toolGlob:
		return c.runGlob(tc)
	case toolLS:
		return c.runLS(tc)
	case toolSymbol:
		return c.runSymbol(tc)
	}
	t := "unknown tool in test: " + name
	panic(t)
}

// An unknown name is answered rather than panicked, in the dispatch's own
// words: Run takes its name from outside, and outside is where surprises are.
func TestInspectorNamesAnUnknownTool(t *testing.T) {
	c, _ := observeEnv(t, nil)
	insp := &Inspector{Root: c.Root, Files: c.Files, Out: DiscardReporter{}}

	if got, want := insp.Run("frobnicate", `{}`), `Unknown tool "frobnicate".`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, name := range InspectorTools() {
		if strings.Contains(insp.Run(name, `{}`), "Unknown tool") {
			t.Errorf("%s is advertised by InspectorTools but not dispatched", name)
		}
	}
}
