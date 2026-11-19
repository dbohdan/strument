package repl

import (
	"slices"
	"sync"

	"dbohdan.com/strument/internal/coder"
)

// heldOutput is the coder's output, which the Ctrl-C menu can hold. While
// held, every call is kept, in order, and the menu writes to the terminal
// directly; on release the calls replay. The reply being written keeps
// streaming the whole time, so the hold costs the model nothing, and the
// user reads the menu without the transcript scrolling under it.
type heldOutput struct {
	inner coder.Output

	mu    sync.Mutex
	held  bool
	queue []func()
}

func (h *heldOutput) do(f func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held {
		h.queue = append(h.queue, f)
		return
	}
	f()
}

func (h *heldOutput) hold() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held = true
}

// release replays what was held and lets output through again. The lock is
// kept for the replay, so nothing the coder writes meanwhile can overtake
// it.
func (h *heldOutput) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, f := range h.queue {
		f()
	}
	h.queue = nil
	h.held = false
}

func (h *heldOutput) Printf(format string, args ...any) {
	args = slices.Clone(args)
	h.do(func() { h.inner.Printf(format, args...) })
}

func (h *heldOutput) Warningf(format string, args ...any) {
	args = slices.Clone(args)
	h.do(func() { h.inner.Warningf(format, args...) })
}

func (h *heldOutput) Errorf(format string, args ...any) {
	args = slices.Clone(args)
	h.do(func() { h.inner.Errorf(format, args...) })
}

func (h *heldOutput) Toolf(format string, args ...any) {
	args = slices.Clone(args)
	h.do(func() { h.inner.Toolf(format, args...) })
}

func (h *heldOutput) ToolBlock(title, body string) { h.do(func() { h.inner.ToolBlock(title, body) }) }
func (h *heldOutput) Link(target string)           { h.do(func() { h.inner.Link(target) }) }
func (h *heldOutput) CommandOutput(text string)    { h.do(func() { h.inner.CommandOutput(text) }) }
func (h *heldOutput) StreamText(delta string)      { h.do(func() { h.inner.StreamText(delta) }) }
func (h *heldOutput) StreamReasoning(delta string) { h.do(func() { h.inner.StreamReasoning(delta) }) }
func (h *heldOutput) FlushStream()                 { h.do(h.inner.FlushStream) }

func (h *heldOutput) StreamToolCall(index int, name, argsFragment string) {
	h.do(func() { h.inner.StreamToolCall(index, name, argsFragment) })
}
