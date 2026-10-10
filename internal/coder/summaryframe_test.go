package coder

import (
	"context"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// answerStub replies with a fixed answer and keeps the user message it was sent.
type answerStub struct{ answer, sent string }

func (a *answerStub) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, m := range req.Messages {
			if m.Role == llm.RoleUser {
				a.sent = m.Text()
			}
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: a.answer}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

const finalAnswer = "Read all five files in full, one per step. What they have in common: each one " +
	"tracks progress explicitly and latches a sticky first error."

func framedHistory() []llm.Message {
	return []llm.Message{
		llm.TextMessage("user", "Note for later: the staging region is eu-north-7. Read five files."),
		llm.TextMessage("assistant", finalAnswer),
	}
}

// The transcript ends on the assistant's answer, so unframed, the likeliest
// next text is more transcript. The frame puts the instruction after it too.
func TestSummaryInputIsFramed(t *testing.T) {
	stub := &answerStub{answer: "Said by the user:\n- the staging region is eu-north-7\n\nFive files were read."}
	s := NewChatSummary(stub, &config.Model{Slug: "side", Context: 100000}, RuneCounter{}, &summaryOutput{}, &fastClock{}, nil)
	if _, err := s.summarizeAll(framedHistory()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stub.sent, prompts.SummaryInputBefore) {
		t.Errorf("the input does not open with the framing instruction:\n%s", stub.sent)
	}
	if !strings.HasSuffix(stub.sent, "</conversation>\n\n"+prompts.SummaryInputAfter) {
		t.Errorf("the input does not end with the instruction after the transcript:\n%s", stub.sent)
	}
	if !strings.Contains(stub.sent, "<conversation>\n# USER\n") {
		t.Errorf("the transcript is not inside the markers:\n%s", stub.sent)
	}
}

// Both shapes the unframed summarizer produced in the trial are refused, and
// the history is left as it was rather than replaced by them.
func TestContinuationIsNotASummary(t *testing.T) {
	for name, answer := range map[string]string{
		"tool call":   `<tool_call><function=read><parameter=file_path>std/x.go</parameter></function></tool_call>`,
		"tool call 2": "I'll read the next file.<tool_call>{\"name\": \"read\"}</tool_call>",
		"copy":        finalAnswer + "\n\nNoted for later: the staging region is eu-north-7.",
		"copy reflow": strings.ReplaceAll(finalAnswer, " one per step.", "\none per step."),
	} {
		t.Run(name, func(t *testing.T) {
			s := NewChatSummary(&answerStub{answer: answer}, &config.Model{Slug: "side", Context: 100000},
				RuneCounter{}, &summaryOutput{}, &fastClock{}, nil)
			msgs := append(framedHistory(), msgTok("user", 80), msgTok("assistant", 80))
			msgs = append(msgs, llm.TextMessage("assistant", finalAnswer))
			out, err := s.summarize(msgs, 50)
			if err == nil {
				t.Fatalf("a continuation was accepted as a summary: %q", out)
			}
			if len(out) != len(msgs) {
				t.Errorf("the history changed: %d -> %d messages", len(msgs), len(out))
			}
		})
	}
}

// The guard is narrow on purpose: a summary may restate the last conclusion,
// mention the read tool, or begin with the user's list, and is still one.
func TestRealSummaryPassesTheGuard(t *testing.T) {
	for _, answer := range []string{
		"Said by the user:\n- the staging region is eu-north-7\n\nFive files were read with the read tool. " + finalAnswer,
		"Five files were read; in common, each tracks progress explicitly and latches a sticky first error.",
		"OK", // shorter than any copy the guard looks for
	} {
		s := NewChatSummary(&answerStub{answer: answer}, &config.Model{Slug: "side", Context: 100000},
			RuneCounter{}, &summaryOutput{}, &fastClock{}, nil)
		if _, err := s.summarizeAll(framedHistory()); err != nil {
			t.Errorf("a real summary was refused: %v\n%s", err, answer)
		}
	}
}
