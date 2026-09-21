package coder

import (
	"slices"
	"testing"
)

// codeToolParams names a positional order for arguments a ToolDef declares in a
// map, so the two can drift, and both directions of drift are silent.
//
// A parameter added to a schema and not here cannot be passed positionally —
// Monty drops a positional past the end of the list, without an error. One
// removed from a schema and left here binds a positional to a name the tool no
// longer accepts, which the tool then reports as unknown while the argument the
// program meant goes nowhere.
func TestCodeToolParamsMatchTheSchemas(t *testing.T) {
	schemas := map[string][]string{}
	for _, def := range append(readOnlyTools(), symbolTool()) {
		props, ok := def.Parameters["properties"].(map[string]any)
		if !ok {
			continue
		}
		var names []string
		for name := range props {
			names = append(names, name)
		}
		slices.Sort(names)
		schemas[def.Name] = names
	}

	// The counter-arm. If the tool definitions stop being reachable this way
	// the loop below iterates over nothing and passes by checking nothing.
	if len(schemas) < len(InspectorTools()) {
		t.Fatalf("found schemas for %d tools, want at least the %d bridged ones; "+
			"this check is passing because it found nothing to check", len(schemas), len(InspectorTools()))
	}

	for _, tool := range InspectorTools() {
		want, ok := schemas[tool]
		if !ok {
			t.Errorf("no schema found for the bridged tool %q", tool)
			continue
		}
		got := slices.Clone(codeToolParams[tool])
		if got == nil {
			t.Errorf("%s is callable from run_code and has no positional order, so every "+
				"positional argument to it is dropped in silence", tool)
			continue
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("codeToolParams[%q] = %v, but its schema declares %v.\n"+
				"Every parameter needs a position or it cannot be passed positionally, "+
				"and a position with no parameter binds an argument to a name the tool rejects.",
				tool, codeToolParams[tool], want)
		}
	}
}

// Every function the program can call needs an order, including the run_code-only
// ones — whose summaries state a signature, so a program written to the
// documentation is exactly the program that breaks without it. The data shapes
// are in the same position, with more history behind them: `glob("*.go")` is
// the first call a model writes, and the positional that name binds is the one
// the params table exists to bind.
func TestCodeFuncsDeclareTheirParams(t *testing.T) {
	both := append(slices.Clone(codeFuncs), codeDataFuncs...)
	if len(both) == 0 {
		t.Fatal("the registries are empty; this check has nothing to check")
	}
	for _, d := range both {
		if len(d.params) == 0 {
			t.Errorf("the code function %q declares no positional order", d.name)
		}
	}
}

// TestCodeDataFuncsMatchTheirSchemas holds the data shapes to the same
// discipline the bridged tools are held to: the params each registry entry
// declares must match the parameters of the tool schema carrying the same
// name — which is the schema a model reads when it writes glob(pattern=...)
// or ls(path=...). A drift here means the data shape silently rejects an
// argument the tool documents.
func TestCodeDataFuncsMatchTheirSchemas(t *testing.T) {
	schemas := map[string][]string{}
	for _, def := range append(readOnlyTools(), symbolTool()) {
		props, ok := def.Parameters["properties"].(map[string]any)
		if !ok {
			continue
		}
		var names []string
		for name := range props {
			names = append(names, name)
		}
		schemas[def.Name] = names
	}

	for _, d := range codeDataFuncs {
		want, ok := schemas[d.name]
		if !ok {
			t.Errorf("no tool schema found for the data function %q", d.name)
			continue
		}
		if !slices.Equal(d.params, want) {
			t.Errorf("codeDataFuncs[%q].params = %v, but the tool schema declares %v; "+
				"a program passing a documented argument must not lose it to the override",
				d.name, d.params, want)
		}
	}
}
