// Config-provided few-shot examples (example_messages) must reach the
// conversation and behave across format switches.

package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

func TestExamplesReachAssembly(t *testing.T) {
	c := testCoder(t)
	c.Examples = []config.ExampleMessage{
		{Role: "user", Content: "What is two plus two?"},
		{Role: "assistant", Content: "Four."},
	}
	c.SetEditFormat("") // re-applies the examples, as session setup does

	msgs := c.formatChatChunks().allMessages()
	found := false
	for _, m := range msgs {
		if m.Role == llm.RoleUser && strings.Contains(m.Text(), "two plus two") {
			found = true
		}
	}
	if !found {
		t.Errorf("the example user message did not reach assembly:\n%v", msgs)
	}
}

// SetEditFormat rebuilds the prompt set from scratch, so a switch to /ask and
// back must not duplicate or drop the config examples — re-application has to
// be idempotent because the built-in sets carry no copy of them.
func TestExamplesSurviveFormatSwitch(t *testing.T) {
	c := testCoder(t)
	c.Examples = []config.ExampleMessage{{Role: "user", Content: "example one"}}

	c.SetEditFormat("")
	c.SetEditFormat("ask")
	c.SetEditFormat("")

	count := 0
	for _, m := range c.formatChatChunks().allMessages() {
		if strings.Contains(m.Text(), "example one") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("example appeared %d times after format switches, want 1", count)
	}
}
