package skill

import (
	"strings"
	"testing"
)

func TestParseMinimal(t *testing.T) {
	fm, body, err := Parse("---\nname: example\ndescription: Example skill.\n---\n# Instructions\n")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "example" || fm.Description != "Example skill." {
		t.Errorf("frontmatter = %+v", fm)
	}
	// The body comes back untouched. Nothing past the closing delimiter is
	// parsed, which is what keeps a skill's Markdown out of the parser's reach.
	if body != "# Instructions\n" {
		t.Errorf("body = %q", body)
	}
}

func TestParseAllKnownFields(t *testing.T) {
	fm, _, err := Parse(`---
name: example
description: Example skill.
license: Apache-2.0
compatibility: Requires Python 3.
allowed-tools: Bash Read
---
body
`)
	if err != nil {
		t.Fatal(err)
	}
	if fm.License != "Apache-2.0" || fm.Compatibility != "Requires Python 3." {
		t.Errorf("frontmatter = %+v", fm)
	}
	// Carried, so a user can be shown what a skill asks for. It grants nothing;
	// that is enforced by there being no code anywhere that reads it as a
	// permission, and by the note on the field.
	if fm.AllowedTools != "Bash Read" {
		t.Errorf("AllowedTools = %q", fm.AllowedTools)
	}
}

func TestParseQuoted(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"double", `description: "An example: useful for testing."`, "An example: useful for testing."},
		{"single", `description: 'Apache''s License'`, "Apache's License"},
		{"escapes", `description: "a\\b\"c\nd\te"`, "a\\b\"c\nd\te"},
		{"empty double", `description: ""`, ""},
		// A colon inside a plain scalar is the common real-world case, and the
		// value runs to the end of the line, so it needs no quoting to work.
		{"plain with colon", `description: Use when: the user asks.`, "Use when: the user asks."},
		// "#" does not start a comment. Stripping one would quietly truncate a
		// description that mentions a heading or a channel.
		{"plain with hash", `description: Use # for headings`, "Use # for headings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fm, _, err := Parse("---\nname: x\n" + tc.src + "\n---\n")
			if err != nil {
				t.Fatal(err)
			}
			if fm.Description != tc.want {
				t.Errorf("got %q, want %q", fm.Description, tc.want)
			}
		})
	}
}

// All six chomping forms. The distinction only shows in trailing newlines, so
// the assertions are on the exact string rather than on its trimmed shape.
func TestParseBlockScalars(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"|", "one\ntwo\n"},
		{"|-", "one\ntwo"},
		{"|+", "one\ntwo\n\n\n"},
		{">", "one two\n"},
		{">-", "one two"},
		{">+", "one two\n\n\n"},
	} {
		t.Run(tc.header, func(t *testing.T) {
			src := "---\nname: x\ndescription: " + tc.header + "\n  one\n  two\n\n\n---\n"
			fm, _, err := Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			if fm.Description != tc.want {
				t.Errorf("got %q, want %q", fm.Description, tc.want)
			}
		})
	}
}

// A folded scalar keeps a blank line as a paragraph break rather than folding
// everything into one line.
func TestFoldedKeepsParagraphs(t *testing.T) {
	fm, _, err := Parse("---\nname: x\ndescription: >\n  one\n  two\n\n  three\n---\n")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Description != "one two\nthree\n" {
		t.Errorf("got %q", fm.Description)
	}
}

// The delimiter is recognised at the grammar level, so three hyphens inside a
// block scalar are content. A substring search for "---" would end the
// frontmatter here and hand back a truncated file that still parses.
func TestDelimiterInsideBlockScalarIsContent(t *testing.T) {
	fm, body, err := Parse("---\nname: x\ndescription: |\n  before\n  ---\n  after\n---\nbody\n")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Description != "before\n---\nafter\n" {
		t.Errorf("description = %q", fm.Description)
	}
	if body != "body\n" {
		t.Errorf("body = %q", body)
	}
}

// Every construct the grammar refuses, each with a message that names what is
// unsupported. Silently misreading these is the outcome worth avoiding: an
// anchor or a tag that parses as a plain string is a value nobody wrote.
func TestParseRejects(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"missing opening", "name: x\n", "opening delimiter"},
		{"missing closing", "---\nname: x\n", "closing delimiter"},
		{"unknown field", "---\nname: x\nfoo: y\n---\n", `unknown field "foo"`},
		{"duplicate field", "---\nname: x\nname: y\n---\n", `duplicate field "name"`},
		{"sequence", "---\nallowed-tools:\n  - Read\n---\n", "sequences are not supported"},
		{"flow mapping", "---\nname: {a: b}\n---\n", "flow collections"},
		{"flow sequence", "---\nname: [a, b]\n---\n", "flow collections"},
		{"anchor", "---\nname: &x value\n---\n", "anchors, aliases and tags"},
		{"alias", "---\nname: *x\n---\n", "anchors, aliases and tags"},
		{"tag", "---\nname: !!str x\n---\n", "anchors, aliases and tags"},
		{"directive", "---\n%YAML 1.2\nname: x\n---\n", "directives"},
		{"document marker", "---\nname: x\n...\n---\n", "document markers"},
		{"merge key", "---\n<<: *base\n---\n", "merge keys"},
		{"nested mapping", "---\nname: x\n  author: y\n---\n", "unexpected indentation"},
		{"no colon", "---\nname\n---\n", `expected "key: value"`},
		{"unterminated single", "---\nname: 'x\n---\n", "unterminated single-quoted"},
		{"unterminated double", "---\nname: \"x\n---\n", "unterminated double-quoted"},
		{"bad escape", `---` + "\n" + `name: "a\qb"` + "\n---\n", "unsupported escape"},
		// The escaped quote is consumed as content, so the value ends inside an
		// escape rather than inside a string. Both are errors; this one names
		// the actual problem.
		{"trailing backslash", `---` + "\n" + `name: "a\"` + "\n---\n", "ends in a backslash"},
		{"tab indentation", "---\nname: |\n\tone\n---\n", "tabs cannot be used"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Parse(tc.src)
			if err == nil {
				t.Fatal("parsed without complaint")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// An error names the line an editor would show, counting from the top of the
// file rather than from the start of the frontmatter.
func TestErrorsCarryFileLineNumbers(t *testing.T) {
	_, _, err := Parse("---\nname: x\ndescription: y\nbogus: z\n---\n")
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Errorf("error = %v, want it to name line 4", err)
	}
}

// The limits bound work on hostile input. Each is checked through Parse rather
// than against the constant, so a limit that stops being enforced fails here.
func TestLimits(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		big := "---\nname: x\ndescription: " + strings.Repeat("a", MaxFrontmatterBytes+1) + "\n---\n"
		if _, _, err := Parse(big); err == nil {
			t.Error("oversized frontmatter was accepted")
		}
	})
	t.Run("lines", func(t *testing.T) {
		many := "---\n" + strings.Repeat("\n", MaxFrontmatterLines+1) + "name: x\n---\n"
		if _, _, err := Parse(many); err == nil {
			t.Error("a frontmatter with too many lines was accepted")
		}
	})
	t.Run("scalar", func(t *testing.T) {
		// Built as a block scalar so the per-scalar limit is what refuses it
		// rather than the whole-frontmatter one.
		const line = "  aaaaaa\n" // 7 bytes of content once the indent is removed
		var b strings.Builder
		b.WriteString("---\nname: x\ndescription: |\n")
		for range MaxScalarBytes/7 + 2 {
			b.WriteString(line)
		}
		b.WriteString("---\n")
		if _, _, err := Parse(b.String()); err == nil || !strings.Contains(err.Error(), "longer than") {
			t.Errorf("oversized scalar: err = %v", err)
		}
	})
}

// A BOM and CRLF line endings are what a Windows editor leaves behind. Neither
// changes the meaning of a file, so neither is a parse error.
func TestToleratesBOMAndCRLF(t *testing.T) {
	fm, body, err := Parse("\ufeff---\r\nname: x\r\ndescription: y\r\n---\r\nbody\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "x" || fm.Description != "y" {
		t.Errorf("frontmatter = %+v", fm)
	}
	if !strings.HasPrefix(body, "body") {
		t.Errorf("body = %q", body)
	}
}

// The contract the fuzz target enforces, stated as a test so it is also
// checked without the corpus: any input returns a value or an error, and never
// panics.
func TestNeverPanics(t *testing.T) {
	for _, src := range []string{
		"", "-", "--", "---", "---\n", "---\n---", "---\n---\n",
		"---\n:\n---\n", "---\nname:\n---\n", "---\nname: |\n---\n",
		"---\nname: >\n\n\n---\n", "---\n\x00\n---\n", "---\nname: '\n---\n",
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic on %q: %v", src, r)
				}
			}()
			_, _, _ = Parse(src)
		}()
	}
}

func FuzzParse(f *testing.F) {
	f.Add("---\nname: x\ndescription: y\n---\nbody\n")
	f.Add("---\nname: x\ndescription: |\n  a\n  ---\n  b\n---\n")
	f.Add("---\nname: 'a''b'\ndescription: \"c\\nd\"\n---\n")
	f.Add("---\n%YAML 1.2\n---\n")
	f.Add("---\nname: >\n\ta\n---\n")
	f.Fuzz(func(t *testing.T, src string) {
		fm, _, err := Parse(src)
		if err != nil {
			return
		}
		// A successful parse must not invent values longer than the limit it
		// claims to enforce, which is the property a clever input would break.
		for _, v := range []string{fm.Name, fm.Description, fm.License, fm.Compatibility, fm.AllowedTools} {
			if len(v) > MaxScalarBytes {
				t.Fatalf("value of %d bytes survived the %d-byte limit", len(v), MaxScalarBytes)
			}
		}
	})
}
