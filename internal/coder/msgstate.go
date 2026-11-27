package coder

import (
	"time"

	"dbohdan.com/strument/internal/llm"
)

// streamState is what one send's stream writes as it arrives.
type streamState struct {
	response  string // the reply so far
	reasoning string
	// reasoningField is the wire field the reasoning streamed on, "" when the
	// dialect names none.
	reasoningField string
	// multi holds the earlier parts of a reply that hit the output limit and
	// was continued, ahead of response.
	multi string
	// toolCalls accumulates in first-seen index order; toolCallIndex maps a
	// provider's call index to its place there.
	toolCalls     []llm.ToolCall
	toolCallIndex map[int]int
}

// messageUsage is a message's accounting across its sends: a turn's, or a
// side call's.
type messageUsage struct {
	cost      float64
	costKnown bool    // in-band or priced cost seen in this message
	goSpend   goSpend // what opencode Go charged the subscription
	sent      int
	received  int
	cacheRead int
	// cacheWrite and cacheRead are a breakdown of sent, not additions to it.
	cacheWrite int
	estimated  bool // any send fell back to an estimate
	sends      int
	// modelTime is how long the message spent waiting on the model: the sum
	// of each send's request-to-last-byte, and nothing else. Tool execution,
	// edits, confirmation prompts and the user's own thinking all happen
	// between sends and are excluded by construction.
	modelTime time.Duration
	// lastReport is the last send's line, as finalizeUsage built it.
	lastReport string
}

// minus is what u accumulated since before was taken, for a side call that
// runs on top of a message's totals instead of from zero.
func (u messageUsage) minus(before messageUsage) messageUsage {
	return messageUsage{
		cost: u.cost - before.cost, costKnown: u.costKnown, goSpend: u.goSpend.minus(before.goSpend),
		sent: u.sent - before.sent, received: u.received - before.received,
		cacheRead: u.cacheRead - before.cacheRead, cacheWrite: u.cacheWrite - before.cacheWrite,
		estimated: u.estimated, sends: u.sends - before.sends,
		modelTime: u.modelTime - before.modelTime, lastReport: u.lastReport,
	}
}
