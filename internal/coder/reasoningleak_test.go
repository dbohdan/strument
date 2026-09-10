package coder

import "testing"

// The live case: Ling-3.0-tiny on llama.cpp b10902, whose Bailing V3 template
// ends the generation prompt with an open "<think>". The wire capture showed
// 164 characters of reasoning_content, no content and no tool calls, and the
// harness called that "empty".
func TestReasoningLeakNamesTheMarker(t *testing.T) {
	const trapped = "<tool_call>bash\n<arg_key>command</arg_key>\n" +
		"<arg_value>which node</arg_value>\n</tool_call>"

	marker, leaked := reasoningLeak("", trapped, 0)
	if !leaked {
		t.Fatal("a reply whose only output is a tool call in reasoning is not an empty response")
	}
	if marker != "<tool_call>" {
		t.Errorf("marker = %q, want the opener that matched", marker)
	}
}

// The counter-arms. Each is a state that must NOT be reported as a leak, or
// the check is measuring "reasoning is non-empty" rather than the phenomenon.
func TestReasoningLeakStaysQuiet(t *testing.T) {
	const trapped = "<tool_call>bash\n<arg_key>command</arg_key>\n</tool_call>"

	for _, tc := range []struct {
		name              string
		answer, reasoning string
		toolCalls         int
	}{
		{"the tool call actually arrived", "", trapped, 1},
		{"the model also answered", "Here you go.", trapped, 0},
		{"ordinary thinking, no action in it", "", "Let me look at the file first.", 0},
		{"genuinely empty", "", "", 0},
		{"reasoning is only whitespace", "", "  \n\t ", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, leaked := reasoningLeak(tc.answer, tc.reasoning, tc.toolCalls); leaked {
				t.Error("reported a leak where there is none")
			}
		})
	}
}
