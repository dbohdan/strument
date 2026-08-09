package repomap

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseStatus(t *testing.T) {
	cases := []struct {
		name      string
		fname     string
		src       string
		wantClean bool
		wantKnown bool
	}{
		{
			name:      "clean Go",
			fname:     "a.go",
			src:       "package a\n\nfunc F() int { return 1 }\n",
			wantClean: true,
			wantKnown: true,
		},
		{
			name:      "unbalanced brace",
			fname:     "a.go",
			src:       "package a\n\nfunc F() int { return 1\n",
			wantClean: false,
			wantKnown: true,
		},
		{
			name:      "clean Python",
			fname:     "a.py",
			src:       "def f():\n    return 1\n",
			wantClean: true,
			wantKnown: true,
		},
		{
			// Chosen by probe, not by guess: the Python grammar here silently
			// accepts "def f(:" and an unindented body, and flags a missing
			// class colon. The check is one-sided — it catches some breakage
			// and never proves a file is fine — which is why it warns rather
			// than refuses.
			name:      "broken Python",
			fname:     "a.py",
			src:       "class A\n    pass\n",
			wantClean: false,
			wantKnown: true,
		},
		{
			// No grammar covers it, so there is nothing to say — which is not
			// the same as saying the file is fine.
			name:      "no grammar",
			fname:     "notes.txt",
			src:       "}}} not code {{{\n",
			wantKnown: false,
		},
		{
			// An empty .go file has no package clause, so it is not valid Go —
			// go/parser says so and the grammar did not. This costs nothing in
			// practice: parseNote reports only regressions, and a file created
			// empty was never clean to regress from.
			name:      "empty file",
			fname:     "a.go",
			src:       "",
			wantClean: false,
			wantKnown: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clean, line, known := ParseStatus(c.fname, []byte(c.src))
			if known != c.wantKnown {
				t.Fatalf("known = %v, want %v", known, c.wantKnown)
			}
			if !known {
				return
			}
			if clean != c.wantClean {
				t.Errorf("clean = %v, want %v (line %d)", clean, c.wantClean, line)
			}
			if !clean && line < 1 {
				t.Errorf("a reported error must point somewhere; line = %d", line)
			}
			if clean && line != 0 {
				t.Errorf("a clean parse must report no line; line = %d", line)
			}
		})
	}
}

// TestParseStatusPointsAtTheErrorRegion records what the line number actually
// means, which a probe settled and intuition got wrong. tree-sitter recovers by
// wrapping a span in one ERROR node, and that span can start before the
// mistake: "def f():\n    return 1)))" collapses the whole module into an ERROR
// beginning at line 1, with the stray parens on line 2 as ordinary tokens
// inside it. So the number is where the parser lost the thread, not where a
// human would point — which is why the harness says "near line N" and warns
// rather than refusing.
func TestParseStatusPointsAtTheErrorRegion(t *testing.T) {
	clean, line, known := ParseStatus("a.py", []byte("def f():\n    return 1)))\n"))
	if !known || clean {
		t.Fatalf("clean = %v, known = %v; want a known break", clean, known)
	}
	if line != 1 {
		t.Errorf("line = %d, want 1 (where the error region starts)", line)
	}
}

// TestParseBudgetYieldsUnknown: a file too big to parse inside the budget must
// report known=false, not a verdict from a partial tree.
//
// In Python, because Go no longer goes through the grammar at all — go/parser
// handles a megabyte in under a tenth of a second, which is the whole reason
// that path exists. The budget still guards every other language.
func TestParseBudgetYieldsUnknown(t *testing.T) {
	var b strings.Builder
	for i := range 60000 {
		fmt.Fprintf(&b, "def f%d(x):\n    return x + %d\n", i, i)
	}
	src := []byte(b.String())

	start := time.Now()
	clean, line, known := ParseStatus("big.py", src)
	elapsed := time.Since(start)

	if known {
		t.Errorf("known = true on a parse that should have run out of budget (%v)", elapsed)
	}
	if clean || line != 0 {
		t.Errorf("clean = %v, line = %d; an unknown result must claim nothing", clean, line)
	}
	if elapsed > 4*parseBudget {
		t.Errorf("parse took %v with a %v budget", elapsed, parseBudget)
	}
}

// TestGoParseIsExactAndFast pins what go/parser bought. A sweep of the Go
// standard library found valid files the grammar reported as broken, and the
// same files were pathologically slow because error recovery is expensive.
// go/parser cannot produce that error: for Go, "parses" and "is valid Go" are
// the same question.
func TestGoParseIsExactAndFast(t *testing.T) {
	for _, p := range []string{
		"/usr/local/go/src/archive/tar/reader_test.go",
		"/usr/local/go/src/cmd/compile/internal/ssa/regalloc.go",
		"/usr/local/go/src/crypto/tls/ticket.go",
	} {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Skipf("Go source tree not available: %v", err)
		}
		start := time.Now()
		clean, _, known := ParseStatus(p, src)
		elapsed := time.Since(start)
		if !known || !clean {
			t.Errorf("%s: clean=%v known=%v — this file is in the standard library and compiles",
				p, clean, known)
		}
		if elapsed > 100*time.Millisecond {
			t.Errorf("%s took %v; the grammar's seconds were the reason to switch", p, elapsed)
		}
	}
}

// TestGoParsePointsAtTheError: go/parser reports where the syntax error is, not
// where a recovering parser gave up, so this one says "at" and means it.
func TestGoParsePointsAtTheError(t *testing.T) {
	src := "package a\n\nfunc F() int {\n\treturn 1\n\nfunc G() {}\n"
	clean, line, known := ParseStatus("a.go", []byte(src))
	if !known || clean {
		t.Fatalf("clean=%v known=%v; want a known break", clean, known)
	}
	if line != 6 {
		t.Errorf("line = %d, want 6 (where the parser actually objects)", line)
	}
}
