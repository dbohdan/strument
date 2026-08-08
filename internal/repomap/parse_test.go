package repomap

import "testing"

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
			name:      "empty file",
			fname:     "a.go",
			src:       "",
			wantClean: true,
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
