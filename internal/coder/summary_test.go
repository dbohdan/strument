package coder

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/prompts"
)

// summaryStub returns a fixed condensed summary for any request, counting calls.
type summaryStub struct{ calls int }

func (s *summaryStub) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		s.calls++
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "CONDENSED"}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

type summaryOutput struct{ lines []string }

func (o *summaryOutput) Printf(format string, args ...any) {
	o.lines = append(o.lines, fmt.Sprintf(format, args...))
}
func (o *summaryOutput) Warningf(format string, args ...any) { o.Printf(format, args...) }
func (o *summaryOutput) Errorf(format string, args ...any)   { o.Printf(format, args...) }
func (o *summaryOutput) Toolf(format string, args ...any)    { o.Printf(format, args...) }
func (*summaryOutput) StreamText(string)                     {}
func (*summaryOutput) StreamReasoning(string)                {}
func (*summaryOutput) StreamToolCall(int, string, string)    {}
func (*summaryOutput) FlushStream()                          {}

type summaryEmptyStub struct{ calls int }

func (s *summaryEmptyStub) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		s.calls++
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

type summaryErrStub struct{}

func (summaryErrStub) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrServer, Message: "boom"})
	}
}

// msgTok builds a message of exactly `tokens` tokens under RuneCounter (runes/4).
func msgTok(role string, tokens int) llm.Message {
	return llm.TextMessage(role, strings.Repeat("x", tokens*4))
}

func TestMaxChatHistoryTokens(t *testing.T) {
	for _, c := range []struct{ ctx, want int }{
		{0, 2048},           // unknown => floor
		{8000, 2048},        // 500 -> clamped up
		{16384, 2048},       // exactly the floor
		{100000, 12500},     // within range
		{200000, 25000},     // within range
		{1_000_000, 125000}, // within range
	} {
		if got := maxChatHistoryTokens(c.ctx); got != c.want {
			t.Errorf("maxChatHistoryTokens(%d) = %d, want %d", c.ctx, got, c.want)
		}
	}
}

func TestChatSummaryTooBig(t *testing.T) {
	s := NewChatSummary(&summaryStub{}, &config.Model{Slug: "w"}, RuneCounter{}, &summaryOutput{}, &fastClock{})
	msgs := []llm.Message{msgTok("user", 60), msgTok("assistant", 60)} // 120 tokens
	if !s.tooBig(msgs, 100) {
		t.Error("120 tokens should exceed budget 100")
	}
	if s.tooBig(msgs, 200) {
		t.Error("120 tokens should fit budget 200")
	}
}

// An assistant turn that is all tool call has no text, so counting m.Text()
// alone made the history most worth compacting look small. The budget that
// decides when to compact must see the arguments.
func TestChatSummaryCountSeesToolCalls(t *testing.T) {
	s := NewChatSummary(&summaryStub{}, &config.Model{Slug: "w"}, RuneCounter{}, &summaryOutput{}, &fastClock{})
	msgs := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
		{ID: "c1", Name: "edit", Arguments: `{"path":"a.go","old_string":"` +
			strings.Repeat("x", 4000) + `","new_string":"y"}`},
	}}}
	if got := s.total(msgs); got < 900 {
		t.Errorf("a 4KB tool call counted as %d tokens; the arguments are invisible", got)
	}
}

func TestChatSummaryCollapsesHeadKeepsTail(t *testing.T) {
	stub := &summaryStub{}
	side := &config.Model{Slug: "side", Context: 100000}
	s := NewChatSummary(stub, side, RuneCounter{}, &summaryOutput{}, &fastClock{})

	// Six big older messages + two small recent ones; budget 200 (half 100)
	// keeps the two small recent messages and collapses the rest.
	msgs := []llm.Message{
		msgTok("user", 80), msgTok("assistant", 80),
		msgTok("user", 80), msgTok("assistant", 80),
		msgTok("user", 80), msgTok("assistant", 80),
		msgTok("user", 10), msgTok("assistant", 10),
	}
	tailU, tailA := msgs[6], msgs[7]

	out, err := s.summarize(msgs, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) >= len(msgs) {
		t.Fatalf("no compaction: %d -> %d", len(msgs), len(out))
	}
	if !isSummaryMessage(out[0]) ||
		!strings.Contains(out[0].Text(), "CONDENSED") {
		t.Errorf("first message is not the summary: %s %q", out[0].Role, out[0].Text())
	}
	// The recent tail survives verbatim.
	if out[len(out)-2].Text() != tailU.Text() || out[len(out)-1].Text() != tailA.Text() {
		t.Errorf("recent tail not preserved: %+v", out)
	}
	if stub.calls != 1 {
		t.Errorf("side model called %d times, want 1", stub.calls)
	}
}

func TestChatSummarizeErrorLeavesHistoryIntact(t *testing.T) {
	s := NewChatSummary(summaryErrStub{}, &config.Model{Slug: "w", Context: 100000}, RuneCounter{}, &summaryOutput{}, &fastClock{})
	msgs := []llm.Message{
		msgTok("user", 80), msgTok("assistant", 80),
		msgTok("user", 80), msgTok("assistant", 80),
		msgTok("user", 80), msgTok("assistant", 80),
	}
	out, err := s.summarize(msgs, 100)
	if err == nil {
		t.Error("expected an error from the failing side model")
	}
	if len(out) != len(msgs) {
		t.Errorf("history changed on error: %d -> %d", len(msgs), len(out))
	}
}

func TestMaybeSummarizeGating(t *testing.T) {
	// ~2500 tokens of settled history, so it clears the 2048 floor threshold.
	bigHistory := func() []llm.Message {
		return []llm.Message{
			msgTok("user", 300), msgTok("assistant", 300),
			msgTok("user", 300), msgTok("assistant", 300),
			msgTok("user", 300), msgTok("assistant", 300),
			msgTok("user", 300), msgTok("assistant", 300),
			msgTok("user", 50), msgTok("assistant", 50),
		}
	}

	t.Run("unknown context is a no-op", func(t *testing.T) {
		c := testCoder(t)
		c.Model.Context = 0
		c.Summarizer = NewChatSummary(&summaryStub{}, c.Model.SideModel, c.Tokens, c.Out, c.Clock)
		c.doneMessages = bigHistory()
		c.maybeSummarize()
		if len(c.doneMessages) != 10 {
			t.Errorf("summarized despite an unknown window: %d", len(c.doneMessages))
		}
	})

	t.Run("no summarizer is a no-op", func(t *testing.T) {
		c := testCoder(t)
		c.Model.Context = 16384
		c.doneMessages = bigHistory()
		c.maybeSummarize()
		if len(c.doneMessages) != 10 {
			t.Errorf("summarized without a summarizer wired: %d", len(c.doneMessages))
		}
	})

	t.Run("under budget is a no-op", func(t *testing.T) {
		c := testCoder(t)
		c.Model.Context = 1_000_000 // threshold 125_000; ~2500 tokens fits
		c.Summarizer = NewChatSummary(&summaryStub{}, c.Model.SideModel, c.Tokens, c.Out, c.Clock)
		c.doneMessages = bigHistory()
		c.maybeSummarize()
		if len(c.doneMessages) != 10 {
			t.Errorf("summarized while under budget: %d", len(c.doneMessages))
		}
	})

	t.Run("over budget compacts", func(t *testing.T) {
		c := testCoder(t)
		c.Model.Context = 16384 // threshold 2048; ~2500 tokens overflows
		stub := &summaryStub{}
		c.Summarizer = NewChatSummary(stub, c.Model.SideModel, c.Tokens, c.Out, c.Clock)
		c.doneMessages = bigHistory()
		c.maybeSummarize()
		if len(c.doneMessages) >= 10 {
			t.Errorf("did not compact over-budget history: %d", len(c.doneMessages))
		}
		if stub.calls == 0 {
			t.Error("side model was never called")
		}
	})
}

func TestMaybeSummarizeReportsCompaction(t *testing.T) {
	c := testCoder(t)
	out := &summaryOutput{}
	c.Out = out
	c.Model.Context = 16384
	c.Summarizer = NewChatSummary(&summaryStub{}, c.Model.SideModel, c.Tokens, c.Out, c.Clock)
	c.doneMessages = []llm.Message{
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 50), msgTok("assistant", 50),
	}

	c.maybeSummarize()

	var report string
	for _, line := range out.lines {
		if strings.HasPrefix(line, "Chat history compacted:") {
			report = line
		}
	}
	if report == "" {
		t.Fatalf("compaction report not printed: %v", out.lines)
	}
	if !strings.Contains(report, "2500 tokens/10 messages ->") {
		t.Errorf("report does not include before counts: %q", report)
	}
	if !strings.Contains(report, "1 summary retained") {
		t.Errorf("report does not include retained summary count: %q", report)
	}
}

func TestMaybeSummarizeBacksOffAfterFailure(t *testing.T) {
	c := testCoder(t)
	out := &summaryOutput{}
	c.Out = out
	c.Model.Context = 16384
	stub := &summaryEmptyStub{}
	c.Summarizer = NewChatSummary(stub, c.Model.SideModel, c.Tokens, c.Out, c.Clock)
	c.doneMessages = []llm.Message{
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 50), msgTok("assistant", 50),
	}

	// One attempt plus its empty-response retries; the second call is skipped
	// entirely by the backoff.
	perAttempt := maxEmptyRetries + 1

	c.maybeSummarize()
	c.maybeSummarize()
	if stub.calls != perAttempt {
		t.Errorf("side model called %d times during backoff, want %d", stub.calls, perAttempt)
	}

	c.maybeSummarize()
	if stub.calls != 2*perAttempt {
		t.Errorf("side model called %d times after backoff, want %d", stub.calls, 2*perAttempt)
	}
	// The empty summary is caught at the transport, where it can still be
	// retried, rather than downstream by validCompaction. Same backoff, named
	// cause: "the result was not smaller" describes a summary that came back,
	// and nothing came back.
	if !strings.Contains(strings.Join(out.lines, "\n"), emptySideResponse) {
		t.Errorf("the empty response was not named:\n%s", strings.Join(out.lines, "\n"))
	}
}

// summaryBloatStub returns a summary larger than the history it replaces.
//
// validCompaction's own case, which used to be covered only by accident: the
// empty stub above tripped it, and now fails earlier as a transport error, so
// nothing would exercise the size check without a stub that actually answers.
type summaryBloatStub struct{ calls int }

func (s *summaryBloatStub) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		s.calls++
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: strings.Repeat("summary text ", 4000)}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

func TestMaybeSummarizeRejectsABiggerSummary(t *testing.T) {
	c := testCoder(t)
	out := &summaryOutput{}
	c.Out = out
	c.Model.Context = 16384
	stub := &summaryBloatStub{}
	c.Summarizer = NewChatSummary(stub, c.Model.SideModel, c.Tokens, c.Out, c.Clock)
	before := []llm.Message{
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 300), msgTok("assistant", 300),
		msgTok("user", 50), msgTok("assistant", 50),
	}
	c.doneMessages = before

	c.maybeSummarize()

	if !strings.Contains(strings.Join(out.lines, "\n"), "result was not smaller") {
		t.Errorf("a summary bigger than the history was accepted:\n%s", strings.Join(out.lines, "\n"))
	}
	if len(c.doneMessages) != len(before) {
		t.Errorf("the history was replaced by the bigger summary: %d messages", len(c.doneMessages))
	}
	if !c.summaryBackoff {
		t.Error("no backoff was set, so the next turn pays for the same failure")
	}
}

// the tests below assert on the wire rather than on the rendering function.
type captureStub struct{ sent string }

func (c *captureStub) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, m := range req.Messages {
			if m.Role == llm.RoleUser {
				c.sent = m.Text()
			}
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "CONDENSED"}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// Compaction must not fabricate a turn. aider's prompt ended "Write as the user,
// in the first person... Begin with \"I asked you...\"", the result went in as a
// user message under "I spoke to you previously about a number of things.", and
// the coder appended an assistant "Ok." agreeing to it — the exact pattern
// readOnlyFilesPrefix's comment describes as what it replaced.
//
// Pinned as a property of the output rather than of the wording, so a future
// rewrite of the prompt cannot quietly reintroduce the shape.
func TestSummaryFabricatesNoTurn(t *testing.T) {
	s := NewChatSummary(&summaryStub{}, &config.Model{Slug: "side", Context: 100000}, RuneCounter{}, &summaryOutput{}, &fastClock{})
	msgs := []llm.Message{
		msgTok("user", 80), msgTok("assistant", 80),
		msgTok("user", 80), msgTok("assistant", 80),
		msgTok("user", 80), msgTok("assistant", 80),
		msgTok("user", 10), msgTok("assistant", 10),
	}

	out, err := s.summarize(msgs, 200)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range out {
		if strings.Contains(m.Text(), "CONDENSED") {
			// It rides in a user-role message, so the marker is the whole of
			// what keeps it from reading as the user's own words. Without it
			// this is aider's shape again with better prose.
			if !strings.HasPrefix(m.Text(), llm.HarnessMarker) {
				t.Errorf("the summary is unmarked; it reads as something %s said", m.Role)
			}
			if m.Role == llm.RoleAssistant {
				t.Error("the summary is an assistant turn; the model did not say this")
			}
		}
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Text()) == "Ok." {
			t.Errorf("message %d is a fabricated assistant agreement", i)
		}
	}
	if last := out[len(out)-1]; strings.TrimSpace(last.Text()) == "Ok." {
		t.Error("compaction still ends on a fabricated \"Ok.\"")
	}
}

// The prompt itself must not ask for the impersonation either. Fixing the
// injection alone would have left the side model writing "I asked you..." into
// the summary text, where it reads as the user's words wherever it lands.
func TestSummarizePromptDoesNotAskForImpersonation(t *testing.T) {
	for _, banned := range []string{"as the user", "first person", "I asked you", "refer to the assistant"} {
		if strings.Contains(strings.ToLower(prompts.Summarize), strings.ToLower(banned)) {
			t.Errorf("the summarize prompt still asks for %q", banned)
		}
	}
}

// A tool-driven harness whose summarizer only reads prose summarizes the wrong
// thing: twelve tool calls and one closing sentence became that sentence.
func TestSummarySeesToolWork(t *testing.T) {
	stub := &captureStub{}
	s := NewChatSummary(stub, &config.Model{Slug: "side", Context: 100000}, RuneCounter{}, &summaryOutput{}, &fastClock{})

	big := strings.Repeat("z", summaryToolBytes*2)
	msgs := []llm.Message{
		llm.TextMessage("user", strings.Repeat("q", 400)),
		{Role: llm.RoleAssistant, Content: llm.TextContent("looking"), ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "grep", Arguments: `{"pattern":"defaultTimeout"}`},
		}},
		llm.ToolResult("c1", "internal/poll/poll.go:3: "+big),
		// A prior summary, which is a system message now. Skipping the role
		// would erase compaction's own output on the next pass.
		llm.HarnessNote(prompts.SummaryLabel + "EARLIER-WORK-MARKER"),
		msgTok("user", 10), msgTok("assistant", 10),
	}
	if _, err := s.summarizeAll(msgs); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"grep", "defaultTimeout", "EARLIER-WORK-MARKER"} {
		if !strings.Contains(stub.sent, want) {
			t.Errorf("the summarizer never saw %q:\n%s", want, stub.sent)
		}
	}
	// The result is there but clipped: enough to see what came back, not enough
	// to pay for the file contents twice.
	if !strings.Contains(stub.sent, "(cut;") {
		t.Error("an oversized tool result should be clipped and say so")
	}
	if len(stub.sent) > summaryToolBytes*3 {
		t.Errorf("rendered input is %d bytes; the clip is not holding", len(stub.sent))
	}
}
