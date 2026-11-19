package coder

import (
	"context"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"dbohdan.com/strument/internal/allowance"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// costClient answers every request with "Done." and a usage report whose
// in-band cost is cost.
type costClient struct{ cost float64 }

func (s costClient) Send(context.Context, llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
			return
		}
		cost := s.cost
		if !yield(llm.StreamEvent{Kind: llm.EventUsage, Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 5, Cost: &cost}}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// meter is a fake allowance: each read returns the next reading, or err.
type meter struct {
	mu       sync.Mutex
	readings []float64
	err      error
	reads    int
}

func (m *meter) read(context.Context) (allowance.Reading, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if m.err != nil {
		return allowance.Reading{}, m.err
	}
	p := m.readings[0]
	if len(m.readings) > 1 {
		m.readings = m.readings[1:]
	}
	return allowance.Reading{Rolling: allowance.Window{Percent: p, Known: true,
		ResetsAt: time.Now().Add(3*time.Hour + 12*time.Minute + 30*time.Second)}}, nil
}

func allowanceCoder(t *testing.T, cost float64, m *meter) (*Coder, *captureOut) {
	t.Helper()
	c := testCoder(t)
	c.Client = costClient{cost: cost}
	c.Allowance = func(*config.Model) AllowanceReader { return m.read }
	out := &captureOut{}
	c.Out = out
	return c, out
}

func usageLine(out *captureOut) string {
	for _, l := range slices.Backward(out.lines) {
		if strings.HasPrefix(l, "Tokens:") {
			return l
		}
	}
	return ""
}

// On a subscription the cost is zero and says nothing; the allowance stands
// in for it, with the turn's share measured from the reading as it began.
func TestAllowanceReplacesAZeroCost(t *testing.T) {
	m := &meter{readings: []float64{22.6, 23.4, 24}}
	c, out := allowanceCoder(t, 0, m)
	c.Run(context.Background(), "hi")
	line := usageLine(out)
	if strings.Contains(line, "Cost:") || !strings.Contains(line, "Go: 5h 23.4% used (+0.8%), resets in 3h 12m.") {
		t.Errorf("first turn: %q", line)
	}
	c.Run(context.Background(), "again")
	if line := usageLine(out); !strings.Contains(line, "Go: 5h 24% used (+0.6%)") {
		t.Errorf("second turn, measured from the first turn's end: %q", line)
	}
	if m.reads != 3 {
		t.Errorf("%d reads, want 3: one baseline, then one per turn", m.reads)
	}
}

// A real cost keeps its place, with the allowance after it.
func TestAllowanceBesideARealCost(t *testing.T) {
	c, out := allowanceCoder(t, 0.0021, &meter{readings: []float64{5}})
	c.Run(context.Background(), "hi")
	if line := usageLine(out); !strings.Contains(line, "Cost: $0.0021 turn") || !strings.Contains(line, "Go: 5h 5% used") {
		t.Errorf("%q", line)
	}
}

// An endpoint with no allowance, such as Zen's, shows the cost as before and
// is not asked again.
func TestNoAllowanceIsNotAskedAgain(t *testing.T) {
	m := &meter{err: allowance.ErrNone}
	c, out := allowanceCoder(t, 0.0021, m)
	c.Run(context.Background(), "hi")
	c.Run(context.Background(), "again")
	if line := usageLine(out); strings.Contains(line, "Go:") || !strings.Contains(line, "Cost: $0.0021 turn") {
		t.Errorf("%q", line)
	}
	if m.reads != 1 {
		t.Errorf("%d reads, want 1", m.reads)
	}
}
