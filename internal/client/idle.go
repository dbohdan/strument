package client

import (
	"io"
	"sync"
	"time"
)

// defaultStreamIdleTimeout bounds how long a started stream may go silent.
//
// Not a total deadline: a long generation is legitimate and http.Client.Timeout
// would kill it. Not ResponseHeaderTimeout either: the headers have already
// arrived by the time this matters. What is being bounded is the gap *between*
// bytes, which is the shape of the failure it exists for.
//
// Generous on purpose. Providers emit keepalives during long waits (OpenRouter
// sends ": OPENROUTER PROCESSING" comment lines), and those are bytes, so they
// reset the clock. A gap this long is a stall, not slow thinking.
const defaultStreamIdleTimeout = 3 * time.Minute

// idleReader fails a read stream that stops producing bytes.
//
// The bug it closes: the chat send built its request with no deadline of any
// kind — http.Client{Transport: …} with no Timeout, a bare bufio scan over the
// body, and a context wrapped in WithCancel rather than WithTimeout. Every
// auxiliary call (commit message, notes, summary, shell) had a timeout; the
// primary one did not. A provider that sent headers, began streaming and then
// went silent without closing the socket therefore hung the send forever.
// Interactively that is a Ctrl-C; in an unattended run it is a wedged process,
// which is what ten mid-stream deaths in the 2026-08 code-mode trial were, and
// what a hang under trial load looked like to the operator.
//
// Reported as a network error so the existing retry path handles it, because a
// stalled stream is exactly the transient it already knows how to answer.
type idleReader struct {
	r       io.ReadCloser
	timeout time.Duration

	mu    sync.Mutex
	timer *time.Timer
	fired bool
}

// newIdleReader wraps body so that a gap longer than timeout closes it. Closing
// the body is what unblocks the read: cancelling the request context would also
// work, but the body is what this layer holds, and closing it needs no
// cooperation from the caller.
func newIdleReader(body io.ReadCloser, timeout time.Duration) *idleReader {
	ir := &idleReader{r: body, timeout: timeout}
	ir.timer = time.AfterFunc(timeout, ir.onIdle)
	return ir
}

func (ir *idleReader) onIdle() {
	ir.mu.Lock()
	ir.fired = true
	ir.mu.Unlock()
	// Unblocks the in-flight Read. The error it surfaces is replaced by
	// Stalled() at the call site, which knows what actually happened.
	_ = ir.r.Close()
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.mu.Lock()
		if !ir.fired {
			ir.timer.Reset(ir.timeout)
		}
		ir.mu.Unlock()
	}
	return n, err
}

// Stalled reports whether the stream was closed by the idle timer rather than
// by the server or the caller. The distinction matters: "the provider stopped
// sending" and "the connection dropped" read the same at the io layer and mean
// different things to a user.
func (ir *idleReader) Stalled() bool {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	return ir.fired
}

func (ir *idleReader) Close() error {
	ir.mu.Lock()
	ir.timer.Stop()
	ir.mu.Unlock()
	return ir.r.Close()
}
