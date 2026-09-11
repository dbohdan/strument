package coder

import "strings"

// toolCallOpeners are the markers models use to begin a tool call in their own
// native syntax, before an inference server translates it into the API's
// tool_calls field.
//
// They are matched, never parsed. A tool call that arrives inside reasoning is
// a draft: the model is in the middle of deciding, and a think block is exactly
// where a model considers an action and then talks itself out of it. Executing
// what we find there would run the rejected branch, and would put the harness
// in the business of implementing a second, per-family tool-call protocol that
// belongs to the inference server. So this list exists to explain a failed
// turn, and for nothing else.
var toolCallOpeners = []string{
	"<tool_call>",          // Qwen, GLM, Ling/Bailing
	"<function_call>",      // several
	"<function=",           // Llama-style pythonic
	"<|tool_calls_begin|>", // DeepSeek
	"<|tool▁calls▁begin|>", // DeepSeek, with its private-use separators
	"[TOOL_CALLS]",         // Mistral
	"<|python_tag|>",       // Llama 3.x built-ins
}

// reasoningLeak reports the marker a reply's reasoning holds, if the reply said
// nothing and called nothing but its reasoning looks like it tried to.
//
// This exists because the honest reading of that state is not "empty". In a
// live session against Ling-3.0-tiny on llama.cpp b10902 the model produced
// 5.1k tokens, the whole of one turn's work, and the harness reported an empty
// response — because every token landed in reasoning_content. The cause is one
// line of the Bailing V3 chat template: it ends the generation prompt with an
// open "<think>", so the model starts inside its own reasoning block and must
// close it before acting. Emit the tool call a token early and the server files
// the action as a thought, correctly, by its own rules.
//
// doc/experimenting.md#no-answer-vs-wrong-answer is the general form: "no
// answer" and "wrong answer" are different columns, and a provider failure and
// a model whose tool call went out as text mean opposite things. This is the
// harness's own instance of it, in a user-facing message, not in a scorer.
func reasoningLeak(answer, reasoning string, toolCalls int) (marker string, leaked bool) {
	if answer != "" || toolCalls > 0 || strings.TrimSpace(reasoning) == "" {
		return "", false
	}
	for _, m := range toolCallOpeners {
		if strings.Contains(reasoning, m) {
			return m, true
		}
	}
	return "", false
}
