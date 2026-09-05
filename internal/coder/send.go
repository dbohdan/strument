package coder

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"dbohdan.com/strument/internal/llm"
)

// SendOutcome is the terminal state of one sendMessage.
type SendOutcome int

const (
	OutcomeSuccess SendOutcome = iota
	// OutcomeReflect: the model made a recoverable mistake (an unmatched
	// search, a malformed argument) and the turn re-sends so it can fix it.
	OutcomeReflect
	// OutcomeContinue: the model's tool calls succeeded and their results are
	// waiting to be seen. A turn ending in tool calls is mid-sentence — the
	// model asked something and the results are the answer — so the turn
	// re-sends rather than handing control back to the human.
	OutcomeContinue
	OutcomeInterrupted
	// OutcomeSelfInterrupted: the model called the interrupt tool to end its
	// own turn. Distinct from OutcomeInterrupted so the human is not asked
	// "what now?" — the model already answered that by stopping — and from
	// OutcomeLooping because nothing degenerated; this is a deliberate stop.
	OutcomeSelfInterrupted
	// OutcomeLooping: the reply degenerated into repeating itself and the
	// harness stopped it. Distinct from OutcomeInterrupted because the human
	// did not do it, and because "carry on from where you stopped" — the right
	// thing to say after a Ctrl-C — is the one instruction that would resume
	// the loop.
	OutcomeLooping
	OutcomeContextExhausted
	OutcomeOutputExhausted
	OutcomeFailed
)

func (o SendOutcome) String() string {
	switch o {
	case OutcomeSuccess:
		return "Success"
	case OutcomeReflect:
		return "Reflect"
	case OutcomeContinue:
		return "Continue"
	case OutcomeInterrupted:
		return "Interrupted"
	case OutcomeSelfInterrupted:
		return "SelfInterrupted"
	case OutcomeLooping:
		return "Looping"
	case OutcomeContextExhausted:
		return "ContextExhausted"
	case OutcomeOutputExhausted:
		return "OutputExhausted"
	default:
		return "Failed"
	}
}

// Retry/continuation constants. RETRY_TIMEOUT matches aider;
// the continuation cap is a declared divergence (aider is unbounded).
const (
	retryTimeout      = 60 * time.Second
	continuationCap   = 4
	initialRetryDelay = 125 * time.Millisecond
)

// sendUsage is the per-send accumulator; sendMessage owns it and a defer
// finalizes it so a mid-send panic can't lose accounting.
type sendUsage struct {
	// modelTime is how long the provider had this send, summed over the
	// stream attempts a retry may make. The turn's total accumulates
	// separately on the Coder: an aside reports one send, a turn reports all
	// of them, and neither should borrow the other's clock.
	modelTime                                 time.Duration
	prompt, completion, cacheWrite, cacheRead int
	cost                                      float64
	costKnown                                 bool
	finalized                                 bool
	// estSent is the pre-send token estimate, the fallback when the turn is
	// aborted before the provider's usage arrives.
	estSent int
	// rejected records how the *last* attempt ended: true when the provider
	// refused the request rather than answering it. Last-wins, because a retry
	// that succeeds brings real usage with it.
	rejected bool
}

func (u *sendUsage) add(usage *llm.Usage) {
	if usage == nil {
		return
	}
	u.prompt += usage.PromptTokens
	u.completion += usage.CompletionTokens
	u.cacheWrite += usage.CacheWriteTokens
	u.cacheRead += usage.CacheReadTokens
	if usage.Cost != nil {
		u.cost += *usage.Cost
		u.costKnown = true
	}
}

// streamResult classifies how one streamOnce call ended.
type streamResult int

const (
	resDone streamResult = iota
	resContinuation
	resContextExhausted
	resOutputExhausted
	resInterrupted
	resLooping
	resFailed
)

// streamOnce runs one request through the client, dispatching stream events to
// the output and accumulating into the partial* fields and usage. It returns a
// coarse classification of how the stream ended. The caller resets the partial*
// fields before each call and decides whether to retry or continue.
//
// loops may be nil, which detects nothing; returning early from the range over
// Send is what stops the reply, so a detected loop costs the provider nothing
// more than one abandoned stream.
func (c *Coder) streamOnce(ctx context.Context, req llm.Request, usage *sendUsage, loops *loopDetector) (streamResult, error) {
	finishReason := ""
	usage.rejected = false

	// The clock brackets exactly this loop, which is the whole of the time the
	// provider owns: from the request going out to the last byte of the
	// stream. Everything a turn does between sends — running tools, applying
	// edits, asking the user — is outside it, so summing these across a turn
	// gives model time rather than wall time.
	started := c.Clock.Now()
	defer func() {
		elapsed := c.Clock.Now().Sub(started)
		usage.modelTime += elapsed
		c.messageModelTime += elapsed
	}()

	for ev, err := range c.Client.Send(ctx, req) {
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return resInterrupted, nil
			}
			usage.rejected = true
			var se *llm.StreamError
			if errors.As(err, &se) {
				if se.Class == llm.ErrContextWindow {
					usage.rejected = false
					return resContextExhausted, nil
				}
				if se.Retryable() {
					return resFailed, se // maybe retried by the caller
				}
			}
			return resFailed, err
		}
		switch ev.Kind {
		case llm.EventAnswer:
			c.partialResponseContent += ev.Text
			c.Out.StreamText(ev.Text)
			if loops.feed(loopAnswer, ev.Text) != nil {
				return resLooping, nil
			}
		case llm.EventReasoning:
			c.partialReasoningContent += ev.Text
			c.Out.StreamReasoning(ev.Text)
			if loops.feed(loopReasoning, ev.Text) != nil {
				return resLooping, nil
			}
		case llm.EventToolCall:
			c.accumulateToolCall(ev.ToolCall)
			if ev.ToolCall != nil {
				c.Out.StreamToolCall(ev.ToolCall.Index, ev.ToolCall.Name, ev.ToolCall.Args)
			}
		case llm.EventFinish:
			finishReason = ev.FinishReason
		case llm.EventUsage:
			usage.add(ev.Usage)
		}
	}
	if finishReason == "length" {
		return resContinuation, nil
	}
	return resDone, nil
}

// retryBackoff carries the doubling delay for transient stream errors across a
// send's retry loop.
type retryBackoff struct{ delay time.Duration }

// retry reports whether to retry a failed stream. A retryable error backs off —
// doubling the delay (capped at retryTimeout) and sleeping — then returns true;
// a non-retryable error, or one past the cap, reports the failure and returns
// false. It takes Output and Clock rather than the Coder so the side-model
// side calls (side.go) share it without owning one; sendMessage, RunAside, and
// those side calls all retry identically.
func (rb *retryBackoff) retry(out Output, clock Clock, streamErr error) bool {
	var se *llm.StreamError
	if !errors.As(streamErr, &se) || !se.Retryable() {
		out.Errorf("%v", streamErr)
		return false
	}
	rb.delay *= 2
	if rb.delay > retryTimeout {
		out.Errorf("%s", se.Error())
		return false
	}
	out.Warningf("%s", se.Error())
	out.Printf("Retrying in %.1f seconds...", rb.delay.Seconds())
	clock.Sleep(rb.delay)
	return true
}

// sendMessage is the phase machine. It returns the outcome and, for
// OutcomeReflect, the next message to send.
func (c *Coder) sendMessage(ctx context.Context, inp string) (SendOutcome, string) {
	// --- Setup ---
	// A resumed send re-enters on what is already in curMessages — tool
	// results, or an interrupted reply the user chose to continue — so it adds
	// no user turn; inp is unused on that path.
	appendedUser := false
	if c.resumeInPlace {
		c.resumeInPlace = false
	} else {
		c.curMessages = append(c.curMessages, llm.TextMessage("user", inp))
		appendedUser = true
	}

	chunks := c.formatMessages()
	messages := chunks.allMessages()

	if !c.checkTokens(messages) {
		if appendedUser {
			c.curMessages = c.curMessages[:len(c.curMessages)-1]
		}
		return OutcomeFailed, ""
	}

	// --- Stream ---
	c.multiResponseContent = "" // per-send reset (H1)

	usage := &sendUsage{estSent: c.countMessages(messages) + c.countTools()}
	defer c.finalizeUsage(usage)

	backoff := retryBackoff{delay: initialRetryDelay}
	continuations := 0

	// One detector per send, not per streamOnce: a continuation is the same
	// reply resumed, and a loop that spans the stitch is still a loop.
	loops := newLoopDetector(c.LoopDetection)

	// term is how the whole stream phase ended, as opposed to how one
	// streamOnce ended: retries and continuations loop without setting it.
	// streamOnce already classifies precisely, so the phase carries that
	// classification forward rather than re-encoding it into flags. Every path
	// out of the loop below assigns it before breaking.
	var term streamResult

	for {
		c.partialResponseContent = ""
		c.partialReasoningContent = ""
		c.partialToolCalls = nil
		c.toolCallIndex = map[int]int{}

		res, streamErr := c.streamOnce(ctx, c.buildRequest(messages), usage, loops)

		if res == resFailed {
			// A retryable error backs off and retries (the partial is discarded
			// at the loop top; accumulated multiResponseContent is untouched);
			// anything else is a hard failure.
			if backoff.retry(c.Out, c.Clock, streamErr) {
				continue
			}
			term = resFailed
			break
		}

		if res == resContinuation {
			if !c.PrefillSupported {
				term = resOutputExhausted
				break
			}
			continuations++
			if continuations > continuationCap {
				term = resOutputExhausted // cap hit
				break
			}
			// Stitch: accumulated + current partial becomes the prefill.
			c.multiResponseContent += c.partialResponseContent
			if n := len(messages); n > 0 && messages[n-1].Role == "assistant" {
				messages[n-1] = llm.TextMessage("assistant", c.multiResponseContent)
			} else {
				messages = append(messages, llm.TextMessage("assistant", c.multiResponseContent))
			}
			continue
		}

		term = res // resDone, resInterrupted, resLooping, resContextExhausted
		break
	}
	interrupted := term == resInterrupted

	// --- Finally (always runs) ---
	c.Out.FlushStream()
	answer := c.multiResponseContent + c.partialResponseContent
	answer = stripReasoning(answer, c.Model.ReasoningTag)
	c.partialResponseContent = answer
	c.multiResponseContent = ""

	// --- Post-stream dispatch ---
	c.finalizeUsage(usage)

	if answer != "" || len(c.partialToolCalls) > 0 {
		msg := llm.TextMessage("assistant", answer)
		if len(c.partialToolCalls) > 0 {
			msg.ToolCalls = c.partialToolCalls
		}
		c.curMessages = append(c.curMessages, msg)
	}

	// dropUserTurn un-appends the user message this send added, so a send that
	// produced nothing doesn't poison the history. It is a no-op on a tool
	// continuation, which appended no user turn — truncating there would drop a
	// tool result and leave a tool_call unanswered, making the next request
	// malformed.
	dropUserTurn := func() {
		if appendedUser {
			c.curMessages = c.curMessages[:len(c.curMessages)-1]
		}
	}

	switch term {
	case resContextExhausted:
		// A system message, not an assistant one. The model did not say this —
		// the request never reached a reply — so putting it in the assistant's
		// voice fabricates a turn, and one in which the model appears to report
		// its own transport error. The note exists so the next request does not
		// end on a bare user turn with no answer, which is what the role check
		// below is for; whose voice carries it is a separate question, and the
		// harness's own is the honest one.
		if n := len(c.curMessages); n > 0 && c.curMessages[n-1].Role == "user" {
			c.curMessages = append(c.curMessages,
				llm.TextMessage(llm.RoleSystem, "The reply was cut off: the request exceeded the context limit."))
		}
		c.showExhaustedError()
		return OutcomeContextExhausted, ""
	case resOutputExhausted:
		// The large partial was kept above; no diagnostic (trailing message
		// is the assistant partial).
		return OutcomeOutputExhausted, ""
	case resFailed:
		if answer == "" {
			dropUserTurn()
		}
		return OutcomeFailed, ""
	case resLooping:
		// The user turn stays. Unlike an empty reply, this one produced
		// something — it produced too much of one thing — and the note below
		// only makes sense following the message it answers.
		c.warnLoop(loops.Found)
		c.noteLoop(loops.Found)
		return OutcomeLooping, ""
	}

	if !interrupted && answer == "" && len(c.partialToolCalls) == 0 {
		dropUserTurn()
		c.Out.Warningf("Empty response received from LLM. Check your provider account?")
		return OutcomeFailed, ""
	}

	if interrupted {
		c.noteInterrupt()
		return OutcomeInterrupted, ""
	}

	// Everything a turn does now arrives as tool calls, in every mode: ask mode
	// is the same dispatch with the mutating tools withheld. There is no longer
	// a second path that reads the answer text for edits, shell blocks, or file
	// mentions — those were the text formats' way of saying what a tool call
	// says directly.
	return c.applyToolCalls(ctx), ""
}

// noteInterrupt records that the human stopped the reply, in a shape the next
// request can be built from.
//
// Two things it deliberately does not do, both of which it used to.
//
// It does not put words in the model's mouth. The old shape appended an
// assistant turn saying "I see that you interrupted my previous reply", which
// the model never said; the harness's own voice is the honest one, and the rest
// of this file already says so about the context-exhausted note.
//
// It does not edit "^C KeyboardInterrupt" into the user's own message. Whatever
// the human typed is what the human typed. assemble.go makes the same argument
// about the system reminder, and this was the last place still doing it.
//
// The partial reply itself stays. It is real output the model produced, and
// keeping it is what makes "continue as before" continue rather than restart.
// Its *tool calls* do not stay — see dropPartialToolCalls.
func (c *Coder) noteInterrupt() {
	c.dropPartialToolCalls()
	c.curMessages = append(c.curMessages, llm.HarnessNote(
		"The user pressed Ctrl-C, so your reply above was cut off where it stops. "+
			"Anything you were part-way through saying was not finished, and any tool "+
			"call you had begun was not run — nothing it would have changed has changed."))
}

// warnLoop tells the user what the model was doing when it was stopped, in
// every mode: script runs have no steer menu, and a run that stopped early is
// the one thing a log must not be silent about.
func (c *Coder) warnLoop(f *loopFinding) {
	if f == nil {
		c.Out.Warningf("The model's reply was repeating itself, so it was stopped.")
		return
	}
	c.Out.Warningf("The model's %s was repeating itself (%q %d times), so it was stopped.",
		f.Kind, f.Sample, f.Count)
}

// noteLoop records that the harness stopped a reply that was repeating itself.
//
// In the harness's own voice, for the same reason as noteInterrupt: nobody
// pressed Ctrl-C, and saying the user did would be a fabrication about the one
// participant whose actions the model cannot check.
//
// It quotes the repeating text back. A model given "you were repeating
// yourself" and nothing else has to guess which part, and the guess is often
// the whole reply; the sample makes the instruction actionable.
func (c *Coder) noteLoop(f *loopFinding) {
	c.dropPartialToolCalls()
	where := "reply"
	if f != nil && f.Kind == loopReasoning {
		where = "reasoning"
	}
	note := "Strument stopped your " + where + " because it had begun repeating itself"
	if f != nil {
		note += fmt.Sprintf(" — %q appeared %d times", f.Sample, f.Count)
	}
	note += ". Nothing you were part-way through was finished, and any tool call " +
		"you had begun was not run. Do not continue that text or write it again: " +
		"take a different approach, and if you are stuck, say so plainly and stop."
	c.curMessages = append(c.curMessages, llm.HarnessNote(note))
}

// noteToolInterrupt records a Ctrl-C that landed while a tool was running.
//
// Distinct from noteInterrupt, which describes a *stream* cut off mid-word and
// says any tool call the model had begun was never run. Here the opposite is
// true: the calls ran, their results are in the history, and one of them was
// stopped part-way. Telling the model the other story would have it repeat work
// that already happened, or distrust a result that is real.
func (c *Coder) noteToolInterrupt() {
	c.partialToolCalls = nil
	c.curMessages = append(c.curMessages, llm.HarnessNote(
		"The user pressed Ctrl-C while your tool calls were running. Their results "+
			"above are real as far as they got, and a command that was stopped says so "+
			"in its own output. Nothing after the interruption ran."))
}

// dropPartialToolCalls strips tool calls from the assistant message an
// interrupted send just appended.
//
// Not tidiness — correctness. An unanswered tool_call makes the *next* request
// malformed, and after an interrupt no result will ever answer one: the call is
// either half-streamed and unparseable, or complete but never dispatched,
// because the interrupt landed before applyToolCalls ran. Either way the model
// must be told it did not happen, which the note above does, rather than shown
// a call it may assume succeeded.
//
// The message's text survives. Only an assistant message can carry tool calls,
// so this looks at one message and only when it is the last.
func (c *Coder) dropPartialToolCalls() {
	c.partialToolCalls = nil
	n := len(c.curMessages)
	if n == 0 {
		return
	}
	last := &c.curMessages[n-1]
	if last.Role != llm.RoleAssistant || len(last.ToolCalls) == 0 {
		return
	}
	if last.Text() == "" {
		// Nothing but the abandoned calls: drop the whole turn rather than
		// leave an empty assistant message in the history.
		c.curMessages = c.curMessages[:n-1]
		return
	}
	last.ToolCalls = nil
}

// buildRequest translates state into an llm.Request.
func (c *Coder) buildRequest(messages []llm.Message) llm.Request {
	req := llm.Request{
		Model:           c.Model.Slug,
		Messages:        messages,
		Temperature:     c.Model.Temperature,
		ReasoningEffort: c.Model.Reasoning,
		ExtraParams:     c.Model.RequestExtraParams(),
		MaxTokens:       c.Model.MaxOutput,
	}
	// Whatever tools this mode offers, offer them. The condition used to be
	// editFormat == "tool", which meant ask mode sent none at all — and made
	// toolDefs's own "ask" branch unreachable, so the code read as though ask
	// had a read-only tool set while the wire carried nothing. A model told to
	// look at the project with no way to do it invents the syntax: MiMo emitted
	// <bash>ls -la</bash> as prose, and one run spent 759 lines guessing at
	// markup. RunAside builds its own request and is deliberately not affected.
	if defs := c.toolDefs(); len(defs) > 0 {
		req.Tools = defs
		req.ToolChoice = "auto"
	}
	return req
}

// CheckRestoredContext warns if what a session *starts* with already overruns
// the declared window.
//
// Restored context is invisible until the first request, and by then the request
// has failed. aider #2979 is the shape: an 80k-token restored history fails on
// the wire, and configuring the side model for summarization does not help,
// because compaction runs at the end of a turn and there has not been one yet.
// Strument restores less — pins, notes, and the read-only block rather than a
// conversation — but the same trap is available, and a user who pinned a large
// spec last session should hear about it before typing rather than after.
//
// It warns rather than asking. A confirmation before the user has typed
// anything is a toll on every start, and there is nothing to decide yet: the
// remedy is /drop or /notes drop, which they can reach either way.
func (c *Coder) CheckRestoredContext() {
	if c.Model == nil || c.Model.Context <= 0 {
		return
	}
	n := c.countMessages(c.formatMessages().allMessages()) + c.countTools()
	if n < c.Model.Context {
		return
	}
	c.Out.Warningf("This session starts with about %d tokens of context, which already reaches "+
		"the %d-token limit for %s.", n, c.Model.Context, c.Model.QualifiedSlug())
	c.Out.Printf("Use /drop to unpin files, /notes drop to discard the session notes, or /tokens to see the split.")
}

// checkTokens warns when the estimate reaches the input window and asks to
// proceed.
func (c *Coder) checkTokens(messages []llm.Message) bool {
	maxInput := c.Model.Context
	if maxInput <= 0 {
		return true
	}
	// The schemas count: they go out with this request like everything else,
	// and leaving them out made the guard cheerful about a prompt 1.3k tokens
	// closer to the limit than it reported.
	inputTokens := c.countMessages(messages) + c.countTools()
	if inputTokens < maxInput {
		return true
	}
	c.Out.Errorf("Your estimated chat context of %d tokens exceeds the %d token limit for %s!",
		inputTokens, maxInput, c.Model.QualifiedSlug())
	c.Out.Printf("To reduce the chat context:")
	c.Out.Printf("- Use /drop to remove unneeded files from the chat")
	c.Out.Printf("- Use /clear to clear the chat history")
	c.Out.Printf("- Break your code into smaller files")
	c.Out.Printf("It's probably safe to try and send the request, most providers won't charge if the context limit is exceeded.")
	res := c.Confirm.Confirm(ConfirmRequest{Prompt: "Try to proceed anyway?", Grant: GrantContext})
	return res.Yes
}

// moveBackCurMessages rotates cur into done on edited turns.
//
// aider appended a synthetic "I applied and committed your changes" user turn
// here, plus an "Ok." from the assistant, because with SEARCH/REPLACE there was
// no other channel: a text edit format has nothing to answer the model with.
// The tool loop has one. Every edit call already gets a tool result saying what
// it did to which file, so the pair would only repeat it — two fabricated
// messages per turn, in a loop whose whole correction was to stop inventing
// turns the model never took. The commit is reported to the user instead, where
// the hash is worth something.
func (c *Coder) moveBackCurMessages() {
	c.doneMessages = append(c.doneMessages, c.curMessages...)
	c.curMessages = nil
	c.recordedMessages = 0
	c.maybeSummarize()
}

// endTurnHistory settles the finished turn and compacts if it has outgrown the
// budget. Called once from runOne's defer, after the commit and the snapshot.
//
// It used to be gated on `editFormat == "tool" && len(turnEditedFiles) > 0`, so
// only a turn that changed a file produced any settled history at all. A live
// pass found what that costs: a session of questions, or any stretch of /ask,
// kept everything in curMessages — which goes on the wire in full and which
// maybeSummarize never examines — so the history budget existed and nothing was
// ever measured against it.
//
// The gate is a vestige. In aider the rotation carried a synthetic "I applied
// and committed your changes" pair, which only made sense after an edit;
// moveBackCurMessages's own comment records removing the pair, and the gate
// stayed behind. What genuinely must not happen is rotating *mid-loop*, where
// compaction would eat the tool results the next send reacts to — and that is
// enforced by this running in the turn-end defer, not by asking what the turn
// did.
//
// Rotation does not move anything on the wire: done and cur are adjacent and
// both sit after the read-only block, so the split is bookkeeping. What changes
// is that compaction can now see the conversation.
func (c *Coder) endTurnHistory() {
	if len(c.curMessages) == 0 {
		return
	}
	c.moveBackCurMessages()
}

// maxChatHistoryTokens is the settled-history budget: context/8,
// derived from the main model's window, with a 2048 floor for small windows.
// It is increased from aider's context/16.
func maxChatHistoryTokens(context int) int {
	return max(context/8, 2048)
}

// countSummaryMessages reports how many retained history messages are compaction summaries.
func countSummaryMessages(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		if isSummaryMessage(m) {
			n++
		}
	}
	return n
}

func validCompaction(msgs []llm.Message, beforeTokens, beforeMessages, afterTokens int) bool {
	return len(msgs) < beforeMessages && afterTokens < beforeTokens && hasSummaryContent(msgs)
}

func hasSummaryContent(msgs []llm.Message) bool {
	for _, m := range msgs {
		if isSummaryMessage(m) && strings.TrimSpace(strings.TrimPrefix(m.Text(), summaryPrefix)) != "" {
			return true
		}
	}
	return false
}

// budget. It runs only when a summarizer is wired and the model's window is
// known (Context > 0) — mirroring checkTokens, which treats an unknown window
// as "no limit to enforce". Synchronous: the side-model call happens here,
// before the next prompt is assembled. On failure the history is left intact.
func (c *Coder) maybeSummarize() {
	if c.Summarizer == nil || c.Model.Context <= 0 {
		return
	}
	budget := maxChatHistoryTokens(c.Model.Context)
	if !c.Summarizer.tooBig(c.doneMessages, budget) {
		return
	}
	if c.summaryBackoff {
		c.summaryBackoff = false
		return
	}
	c.Out.Printf("Summarizing chat history to fit the context window...")
	beforeTokens := c.Summarizer.total(c.doneMessages)
	beforeMessages := len(c.doneMessages)
	out, err := c.Summarizer.summarize(c.doneMessages, budget)
	if err != nil {
		c.summaryBackoff = true
		c.Out.Warningf("Could not summarize chat history: %v", err)
		return
	}
	afterTokens := c.Summarizer.total(out)
	if !validCompaction(out, beforeTokens, beforeMessages, afterTokens) {
		c.summaryBackoff = true
		c.Out.Warningf("Could not summarize chat history: the result was not smaller than the original history")
		return
	}
	c.doneMessages = out
	c.Out.Printf("Chat history compacted: %d tokens/%s -> %d tokens/%s; %s retained.",
		beforeTokens, plural(beforeMessages, "message", "messages"),
		afterTokens, plural(len(out), "message", "messages"),
		plural(countSummaryMessages(out), "summary", "summaries"))
}

// finalizeUsage resolves cost — (1) in-band cost, (2) config
// pricing, (3) tokens only, never a fabricated $0 — merges into session
// totals, and emits the immutable report. Idempotent; also deferred so a
// mid-send panic can't lose accounting.
func (c *Coder) finalizeUsage(u *sendUsage) {
	if u.finalized {
		return
	}
	u.finalized = true

	// prompt_tokens is the whole prompt. Both cache figures live under
	// prompt_tokens_details and are a *breakdown* of it, not additions to it:
	// on a cold request with a breakpoint, OpenRouter returns
	// prompt_tokens=14021, cache_write_tokens=14018, and
	// total_tokens=14026 = prompt + completion. Adding the write on top counted
	// most of the prompt twice, and only on models that write a cache — which
	// is why GPT-5.6 Luna looked like it was sending an order of magnitude more
	// than Haiku for the same work. Cache reads were already treated as the
	// subset they are; the two are the same kind of number.
	sent := u.prompt
	received := u.completion
	estimated := false

	// A request the provider refused outright — a bad key, a model slug that
	// does not exist, an account out of credit — was not processed: no usage,
	// no reply, nothing billed. Estimating it printed
	//
	//	Tokens: 735 sent, 0 received. (estimated)
	//
	// under a 401, which reads as a charge for the failure. Returning before
	// the counters means messageSends stays 0 and flushTurnUsage says nothing
	// at all, which is the honest report.
	//
	// A stream that produced text before it broke is a different case: those
	// tokens were generated and will be billed, so it still estimates below.
	if u.rejected && sent == 0 && received == 0 && !u.costKnown && c.streamedText() == "" {
		return
	}

	// No usage arrived — the turn was aborted before the provider's final
	// usage chunk (or the provider sends none). The request still went out
	// and may have streamed a partial reply, so report our own estimate
	// instead of a misleading zero, and mark it so a guess is never shown as
	// the provider's ground truth.
	if sent == 0 && received == 0 && !u.costKnown && (u.estSent > 0 || c.streamedText() != "") {
		estimated = true
		sent = u.estSent
		received = c.Tokens.Count(c.streamedText())
	}

	// Accumulate into the turn rather than assign: a turn is many sends, and the
	// line that reports it is printed once, at the end.
	c.messageSends++
	c.messageTokensSent += sent
	c.messageTokensReceived += received
	c.messageCacheRead += u.cacheRead
	c.messageCacheWrite += u.cacheWrite
	c.messageEstimated = c.messageEstimated || estimated
	c.totalTokensSent += sent
	c.totalTokensReceived += received
	c.peakTokensSent = max(c.peakTokensSent, sent)

	report := formatTokenLine(sent, u.cacheWrite, u.cacheRead, received, u.modelTime)

	cost := 0.0
	known := false
	switch {
	case u.costKnown:
		cost = u.cost
		known = true
	case c.Model.InputCost != nil && c.Model.OutputCost != nil:
		pin := c.Model.InputCost.USD
		pout := c.Model.OutputCost.USD
		if estimated {
			cost = float64(sent)*pin + float64(received)*pout
		} else {
			// Anthropic-style cache pricing adjustments; no-ops at zero counts
			// (DeepSeek's cache-read discount uses the same 0.10 factor). The
			// cache figures are carved *out* of the prompt rather than added to
			// it — see sent above — so each token is priced exactly once, at
			// whichever rate applies to it. Charging them on top of a full-price
			// prompt was the same double-count the token line had.
			full := max(u.prompt-u.cacheWrite-u.cacheRead, 0)
			cost = float64(u.cacheWrite)*pin*1.25 +
				float64(u.cacheRead)*pin*0.10 +
				float64(full)*pin +
				float64(u.completion)*pout
		}
		known = true
	}

	if known {
		c.messageCost += cost
		c.totalCost += cost
		c.costKnown = true
		c.sessionKnown = true
		report += fmt.Sprintf(" Cost: $%s message, $%s session.", formatCost(cost), formatCost(c.totalCost))
	}
	if estimated {
		report += " (estimated)"
	}

	c.lastUsageReport = report
}

// flushSendUsage prints one send's own line.
//
// Only an aside calls it. A /btw is not a turn — no tool calls to wait for, no
// later sends to sum with — so its accounting is complete the moment the stream
// ends.
func (c *Coder) flushSendUsage() {
	if c.lastUsageReport == "" {
		return
	}
	c.Out.Printf("")
	c.Out.Printf("%s", c.lastUsageReport)
	c.lastUsageReport = ""
}

// messageUsageReport builds the accounting line from the message-scoped totals
// — the token breakdown, the cost, and the estimated marker — for a whole turn.
func (c *Coder) messageUsageReport(label, prefix string) string {
	return usageReport(label, prefix, c.messageTokensSent, c.messageCacheWrite,
		c.messageCacheRead, c.messageTokensReceived, c.messageModelTime, c.messageCost,
		c.costKnown, c.totalCost, c.messageEstimated)
}

func usageReport(label, prefix string, sent, cacheWrite, cacheRead, received int,
	modelTime time.Duration, cost float64, costKnown bool, totalCost float64, estimated bool,
) string {
	report := prefix + formatTokenLine(sent, cacheWrite, cacheRead, received, modelTime)
	if costKnown {
		report += fmt.Sprintf(" Cost: $%s %s, $%s session.", formatCost(cost), label, formatCost(totalCost))
	}
	if estimated {
		report += " (estimated)"
	}
	return report
}

// FlushSideUsage prints and consumes the token/cost line for the current
// session-notes request. Notes can be generated between turns, so their usage
// has its own accumulator rather than sharing the current turn's totals.
func (c *Coder) FlushSideUsage() {
	if !c.sideUsageRecorded {
		return
	}
	// No duration: a side request (session notes, a commit message) goes out
	// on its own path that does not run through streamOnce's clock, so there
	// is no measurement to report and a zero suppresses the rate rather than
	// printing a wrong one.
	report := usageReport("message", "", c.sideTokensSent, c.sideCacheWrite,
		c.sideCacheRead, c.sideTokensReceived, 0, c.sideCost, c.sideCostKnown,
		c.totalCost, false)
	c.sideCost = 0
	c.sideCostKnown = false
	c.sideTokensSent = 0
	c.sideTokensReceived = 0
	c.sideCacheRead = 0
	c.sideCacheWrite = 0
	c.sideUsageRecorded = false
	c.Out.Printf("%s", report)
}

// sideUsageDoneMessage is the message printed after a side usage line that
// named the charge — the session notes — so the user knows what the charge was
// for. Both the startup --continue notes call and /notes generate print it.
const sideUsageDoneMessage = "Session notes generated from the transcript."

// ReportSideUsageDone prints sideUsageDoneMessage through the coder's output
// writer. Call it after FlushSideUsage so the charge — the session notes — is
// named on screen rather than left to the token/cost line alone.
func (c *Coder) ReportSideUsageDone() {
	c.Out.Printf("%s", sideUsageDoneMessage)
}

// RecordTurnSideUsage folds in a request the turn made outside the tool loop.
// It is used for commit-message requests, which belong in the enclosing turn's
// totals.
func (c *Coder) RecordTurnSideUsage(u llm.Usage) {
	c.messageTokensSent += u.PromptTokens
	c.messageTokensReceived += u.CompletionTokens
	c.messageCacheRead += u.CacheReadTokens
	c.messageCacheWrite += u.CacheWriteTokens
	c.totalTokensSent += u.PromptTokens
	c.totalTokensReceived += u.CompletionTokens
	c.peakTokensSent = max(c.peakTokensSent, u.PromptTokens)
	if u.Cost != nil {
		c.messageCost += *u.Cost
		c.totalCost += *u.Cost
		c.costKnown, c.sessionKnown = true, true
	}
}

// RecordSideUsage records one session-notes request in its independent
// accumulator and folds it into the session totals. FlushSideUsage consumes the
// accumulator after the request is complete.
func (c *Coder) RecordSideUsage(u llm.Usage) {
	c.sideTokensSent += u.PromptTokens
	c.sideTokensReceived += u.CompletionTokens
	c.sideCacheRead += u.CacheReadTokens
	c.sideCacheWrite += u.CacheWriteTokens
	c.totalTokensSent += u.PromptTokens
	c.totalTokensReceived += u.CompletionTokens
	c.peakTokensSent = max(c.peakTokensSent, u.PromptTokens)
	if u.Cost != nil {
		c.sideCost += *u.Cost
		c.totalCost += *u.Cost
		c.sideCostKnown = true
		c.sessionKnown = true
	}
	if u.PromptTokens != 0 || u.CompletionTokens != 0 || u.CacheReadTokens != 0 ||
		u.CacheWriteTokens != 0 || u.Cost != nil {
		c.sideUsageRecorded = true
	}
}

// formatTokenLine renders the token half of the usage report, for one send and
// for a whole turn alike.
//
// The cache figures go in parentheses because they are a *breakdown* of what
// was sent, not two more things that were sent. As a flat comma list —
// "330.3k sent, 127.8k cache write, 74.7k cache hit" — they read as separate
// quantities, which is exactly the misreading that had the sent figure adding
// the cache write to a prompt that already contained it.
// The rate is received tokens over the time the provider had the request —
// summed across the turn's sends, so tool runs, edits and confirmation prompts
// between them are excluded. It is *not* decode speed: the wait for the first
// token is part of it, because that wait is part of what the number is for.
//
// Measured before choosing, on one OpenRouter request with a large prompt: 42.9
// t/s over the whole request against 138.0 over the gap between first and last
// token, a factor of three. Decode speed is the flattering one and the one
// people usually quote, and it is also undefined here for the turn shape this
// harness produces most — a short reply and a tool call, where the content
// arrives in a chunk or two and the gap is zero. A figure that divides by zero
// on the common case is not a figure.
//
// Reasoning tokens are in `received` where a provider bills them there, so the
// rate covers thinking as well as answering. That is the right side to err on:
// the user waited for it.
func formatTokenLine(sent, cacheWrite, cacheRead, received int, modelTime time.Duration) string {
	var parts []string
	if cacheWrite > 0 {
		parts = append(parts, formatTokens(cacheWrite)+" cache write")
	}
	if cacheRead > 0 {
		parts = append(parts, formatTokens(cacheRead)+" cache hit")
	}
	line := "Tokens: " + formatTokens(sent) + " sent"
	if len(parts) > 0 {
		line += " (" + strings.Join(parts, ", ") + ")"
	}
	line += ", " + formatTokens(received) + " received"
	if rate := tokenRate(received, modelTime); rate != "" {
		line += ", " + rate
	}
	return line + "."
}

// tokenRate renders the throughput, or "" when there is nothing honest to say:
// no tokens, or a duration too short to divide by without inventing precision.
// A fake clock in tests reports no elapsed time at all, which lands here.
func tokenRate(received int, modelTime time.Duration) string {
	const floor = 50 * time.Millisecond
	if received <= 0 || modelTime < floor {
		return ""
	}
	rate := float64(received) / modelTime.Seconds()
	if rate < 10 {
		return fmt.Sprintf("%.1f t/s", rate)
	}
	return fmt.Sprintf("%.0f t/s", rate)
}

// flushTurnUsage prints the turn's accounting, once, at its end.
//
// It replaces a line per send. In a seven-step turn that was seven of the
// noisiest lines on the screen, each reporting a fragment — "Cost: $0.000038
// message" — where the number a reader wants is the total. What survives of
// turnProgress is the step count, which is worth saying once it is not being
// said on every step.
func (c *Coder) flushTurnUsage() {
	if c.messageSends == 0 {
		return // an empty turn has no accounting
	}
	c.messageSends = 0 // idempotent: never report the same turn twice

	report := c.messageUsageReport("turn", "") + c.turnSummary()

	c.Out.Printf("")
	c.Out.Printf("%s", report)

	c.record(Record{
		Type:       "turn",
		Outcome:    c.lastSendOutcome.String(),
		Steps:      c.numSteps,
		Sent:       c.messageTokensSent,
		Received:   c.messageTokensReceived,
		Cost:       c.messageCost,
		CostKnown:  c.costKnown,
		Pinned:     c.pinnedRecordPaths(),
		EditsExact: c.editsExact,
		EditsFuzzy: c.editsFuzzy,
	})

	if c.RecordUsage != nil {
		u := TurnUsage{
			Model:        c.Model.QualifiedSlug(),
			TokensSent:   c.messageTokensSent,
			TokensRecv:   c.messageTokensReceived,
			CacheRead:    c.messageCacheRead,
			CacheWrite:   c.messageCacheWrite,
			Estimated:    c.messageEstimated,
			Steps:        c.numSteps + 1,
			FilesChanged: len(c.turnEditedFiles),
		}
		if c.costKnown {
			cost := c.messageCost
			u.Cost = &cost
		}
		c.RecordUsage(u)
	}
}

// TurnUsage is one turn's accounting, handed to RecordUsage at turn end — the
// same numbers the closing usage line prints.
type TurnUsage struct {
	Model        string
	TokensSent   int
	TokensRecv   int
	CacheRead    int
	CacheWrite   int
	Cost         *float64 // nil when the provider reported none
	Estimated    bool
	Steps        int
	FilesChanged int
}

// streamedText is everything received this send — stitched continuations plus
// the current partial answer and reasoning — used to estimate received tokens
// when the provider's usage never arrived.
func (c *Coder) streamedText() string {
	return c.multiResponseContent + c.partialResponseContent + c.partialReasoningContent
}

func formatTokens(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000.0)
}

func formatCost(v float64) string {
	if v == 0 {
		return "0.00"
	}
	mag := math.Abs(v)
	if mag >= 0.01 {
		return fmt.Sprintf("%.2f", v)
	}
	prec := max(2, 2-int(math.Log10(mag)))
	return fmt.Sprintf("%.*f", prec, v)
}

func (c *Coder) showExhaustedError() {
	c.Out.Errorf("The chat session exhausted the model's context window. Use /clear or /drop to reduce it.")
}

// stripReasoning removes an inline reasoning tag from the answer before the
// edit parser sees it.
func stripReasoning(answer, tag string) string {
	if tag == "" {
		return answer
	}
	answer = reasoningTagRe(tag).ReplaceAllString(answer, "")
	answer = strings.TrimSpace(answer)
	closing := "</" + tag + ">"
	if idx := strings.Index(answer, closing); idx >= 0 {
		answer = strings.TrimSpace(answer[idx+len(closing):])
	}
	return answer
}

// turnSummary is how far the turn ran, appended to its closing usage line.
//
// It says nothing about a one-step turn, which is most of them, so the ordinary
// case reads as a plain accounting line. Past that it is the only place the
// step count now appears: it used to ride every send's line, where twenty-five
// steps meant twenty-five status lines competing with the diffs.
func (c *Coder) turnSummary() string {
	var parts []string
	if c.numSteps > 0 {
		parts = append(parts, plural(c.numSteps+1, "step", "steps"))
	}
	if n := len(c.turnEditedFiles); n > 0 {
		parts = append(parts, plural(n, "file", "files")+" changed")
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, ", ") + "."
}
