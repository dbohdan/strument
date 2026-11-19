package coder

import (
	"context"
	"errors"
	"time"

	"dbohdan.com/strument/internal/allowance"
)

// AllowanceReader reads a subscription allowance, for a model whose provider
// has one. client.GoAllowance is the real one.
type AllowanceReader func(context.Context) (allowance.Reading, error)

// The usage line waits this long for the reading at a turn's end. The answer
// is already on screen; past this, the line goes without the allowance rather
// than keep the prompt from returning.
const allowanceWait = 3 * time.Second

// allowanceState is what the coder knows of each provider's allowance: the
// last reading, which the next turn's change is measured from, and the
// providers that turned out to have none, which are not asked again.
type allowanceState struct {
	last     map[string]allowance.Reading
	none     map[string]bool
	baseline chan allowanceResult // a first reading in flight, for the turn's change
}

type allowanceResult struct {
	key     string
	reading allowance.Reading
	err     error
}

// allowanceReader is the reader for the current model and the key its
// readings are kept under, or nil when there is nothing to read.
func (c *Coder) allowanceReader() (AllowanceReader, string) {
	if c.Allowance == nil || c.Model == nil {
		return nil, ""
	}
	key := c.Model.Provider.GroupKey() + "\x00" + c.Model.Provider.APIKey
	if c.allow.none[key] {
		return nil, ""
	}
	read := c.Allowance(c.Model)
	if read == nil {
		return nil, ""
	}
	return read, key
}

// startAllowance takes a first reading as a turn starts, when there is none
// yet, so the turn's own share can be shown at its end. It runs beside the
// turn and is never waited for longer than allowanceWait.
func (c *Coder) startAllowance() {
	read, key := c.allowanceReader()
	if read == nil || c.allow.baseline != nil {
		return
	}
	if _, ok := c.allow.last[key]; ok {
		return
	}
	ch := make(chan allowanceResult, 1)
	c.allow.baseline = ch
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*allowanceWait)
		defer cancel()
		r, err := read(ctx)
		ch <- allowanceResult{key: key, reading: r, err: err}
	}()
}

// allowanceLine reads the allowance at a turn's end and words it, or returns
// "" when there is none or it did not answer in time.
func (c *Coder) allowanceLine() string {
	read, key := c.allowanceReader()
	if read == nil {
		return ""
	}
	if ch := c.allow.baseline; ch != nil {
		c.allow.baseline = nil
		select {
		case res := <-ch:
			c.noteAllowance(res)
		case <-time.After(allowanceWait):
		}
		if c.allow.none[key] {
			return ""
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), allowanceWait)
	defer cancel()
	cur, err := read(ctx)
	prev, hadPrev := c.allow.last[key]
	if !c.noteAllowance(allowanceResult{key: key, reading: cur, err: err}) {
		return ""
	}
	if !hadPrev {
		return allowance.Line(nil, cur, time.Now())
	}
	return allowance.Line(&prev, cur, time.Now())
}

// noteAllowance keeps a reading, or remembers that a provider has no
// allowance, and reports whether there was a reading.
func (c *Coder) noteAllowance(res allowanceResult) bool {
	if res.err != nil {
		if errors.Is(res.err, allowance.ErrNone) {
			if c.allow.none == nil {
				c.allow.none = map[string]bool{}
			}
			c.allow.none[res.key] = true
		}
		return false
	}
	if c.allow.last == nil {
		c.allow.last = map[string]allowance.Reading{}
	}
	c.allow.last[res.key] = res.reading
	return true
}
