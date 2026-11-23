package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"dbohdan.com/strument/internal/llm"
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

// defaultStreamQuietTimeout bounds how long a stream may send nothing but
// keepalives: SSE comment lines, the ones starting with ":".
//
// The idle timeout cannot catch a provider that accepted the request and
// never started, because the gateway in front of it keeps the connection busy.
// OpenRouter sent ": OPENROUTER PROCESSING" for thirty minutes, three sessions
// running, for a model whose only provider had stopped answering, and every
// one of those lines reset the idle clock. Longer than the idle timeout,
// because a provider that does not stream its reasoning sends only keepalives
// while the model thinks, and a long think is minutes; ten minutes of nothing
// else is a provider that is not going to answer.
const defaultStreamQuietTimeout = 10 * time.Minute

// orDefault is d, or def when d is not set.
func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

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
	timeout time.Duration // any bytes reset it
	quiet   time.Duration // only bytes of a line that is not a comment reset it

	mu          sync.Mutex
	timer       *time.Timer
	quietTimer  *time.Timer
	fired       bool // the stream went silent
	firedQuiet  bool // the stream sent only keepalives
	atLineStart bool
}

// newIdleReader wraps body so that a gap longer than timeout, or keepalives
// alone for longer than quiet, closes it. Closing the body is what unblocks the
// read: cancelling the request context would also work, but the body is what
// this layer holds, and closing it needs no cooperation from the caller.
func newIdleReader(body io.ReadCloser, timeout, quiet time.Duration) *idleReader {
	ir := &idleReader{r: body, timeout: timeout, quiet: quiet, atLineStart: true}
	ir.timer = time.AfterFunc(timeout, func() { ir.stop(&ir.fired) })
	ir.quietTimer = time.AfterFunc(quiet, func() { ir.stop(&ir.firedQuiet) })
	return ir
}

func (ir *idleReader) stop(flag *bool) {
	ir.mu.Lock()
	*flag = true
	ir.mu.Unlock()
	// Unblocks the in-flight Read. The error it surfaces is replaced by
	// StallError at the call site, which knows what actually happened.
	_ = ir.r.Close()
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.mu.Lock()
		if !ir.fired && !ir.firedQuiet {
			ir.timer.Reset(ir.timeout)
			if ir.content(p[:n]) {
				ir.quietTimer.Reset(ir.quiet)
			}
		}
		ir.mu.Unlock()
	}
	return n, err
}

// content reports whether chunk starts a line that is not an SSE comment, and
// keeps track of where lines start across reads. A data or event line is the
// provider answering; a line starting with ":" is a keepalive.
func (ir *idleReader) content(chunk []byte) bool {
	seen := false
	for _, b := range chunk {
		if ir.atLineStart && b != ':' && b != '\n' && b != '\r' {
			seen = true
		}
		ir.atLineStart = b == '\n'
	}
	return seen
}

// Stalled reports whether the stream was closed by a timer rather than by the
// server or the caller. The distinction matters: "the provider stopped
// sending" and "the connection dropped" read the same at the io layer and mean
// different things to a user.
func (ir *idleReader) Stalled() bool {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	return ir.fired || ir.firedQuiet
}

// StallError says which timer closed the stream. It is a network error, so the
// retry path answers it as the transient it usually is.
func (ir *idleReader) StallError() *llm.StreamError {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	msg := fmt.Sprintf("the provider stopped sending for %s mid-response", ir.timeout)
	if ir.firedQuiet && !ir.fired {
		msg = fmt.Sprintf("the provider sent only keepalives for %s and no response", ir.quiet)
	}
	return &llm.StreamError{Class: llm.ErrNetwork, Message: msg}
}

func (ir *idleReader) Close() error {
	ir.mu.Lock()
	ir.timer.Stop()
	ir.quietTimer.Stop()
	ir.mu.Unlock()
	return ir.r.Close()
}

// contextError turns a cancelled or expired request context into a typed
// StreamError.
//
// Yielded bare, it made a call that died unreadable. Two things went wrong at
// once. The text was Go's — "context deadline exceeded", which names an
// implementation detail and no remedy. And because a
// bare context error is not a *llm.StreamError, errors.As failed on it in
// retryBackoff.retry, so the call took the non-retryable branch by accident
// rather than by decision.
//
// Non-retryable is nonetheless the right answer here, which is why this is a
// classification and not a repair: the context carries the whole call's budget,
// so once it is gone there is nothing left to retry into. What changes is that
// the decision is now made on purpose and says so. ErrRequest is the class for
// "this request will not work as asked", which is exactly the case.
//
// The original error is wrapped rather than replaced, so errors.Is still
// reaches context.DeadlineExceeded and context.Canceled for a caller that
// distinguishes them.
func contextError(err error) error {
	message := "the request was cancelled"
	if errors.Is(err, context.DeadlineExceeded) {
		message = "the request ran out of time"
	}
	return &llm.StreamError{Class: llm.ErrRequest, Message: message, Err: err}
}
