// What a symbol answer has to contain to be worth choosing over grep.

package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/repomap"
)

// symbolFixture writes a small Go project and returns an Inspector over it.
func symbolFixture(t *testing.T) *Inspector {
	t.Helper()
	c := testCoder(t)
	src := `package fixture

// Set is a named struct, so its fields are declarations.
type Set struct {
	FilesNoFullFiles string
	MainSystem       string
}

type HarnessNote struct{ Text string }

func build() Set {
	// A table-driven row type, whose columns are not declarations worth listing.
	for _, tc := range []struct{ name, want string }{{"a", "b"}} {
		_ = tc
	}
	return Set{}
}
`
	if err := os.WriteFile(filepath.Join(c.Root, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	c.RepoMap = repomap.New(c.Root)
	return &Inspector{Root: c.Root, Files: c.Files, RepoMap: c.RepoMap, Out: DiscardReporter{}}
}

// Every site carries its source line.
//
// Without it a symbol answer is a coordinate, and answering the question it was
// asked costs a second call — while grep returns the matching line in the
// first. Measured head to head on this repo, that one difference was why grep
// won on call count even where symbol was the better instrument.
func TestSymbolSitesCarryTheirSourceLine(t *testing.T) {
	insp := symbolFixture(t)
	text, count, problem := insp.SymbolLookup("MainSystem", "")
	if problem != "" {
		t.Fatal(problem)
	}
	if count != 1 {
		t.Fatalf("found %d sites, want 1:\n%s", count, text)
	}
	if !strings.Contains(text, "MainSystem       string") {
		t.Errorf("the site has no source line, so it still needs a read:\n%s", text)
	}
}

// A named struct's fields are declarations; a table-driven row's are not.
//
// Half the identifiers models actually looked up and missed in live sessions
// were struct fields — the tree-sitter Go query has no rule for them. Tagging
// every anonymous struct too would have put a `want` and a `name` definition in
// every test file in the project, so the line is drawn at a declared name.
func TestSymbolFindsNamedStructFieldsOnly(t *testing.T) {
	insp := symbolFixture(t)

	if _, count, problem := insp.SymbolLookup("FilesNoFullFiles", ""); problem != "" || count != 1 {
		t.Errorf("a named struct's field is not a definition: count=%d problem=%q", count, problem)
	}
	if _, count, _ := insp.SymbolLookup("want", ""); count != 0 {
		t.Errorf("a table-driven row's column was tagged as a definition (%d sites)", count)
	}
}

// A definition answer for a name that is used elsewhere offers the other kind.
//
// kind defaults to definition, so a model asking the natural first question of
// a "who calls this" question gets an answer to a different one. GLM-5.3 did
// exactly that in doc/experiments/2026-08-symbol-uptake.md: it called
// kind=definition on settleEdits, got the single declaration site, and half the
// time treated that as the tool's last word and went back to grep for the rest.
// The miss path already pointed at the other kind; a confident short answer is
// the one nobody follows up, so the hit path is where it was needed.
func TestSymbolDefinitionOffersTheReferenceKind(t *testing.T) {
	insp := symbolFixture(t)
	text, count, problem := insp.SymbolLookup("Set", "")
	if problem != "" || count == 0 {
		t.Fatalf("expected a definition: count=%d problem=%q", count, problem)
	}
	if !strings.Contains(text, `"reference"`) {
		t.Errorf("a definition answer for a used name does not offer the other kind:\n%s", text)
	}
}

// ...and it stays quiet where the line would be noise.
//
// Both directions matter. A reference answer is already what a callers question
// wanted, and a name declared but never used has nothing to point at. A nudge
// on every answer is a nudge nobody reads.
func TestSymbolDoesNotOfferTheOtherKindNeedlessly(t *testing.T) {
	insp := symbolFixture(t)

	ref, _, _ := insp.SymbolLookup("Set", "reference")
	if strings.Contains(ref, `call symbol again`) {
		t.Errorf("a reference answer points back at definitions:\n%s", ref)
	}
	// Declared in the fixture, used nowhere in it.
	unused, count, _ := insp.SymbolLookup("FilesNoFullFiles", "")
	if count == 0 {
		t.Fatal("fixture changed: FilesNoFullFiles is no longer a definition")
	}
	if strings.Contains(unused, `call symbol again`) {
		t.Errorf("an unused declaration offers references that do not exist:\n%s", unused)
	}
}

// A miss must not blame the grammar when the grammar was there.
//
// The old message said "the parser only sees languages it has a grammar for",
// which in a Go project is the wrong reason and usually a false one. A wrong
// explanation is worse than none: it generalizes, and a model told once that
// the parser might not know this language has been given a reason to stop
// trusting the tool for the rest of the session.
func TestSymbolMissDoesNotBlameTheGrammar(t *testing.T) {
	insp := symbolFixture(t)
	text, count, problem := insp.SymbolLookup("no_such_placeholder", "")
	if problem != "" || count != 0 {
		t.Fatalf("expected a miss: count=%d problem=%q", count, problem)
	}
	if strings.Contains(text, "only sees languages it has a grammar for") {
		t.Errorf("the miss still blames grammar coverage:\n%s", text)
	}
	if !strings.Contains(text, "struct fields") {
		t.Errorf("the miss does not say what symbol actually indexes:\n%s", text)
	}
}

// A name that differs only in case says so, which is the answer the caller
// wanted and is one call away.
func TestSymbolMissReportsACaseNearMiss(t *testing.T) {
	insp := symbolFixture(t)
	text, _, _ := insp.SymbolLookup("harnessnote", "")
	if !strings.Contains(text, "HarnessNote") {
		t.Errorf("a case-only miss did not name the real declaration:\n%s", text)
	}
}

// ...and a name that exists under the other kind points at it rather than
// leaving the caller to conclude the name is absent.
func TestSymbolMissPointsAtTheOtherKind(t *testing.T) {
	insp := symbolFixture(t)
	// A field declared in the fixture and used nowhere in it, so the reference
	// lookup genuinely finds nothing. Asserting the miss first, because a name
	// that turned out to have references would make the rest of this vacuous.
	text, count, problem := insp.SymbolLookup("FilesNoFullFiles", "reference")
	if problem != "" || count != 0 {
		t.Fatalf("expected no references: count=%d problem=%q", count, problem)
	}
	if !strings.Contains(text, `"definition"`) {
		t.Errorf("a name declared but not referenced did not point at kind=definition:\n%s", text)
	}
}
