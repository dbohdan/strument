// Pins the aborted-turn usage report: when no provider usage arrives (the
// turn was interrupted before the final usage chunk), finalizeUsage falls
// back to the pre-send estimate and the streamed-reply estimate, marks the
// line "(estimated)", and still folds the cost into the session totals.

package coder

import (
	"context"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

func TestFinalizeUsageEstimatesWhenAborted(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.Model.InputCost = &llm.Money{Known: true, USD: 0.000001}
	c.Model.OutputCost = &llm.Money{Known: true, USD: 0.000002}
	c.partialResponseContent = strings.Repeat("x", 40) // RuneCounter: ~10 tokens

	c.finalizeUsage(&sendUsage{estSent: 1234}) // all-zero usage => estimate path

	sent, received := c.SessionTokens()
	if sent != 1234 {
		t.Errorf("session sent = %d, want 1234 (pre-send estimate)", sent)
	}
	if received == 0 {
		t.Errorf("received should be estimated from the streamed reply, got 0")
	}
	if cost, known := c.SessionCost(); !known || cost <= 0 {
		t.Errorf("estimated cost should be known and > 0, got %v (known=%v)", cost, known)
	}
	if !strings.Contains(c.lastUsageReport, "(estimated)") {
		t.Errorf("report must be marked estimated: %q", c.lastUsageReport)
	}
}

func TestFinalizeUsageRealUsageNotMarked(t *testing.T) {
	c := toolCoder(t, t.TempDir())

	c.finalizeUsage(&sendUsage{prompt: 100, completion: 50, estSent: 9999})

	if sent, received := c.SessionTokens(); sent != 100 || received != 50 {
		t.Errorf("session tokens = (%d, %d), want (100, 50) from real usage", sent, received)
	}
	if strings.Contains(c.lastUsageReport, "(estimated)") {
		t.Errorf("real usage must not be marked estimated: %q", c.lastUsageReport)
	}
}

// A request the provider refused was never processed: no usage, no reply,
// nothing billed. It used to be reported as
//
//	Tokens: 735 sent, 0 received. (estimated)
//
// — the harness's own pre-send estimate for a request that never ran, printed
// in the one place a reader looks for what they were charged. Under a 401 that
// is the opposite of what happened.
func TestRefusedRequestReportsNothing(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.Model.InputCost = &llm.Money{Known: true, USD: 0.000001}
	c.Model.OutputCost = &llm.Money{Known: true, USD: 0.000002}

	c.finalizeUsage(&sendUsage{estSent: 735, rejected: true})

	if sent, recv := c.SessionTokens(); sent != 0 || recv != 0 {
		t.Errorf("session tokens = (%d, %d), want (0, 0)", sent, recv)
	}
	if _, known := c.SessionCost(); known {
		t.Error("a refused request must not put a cost on the session")
	}
	if c.lastUsageReport != "" {
		t.Errorf("report = %q, want none", c.lastUsageReport)
	}
	// Zero sends is what makes flushTurnUsage say nothing at all rather than
	// print a line of zeroes.
	if c.messageSends != 0 {
		t.Errorf("messageSends = %d, want 0", c.messageSends)
	}
}

// A stream that broke *after* the model had already said something is the other
// case: those tokens were generated and will be billed, so the estimate stands.
func TestStreamBrokenMidReplyStillEstimates(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.partialResponseContent = strings.Repeat("x", 40) // RuneCounter: ~10 tokens

	c.finalizeUsage(&sendUsage{estSent: 735, rejected: true})

	if sent, recv := c.SessionTokens(); sent != 735 || recv == 0 {
		t.Errorf("session tokens = (%d, %d), want (735, >0)", sent, recv)
	}
	if !strings.Contains(c.lastUsageReport, "(estimated)") {
		t.Errorf("report must be marked estimated: %q", c.lastUsageReport)
	}
}

// refuseStub is a provider that answers every request with a refusal and
// streams nothing — a bad key, a model slug that does not exist.
type refuseStub struct{ class llm.ErrorClass }

func (s refuseStub) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, &llm.StreamError{
			Class: s.class, Message: "HTTP 401: No auth credentials found"})
	}
}

// The end-to-end half: streamOnce has to mark the send refused for the check
// above to fire on a real failure.
func TestFailedSendAccountsForNothing(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.Client = refuseStub{llm.ErrAuth}

	if out, _ := c.sendMessage(context.Background(), "hello"); out != OutcomeFailed {
		t.Errorf("outcome = %v, want OutcomeFailed", out)
	}
	if sent, recv := c.SessionTokens(); sent != 0 || recv != 0 {
		t.Errorf("session tokens = (%d, %d), want (0, 0)", sent, recv)
	}
	if c.messageSends != 0 {
		t.Errorf("messageSends = %d, want 0 — a failed turn prints no usage line", c.messageSends)
	}
}

// The cache figures live under prompt_tokens_details and are a breakdown of
// prompt_tokens, not additions to it. These are real numbers off the wire: one
// cold request with a cache breakpoint, then the same request warm.
//
// Adding the write to the prompt counted almost the whole prompt twice, and did
// so only on models that write a cache — so it looked like GPT-5.6 Luna was
// sending an order of magnitude more than Haiku for identical work, rather than
// like an arithmetic bug. total_tokens is the tell: 14021 + 5 = 14026, with the
// 14018-token write inside the prompt, not beside it.
func TestSentDoesNotDoubleCountTheCache(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage sendUsage
	}{
		{"cold: the whole prompt is written to cache",
			sendUsage{prompt: 14021, completion: 5, cacheWrite: 14018}},
		{"warm: the whole prompt is served from cache",
			sendUsage{prompt: 14021, completion: 5, cacheRead: 14018}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := toolCoder(t, t.TempDir())
			u := tc.usage
			c.finalizeUsage(&u)

			if sent, _ := c.SessionTokens(); sent != 14021 {
				t.Errorf("sent = %d, want 14021 (prompt_tokens, cache included)", sent)
			}
		})
	}
}

// Every token is priced exactly once, at whichever rate applies to it. The
// fallback formula charged a full-price prompt *and* the cache rates on top,
// the same double-count the token line had. This path runs only when the
// provider reports no in-band cost.
func TestFallbackCostPricesEachTokenOnce(t *testing.T) {
	const pin, pout = 0.000001, 0.000002
	c := toolCoder(t, t.TempDir())
	c.Model.InputCost = &llm.Money{Known: true, USD: pin}
	c.Model.OutputCost = &llm.Money{Known: true, USD: pout}

	// 1000 prompt tokens: 600 written to cache, 300 served from it, 100 fresh.
	c.finalizeUsage(&sendUsage{prompt: 1000, completion: 10, cacheWrite: 600, cacheRead: 300})

	want := 600*pin*1.25 + 300*pin*0.10 + 100*pin + 10*pout
	got, known := c.SessionCost()
	if !known {
		t.Fatal("cost should be known from configured pricing")
	}
	if diff := got - want; diff > 1e-12 || diff < -1e-12 {
		t.Errorf("cost = %v, want %v (each token once)", got, want)
	}
}

// The commit-message request went out through the client directly and never
// reached finalizeUsage, so the turn line reported four requests where five
// were paid for — $0.00084 against $0.00093 on a measured turn, and the share
// grows with the diff because that call re-sends it uncached.
func TestSideUsageReachesTheTurnTotals(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.finalizeUsage(&sendUsage{prompt: 2000, completion: 100})

	cost := 0.0001
	c.RecordTurnSideUsage(llm.Usage{PromptTokens: 794, CompletionTokens: 13, Cost: &cost})

	if sent, recv := c.SessionTokens(); sent != 2794 || recv != 113 {
		t.Errorf("session tokens = (%d, %d), want (2794, 113)", sent, recv)
	}
	if got, known := c.SessionCost(); !known || got != cost {
		t.Errorf("cost = %v (known=%v), want %v", got, known, cost)
	}
	// The peak is a high-water mark, not a sum: the 794-token side request
	// never displaces the 2000-token one it rode behind.
	if got := c.TokensReport(); !strings.Contains(got, "Largest request so far: 2000") {
		t.Errorf("peak should stay at the largest single request:\n%s", got)
	}
}

// The cache figures are parenthesized because they are a breakdown. A flat
// comma list reads as separate quantities, which is how the arithmetic bug
// above stayed plausible for as long as it did.
func TestTokenLineParenthesizesTheBreakdown(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"both", "Tokens: 14.0k sent (14.0k cache write, 1.0k cache hit), 5 received."},
		{"neither", "Tokens: 14.0k sent, 5 received."},
	} {
		var got string
		if tc.name == "both" {
			got = formatTokenLine(14021, 14018, 1000, 5)
		} else {
			got = formatTokenLine(14021, 0, 0, 5)
		}
		if got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// The startup --continue notes call is a side request that reports its usage
// through RecordSideUsage. FlushSideUsage prints the same token/cost line a turn
// ends with, so the charge is not invisible — but only when something was
// actually spent.
func TestFlushSideUsageReportsTheSideCall(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	out := &summaryOutput{}
	c.Out = out

	cost := 0.0003
	c.RecordSideUsage(llm.Usage{
		PromptTokens:     794,
		CompletionTokens: 13,
		Cost:             &cost,
	})

	c.FlushSideUsage()

	lines := strings.Join(out.lines, "\n")
	if !strings.Contains(lines, "Tokens: 794 sent, 13 received.") {
		t.Errorf("side usage line missing token counts:\n%s", lines)
	}
	if !strings.Contains(lines, "Cost: $0.00030 message, $0.00030 session.") {
		t.Errorf("side usage line missing the cost:\n%s", lines)
	}
}

func TestFlushSideUsageConsumesOnlySideUsage(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	out := &summaryOutput{}
	c.Out = out

	c.finalizeUsage(&sendUsage{prompt: 2000, completion: 100})
	c.flushTurnUsage()

	firstCost := 0.0003
	c.RecordSideUsage(llm.Usage{PromptTokens: 794, CompletionTokens: 13, Cost: &firstCost})
	c.FlushSideUsage()

	secondCost := 0.0004
	c.RecordSideUsage(llm.Usage{PromptTokens: 500, CompletionTokens: 7, Cost: &secondCost})
	c.FlushSideUsage()
	c.FlushSideUsage()

	lines := strings.Join(out.lines, "\n")
	if strings.Count(lines, "Tokens:") != 3 {
		t.Errorf("unexpected number of usage lines:\n%s", lines)
	}
	for _, want := range []string{
		"Tokens: 794 sent, 13 received.",
		"Cost: $0.00030 message, $0.00030 session.",
		"Tokens: 500 sent, 7 received.",
		"Cost: $0.00040 message, $0.00070 session.",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("side usage line missing %q:\n%s", want, lines)
		}
	}
	if strings.Contains(lines, "Tokens: 2.3k sent") {
		t.Errorf("side usage was compounded:\n%s", lines)
	}
	if sent, received := c.SessionTokens(); sent != 3294 || received != 120 {
		t.Errorf("session tokens = (%d, %d), want (3294, 120)", sent, received)
	}
}

func TestFlushSideUsageReportsCostOnly(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	out := &summaryOutput{}
	c.Out = out

	cost := 0.0003
	c.RecordSideUsage(llm.Usage{Cost: &cost})
	c.FlushSideUsage()

	lines := strings.Join(out.lines, "\n")
	if !strings.Contains(lines, "Tokens: 0 sent, 0 received.") {
		t.Errorf("cost-only usage line missing token counts:\n%s", lines)
	}
	if !strings.Contains(lines, "Cost: $0.00030 message, $0.00030 session.") {
		t.Errorf("cost-only usage line missing the cost:\n%s", lines)
	}
}

// A side request that recorded nothing must print nothing — a --continue with
// no transcript, or a model that returned empty, is not a charge to surface.
func TestFlushSideUsageIsSilentWhenNothingSpent(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	out := &summaryOutput{}
	c.Out = out

	c.FlushSideUsage()

	if len(out.lines) != 0 {
		t.Errorf("expected no lines when nothing was spent, got:\n%s", strings.Join(out.lines, "\n"))
	}
}
