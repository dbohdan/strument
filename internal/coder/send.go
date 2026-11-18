package coder

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"dbohdan.com/strument/internal/config"
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
	// OutcomeStopped: the user pressed Ctrl-C during the turn's first send,
	// before anything came back. That is a connection being waited on or
	// retried, not a reply being steered, so the turn ends as if it had not
	// begun: no "what now?" and nothing added to the conversation.
	OutcomeStopped
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
	case OutcomeStopped:
		return "Stopped"
	case OutcomeContextExhausted:
		return "ContextExhausted"
	case OutcomeOutputExhausted:
		return "OutputExhausted"
	default:
		return "Failed"
	}
}

// Retry/continuation constants. The default retry timeout is a minute, and
// continuations are capped.
//
// maxRetryDelay is the longest single wait. It only binds once retry_timeout is
// raised past the default: under the default the ladder stops at 32s anyway,
// and without a ceiling a ten-minute budget would double its way to one
// 256-second sleep, which rides out a burst of 429s worse than several
// minute-long ones do.
const (
	retryTimeout      = 60 * time.Second
	maxRetryDelay     = 60 * time.Second
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
	// stopped is a send the user stopped before anything came back
	// (OutcomeStopped). Like a rejected one it reports no estimate: the turn
	// it belonged to did not happen, and a usage line under it would read as
	// a charge for a reply nobody saw.
	stopped bool
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
//
// call names the request in its log record: "turn" or "aside".
func (c *Coder) streamOnce(ctx context.Context, call string, req llm.Request, usage *sendUsage, loops *loopDetector) (res streamResult, err error) {
	finishReason := ""
	usage.rejected = false
	// This request's own usage, apart from usage's running sum, for its record.
	var got llm.Usage
	sawUsage := false

	// The clock brackets exactly this loop, which is the whole of the time the
	// provider owns: from the request going out to the last byte of the
	// stream. Everything a turn does between sends — running tools, applying
	// edits, asking the user — is outside it, so summing these across a turn
	// gives model time rather than wall time.
	started := c.Clock.Now()
	estimated := c.requestTokens(req.Messages)
	defer func() {
		elapsed := c.Clock.Now().Sub(started)
		usage.modelTime += elapsed
		c.usage.modelTime += elapsed
		c.recordRequest(call, res, err, finishReason, got, sawUsage, elapsed, estimated)
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
			c.stream.response += ev.Text
			c.Out.StreamText(ev.Text)
			if loops.feed(loopAnswer, ev.Text) != nil {
				return resLooping, nil
			}
		case llm.EventReasoning:
			c.stream.reasoning += ev.Text
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
			// The provider ended the response with an error: what streamed
			// is a fragment. Accepted as a finish, it became the answer — a
			// truncated reply, or a consult's, reported as complete. It is
			// retried like the error object a provider sends mid-stream.
			if ev.FinishReason == "error" {
				return resFailed, &llm.StreamError{Class: llm.ErrServer,
					Message: "the provider ended the response with an error"}
			}
		case llm.EventUsage:
			usage.add(ev.Usage)
			if call == "turn" && ev.Usage != nil && ev.Usage.PromptTokens > 0 {
				c.calibrateTokens(estimated, ev.Usage.PromptTokens)
			}
			if ev.Usage != nil {
				got.Add(*ev.Usage)
				sawUsage = true
			}
		}
	}
	if finishReason == "length" {
		return resContinuation, nil
	}
	return resDone, nil
}

// retryBackoff carries the doubling delay for transient stream errors across a
// send's retry loop.
//
// cap bounds the total time spent waiting; once the waits reach it, the next
// failure gives up. Zero means retryTimeout. The turn and /btw pass the
// configured retry_timeout.
//
// The cap is compared with the waiting done so far, not with the next delay.
// For a doubling ladder from 0.25s the two agree — the waits before a delay d
// sum to d-0.25s — so the default ladder is eight waits, 0.25s to 32s, either
// way. They part once a delay is clamped to maxRetryDelay, which is the case
// the total is for: with a ceiling on the delay, the delay alone would never
// reach the cap.
type retryBackoff struct {
	delay  time.Duration
	cap    time.Duration
	waited time.Duration
}

func (rb *retryBackoff) limit() time.Duration {
	if rb.cap == 0 {
		return retryTimeout
	}
	return rb.cap
}

// retry reports whether to retry a failed stream. A retryable error backs off —
// doubling the delay (capped) and sleeping — then returns true; a non-retryable
// error, one past the cap, or a context that ended during the wait reports the
// failure and returns false. It takes Output and Clock rather than the Coder,
// so sendMessage and RunAside retry, and report, identically.
func (rb *retryBackoff) retry(ctx context.Context, out Output, clock Clock, streamErr error) bool {
	var se *llm.StreamError
	if !errors.As(streamErr, &se) || !se.Retryable() {
		out.Errorf("%v", streamErr)
		return false
	}
	if rb.waited >= rb.limit() {
		out.Errorf("%s", se.Error())
		return false
	}
	rb.delay = min(rb.delay*2, maxRetryDelay)
	rb.waited += rb.delay
	out.Warningf("%s", se.Error())
	out.Printf("Retrying in %.1f seconds...", rb.delay.Seconds())
	if !clock.Sleep(ctx, rb.delay) {
		// Ctrl-C during the wait is the user stopping the retries, and the
		// caller says so; "gave up: context canceled" would read as a failure.
		if errors.Is(ctx.Err(), context.Canceled) {
			return false
		}
		// The wait outlived the call's own deadline. Said rather than returned
		// quietly, or a failed call looks like a model with nothing to say.
		out.Errorf("gave up: %v", ctx.Err())
		return false
	}
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
	var consumedAttachments []llm.ImageSource
	if c.resumeInPlace {
		c.resumeInPlace = false
	} else {
		var msg llm.Message
		msg, consumedAttachments = c.userMessage(inp)
		c.curMessages = append(c.curMessages, msg)
		appendedUser = true
	}

	messages := c.formatMessages().allMessages()
	// Before the token check, not after: the projection changes what is
	// actually sent, and a count taken on the unprojected messages would be
	// counting something no provider will ever see.
	messages = projectForModel(messages, c.Model)

	// Over the limit by Strument's own count: fold the settled history first,
	// and ask only if that was not enough.
	if !c.fitsContext(messages) && c.foldForOverflow() {
		messages = projectForModel(c.formatMessages().allMessages(), c.Model)
	}
	if !c.checkTokens(messages) {
		if appendedUser {
			c.curMessages = c.curMessages[:len(c.curMessages)-1]
			// Give the attachments back. The turn never happened, and a user
			// who has to shorten their context should not also have to find
			// and re-attach the screenshot they were asking about.
			c.pendingAttachments = consumedAttachments
		}
		return OutcomeFailed, ""
	}

	// --- Stream ---
	c.stream.multi = "" // per-send reset (H1)

	usage := &sendUsage{estSent: c.countMessages(messages) + c.countTools()}
	defer c.finalizeUsage(usage)

	backoff := retryBackoff{delay: initialRetryDelay, cap: c.RetryTimeout}
	continuations := 0
	// Why the reply stopped short, for the notice below: the model does not
	// continue a partial answer, or it does and the continuation cap ran out.
	// Both end as resOutputExhausted and differ only in what the user can do.
	declinedPrefill := false

	// One detector per send, not per streamOnce: a continuation is the same
	// reply resumed, and a loop that spans the stitch is still a loop.
	loops := newLoopDetector(c.LoopDetection)

	// term is how the whole stream phase ended, as opposed to how one
	// streamOnce ended: retries and continuations loop without setting it.
	// streamOnce already classifies precisely, so the phase carries that
	// classification forward rather than re-encoding it into flags. Every path
	// out of the loop below assigns it before breaking.
	var term streamResult
	foldedForOverflow := false

	for {
		c.stream.response = ""
		c.stream.reasoning = ""
		c.stream.toolCalls = nil
		c.stream.toolCallIndex = map[int]int{}

		res, streamErr := c.streamOnce(ctx, "turn", c.buildRequest(messages), usage, loops)
		// Per stream, not sticky: a continuation that completes normally leaves
		// this false, so only a reply whose *last* stream was capped is
		// reported as truncated.
		c.hitOutputLimit = res == resContinuation

		if res == resFailed {
			// A retryable error backs off and retries (the partial is discarded
			// at the loop top; accumulated stream.multi is untouched);
			// anything else is a hard failure.
			if backoff.retry(ctx, c.Out, c.Clock, streamErr) {
				continue
			}
			term = resFailed
			if ctx.Err() != nil {
				// Stopped during the wait between attempts.
				term = resInterrupted
			}
			break
		}

		if res == resContinuation {
			// Read from the model on every send, not copied at New: /model and
			// /reload swap c.Model, and a copy kept the startup model's answer —
			// so starting on a model that continues and switching to one that
			// restarts left prefill on, the silent half of the asymmetry
			// config.Model.Prefill describes.
			if !c.Model.Prefill {
				declinedPrefill = true
				term = resOutputExhausted
				break
			}
			continuations++
			if continuations > continuationCap {
				term = resOutputExhausted // cap hit
				break
			}
			// Stitch: accumulated + current partial becomes the prefill.
			c.stream.multi += c.stream.response
			if n := len(messages); n > 0 && messages[n-1].Role == "assistant" {
				messages[n-1] = llm.TextMessage("assistant", c.stream.multi)
			} else {
				messages = append(messages, llm.TextMessage("assistant", c.stream.multi))
			}
			continue
		}

		// The provider refused the request as too long: fold the settled
		// history and send once more. Not after a continuation, whose stitched
		// partial a rebuild would drop, and not twice — a request still too long
		// after a fold is too long because of this turn, which cannot be folded.
		if res == resContextExhausted && !foldedForOverflow && continuations == 0 && c.foldForOverflow() {
			foldedForOverflow = true
			messages = projectForModel(c.formatMessages().allMessages(), c.Model)
			continue
		}

		term = res // resDone, resInterrupted, resLooping, resContextExhausted
		break
	}
	interrupted := term == resInterrupted
	// Interrupted in the turn's first send with nothing back at all: no text,
	// no reasoning, no tool call. On a poor connection this is Ctrl-C during
	// the retries, and the user wants them to stop, not a "what now?" about a
	// reply that never began, nor a note about one in the conversation.
	stoppedBeforeReply := interrupted && c.turnSends == 1 &&
		c.stream.multi == "" && c.stream.response == "" &&
		len(c.stream.toolCalls) == 0 && strings.TrimSpace(c.stream.reasoning) == ""
	usage.stopped = stoppedBeforeReply

	// --- Finally (always runs) ---
	c.Out.FlushStream()
	answer := c.stream.multi + c.stream.response
	answer = stripReasoning(answer, c.Model.ReasoningTag)
	c.stream.response = answer
	c.stream.multi = ""

	// --- Post-stream dispatch ---
	c.finalizeUsage(usage)

	if answer != "" || len(c.stream.toolCalls) > 0 {
		msg := llm.TextMessage("assistant", answer)
		msg.Model = c.Model.Slug
		if len(c.stream.toolCalls) > 0 {
			fillToolCalls(c.stream.toolCalls, len(c.doneMessages)+len(c.curMessages))
			msg.ToolCalls = c.stream.toolCalls
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

	if stoppedBeforeReply {
		dropUserTurn()
		if appendedUser {
			// As when the request is refused for size: the turn never
			// happened, so the attachments go back with it.
			c.pendingAttachments = consumedAttachments
		}
		c.Out.Printf("Stopped before the model replied. Nothing was added to the conversation.")
		return OutcomeStopped, ""
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
			// "No reply came back" rather than "the reply was cut off", which
			// described something that did not happen: on this path the
			// request exceeded the limit and was refused, so there was never a
			// reply to truncate. The distinction matters to the model reading
			// it — a truncated reply invites continuing, and there is nothing
			// here to continue from.
			c.curMessages = append(c.curMessages,
				llm.TextMessage(llm.RoleSystem, "No reply came back: the request exceeded the context limit."))
		}
		c.showExhaustedError()
		return OutcomeContextExhausted, ""
	case resOutputExhausted:
		// The large partial was kept above, and no diagnostic goes into the
		// conversation — the trailing message is the assistant partial, and a
		// system note after it would be describing what the model can see.
		//
		// The *user* is told, though, and after FlushStream so the notice does
		// not land against an unterminated stream. An answer that stops
		// mid-sentence otherwise reads as the model losing its thread rather
		// than as a cap, and with prefill off by default that is no longer a
		// rare event. Each cause names the setting that changes it.
		if declinedPrefill {
			c.Out.Warningf("The reply was cut off at the model's output limit. " +
				"Ask for the rest, raise `max_output`, or set `prefill = True` on the model " +
				"if it can continue a partial answer.")
		} else {
			c.Out.Warningf("The reply was cut off: it reached the model's output limit %d times. "+
				"Ask for the rest, or raise `max_output`.", continuationCap+1)
		}
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

	if !interrupted && answer == "" && len(c.stream.toolCalls) == 0 {
		dropUserTurn()
		// "Empty" is the wrong word when the model filled its whole budget and
		// the reply went out through the wrong channel; see reasoningLeak.
		switch marker, leaked := reasoningLeak(answer, c.stream.reasoning, len(c.stream.toolCalls)); {
		case leaked:
			// Neither half of this is the harness's to adjudicate, and an
			// earlier version tried: it told the user the parser was at fault
			// and "not the model's competence", which is a confident causal
			// claim from a position that cannot see either. The template sets
			// the trap and the model's format-adherence decides whether it
			// walks into it; both are levers, so both are named and neither is
			// blamed.
			c.Out.Warningf("Nothing ran: the model's tool call (%s) came before its reasoning block "+
				"closed, so the inference server recorded it as a thought.", marker)
			c.Out.Warningf("  A chat template that opens `<think>` for the model leaves it to close " +
				"the block before acting, and smaller models close it late more often. With " +
				"llama.cpp, check the template's thinking option.")
		case strings.TrimSpace(c.stream.reasoning) != "":
			c.Out.Warningf("The model returned reasoning but no answer and no tool call.")
		default:
			c.Out.Warningf("The model returned an empty response.")
		}
		return OutcomeFailed, ""
	}

	if interrupted {
		c.noteInterrupt()
		return OutcomeInterrupted, ""
	}

	// Everything a turn does now arrives as tool calls. There is no longer
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
		c.Out.Warningf("Stopped the model's reply because it was repeating itself.")
		return
	}
	c.Out.Warningf("Stopped the model's %s because it was repeating itself (%q, %d times).",
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
	c.stream.toolCalls = nil
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
	c.stream.toolCalls = nil
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
	// Whatever tools the session offers, offer them. A condition on the edit
	// format once meant the old ask mode sent none at all. A model told to
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
// the wire, and compaction does not help, because it runs at the end of a turn
// and there has not been one yet. RestoreHistory answers that by compacting at
// restore time; this is the backstop for what compaction does not bound, such
// as a window smaller than the system prompt and tool schemas.
//
// It warns rather than asking. A confirmation before the user has typed
// anything is a toll on every start, and there is nothing to decide yet: the
// remedy is /clear, which they can reach either way.
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
	c.Out.Printf("Use /clear to forget the restored conversation, or /tokens to see the split.")
}

// checkTokens warns when the estimate reaches the input window and asks to
// proceed.
func (c *Coder) checkTokens(messages []llm.Message) bool {
	if c.fitsContext(messages) {
		return true
	}
	maxInput := c.Model.Context
	inputTokens := c.scaledTokens(c.requestTokens(messages))
	c.Out.Errorf("This request is about %d tokens; the context limit for %s is %d.",
		inputTokens, c.Model.QualifiedSlug(), maxInput)
	// The history is what fills the window, so that is the advice. Say nothing
	// about what providers charge for an over-limit request: a guess stated as
	// reassurance.
	c.Out.Printf("To make room, use /clear to clear the history, or /tokens to see what takes the space.")
	res := c.Confirm.Confirm(ConfirmRequest{Prompt: "Send it anyway?", Grant: GrantContext})
	return res.Yes
}

// requestTokens estimates a request's size. The schemas count: they go out
// with the request like everything else, and leaving them out made the guard
// cheerful about a prompt 1.3k tokens closer to the limit than it reported.
func (c *Coder) requestTokens(messages []llm.Message) int {
	return c.countMessages(messages) + c.countTools() + c.countImages(messages)
}

// fitsContext reports whether a request fits the main model's window; an
// unknown window always fits, as there is no limit to enforce.
func (c *Coder) fitsContext(messages []llm.Message) bool {
	return c.Model.Context <= 0 || c.scaledTokens(c.requestTokens(messages)) < c.Model.Context
}

// calibrateTokens folds one request's provider-counted prompt size into
// tokenScale, the ratio of what the provider counts to what Strument estimates.
//
// The estimate is characters over four (RuneCounter), right for English prose
// and wrong for what a coding session mostly holds. On the Larkspur session —
// Go source, JSON, test output — the provider counted 171,443 tokens for a
// request Strument put at 124,464 plus schemas: three characters a token, not
// four. The history budget was measured in the estimate, so a history the
// provider saw as well over budget never folded. The provider reports the true
// size of every request it answers, so rather than guess a better constant for
// every tokenizer and every mix of code and prose, Strument learns the ratio
// from the session itself. Smoothed, because one request with a large image or
// an odd tool result should not swing the budget; clamped, because a provider
// that reports nonsense should not be able to.
func (c *Coder) calibrateTokens(estimated, counted int) {
	if estimated <= 0 || counted <= 0 {
		return
	}
	r := min(max(float64(counted)/float64(estimated), 0.5), 3)
	if c.tokenScale == 0 {
		c.tokenScale = r
		return
	}
	c.tokenScale = (c.tokenScale + r) / 2
}

// SeedTokenScale starts a restored session from the token ratio its last turn
// request measured on this model, so that the restore-time fold measures the
// history in the provider's units. Without it a restore measured with the bare
// estimate, which undercounts code, and a process per turn (--continue -m) never
// folded at all: each turn's fold came at its end, and was discarded with the
// process. Another model's ratio is no guide to this one's tokenizer.
func (c *Coder) SeedTokenScale(stats RestoreStats) {
	if r, ok := stats.TokenScales[c.Model.QualifiedSlug()]; ok {
		c.tokenScale = 0
		c.calibrateTokens(1000, int(r*1000))
	}
}

// scaledTokens converts an estimate into the provider's units, as far as this
// session has learned them.
func (c *Coder) scaledTokens(estimated int) int {
	if c.tokenScale == 0 {
		return estimated
	}
	return int(float64(estimated) * c.tokenScale)
}

// historyBudget is maxChatHistoryTokens in Strument's own estimate's units, so
// that it can be compared with counts taken by the estimator.
func (c *Coder) historyBudget() int {
	b := maxChatHistoryTokens(c.Model.Context)
	if c.tokenScale == 0 {
		return b
	}
	return int(float64(b) / c.tokenScale)
}

// moveBackCurMessages rotates cur into done on edited turns.
//
// No synthetic "I applied your changes" exchange is added, as a text edit
// format would need: every edit call already gets a tool result saying what it
// did to which file, so the pair would only repeat it with two messages the
// model never sent.
func (c *Coder) moveBackCurMessages() {
	// The turn's range, for /rewind. The files are the turn's own, still
	// valid here: turnEditedFiles resets when the next turn starts.
	start := len(c.doneMessages)
	c.doneMessages = append(c.doneMessages, c.curMessages...)
	c.turns = append(c.turns, TurnSpan{Start: start, End: len(c.doneMessages), Files: c.TurnEditedFiles()})
	c.curMessages = nil
	c.recordedMessages = 0
	c.maybeCompact()
}

// endTurnHistory settles the finished turn and compacts if it has outgrown the
// budget. Called once from runOne's defer, after the commit and the snapshot.
//
// Every turn rotates, whatever it did. Gating rotation on a turn having edited
// a file once left a session of questions entirely in curMessages, which goes
// on the wire in full and which compaction never examines, so the history
// budget existed and nothing was measured against it. What must not happen is
// rotating *mid-loop*, where compaction would eat the tool results the next
// send reacts to; running in the turn-end defer is what prevents that.
//
// Rotation does not move anything on the wire: done and cur are adjacent, so
// the split is bookkeeping. What changes is that compaction can see the
// conversation.
func (c *Coder) endTurnHistory() {
	if len(c.curMessages) == 0 {
		return
	}
	c.moveBackCurMessages()
}

// historyShare is the fraction of the main model's window the settled chat
// history may fill before it is folded: one half. It is a share of a
// *contended* window: history sits there beside the system prompt, the tool
// schemas and the exchange in progress.
//
// It was one eighth, and two trials moved it.
// doc/experiments/2026-10-compaction-threshold found that on MiMo folding early
// cost more than not folding: each fold rewrites the cached prefix, and a 98%
// cache hit on a long history is cheaper than the misses a fold causes. Folding
// at context/8 cost 16% more than never folding. The same trial's recall loss
// turned out to be the summarizer's (doc/experiments/2026-10-compaction-prompt),
// and with that fixed what an early fold still costs is the cache. Every
// harness in doc/experiments/2026-10-compaction-survey folds in the last fifth
// of the window, measuring the whole request rather than history alone. Half
// for history is about as late as the rest of the request leaves room for, and
// foldForOverflow catches a turn that overruns the window anyway.
const historyShare = 2

// minChatHistoryTokens keeps a tiny window from reducing the history budget to
// nothing, which would compact after every turn and never fit.
const minChatHistoryTokens = 2048

// maxChatHistoryTokens is the settled-history budget: context/historyShare,
// with a floor for small windows.
func maxChatHistoryTokens(context int) int {
	return max(context/historyShare, minChatHistoryTokens)
}

// maybeCompact compacts the settled history when it outgrows the chat-history
// budget: old tool output first, then the oldest turns. It runs only when the
// model's window is known (Context > 0) — mirroring checkTokens, which treats an
// unknown window as "no limit to enforce".
func (c *Coder) maybeCompact() {
	if c.Model == nil || c.Model.Context <= 0 || c.Tokens == nil {
		return
	}
	budget := c.historyBudget()
	if c.historyTokens(c.doneMessages) <= budget {
		return
	}
	if c.compactBackoff {
		c.compactBackoff = false
		return
	}
	if c.pruneHistory(budget) {
		return
	}
	if !c.rollHistory(budget) {
		c.compactBackoff = true
	}
}

// foldForOverflow compacts the settled history so that a request over the
// context limit can be sent again, and reports whether it did. The history is
// folded to half its size, or to the usual budget if that is smaller, whether
// or not it had reached the budget: the request is over the limit either way,
// and the history is the part of it that can give.
//
// Five of the six harnesses surveyed in doc/experiments/2026-10-compaction-survey
// fold and retry when a provider refuses a request as too long; Strument
// classified the refusal and ended the turn. With compaction moved late in the
// window, this is the backstop it relies on.
func (c *Coder) foldForOverflow() bool {
	if c.Tokens == nil || len(c.doneMessages) == 0 {
		return false
	}
	target := c.historyTokens(c.doneMessages) / 2
	if c.Model.Context > 0 {
		target = min(target, c.historyBudget())
	}
	if target <= 0 {
		return false
	}
	if c.pruneHistory(target) {
		return true
	}
	return c.rollHistory(target)
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
	// the counters means usage.sends stays 0 and flushTurnUsage says nothing
	// at all, which is the honest report.
	//
	// A stream that produced text before it broke is a different case: those
	// tokens were generated and will be billed, so it still estimates below.
	if (u.rejected || u.stopped) && sent == 0 && received == 0 && !u.costKnown && c.streamedText() == "" {
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
	c.usage.sends++
	c.usage.sent += sent
	c.usage.received += received
	c.usage.cacheRead += u.cacheRead
	c.usage.cacheWrite += u.cacheWrite
	c.usage.estimated = c.usage.estimated || estimated
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
		c.usage.cost += cost
		c.totalCost += cost
		c.usage.costKnown = true
		c.runKnown = true
		report += fmt.Sprintf(" Cost: $%s message, $%s run.", formatCost(cost), formatCost(c.totalCost))
	}
	if estimated {
		report += " (estimated)"
	}

	c.usage.lastReport = report
}

// messageUsageReport builds the accounting line from the message-scoped totals
// — the token breakdown, the cost, and the estimated marker — for a whole turn.
func (c *Coder) messageUsageReport(label, prefix string) string {
	return usageReport(label, prefix, c.usage.sent, c.usage.cacheWrite,
		c.usage.cacheRead, c.usage.received, c.usage.modelTime, c.usage.cost,
		c.usage.costKnown, c.totalCost, c.usage.estimated)
}

func usageReport(label, prefix string, sent, cacheWrite, cacheRead, received int,
	modelTime time.Duration, cost float64, costKnown bool, totalCost float64, estimated bool,
) string {
	report := prefix + formatTokenLine(sent, cacheWrite, cacheRead, received, modelTime)
	if costKnown {
		report += fmt.Sprintf(" Cost: $%s %s, $%s run.", formatCost(cost), label, formatCost(totalCost))
	}
	if estimated {
		report += " (estimated)"
	}
	return report
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

// tokenRateValue is the throughput in tokens per second, or 0 when there is
// nothing honest to say: no tokens, or a duration too short to divide by
// without inventing precision. A fake clock in tests reports no elapsed time at
// all, which lands here.
//
// Zero is the "unknown" value rather than a measurement, and can be, because a
// real rate is never zero: the floor guarantees both operands are positive.
// That is why it needs no companion flag, unlike cost, where $0 is a model that
// is free rather than a model nobody priced.
//
// Split out from tokenRate so the screen, the transcript and the cost ledger
// share one rule about when a rate exists. Three renderings of one measurement
// is three chances for two of them to disagree about whether 40ms of stream
// counts.
func tokenRateValue(received int, modelTime time.Duration) float64 {
	const floor = 50 * time.Millisecond
	if received <= 0 || modelTime < floor {
		return 0
	}
	// Two decimals. The divisor is wall-clock time including the network, so
	// the fourteen significant figures a float64 will print claim an accuracy
	// the measurement does not have.
	return math.Round(float64(received)/modelTime.Seconds()*100) / 100
}

// tokenRate renders the throughput for the usage line, or "" when there is none.
func tokenRate(received int, modelTime time.Duration) string {
	rate := tokenRateValue(received, modelTime)
	if rate == 0 {
		return ""
	}
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
	if c.usage.sends == 0 {
		return // an empty turn has no accounting
	}
	c.usage.sends = 0 // idempotent: never report the same turn twice

	report := c.messageUsageReport("turn", "") + c.turnSummary()

	c.Out.Printf("")
	c.Out.Printf("%s", report)

	c.record(Record{
		Type: "turn",
		// The instant the turn closed, which is what the transcript's header
		// showed: Turn.Time was never set by either caller, so render() fell
		// through to time.Now() at append time — a few microseconds after
		// this.
		Time: time.Now().Format(time.RFC3339),
		// The qualified slug, not the alias the session header carries: the
		// transcript's header showed the slug, and once a project holds
		// several sessions the model is a property of the turn rather than of
		// the process.
		Model: c.Model.QualifiedSlug(),
		// The slug on its own, which is what decides whether a later model is
		// the same one: the qualified form carries the provider's configured
		// name, and renaming a provider is not changing models.
		Slug:      c.Model.Slug,
		Outcome:   c.turnOutcome(),
		Steps:     c.turnSteps,
		Sent:      c.usage.sent,
		Received:  c.usage.received,
		Cost:      c.usage.cost,
		CostKnown: c.usage.costKnown,
		// From the same two numbers the usage line above divides, so the row
		// and the screen cannot disagree.
		TokensPerSecond: tokenRateValue(c.usage.received, c.usage.modelTime),
		EditsExact:      c.editsExact,
		EditsFuzzy:      c.editsFuzzy,
		Files:           c.TurnEditedFiles(),
		Tools:           c.TurnToolLines(),
		OfferedTools:    c.offeredToolNames(),
		Prompt:          c.turnUserMessage,
		Answer:          c.turnAnswer(),
	})

	c.reportUsage(c.turnSteps+1, len(c.turnEditedFiles))
}

// OutcomeCrashed is the turn outcome for a turn that died with a panic. It is
// not a SendOutcome: no send produced it, and by the time it is known the
// sends are over.
const OutcomeCrashed = "Crashed"

// turnOutcome is the last send's outcome, unless the turn never got to finish.
func (c *Coder) turnOutcome() string {
	if c.turnCrashed {
		return OutcomeCrashed
	}
	return c.lastSendOutcome.String()
}

// reportUsage hands the message-scoped accounting to RecordUsage, when there is
// one.
//
// Shared by a turn and by a side call — /btw and a consult — because a side call
// spends money and a ledger that cannot see it is not a ledger. It used to be
// inline in flushTurnUsage, which asides never reach: an aside printed its usage
// line and left no row, so a session's spend on screen and its spend on disk
// could disagree by however many questions had been asked beside the chat. A
// side call arrives as a one-step row with no files changed, under whichever
// model actually answered.
func (c *Coder) reportUsage(steps, filesChanged int) {
	if c.RecordUsage == nil {
		return
	}
	u := TurnUsage{
		Model:           c.Model.QualifiedSlug(),
		Provider:        config.ProviderName(c.Model),
		TokensSent:      c.usage.sent,
		TokensRecv:      c.usage.received,
		CacheRead:       c.usage.cacheRead,
		CacheWrite:      c.usage.cacheWrite,
		Estimated:       c.usage.estimated,
		Steps:           steps,
		FilesChanged:    filesChanged,
		TokensPerSecond: tokenRateValue(c.usage.received, c.usage.modelTime),
	}
	if c.usage.costKnown {
		cost := c.usage.cost
		u.Cost = &cost
	}
	c.RecordUsage(u)
}

// TurnUsage is one turn's accounting, handed to RecordUsage at turn end — the
// same numbers the closing usage line prints.
type TurnUsage struct {
	Model string
	// Provider is the provider the model is declared on: its configured name,
	// or its adapter when unnamed. Carried as a field rather than parsed out
	// of Model later, because slugs contain slashes of their own
	// ("xiaomi/mimo-v2.5") and splitting a joined "provider/slug" apart again
	// is a parse that only has to be wrong once.
	Provider     string
	TokensSent   int
	TokensRecv   int
	CacheRead    int
	CacheWrite   int
	Cost         *float64 // nil when the provider reported none
	Estimated    bool
	Steps        int
	FilesChanged int
	// TokensPerSecond is zero when there is no rate to report; see
	// tokenRateValue for why zero can carry that meaning here and nil has to
	// carry it for Cost.
	TokensPerSecond float64
}

// streamedText is everything received this send — stitched continuations plus
// the current partial answer and reasoning — used to estimate received tokens
// when the provider's usage never arrived.
func (c *Coder) streamedText() string {
	return c.stream.multi + c.stream.response + c.stream.reasoning
}

func formatTokens(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000.0)
}

// FormatCost renders a dollar amount the way the usage line does, with enough
// digits that a fraction of a cent is not shown as 0.00.
func FormatCost(v float64) string { return formatCost(v) }

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
	c.Out.Errorf("The conversation no longer fits the model's context window. Use /clear to make room.")
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
	if c.turnSteps > 0 {
		parts = append(parts, plural(c.turnSteps+1, "step", "steps"))
	}
	if n := len(c.turnEditedFiles); n > 0 {
		parts = append(parts, plural(n, "file", "files")+" changed")
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, ", ") + "."
}
