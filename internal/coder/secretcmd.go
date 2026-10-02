package coder

import (
	"os"
	"path/filepath"
	"strings"

	"dbohdan.com/strument/internal/secretfile"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// secretInCommand finds a secret-shaped path a shell command names, so the
// shell gate can ask the user rather than approve_model.
//
// It parses rather than matching text, because text matching is brittle in the
// way that matters: `cat ~/.n'e'trc`, `cat "$HOME"/.netrc` and `cat ~/.ne*` all
// name the same file and share no substring with ".netrc". Each word is
// expanded the way the shell would — quote removal, tilde, $HOME and $PWD,
// braces, and globbing against the real filesystem — and every field is
// checked as a path, as is the part after "=" in a word like --file=x or
// if=x. A `cd` whose argument resolves moves the directory later relative
// paths are taken from, so `cd ~/.aws && cat credentials` is caught too.
//
// The line it does not cross is computation. A word that needs a command run,
// or any variable but HOME and PWD, to know its value is skipped, so
// `cat ~/$(echo .argep | rot13)` gets through. That is deliberate: resolving
// it would mean running the command, and a check that runs the model's code
// to decide whether to ask about the model's code has stopped being a check.
// A word inside a command substitution is still a word, though, and is
// checked, so `echo $(cat ~/.netrc)` is caught.
//
// What it finds sends the command to the user; it never refuses one. A false
// positive — `echo .env` — costs a question, which is the right direction to
// be wrong in.
func secretInCommand(command, dir, home string, m *secretfile.Matcher) (path, pattern string, ok bool) {
	if m == nil {
		return "", "", false
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return "", "", false
	}
	cwd := dir
	cfg := func() *expand.Config {
		return &expand.Config{
			Env:      expand.ListEnviron("HOME="+home, "PWD="+cwd),
			NoUnset:  true,
			ReadDir2: os.ReadDir,
		}
	}
	check := func(w *syntax.Word) bool {
		fields, err := expand.Fields(cfg(), w)
		if err != nil {
			return false
		}
		for _, f := range fields {
			cands := []string{f}
			if i := strings.Index(f, "="); i >= 0 && i+1 < len(f) {
				cands = append(cands, f[i+1:])
			}
			for _, c := range cands {
				if c == "" {
					continue
				}
				abs := c
				if !filepath.IsAbs(abs) {
					abs = filepath.Join(cwd, abs)
				}
				// Absolute in the answer: after a cd, a relative name does not
				// say which directory it is in.
				abs = filepath.Clean(abs)
				if p, hit := m.Match(abs); hit {
					path, pattern, ok = abs, p, true
					return true
				}
			}
		}
		return false
	}
	// Words a CallExpr has already checked, and heredoc bodies, which are
	// text rather than paths.
	skip := map[*syntax.Word]bool{}
	syntax.Walk(file, func(n syntax.Node) bool {
		if ok {
			return false
		}
		switch n := n.(type) {
		case *syntax.Redirect:
			if n.Hdoc != nil {
				skip[n.Hdoc] = true
			}
		case *syntax.CallExpr:
			// The arguments first, against the directory as it was, then the
			// move: `cd sub` names sub relative to where the shell stood.
			for _, w := range n.Args {
				skip[w] = true
				if check(w) {
					return false
				}
			}
			if len(n.Args) >= 1 && n.Args[0].Lit() == "cd" {
				switch len(n.Args) {
				case 1:
					cwd = home
				case 2:
					if f, err := expand.Literal(cfg(), n.Args[1]); err == nil && f != "-" {
						if !filepath.IsAbs(f) {
							f = filepath.Join(cwd, f)
						}
						cwd = filepath.Clean(f)
					}
				}
			}
		case *syntax.Word:
			if !skip[n] && check(n) {
				return false
			}
		}
		return true
	})
	return path, pattern, ok
}
