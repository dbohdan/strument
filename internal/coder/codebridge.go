package coder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"dbohdan.com/strument/internal/llm"
)

// A run_code program can call every tool the session offers but three, and
// each call does what the direct call does: an edit lands, a command asks
// first. This file is the part of the bridge that reaches past the
// observation tools, and the refusal a program meets when a call is not run.
// doc/experiments/2026-11-bridge-all has the design and the options weighed.

// codeExcluded are the tools a program cannot call. run_code would be a
// program starting a program. interrupt ends the turn, which a program
// cannot do; it returns. ask_user_question's answer is for the model to read
// and think about, not for a program to compute over.
var codeExcluded = []string{toolRunCode, toolInterrupt, toolAskUser}

// codeCallableTools is the one list behind the run_code description, the
// registration, and the bridge's allow check: every tool otherToolDefs offers,
// in its order, but the excluded ones. It follows the schema by construction,
// so symbol is callable exactly where the parser is, bash exactly where the
// shell is, and so on; a program can never call a tool the session does not
// offer, and never miss one it does.
func (c *Coder) codeCallableTools() []string {
	var out []string
	for _, d := range c.otherToolDefs() {
		if slices.Contains(codeExcluded, d.Name) ||
			c.ObserveOnly && !slices.Contains(InspectorTools(), d.Name) {
			continue
		}
		out = append(out, d.Name)
	}
	return out
}

// refusal is who declined a gated call and why. It is recorded at the one
// place every permission prompt passes through, confirmGrouped, so that a
// tool that asks needs nothing of its own to tell a program it was refused.
//
// by is one of three authors, because the model must not take one for
// another: "user" is a person who saw the prompt and said no; "approve_model"
// is a classifier with no one at a terminal to overrule it; "nobody" is a
// prompt no one could be shown. A refusal read as the user's judgment of the
// model's work invites an argument, and one read as a classifier's invites a
// retry; neither is right for the others.
type refusal struct {
	by     string
	reason string
}

const (
	refusedByUser         = "user"
	refusedByApproveModel = "approve_model"
	refusedByNobody       = "nobody"
)

// noteRefusal records a declined prompt: always as the last refusal, for the
// bridge to throw, and inside a program as the program's refusal too, after
// which its gated calls are refused without asking.
func (c *Coder) noteRefusal(req ConfirmRequest, res ConfirmResult) {
	r := refusal{by: refusedByUser, reason: "the user declined"}
	if res.Unattended {
		r = refusal{by: refusedByNobody, reason: fmt.Sprintf("no one is available to approve it, "+
			"and the session was not started with `--yes %s`", req.Grant)}
		// approveVerdict is set by runShell alone, and cleared at its start,
		// so it speaks for this request only when this request is a command.
		if req.Grant == GrantBash && c.approveVerdict != "" {
			r = refusal{by: refusedByApproveModel, reason: c.approveVerdict +
				", and no one is at a terminal to overrule it"}
		}
	}
	c.lastRefusal = &r
	if c.program != nil && c.program.refused == nil {
		c.program.refused = &r
	}
}

// refusedInProgram answers a gated call in a program that has already been
// refused once: no prompt, no classifier, the same author as the first. One
// answer per program, because a loop over thirty removals after the first was
// declined is thirty prompts, and an unattended run declines all thirty alike.
func (c *Coder) refusedInProgram() (ConfirmResult, bool) {
	if c.program == nil || c.program.refused == nil {
		return ConfirmResult{}, false
	}
	first := *c.program.refused
	c.lastRefusal = &refusal{by: first.by,
		reason: "an earlier call in this program was refused, so Strument did not ask about this one"}
	return ConfirmResult{Unattended: first.by != refusedByUser}, true
}

// takeRefusal returns the refusal recorded since it was last taken, if any.
func (c *Coder) takeRefusal() *refusal {
	r := c.lastRefusal
	c.lastRefusal = nil
	return r
}

// notRunError is a refused call, thrown into the program as a NotRun.
type notRunError struct {
	refusal

	tool string
}

func (e *notRunError) Error() string { return e.tool + " was not run: " + e.reason }

// jsNotRun defines the NotRun error class in the program's global scope. It is
// an Error subclass so an uncaught one fails the program like any error, with
// its message naming the tool and the reason, and a typed one so a catch can
// tell a refusal from a tool failure without matching text.
const jsNotRun = `class NotRun extends Error {
  constructor(tool, by, reason) {
    super(tool + " was not run: " + reason);
    this.name = "NotRun";
    this.tool = tool;
    this.by = by;
    this.reason = reason;
  }
}
globalThis.NotRun = NotRun;`

// programState is what one running program shares with the bridge: whether
// it has been refused, every call that was not run, and its clock.
type programState struct {
	refused *refusal
	notRun  []notRunRecord
	clock   programClock
}

type notRunRecord struct {
	refusal

	call string // the call as the user would recognise it, `bash "rm -rf build"`
}

// programClock is the time a program itself runs. The limit used to be all
// wall-clock time, which was fine while every call a program could make was a
// local read; a command can take minutes and a prompt can wait on a person,
// so the clock stops while a bridged call runs.
type programClock struct {
	mu      sync.Mutex
	used    time.Duration
	since   time.Time // zero while paused
	started bool
}

func (k *programClock) start() {
	k.mu.Lock()
	k.since, k.started = time.Now(), true
	k.mu.Unlock()
}

func (k *programClock) pause() {
	k.mu.Lock()
	if !k.since.IsZero() {
		k.used += time.Since(k.since)
		k.since = time.Time{}
	}
	k.mu.Unlock()
}

func (k *programClock) resume() {
	k.mu.Lock()
	if k.started && k.since.IsZero() {
		k.since = time.Now()
	}
	k.mu.Unlock()
}

func (k *programClock) elapsed() time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	d := k.used
	if !k.since.IsZero() {
		d += time.Since(k.since)
	}
	return d
}

// bridgeID names a bridged call for the records an edit writes, which are
// keyed by tool-call id: the program's own call, and which of its calls.
func (c *Coder) bridgeID(n int) string {
	return fmt.Sprintf("%s#%d", c.programCallID, n)
}

// runBridgedAction answers a bridged call to a tool that is not an
// observation tool, or reports ok false for the caller to answer it. Each goes
// through the function the direct call uses, so a program's edit is an edit,
// its command is asked about like any command, and its commit is a commit.
func (c *Coder) runBridgedAction(ctx context.Context, call *bridgedCall, n int) (v any, ok bool, err error) {
	tc := llm.ToolCall{ID: c.bridgeID(n), Name: call.Name, Arguments: call.ArgsJSON()}
	c.lastRefusal = nil
	switch call.Name {
	case toolEdit, toolWrite:
		e, msg := parseEditArgs(tc)
		if msg != "" {
			return nil, true, errors.New(msg)
		}
		results := toolResults{}
		failed := false
		edited := c.applyToolEdits([]plannedEdit{e}, results, &failed)
		c.noteEdited(edited)
		text := results[tc.ID].Text
		if len(edited) == 0 {
			return nil, true, errors.New(text)
		}
		return text, true, nil

	case toolBash:
		cmd, msg := parseCommandArgs(tc)
		if msg != "" {
			return nil, true, errors.New(msg)
		}
		c.lastShell = nil
		text, ran := c.runShell(ctx, cmd)
		if ran {
			c.toolLoops.observeMutation()
		}
		if r := c.takeRefusal(); r != nil {
			return nil, true, &notRunError{tool: call.Name, refusal: *r}
		}
		if c.lastShell == nil {
			// Not run, and not refused: shell commands off, or a sandbox that
			// must enforce and cannot. Those are failures of the call.
			return nil, true, errors.New(text)
		}
		r := *c.lastShell
		notice := strings.TrimSpace(r.notice)
		if r.shown > 0 {
			notice = strings.TrimSpace(fmt.Sprintf("Output is the last %d of %d lines; the user saw all of them. %s",
				r.shown, r.total, notice))
		}
		return map[string]any{"status": r.exit, "output": r.output, "notice": notice}, true, nil

	case toolCommit:
		a, msg := parseCommitArgs(tc)
		if msg != "" {
			return nil, true, errors.New(msg)
		}
		before := ""
		if c.Repo != nil {
			before = c.Repo.HeadSHA()
		}
		text := c.runCommitTool(a)
		// Whether HEAD moved, rather than which sentence came back: the
		// sentences are for the model, and this is not one more place to
		// keep in step with them.
		if c.Repo == nil || c.Repo.HeadSHA() == before {
			return nil, true, errors.New(text)
		}
		c.toolLoops.observeMutation()
		return text, true, nil

	case toolWebfetch:
		f, msg := parseFetchArgs(tc)
		if msg != "" {
			return nil, true, errors.New(msg)
		}
		return c.refusedOr(call.Name, c.runWebfetch(ctx, f))

	case toolWebsearch:
		q, msg := parseSearchArgs(tc)
		if msg != "" {
			return nil, true, errors.New(msg)
		}
		return c.refusedOr(call.Name, c.runWebsearch(ctx, q))

	case toolCheck:
		return c.refusedOr(call.Name, c.runCheckTool(ctx, tc))

	case toolSkill:
		s, msg := parseSkillArgs(tc)
		if msg != "" {
			return nil, true, errors.New(msg)
		}
		return c.refusedOr(call.Name, c.runSkill(ctx, s))

	case toolAbout:
		c.Out.Toolf("About Strument")
		return c.About(time.Now()), true, nil
	}
	return nil, false, nil
}

// refusedOr is a tool's text, or the NotRun for the refusal it met.
func (c *Coder) refusedOr(tool, text string) (any, bool, error) {
	if r := c.takeRefusal(); r != nil {
		return nil, true, &notRunError{tool: tool, refusal: *r}
	}
	return text, true, nil
}

// noteEdited is the bookkeeping for edits that landed, from a step's batch or
// a program's call alike.
func (c *Coder) noteEdited(edited []string) {
	// The mutation signal belongs here, after the edits are applied, not where
	// they were queued. Signalling on a *queued* edit meant a failing one reset
	// both loop counters, so a model retrying an edit that could never match
	// wiped the detector's memory on every attempt -- the one situation where
	// the detector is most needed was the one it could never see.
	if len(edited) > 0 {
		c.toolLoops.observeMutation()
	}
	for _, f := range edited {
		c.turnEditedFiles[f] = true
		if !c.DryRun {
			// Nothing landed under --dry-run, so there is nothing for the
			// automatic checks to check.
			c.editedSinceCheck = true
		}
	}
}

// bashDataDoc is bash's line in the description's list of data shapes. It is
// documentation only: bash is answered by runBridgedAction, which needs the
// turn's context, not by the data-function registry.
//
// {status, output} rather than the direct call's text, because a program
// would otherwise parse its own harness's prose to find the exit status, and
// status rather than exit or code because Node's spawnSync names it so, the
// strongest prior a model has for a command's result.
var bashDataDoc = codeFuncDef{
	name: toolBash,
	summary: "bash({command, purpose}) runs a command as the bash tool does, asking the user first, " +
		"and returns {status, output, notice}: the exit status, what the command printed, and " +
		"Strument's note about the run (a timeout, a cut tail), empty when there is none.",
}

// codeRefusalDoc tells a program's author what a refusal looks like and where
// the harness's own account of a run goes. Three authors are named because
// the model must not take one for another (see refusal).
const codeRefusalDoc = "\n\nA call that is declined — by the user, by approve_model with no one " +
	"there to overrule it, or because no one could be asked — throws NotRun, an Error with " +
	"e.tool, e.by (\"user\", \"approve_model\" or \"nobody\") and e.reason. After one, the " +
	"program's later calls that would ask are refused without asking. The program's result " +
	"comes back inside a fenced block; anything after the block is Strument's, including " +
	"what was not run."

// notRunText is the harness's account of a program's refused calls, written
// after the fence that holds what the program returned (runCode), where the
// program cannot reach it. "" when every call ran.
func notRunText(records []notRunRecord) string {
	if len(records) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Strument did not run %d of this program's calls:\n", len(records))
	for _, r := range records {
		fmt.Fprintf(&b, "- %s: %s (by: %s).\n", r.call, r.reason, r.by)
	}
	return strings.TrimRight(b.String(), "\n")
}

// bridgedCallLabel is a call as the user would recognise it from the screen:
// the tool and its leading argument.
func bridgedCallLabel(call *bridgedCall) string {
	params := codeToolParams[call.Name]
	if len(params) > 0 {
		if v, ok := call.Args[params[0]]; ok {
			if s, ok := v.(string); ok {
				return call.Name + " " + quoteToolArg(firstLine(s))
			}
		}
	}
	return call.Name
}
