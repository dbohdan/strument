package llm

import (
	"encoding/json"
	"testing"
)

// Whatever a model produces, what leaves for a provider has to be valid JSON.
//
// The case that prompted this is the truncated one: Ling 3.0 Flash spent its
// 32,768-token output budget on reasoning, began a write call, and was cut off
// mid-string. OpenRouter then refused every later request in the session —
// "Assistant tool call function.arguments must be valid JSON" — because the
// malformed call was in the history and went out with each one.
func TestWireArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		want string
		why  string
	}{
		{
			name: "valid JSON is passed through untouched",
			args: `{"path": "a.txt", "content": "hi"}`,
			want: `{"path": "a.txt", "content": "hi"}`,
		},
		{
			name: "truncated mid-string",
			args: `{"path": "/tmp/x.html", "content": "<!DOCTYPE htm`,
			want: "{}",
			why:  "the reply hit the output limit inside the argument string",
		},
		{
			name: "empty, as some models send for a no-argument tool",
			args: "",
			want: "{}",
			why:  "an empty string is not valid JSON, and providers reject it",
		},
		{
			name: "whitespace only",
			args: "   \n",
			want: "{}",
		},
		{
			name: "a valid JSON value that is not an object",
			args: `"just a string"`,
			want: `"just a string"`,
			why:  "valid is the rule here; whether the tool accepts it is the tool's business",
		},
		{
			name: "an empty object is already valid",
			args: "{}",
			want: "{}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ToolCall{Arguments: tc.args}.WireArguments()
			if got != tc.want {
				t.Errorf("WireArguments() = %q, want %q\n%s", got, tc.want, tc.why)
			}
			// The property that matters, asserted rather than assumed: whatever
			// comes back parses. A table can be edited into agreeing with a
			// broken implementation; this cannot.
			if !json.Valid([]byte(got)) {
				t.Errorf("WireArguments() returned %q, which is not valid JSON", got)
			}
		})
	}
}
