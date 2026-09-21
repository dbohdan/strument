package repl

import (
	"context"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/history"
)

// /usage reports from the per-provider ledgers without touching them: a
// command typed to ask about spend must not change it, the house rule the
// bare /attach report already holds to.
func TestUsageReportsAndChangesNothing(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	r, _, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	defer r.Close()

	// One turn's worth of usage for the provider the test model is on,
	// filed the way RecordUsage files it.
	if err := history.AppendUsage("openrouter", history.CostEntry{
		Model:      "openrouter/test-model",
		TokensSent: 1500,
		TokensRecv: 120,
	}); err != nil {
		t.Fatal(err)
	}

	r.dispatch(context.Background(), "/usage")
	got := out.String()
	for _, want := range []string{"Last 24 hours:", "test-model", "1.5k in"} {
		if !strings.Contains(got, want) {
			t.Errorf("/usage output does not contain %q:\n%s", want, got)
		}
	}

	// The default is the live model's provider, and /model's switch is what
	// a follow-up question is about — the report itself stays the same shape.
	r.dispatch(context.Background(), "/usage all")
	if !strings.Contains(out.String(), "Last 7 days:") {
		t.Errorf("/usage all does not show the weekly window:\n%s", out.String())
	}

	// An unknown provider is answered with a way forward, not a bare no.
	out.Reset()
	r.dispatch(context.Background(), "/usage nosuch")
	if !strings.Contains(out.String(), "No usage recorded for provider \"nosuch\"") {
		t.Errorf("unknown provider not answered usefully:\n%s", out.String())
	}
}

// The empty case says what there is rather than printing three empty frames.
func TestUsageWithNoLedgers(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	r, _, out := newTestREPL(t, answerStub("hi"), strings.NewReader(""))
	defer r.Close()

	r.dispatch(context.Background(), "/usage all")
	if !strings.Contains(out.String(), "No usage recorded yet") {
		t.Errorf("/usage all with no ledgers does not say so:\n%s", out.String())
	}
}
