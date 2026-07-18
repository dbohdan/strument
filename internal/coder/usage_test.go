// Pins the aborted-turn usage report: when no provider usage arrives (the
// turn was interrupted before the final usage chunk), finalizeUsage falls
// back to the pre-send estimate and the streamed-reply estimate, marks the
// line "(estimated)", and still folds the cost into the session totals.

package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

func TestFinalizeUsageEstimatesWhenAborted(t *testing.T) {
	c := wholeModelCoder(t, t.TempDir())
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
	c := wholeModelCoder(t, t.TempDir())

	c.finalizeUsage(&sendUsage{prompt: 100, completion: 50, estSent: 9999})

	if sent, received := c.SessionTokens(); sent != 100 || received != 50 {
		t.Errorf("session tokens = (%d, %d), want (100, 50) from real usage", sent, received)
	}
	if strings.Contains(c.lastUsageReport, "(estimated)") {
		t.Errorf("real usage must not be marked estimated: %q", c.lastUsageReport)
	}
}
