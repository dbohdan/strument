package coder

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/decide"
	"dbohdan.com/strument/internal/record"
)

// approve_model: a decision model asked before a shell command's prompt is
// shown. If it rates the command safe at or above its threshold, the command
// runs unasked; otherwise, and on any failure, the prompt is shown as before.
//
// It stands in for the prompt and for nothing else. The command still passes
// every check before the prompt, so a refusal, a disabled shell, or the
// sandbox's own limits are untouched. It is consulted only while the sandbox is
// enforcing, the same condition that licenses "a = all turn" at this gate: an
// unseen command then cannot write outside the project, so a wrong approval
// costs a wasted step and a /undo rather than something unbounded.
//
// It answers the shell prompt only. doc/experiments/2026-09-approve-model
// measured it on shell commands and never on URLs, so webfetch keeps asking.

// approveTimeout bounds a decision when the ApproveModel sets none. Config
// always sets one (decision_model's timeout=, default 10 seconds).
const approveTimeout = 10 * time.Second

// ApproveModel is a configured approve_model.
type ApproveModel struct {
	Decide    decide.Func
	Slug      string
	Threshold float64
	Timeout   time.Duration
	// MaxInput is the longest command plus purpose, in characters, the model
	// is asked about; zero means config.DecisionDefaultMaxInput.
	MaxInput int
}

// shellPromptAnswered reports whether the shell prompt would be answered
// without being shown: a --yes or auto_approve grant, or an earlier "a". The
// decision model is not consulted then — it would cost a call to approve what
// is approved already, and record an approval no one needed.
func (c *Coder) shellPromptAnswered(group string) bool {
	if c.Grants.Granted(GrantBash) {
		return true
	}
	return group != "" && (c.turnAutoApprove[group] || c.sessionAutoApprove[group])
}

// approveByModel asks the decision model about a command and reports whether
// it may run without the prompt, and the line to show with the prompt when it
// may not. Every outcome is shown and recorded with its p(safe), so what ran
// unasked can be read back afterwards.
//
// The verdict is shown after the command it is about, never before it: for an
// approval, below the "Running" line (runAndShowTail); for a refusal, below the
// command in the prompt (ConfirmRequest.Note). A service that did not answer
// is reported at once instead, because that is news about the service.
func (c *Coder) approveByModel(ctx context.Context, command, purpose string) (bool, string) {
	c.approveVerdict = ""
	am := c.Approve
	limit := am.MaxInput
	if limit <= 0 {
		limit = config.DecisionDefaultMaxInput
	}
	if n := utf8.RuneCountInString(command) + utf8.RuneCountInString(purpose); n > limit {
		c.record(record.Record{Type: record.KindDecision, Call: "approve_model", Model: am.Slug, Outcome: "too_long"})
		return false, fmt.Sprintf("Not sent to %s: %d characters is more than its max_input of %d.", am.Slug, n, limit)
	}
	timeout := am.Timeout
	if timeout <= 0 {
		timeout = approveTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := c.Clock.Now()
	d, err := am.Decide(ctx, decide.Input{Root: c.Root, Command: command, Purpose: purpose})
	elapsed := c.Clock.Now().Sub(start)

	r := record.Record{
		Type:    record.KindDecision,
		Call:    "approve_model",
		Model:   am.Slug,
		Seconds: elapsed.Round(time.Millisecond).Seconds(),
	}
	if d.Model != "" {
		r.Model = d.Model
	}
	if d.Cost != nil {
		r.Cost, r.CostKnown = *d.Cost, true
	}
	defer func() { c.record(r) }()

	if err != nil {
		r.Outcome, r.Error = "failed", err.Error()
		c.Out.Warningf("approve_model: %s did not answer (%v); asking instead.", am.Slug, err)
		return false, ""
	}
	p := d.PSafe
	r.PSafe = &p
	if p >= am.Threshold {
		r.Outcome = "approved"
		// Printed by runAndShowTail, below the "Running" line, so the approval
		// follows the command it approved.
		c.approvalNote = fmt.Sprintf("Approved by %s, p(safe) = %.2f.", r.Model, p)
		return true, ""
	}
	r.Outcome = "asked"
	c.approveVerdict = fmt.Sprintf("approve_model, a classifier (%s), rated it p(safe) = %.2f, below its threshold of %.2f",
		r.Model, p, am.Threshold)
	return false, fmt.Sprintf("Not approved by %s, p(safe) = %.2f < %.2f.", r.Model, p, am.Threshold)
}
