package coder

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/prompts"
	"dbohdan.com/strument/internal/record"
	"dbohdan.com/strument/internal/repomap"
	"dbohdan.com/strument/internal/skill"
	"dbohdan.com/strument/internal/webpage"
	"dbohdan.com/strument/internal/websearch"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/model-facing.golden from the code")

// Everything a model is told before it says a word, in one file: the system
// message, every tool's definition with every optional tool offered, and the
// harness notes that have names. A change to any of it is a prompt change, and
// prompt changes are measured (doc/messages.md); this is where one shows up in
// review, as a diff of the text itself rather than of the Go that builds it.
//
// When a change is intended, regenerate the file and read the diff:
//
//	go test ./internal/coder -run TestModelFacingText -update
//
// What it does not cover: the text of individual tool results and the notes
// built inline where they are sent. Those are written beside the code that
// produces them.
func TestModelFacingText(t *testing.T) {
	got := modelFacingText(t)
	path := filepath.Join("testdata", "model-facing.golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; create it with: go test ./internal/coder -run TestModelFacingText -update", err)
	}
	if got != string(want) {
		t.Errorf("the model-facing text changed. If that was intended, run\n"+
			"  go test ./internal/coder -run TestModelFacingText -update\n"+
			"and review the diff of %s.\n\nFirst difference:\n%s", path, firstDifference(string(want), got))
	}
}

// modelFacingText renders it all for a session with everything turned on and
// nothing that varies by machine or by day.
func modelFacingText(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	c := New(root, &config.Model{Provider: config.Provider{Adapter: config.AdapterOpenRouter}, Slug: "example/model"})
	c.Out = testOutput{t}
	c.Platform = PlatformInfo{Platform: "Linux-6.1-x86_64",
		Date: "2026-01-01", WorkDir: "/home/user/project", InGit: true}
	c.SuggestShellCommands = true
	c.Scrape = func(context.Context, string, webpage.Options) (string, error) { return "", nil }
	c.Search = func(context.Context, string) (websearch.Results, error) { return websearch.Results{}, nil }
	c.Consult = map[string]ConsultModel{"terra": {Model: terraModel(), Purpose: "documentation and user-facing prose"}}
	s := skill.Skill{Path: "/home/user/project/.strument/skills/release/SKILL.md", Trusted: true}
	s.Name, s.Description = "release", "How this project cuts a release."
	c.Skills = []skill.Skill{s}
	c.Check = []config.Check{{Name: "test", Argv: []string{"go", "test", "./..."}}}
	c.RepoMap = repomap.New(root)
	c.SessionRecords = func() []record.Record { return nil }

	var b strings.Builder
	b.WriteString("# The system message\n\n")
	b.WriteString(c.formatChatChunks().system[0].Text())
	b.WriteString("\n\n# Tools, in the order they are offered\n")
	for _, d := range c.toolDefs() {
		params, err := json.MarshalIndent(d.Parameters, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "\n## %s\n\n%s\n\nParameters:\n%s\n", d.Name, d.Description, params)
	}
	b.WriteString("\n# Named harness notes\n")
	for _, n := range []struct{ name, text string }{
		{"prompts.HarnessNoteLine", prompts.HarnessNoteLine},
		{"prompts.RollLabel", prompts.RollLabel},
		{"prompts.RollRecallLabel", prompts.RollRecallLabel},
		{"prompts.RollRecallTail", prompts.RollRecallTail},
		{"consultIntro", consultIntro},
		{"queuedNote", queuedNote},
		{"seamOther", seamOther},
		{"seamYours", seamYours},
		{"stoppedNote", stoppedNote},
		{"unrunText", unrunText},
	} {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", n.name, n.text)
	}
	return b.String()
}

// firstDifference shows a few lines around where two texts part.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			var out bytes.Buffer
			fmt.Fprintf(&out, "line %d\n  want: %q\n  got:  %q", i+1, wl, gl)
			return out.String()
		}
	}
	return "(lengths differ only in trailing content)"
}
