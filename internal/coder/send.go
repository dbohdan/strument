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
	OutcomeReflect
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
	retryTimeout    = 60 * time.Second
	continuationCap = 4
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

	type streamResult int
	const (
		resDone streamResult = iota
		resContinuation
		resContextExhausted
		resOutputExhausted
		resInterrupted
		resFailed
	)

	retryDelay := 125 * time.Millisecond
	continuations := 0
	interrupted := false
	exhaustedContext := false
	exhaustedOutput := false
	failed := false

	for {
		c.partialResponseContent = ""
		c.partialReasoningContent = ""
		c.partialToolCalls = nil
		c.toolCallIndex = map[int]int{}

		res, streamErr := func() (streamResult, error) {
			finishReason := ""
			for ev, err := range c.Client.Send(ctx, c.buildRequest(messages)) {
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
							return resFailed, se // maybe retried below
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
		}()

		if res == resFailed {
			var se *llm.StreamError
			if errors.As(streamErr, &se) && se.Retryable() {
				retryDelay *= 2
				if retryDelay > retryTimeout {
					c.Out.Errorf("%s", se.Error())
					failed = true
					break
				}
				c.Out.Warningf("%s", se.Error())
				c.Out.Printf("Retrying in %.1f seconds...", retryDelay.Seconds())
				c.Clock.Sleep(retryDelay)
				// Retry discards the partial (reset at loop top); the
				// accumulated multiResponseContent is untouched.
				continue
			}
			c.Out.Errorf("%v", streamErr)
			failed = true
			break
		}

		if res == resInterrupted {
			interrupted = true
			break
		}
		if res == resContextExhausted {
			exhaustedContext = true
			break
		}
		if res == resContinuation {
			if !c.PrefillSupported {
				exhaustedOutput = true
				break
			}
			continuations++
			if continuations > continuationCap {
				exhaustedOutput = true // cap hit
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
		break // resDone
	}

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

	if exhaustedContext {
		if n := len(c.curMessages); n > 0 && c.curMessages[n-1].Role == "user" {
			c.curMessages = append(c.curMessages,
				llm.TextMessage("assistant", "FinishReasonLength exception: you sent too many tokens"))
		}
		c.showExhaustedError()
		return OutcomeContextExhausted, ""
	}
	if exhaustedOutput {
		// The large partial was kept above; no diagnostic (trailing message
		// is the assistant partial).
		return OutcomeOutputExhausted, ""
	}
	if failed {
		if answer == "" {
			// Failed before output: don't poison history.
			c.curMessages = c.curMessages[:len(c.curMessages)-1]
			return OutcomeFailed, ""
		}
		return OutcomeFailed, ""
	}
	if !interrupted && answer == "" && len(c.partialToolCalls) == 0 {
		c.curMessages = c.curMessages[:len(c.curMessages)-1]
		c.Out.Warningf("Empty response received from LLM. Check your provider account?")
		return OutcomeFailed, ""
	}

	if !interrupted {
		if c.editFormat == "tool" {
			return c.applyToolCalls(ctx), ""
		}
		if msg := c.checkForFileMentions(answer); msg != "" {
			return OutcomeReflect, msg
		}
		if c.replyCompleted() {
			return OutcomeSuccess, ""
		}
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

	// --- Success path ---
	edited, reflection := c.applyUpdates(answer)

	if len(edited) > 0 {
		for _, f := range edited {
			c.turnEditedFiles[f] = true
		}
		saved := c.autoCommit(edited)
		if saved == "" {
			saved = c.Prompts.FilesContentGPTEditsNoRepo
		}
		c.moveBackCurMessages(saved)
	}

	if reflection != "" {
		return OutcomeReflect, reflection
	}

	// [lint reflection re-enters here in v2]

	if output := c.runShellCommands(ctx); output != "" {
		c.curMessages = append(c.curMessages,
			llm.TextMessage("user", output),
			llm.TextMessage("assistant", "Ok"),
		)
	}

	// [test reflection re-enters here in v2]

	return OutcomeSuccess, ""
}

// replyCompleted is the no-op v1 hook; a truthy return ends the turn.
func (c *Coder) replyCompleted() bool { return false }

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
		inputTokens, maxInput, c.Model.Slug)
	c.Out.Printf("To reduce the chat context:")
	c.Out.Printf("- Use /drop to remove unneeded files from the chat")
	c.Out.Printf("- Use /clear to clear the chat history")
	c.Out.Printf("- Break your code into smaller files")
	c.Out.Printf("It's probably safe to try and send the request, most providers won't charge if the context limit is exceeded.")
	yes, _ := c.Confirm.Confirm(ConfirmRequest{Prompt: "Try to proceed anyway?"})
	return yes
}

// moveBackCurMessages rotates cur into done on edited turns.
func (c *Coder) moveBackCurMessages(saved string) {
	c.doneMessages = append(c.doneMessages, c.curMessages...)
	if saved != "" {
		c.doneMessages = append(c.doneMessages,
			llm.TextMessage("user", saved),
			llm.TextMessage("assistant", "Ok."),
		)
	}
	c.curMessages = nil
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

	c.messageTokensSent = sent
	c.messageTokensReceived = received
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
		c.messageCost = cost
		c.totalCost += cost
		c.costKnown = true
		c.sessionKnown = true
		report += fmt.Sprintf(" Cost: $%s message, $%s session.", formatCost(c.messageCost), formatCost(c.totalCost))
	}
	if estimated {
		report += " (estimated)"
	}

	c.lastUsageReport = report
	c.Out.Printf("") // blank line before the token/cost report, like aider
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
