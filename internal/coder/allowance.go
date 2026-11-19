package coder

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dbohdan.com/strument/internal/allowance"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/record"
)

// On its subscription opencode Go reports a cost of 0 on every response, at
// any model's price — "Cost: $0.00" after a 58k-token Kimi K3.1 Pro turn —
// and puts what the request took from the allowance beside it, in
// usage.opencode. The usage line shows that in the cost's place, with the
// subscription's meter after it: "Go: $0.0131 turn, $0.42 run; 5h 10% used,
// resets in 1h 2m."

// goSpend is what Go charged the subscription over some sends.
type goSpend struct {
	allowance float64 // dollars taken from the allowance
	list      float64 // the same requests at list price
	known     bool    // a send reported billing_source "go"
}

func (g *goSpend) add(b *llm.OpencodeBilling) {
	if b == nil || b.Source != "go" {
		return
	}
	g.allowance += b.Allowance
	g.list += b.ListCost
	g.known = true
}

func (g *goSpend) plus(o goSpend) {
	g.allowance += o.allowance
	g.list += o.list
	g.known = g.known || o.known
}

func (g *goSpend) minus(before goSpend) goSpend {
	return goSpend{allowance: g.allowance - before.allowance, list: g.list - before.list, known: g.known}
}

// text is the spend as the usage line says it, "Go: $0.0131 turn, $0.42 run",
// or "" when nothing was charged to Go.
func (g *goSpend) text(label string, run goSpend) string {
	if !g.known {
		return ""
	}
	return fmt.Sprintf("Go: $%s %s, $%s run", formatCost(g.allowance), label, formatCost(run.allowance))
}

// AllowanceReader reads a subscription's meter, for a model whose provider
// has one. client.GoAllowance is the real one.
type AllowanceReader func(context.Context) (allowance.Reading, error)

// The usage line waits this long for the meter at a turn's end. The answer is
// already on screen; past this, the line goes without the meter rather than
// keep the prompt from returning.
const allowanceWait = 3 * time.Second

// allowanceState remembers the providers whose meter turned out not to exist,
// so they are not asked again.
type allowanceState struct {
	none map[string]bool
}

// readAllowance reads the current model's meter, when its provider has one
// and the turn was charged to it.
func (c *Coder) readAllowance() (allowance.Reading, bool) {
	if c.Allowance == nil || c.Model == nil || !c.usage.goSpend.known {
		return allowance.Reading{}, false
	}
	key := c.Model.Provider.GroupKey() + "\x00" + c.Model.Provider.APIKey
	if c.allow.none[key] {
		return allowance.Reading{}, false
	}
	read := c.Allowance(c.Model)
	if read == nil {
		return allowance.Reading{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), allowanceWait)
	defer cancel()
	r, err := read(ctx)
	if err != nil {
		if errors.Is(err, allowance.ErrNone) {
			if c.allow.none == nil {
				c.allow.none = map[string]bool{}
			}
			c.allow.none[key] = true
		}
		return allowance.Reading{}, false
	}
	return r, true
}

// goLine is the turn's Go spend and the meter, for the usage line, or "".
func (c *Coder) goLine(meter allowance.Reading, haveMeter bool) string {
	line := c.usage.goSpend.text("turn", c.totalGoSpend)
	if line == "" {
		return ""
	}
	if haveMeter {
		line += "; " + allowance.Line(meter, time.Now())
	}
	return line + "."
}

// goMeterRecord is a meter reading as the turn's record keeps it.
func goMeterRecord(r allowance.Reading, ok bool) *record.GoMeter {
	if !ok {
		return nil
	}
	w := func(w allowance.Window) record.GoWindow {
		g := record.GoWindow{Percent: w.Percent, Limited: w.Limited}
		if !w.ResetsAt.IsZero() {
			g.ResetsAt = w.ResetsAt.UTC().Format(time.RFC3339)
		}
		return g
	}
	return &record.GoMeter{Rolling: w(r.Rolling), Weekly: w(r.Weekly), Monthly: w(r.Monthly)}
}
