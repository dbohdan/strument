package coder

import (
	"context"
	"iter"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// The refusal names a number the user never wrote under a field their config
// does not use. Each case says where the number came from and which setting
// changes it; the three differ in what was sent.
func TestOutputCapHint(t *testing.T) {
	const why = "The provider checks that the key can pay for a reply that long; "
	for _, tc := range []struct {
		name      string
		maxOutput int
		sent      int
		want      string
	}{
		{"unset, provider default", 0, 0,
			`"mimo" has no max_output, so no max_tokens was sent and the provider chose its own.` + "\n" +
				why + "set a smaller max_output, such as 32768, in its model() call."},
		{"unset, built-in default", 0, 32768,
			`"mimo" has no max_output, so max_tokens = 32768 was sent.` + "\n" +
				why + "set a smaller max_output in its model() call."},
		{"set", 65536, 65536,
			`"mimo" has max_output = 65536, which was sent as max_tokens.` + "\n" +
				why + "lower max_output in its model() call."},
	} {
		m := &config.Model{Alias: "mimo", Slug: "xiaomi/mimo-v2.6-flash", MaxOutput: tc.maxOutput}
		got := outputCapHint(m, &llm.OutputCapRefusal{Field: "max_tokens", Sent: tc.sent})
		if got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// capRefuseStub refuses every request as OpenRouter does a key that cannot
// cover the cap.
type capRefuseStub struct{}

func (capRefuseStub) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, &llm.StreamError{
			Class:     llm.ErrRequest,
			Message:   "HTTP 402: This request requires more credits, or fewer max_tokens.",
			OutputCap: &llm.OutputCapRefusal{Field: "max_tokens"},
		})
	}
}

// The hint reaches the user under the provider's message, once, with the
// model named by its alias.
func TestOutputCapRefusalIsExplained(t *testing.T) {
	c := toolCoder(t, t.TempDir())
	c.Model.Alias = "mimo"
	c.Client = capRefuseStub{}
	out := &captureOut{}
	c.Out = out

	if res, _ := c.sendMessage(context.Background(), "hello"); res != OutcomeFailed {
		t.Errorf("outcome = %v, want OutcomeFailed", res)
	}
	got := out.String()
	want := "HTTP 402: This request requires more credits, or fewer max_tokens.\n" +
		`"mimo" has no max_output, so no max_tokens was sent`
	if !strings.Contains(got, want) {
		t.Errorf("output = %q, want it to contain %q", got, want)
	}
	if n := strings.Count(got, "has no max_output"); n != 1 {
		t.Errorf("hint shown %d times, want 1:\n%s", n, got)
	}
}
