package coder

import (
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

func img(w, h int) llm.ImageSource {
	return llm.ImageSource{MediaType: "image/png", Label: "x.png", Width: w, Height: h}
}

// The bug this exists to prevent: an image worth zero tokens. countMessages,
// checkTokens and the compaction budget all summed Message.Text(), so before
// the label change an image contributed nothing to any of them, and the guard
// that warns before a request overruns the window was blind to the largest
// thing in the request.
func TestAnImageIsNeverFree(t *testing.T) {
	for _, adapter := range []string{config.AdapterAnthropic, config.AdapterOpenRouter, ""} {
		if n := imageTokens(img(1024, 768), adapter); n < 100 {
			t.Errorf("adapter %q estimated %d tokens for a 1024x768 image", adapter, n)
		}
	}
	// Unknown dimensions must not mean zero either; they mean "assume a lot",
	// because the failure with a cost attached is the one that under-counts.
	if n := imageTokens(llm.ImageSource{MediaType: "image/webp"}, ""); n != unknownImageTokens {
		t.Errorf("an unsized image estimated %d", n)
	}
}

// Bigger images cost more, and nothing runs away: providers downscale before
// counting, so the raw formula has to be capped or a photograph reports an
// absurd number and the guard refuses a request that would have worked.
func TestImageEstimateGrowsAndIsCapped(t *testing.T) {
	small := imageTokens(img(256, 256), config.AdapterAnthropic)
	large := imageTokens(img(1024, 1024), config.AdapterAnthropic)
	if small >= large {
		t.Errorf("a 256px image (%d) did not cost less than a 1024px one (%d)", small, large)
	}
	if n := imageTokens(img(8000, 8000), config.AdapterAnthropic); n != maxImageTokens {
		t.Errorf("an 8000x8000 image estimated %d, want the cap %d", n, maxImageTokens)
	}
}

// The two formulas are genuinely different -- Anthropic divides pixels,
// everyone else counts 512px tiles -- so the adapter has to reach the estimate.
func TestAdapterChangesTheEstimate(t *testing.T) {
	if imageTokens(img(600, 600), config.AdapterAnthropic) == imageTokens(img(600, 600), config.AdapterOpenRouter) {
		t.Error("both dialects estimated the same; the adapter is not reaching the formula")
	}
}

// /tokens shows attachments on a row of their own, for the reason already
// written into TokensReport about the tool schemas: a cost folded into a sum
// over message text is a cost nobody can see.
func TestTokensReportShowsAttachments(t *testing.T) {
	c := testCoder(t)
	c.curMessages = []llm.Message{{Role: llm.RoleUser, Content: llm.BlocksContent(
		llm.TextBlock("what is this?"),
		llm.ImageBlock(img(800, 600)),
	)}}

	got := c.TokensReport()
	if !strings.Contains(got, "attachments") {
		t.Fatalf("no attachments row:\n%s", got)
	}
	// And the row must not be zero, which is what it would read as if the
	// estimate never reached it.
	if strings.Contains(got, "       0  attachments") {
		t.Errorf("the attachments row is zero with an image in the chat:\n%s", got)
	}
}
