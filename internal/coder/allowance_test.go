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
	"dbohdan.com/strument/internal/record"
)

// billedClient answers every request with "Done." and a usage report with
// cost as its in-band cost and, when billing is set, opencode's account of it.
type billedClient struct {
	cost    float64
	billing *llm.OpencodeBilling
}

func (s billedClient) Send(context.Context, llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Done."}, nil) {
			return
		}
		cost := s.cost
		u := &llm.Usage{PromptTokens: 100, CompletionTokens: 5, Cost: &cost, Opencode: s.billing}
		if !yield(llm.StreamEvent{Kind: llm.EventUsage, Usage: u}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// meter is a fake subscription meter: each read returns percent, or err.
type meter struct {
	mu      sync.Mutex
	percent float64
	err     error
	reads   int
}

func (m *meter) read(context.Context) (allowance.Reading, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if m.err != nil {
		return allowance.Reading{}, m.err
	}
	return allowance.Reading{Rolling: allowance.Window{Percent: m.percent, Known: true,
		ResetsAt: time.Now().Add(1*time.Hour + 2*time.Minute + 30*time.Second)}}, nil
}

func goBilled(allow, list float64) *llm.OpencodeBilling {
	return &llm.OpencodeBilling{Source: "go", Allowance: allow, ListCost: list}
}

func billedCoder(t *testing.T, cl billedClient, m *meter) (*Coder, *captureOut, *capture) {
	t.Helper()
	c := testCoder(t)
	c.Client = cl
	c.Allowance = func(*config.Model) AllowanceReader { return m.read }
	out, rec := &captureOut{}, &capture{}
	c.Out, c.Recorder = out, rec
	return c, out, rec
}

func usageLine(out *captureOut) string {
	for _, l := range slices.Backward(out.lines) {
		if strings.HasPrefix(l, "Tokens:") {
			return l
		}
	}
	return ""
}

func lastTurn(rec *capture) record.Record {
	for _, r := range slices.Backward(rec.recs) {
		if r.Type == record.KindTurn {
			return r
		}
	}
	return record.Record{}
}

// On the subscription the cost is a zero that says nothing. What the turn
// took from the allowance stands in for it, then the meter, and the turn's
// record keeps both.
func TestGoSpendReplacesAZeroCost(t *testing.T) {
	m := &meter{percent: 10}
	c, out, rec := billedCoder(t, billedClient{billing: goBilled(0.0131, 0.00437)}, m)
	c.Run(context.Background(), "hi")
	line := usageLine(out)
	if strings.Contains(line, "Cost:") || !strings.Contains(line, "Go: $0.01 turn, $0.01 run; 5h 10% used, resets in 1h 2m.") {
		t.Errorf("first turn: %q", line)
	}
	c.Run(context.Background(), "again")
	if line := usageLine(out); !strings.Contains(line, "Go: $0.01 turn, $0.03 run;") {
		t.Errorf("second turn: %q", line)
	}
	r := lastTurn(rec)
	if !r.GoKnown || r.GoAllowance != 0.0131 || r.GoListCost != 0.00437 || r.GoMeter == nil || r.GoMeter.Rolling.Percent != 10 {
		t.Errorf("turn record: known=%v allowance=%v list=%v meter=%+v", r.GoKnown, r.GoAllowance, r.GoListCost, r.GoMeter)
	}
}

// A free model's turn is charged nothing, and says so: a zero that is known.
func TestGoSpendOfAFreeModel(t *testing.T) {
	c, out, rec := billedCoder(t, billedClient{billing: goBilled(0, 0)}, &meter{percent: 10})
	c.Run(context.Background(), "hi")
	if line := usageLine(out); !strings.Contains(line, "Go: $0.00 turn, $0.00 run; 5h 10% used") {
		t.Errorf("%q", line)
	}
	if r := lastTurn(rec); !r.GoKnown || r.GoAllowance != 0 {
		t.Errorf("a free turn must record a known zero: %+v", r)
	}
}

// Without opencode's account, as from Zen or any other endpoint, the cost
// shows as before and the meter is never asked.
func TestNoGoBillingLeavesTheCost(t *testing.T) {
	m := &meter{percent: 10}
	c, out, rec := billedCoder(t, billedClient{cost: 0.0021}, m)
	c.Run(context.Background(), "hi")
	if line := usageLine(out); strings.Contains(line, "Go:") || !strings.Contains(line, "Cost: $0.0021 turn") {
		t.Errorf("%q", line)
	}
	if m.reads != 0 || lastTurn(rec).GoKnown {
		t.Errorf("%d meter reads, go_known %v; want none", m.reads, lastTurn(rec).GoKnown)
	}
}

// A meter that does not exist is not asked again; the spend still shows.
func TestNoMeterIsNotAskedAgain(t *testing.T) {
	m := &meter{err: allowance.ErrNone}
	c, out, _ := billedCoder(t, billedClient{billing: goBilled(0.002, 0.002)}, m)
	c.Run(context.Background(), "hi")
	c.Run(context.Background(), "again")
	if line := usageLine(out); !strings.Contains(line, "Go: $0.0020 turn, $0.0040 run.") {
		t.Errorf("%q", line)
	}
	if m.reads != 1 {
		t.Errorf("%d reads, want 1", m.reads)
	}
}
