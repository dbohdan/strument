package anchor

import "testing"

// Round-tripping is the property the write side depends on: what read printed
// must reconstruct the line byte for byte, or the column is a lie.
func TestIndentRoundTrips(t *testing.T) {
	for _, run := range []string{"", "\t", "\t\t\t", " ", "    ", "\t  ", "  \t\t", "\t \t"} {
		enc := EncodeIndent(run)
		got, ok := ParseIndent(enc)
		if !ok {
			t.Errorf("EncodeIndent(%q) = %q, which does not parse", run, enc)
			continue
		}
		if got != run {
			t.Errorf("%q -> %q -> %q, want the original back", run, enc, got)
		}
	}
}

func TestEncodeIndentNames(t *testing.T) {
	for _, tc := range []struct{ run, want string }{
		{"", "0 spaces"},
		{"\t", "1 tab"},
		{"\t\t", "2 tabs"},
		{"    ", "4 spaces"},
		{"\t  ", "1 tab 2 spaces"},
	} {
		if got := EncodeIndent(tc.run); got != tc.want {
			t.Errorf("EncodeIndent(%q) = %q, want %q", tc.run, got, tc.want)
		}
	}
}

// Strictness is the whole value. Each of these is a model that has lost track
// of the indentation it is claiming, and accepting any of them writes a file
// nobody asked for.
func TestParseIndentIsStrict(t *testing.T) {
	for _, bad := range []string{
		"1 tabs",    // agreement
		"2 tab",     // agreement
		"0 space",   // agreement
		"tab",       // no count
		"3",         // no unit
		"3 indents", // unknown unit
		"-1 tabs",
		"1 tab 2",
		"",
		"lots of tabs",
		"99999999 tabs", // out of range: a typo, not an indent
	} {
		if got, ok := ParseIndent(bad); ok {
			t.Errorf("ParseIndent(%q) accepted, giving %q", bad, got)
		}
	}
}

func TestSplitIndent(t *testing.T) {
	for _, tc := range []struct{ line, run, rest string }{
		{"\t\treturn nil", "\t\t", "return nil"},
		{"package p", "", "package p"},
		{"   ", "   ", ""},
		{"", "", ""},
	} {
		run, rest := SplitIndent(tc.line)
		if run != tc.run || rest != tc.rest {
			t.Errorf("SplitIndent(%q) = %q,%q want %q,%q", tc.line, run, rest, tc.run, tc.rest)
		}
	}
}
