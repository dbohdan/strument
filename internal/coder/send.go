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
	resFailed
)

// streamOnce runs one request through the client, dispatching stream events to
// the output and accumulating into the partial* fields and usage. It returns a
// coarse classification of how the stream ended. The caller resets the partial*
// fields before each call and decides whether to retry or continue.
func (c *Coder) streamOnce(ctx context.Context, req llm.Request, usage *sendUsage) (streamResult, error) {
	finishReason := ""
	usage.rejected = false
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

	usage := &sendUsage{estSent: c.countMessages(messages) + c.countTools()}
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
	res := c.Confirm.Confirm(ConfirmRequest{Prompt: "Try to proceed anyway?"})
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

	report := formatTokenLine(sent, u.cacheWrite, u.cacheRead, received)

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

// RecordSideUsage folds in a request the turn made outside the tool loop.
//
// Today that is only the commit message, which goes out through the client
// directly and so never reached finalizeUsage. A measured five-request turn
// reported the four it knew about — $0.00084 — having paid $0.00093. Nine
// percent of a small turn, and the share grows with the diff, since that call
// re-sends it uncached.
//
// It runs before flushTurnUsage (see Run), so the number the user reads is the
// number they paid.
func (c *Coder) RecordSideUsage(u llm.Usage) {
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

// formatTokenLine renders the token half of the usage report, for one send and
// for a whole turn alike.
//
// The cache figures go in parentheses because they are a *breakdown* of what
// was sent, not two more things that were sent. As a flat comma list —
// "330.3k sent, 127.8k cache write, 74.7k cache hit" — they read as separate
// quantities, which is exactly the misreading that had the sent figure adding
// the cache write to a prompt that already contained it.
func formatTokenLine(sent, cacheWrite, cacheRead, received int) string {
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
	return line + ", " + formatTokens(received) + " received."
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

	report := formatTokenLine(c.messageTokensSent, c.messageCacheWrite,
		c.messageCacheRead, c.messageTokensReceived)
	if c.costKnown {
		report += fmt.Sprintf(" Cost: $%s turn, $%s session.", formatCost(c.messageCost), formatCost(c.totalCost))
	}
	if c.messageEstimated {
		report += " (estimated)"
	}
	report += c.turnSummary()

	c.Out.Printf("")
	c.Out.Printf("%s", report)

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
