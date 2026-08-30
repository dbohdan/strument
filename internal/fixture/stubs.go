package fixture

import (
	"context"
	"fmt"
	"iter"

	"dbohdan.com/strument/internal/llm"
)

// StreamStub replays a scenario's turns as an llm.ModelClient. Each Send
// consumes the next turn; OnRequest, when set, receives the outgoing request
// and the fixture's captured request row for assertion.
type StreamStub struct {
	Turns     []Turn
	OnRequest func(turn int, req llm.Request, captured *Request) error
	next      int
}

// NewStreamStub replays sc's turns.
func NewStreamStub(sc *Scenario) *StreamStub {
	return &StreamStub{Turns: sc.Turns}
}

// Remaining reports how many turns have not been consumed.
func (s *StreamStub) Remaining() int { return len(s.Turns) - s.next }

// Send implements llm.ModelClient over the fixture's event rows. Fixture
// "Error" events surface as *llm.StreamError on the error side; context
// cancellation surfaces as ctx.Err() before the next event.
func (s *StreamStub) Send(ctx context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if s.next >= len(s.Turns) {
			yield(llm.StreamEvent{}, fmt.Errorf("fixture: Send call %d but only %d turns", s.next+1, len(s.Turns)))
			return
		}
		turn := s.Turns[s.next]
		idx := s.next
		s.next++
		if s.OnRequest != nil {
			if err := s.OnRequest(idx, req, turn.Request); err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
		}
		for _, ev := range turn.Events {
			if err := ctx.Err(); err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			switch ev.Kind {
			case "Error":
				yield(llm.StreamEvent{}, &llm.StreamError{Class: llm.ErrorClass(ev.Class), Message: ev.Message})
				return
			case string(llm.EventAnswer), string(llm.EventReasoning), string(llm.EventUsage), string(llm.EventFinish):
				if !yield(llm.StreamEvent{
					Kind:         llm.EventKind(ev.Kind),
					Text:         ev.Text,
					Usage:        ev.Usage,
					FinishReason: ev.FinishReason,
				}, nil) {
					return
				}
			case string(llm.EventToolCall):
				if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
					Index: ev.ToolIndex,
					ID:    ev.ToolID,
					Name:  ev.ToolName,
					Args:  ev.ToolArgs,
				}}, nil) {
					return
				}
			case "Panic":
				// A stand-in for any panic inside the turn. Raising it here
				// means the panic unwinds through the harness's send and
				// turn-defer machinery exactly like a real one.
				panic(ev.Message)
			default:
				yield(llm.StreamEvent{}, fmt.Errorf("fixture: unknown event kind %q", ev.Kind))
				return
			}
		}
	}
}

// ConfirmScript answers confirmation prompts from the fixture's confirm rows,
// in order. A prompt that doesn't match the next scripted row is an error:
// the scenario said something unexpected happened.
type ConfirmScript struct {
	Rows []Confirm
	next int
}

// NewConfirmScript scripts sc's confirm rows.
func NewConfirmScript(sc *Scenario) *ConfirmScript {
	return &ConfirmScript{Rows: sc.Confirms}
}

// Ask consumes the next scripted answer for prompt.
func (c *ConfirmScript) Ask(prompt string) (string, error) {
	if c.next >= len(c.Rows) {
		return "", fmt.Errorf("fixture: unscripted confirm prompt %q", prompt)
	}
	row := c.Rows[c.next]
	c.next++
	if row.Prompt != "" && row.Prompt != prompt {
		return "", fmt.Errorf("fixture: confirm prompt %q, script expected %q", prompt, row.Prompt)
	}
	return row.Answer, nil
}

// Remaining reports unconsumed confirm rows (a completed scenario should
// leave zero).
func (c *ConfirmScript) Remaining() int { return len(c.Rows) - c.next }

// AskScript answers ask_user_question prompts from the fixture's ask rows, in
// file order. Like ConfirmScript it fails loudly on a mismatch: a question
// whose wording drifted is a scenario that no longer tests what it says.
type AskScript struct {
	Rows []Ask
	next int
}

// NewAskScript scripts sc's ask rows.
func NewAskScript(sc *Scenario) *AskScript {
	return &AskScript{Rows: sc.Asks}
}

// Answer consumes the next scripted answer for question. The raw row is
// returned — the caller runs it through coder.ParseAskAnswer so a scripted
// "1" resolves to the same label a live user's "1" would.
func (a *AskScript) Answer(question string) (string, error) {
	if a.next >= len(a.Rows) {
		return "", fmt.Errorf("fixture: unscripted ask prompt %q", question)
	}
	row := a.Rows[a.next]
	a.next++
	if row.Question != "" && row.Question != question {
		return "", fmt.Errorf("fixture: ask prompt %q, script expected %q", question, row.Question)
	}
	return row.Answer, nil
}

// Remaining reports unconsumed ask rows (a completed scenario should leave
// zero).
func (a *AskScript) Remaining() int { return len(a.Rows) - a.next }

// CommandScript serves CommandRunner results from the fixture's command rows,
// in order.
type CommandScript struct {
	Rows []Command
	next int
}

// NewCommandScript scripts sc's command rows.
func NewCommandScript(sc *Scenario) *CommandScript {
	return &CommandScript{Rows: sc.Commands}
}

// Run consumes the next scripted command result for block.
func (c *CommandScript) Run(block string) (exit int, output string, err error) {
	if c.next >= len(c.Rows) {
		return 0, "", fmt.Errorf("fixture: unscripted command %q", block)
	}
	row := c.Rows[c.next]
	c.next++
	if row.Block != "" && row.Block != block {
		return 0, "", fmt.Errorf("fixture: command %q, script expected %q", block, row.Block)
	}
	return row.Exit, row.Output, nil
}

// Remaining reports unconsumed command rows.
func (c *CommandScript) Remaining() int { return len(c.Rows) - c.next }
