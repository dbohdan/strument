package coder

import (
	"encoding/json"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// findTool returns the named definition, failing if it is not offered.
func findTool(tb testing.TB, defs []llm.ToolDef, name string) llm.ToolDef {
	tb.Helper()
	for _, d := range defs {
		if d.Name == name {
			return d
		}
	}
	tb.Fatalf("no %q in the offered tools", name)
	return llm.ToolDef{}
}

// TestOrderedPropsMarshalsInDeclaredOrder is the whole point of the type: a
// map would come back sorted, which is how "content" ended up ahead of "path".
func TestOrderedPropsMarshalsInDeclaredOrder(t *testing.T) {
	props := orderedProps{
		{"zebra", strProp("last alphabetically, first here")},
		{"apple", strProp("first alphabetically, last here")},
	}
	got, err := json.Marshal(props)
	if err != nil {
		t.Fatal(err)
	}
	if z, a := strings.Index(string(got), "zebra"), strings.Index(string(got), "apple"); z > a {
		t.Errorf("marshaled sorted, not in order:\n%s", got)
	}
	// It must still be a valid object with both members, not just a string
	// that happens to contain the names in the right order.
	var back map[string]any
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("the ordered form must still parse as an object: %v", err)
	}
	if len(back) != 2 {
		t.Errorf("round-tripped to %d properties, want 2", len(back))
	}
}

// TestEditToolsAdvertisePathFirst is the fix itself, asserted where a model
// sees it: on the wire, in the serialized schema, not in the Go literal.
//
// Sorted, "content" preceded "path" for write and "new_string" preceded
// "old_string" and "path" for edit. A model that emits arguments in schema
// order then sent the file's contents before naming the file, and the
// streaming diff — which cannot draw a line until it knows the file — held the
// whole thing until the call ended.
func TestEditToolsAdvertisePathFirst(t *testing.T) {
	defs := editTools()
	for _, name := range []string{toolEdit, toolWrite} {
		def := findTool(t, defs, name)
		raw, err := json.Marshal(def.Parameters)
		if err != nil {
			t.Fatal(err)
		}
		props, ok := def.Parameters["properties"].(orderedProps)
		if !ok {
			t.Fatalf("%s: properties is %T, want ordered",
				name, def.Parameters["properties"])
		}
		if first := props.names()[0]; first != "path" {
			t.Errorf("%s: properties lead with %q, want \"path\"",
				name, first)
		}
		// The serialized order is what a model actually reads. Every other
		// property must appear after the path in the bytes we send.
		pathAt := strings.Index(string(raw), `"path"`)
		for _, other := range props.names()[1:] {
			if at := strings.Index(string(raw), `"`+other+`"`); at < pathAt {
				t.Errorf("%s: %q is serialized before \"path\":\n%s",
					name, other, raw)
			}
		}
	}
}
