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
	prompt, completion, cacheWrite, cacheRead int
	cost                                      float64
	costKnown                                 bool
	finalized                                 bool
	// estSent is the pre-send token estimate, the fallback when the turn is
	// aborted before the provider's usage arrives.
	estSent int
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
	resFailed
)

// streamOnce runs one request through the client, dispatching stream events to
// the output and accumulating into the partial* fields and usage. It returns a
// coarse classification of how the stream ended. The caller resets the partial*
// fields before each call and decides whether to retry or continue.
func (c *Coder) streamOnce(ctx context.Context, req llm.Request, usage *sendUsage) (streamResult, error) {
	finishReason := ""
	for ev, err := range c.Client.Send(ctx, req) {
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return resInterrupted, nil
			}
			var se *llm.StreamError
			if errors.As(err, &se) {
				if se.Class == llm.ErrContextWindow {
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
		case llm.EventReasoning:
			c.partialReasoningContent += ev.Text
			c.Out.StreamReasoning(ev.Text)
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
// false. Shared by sendMessage and RunAside so /btw retries like a normal turn.
func (rb *retryBackoff) retry(c *Coder, streamErr error) bool {
	var se *llm.StreamError
	if !errors.As(streamErr, &se) || !se.Retryable() {
		c.Out.Errorf("%v", streamErr)
		return false
	}
	rb.delay *= 2
	if rb.delay > retryTimeout {
		c.Out.Errorf("%s", se.Error())
		return false
	}
	c.Out.Warningf("%s", se.Error())
	c.Out.Printf("Retrying in %.1f seconds...", rb.delay.Seconds())
	c.Clock.Sleep(rb.delay)
	return true
}

// sendMessage is the phase machine. It returns the outcome and, for
// OutcomeReflect, the next message to send.
func (c *Coder) sendMessage(ctx context.Context, inp string) (SendOutcome, string) {
	// --- Setup ---
	// A tool continuation re-enters on the tool result messages already
	// appended to curMessages (reflection-as-tool-error), so it adds no user
	// turn; inp is unused on that path.
	appendedUser := false
	if c.toolContinuation {
		c.toolContinuation = false
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

	usage := &sendUsage{estSent: c.countMessages(messages)}
	defer c.finalizeUsage(usage)

	backoff := retryBackoff{delay: initialRetryDelay}
	continuations := 0

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

		res, streamErr := c.streamOnce(ctx, c.buildRequest(messages), usage)

		if res == resFailed {
			// A retryable error backs off and retries (the partial is discarded
			// at the loop top; accumulated multiResponseContent is untouched);
			// anything else is a hard failure.
			if backoff.retry(c, streamErr) {
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

		term = res // resDone, resInterrupted, resContextExhausted
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
		if n := len(c.curMessages); n > 0 && c.curMessages[n-1].Role == "user" {
			c.curMessages = append(c.curMessages,
				llm.TextMessage("assistant", "FinishReasonLength exception: you sent too many tokens"))
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
	}

	if !interrupted && answer == "" && len(c.partialToolCalls) == 0 {
		dropUserTurn()
		c.Out.Warningf("Empty response received from LLM. Check your provider account?")
		return OutcomeFailed, ""
	}

	if interrupted {
		// Interrupt shape.
		if n := len(c.curMessages); n > 0 && c.curMessages[n-1].Role == "user" {
			c.curMessages[n-1] = llm.TextMessage("user", c.curMessages[n-1].Text()+"\n^C KeyboardInterrupt")
		} else {
			c.curMessages = append(c.curMessages, llm.TextMessage("user", "^C KeyboardInterrupt"))
		}
		c.curMessages = append(c.curMessages,
			llm.TextMessage("assistant", "I see that you interrupted my previous reply."))
		return OutcomeInterrupted, ""
	}

	// Everything a turn does now arrives as tool calls, in every mode: ask mode
	// is the same dispatch with the mutating tools withheld. There is no longer
	// a second path that reads the answer text for edits, shell blocks, or file
	// mentions — those were the text formats' way of saying what a tool call
	// says directly.
	return c.applyToolCalls(ctx), ""
}

// buildRequest translates state into an llm.Request.
func (c *Coder) buildRequest(messages []llm.Message) llm.Request {
	req := llm.Request{
		Model:           c.Model.Slug,
		Messages:        messages,
		Temperature:     c.Model.Temperature,
		ReasoningEffort: c.Model.Reasoning,
		ExtraParams:     c.Model.RequestExtraParams(),
	}
	if c.editFormat == "tool" {
		req.Tools = c.toolDefs()
		req.ToolChoice = "auto"
	}
	return req
}

// checkTokens warns when the estimate reaches the input window and asks to
// proceed.
func (c *Coder) checkTokens(messages []llm.Message) bool {
	maxInput := c.Model.Context
	if maxInput <= 0 {
		return true
	}
	inputTokens := c.countMessages(messages)
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
	yes, _ := c.Confirm.Confirm(ConfirmRequest{Prompt: "Try to proceed anyway?"})
	return yes
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
	c.maybeSummarize()
}

// maxChatHistoryTokens is the settled-history budget: aider's
// min(max(context/16, 1024), 8192), derived from the main model's window.
func maxChatHistoryTokens(context int) int {
	return min(max(context/16, 1024), 8192)
}

// maybeSummarize compacts the settled history when it outgrows the chat-history
// budget. It runs only when a summarizer is wired and the model's window is
// known (Context > 0) — mirroring checkTokens, which treats an unknown window
// as "no limit to enforce". Synchronous: the weak-model call happens here,
// before the next prompt is assembled. On failure the history is left intact.
func (c *Coder) maybeSummarize() {
	if c.Summarizer == nil || c.Model.Context <= 0 {
		return
	}
	budget := maxChatHistoryTokens(c.Model.Context)
	if !c.Summarizer.tooBig(c.doneMessages, budget) {
		return
	}
	c.Out.Printf("Summarizing chat history to fit the context window...")
	out, err := c.Summarizer.summarize(c.doneMessages, budget)
	if err != nil {
		c.Out.Warningf("Could not summarize chat history: %v", err)
		return
	}
	c.doneMessages = out
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

	sent := u.prompt + u.cacheWrite
	received := u.completion
	estimated := false

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

	report := fmt.Sprintf("Tokens: %s sent", formatTokens(sent))
	if u.cacheWrite > 0 {
		report += fmt.Sprintf(", %s cache write", formatTokens(u.cacheWrite))
	}
	if u.cacheRead > 0 {
		report += fmt.Sprintf(", %s cache hit", formatTokens(u.cacheRead))
	}
	report += fmt.Sprintf(", %s received.", formatTokens(received))

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
			// (DeepSeek's cache-read discount uses the same 0.10 factor).
			cost = float64(u.cacheWrite)*pin*1.25 +
				float64(u.cacheRead)*pin*0.10 +
				float64(u.prompt)*pin +
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

	report := fmt.Sprintf("Tokens: %s sent", formatTokens(c.messageTokensSent))
	if c.messageCacheWrite > 0 {
		report += fmt.Sprintf(", %s cache write", formatTokens(c.messageCacheWrite))
	}
	if c.messageCacheRead > 0 {
		report += fmt.Sprintf(", %s cache hit", formatTokens(c.messageCacheRead))
	}
	report += fmt.Sprintf(", %s received.", formatTokens(c.messageTokensReceived))
	if c.costKnown {
		report += fmt.Sprintf(" Cost: $%s turn, $%s session.", formatCost(c.messageCost), formatCost(c.totalCost))
	}
	if c.messageEstimated {
		report += " (estimated)"
	}
	report += c.turnSummary()

	c.Out.Printf("")
	c.Out.Printf("%s", report)
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
