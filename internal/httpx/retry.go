package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// HandshakeTimeout bounds a TLS handshake for the requests that retry. The
// default transport allows 10 seconds, which was also approve_model's default
// deadline, so one stuck handshake spent the whole budget and the user was
// asked: "did not answer (... net/http: TLS handshake timeout)". A handshake
// that stalls rarely stalls twice, so a short one leaves time to try again.
const HandshakeTimeout = 3 * time.Second

// QuickHandshake is transport with HandshakeTimeout, or the default transport
// with it when transport is nil. A transport of another kind is used as it is.
func QuickHandshake(transport http.RoundTripper) http.RoundTripper {
	if transport == nil {
		transport = http.DefaultTransport
	}
	t, ok := transport.(*http.Transport)
	if !ok {
		return transport
	}
	t = t.Clone()
	t.TLSHandshakeTimeout = HandshakeTimeout
	return t
}

// RetryDelays are the waits between attempts. The caller's deadline, not the
// count, is what usually ends the trying.
var RetryDelays = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second}

// Retry makes a request until it gets an answer worth reading, the context
// ends, or RetryDelays run out, and returns the last result.
//
// A failure that a second try can fix is tried again: a timeout (a stalled TLS
// handshake is one), a dropped or refused connection, a temporary DNS
// failure, and a response saying to come back (429, 502, 503, 504). Every
// other error and every other response is returned at once, the refusals a
// caller makes on purpose included: a redirect it would not follow and a local
// address it would not dial are errors from the same call. release disposes of
// a response that is not returned, such as by closing its body; it may be nil.
//
// Only requests that are safe to send twice should go through here: a
// question, a search, a fetch.
func Retry[T any](ctx context.Context, do func() (T, int, error), release func(T)) (T, error) {
	for attempt := 1; ; attempt++ {
		v, status, err := do()
		again := err == nil && RetryStatus(status) || err != nil && RetryError(err)
		if !again {
			if err != nil && attempt > 1 {
				err = fmt.Errorf("%d attempts, the last: %w", attempt, err)
			}
			return v, err
		}
		last := ctx.Err() != nil || attempt > len(RetryDelays)
		if !last {
			select {
			case <-ctx.Done():
				last = true
			case <-time.After(RetryDelays[attempt-1]):
			}
		}
		if last {
			if err != nil && attempt > 1 {
				err = fmt.Errorf("%d attempts, the last: %w", attempt, err)
			}
			// A come-back-later response is returned for the caller to
			// report in the service's own words.
			return v, err
		}
		if err == nil && release != nil {
			release(v)
		}
	}
}

// RetryStatus reports whether a response says to try again.
func RetryStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// RetryError reports whether a request that got no response might get one if
// sent again. The caller's own context ending is not such a failure.
func RetryError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return dns.IsTimeout || dns.IsTemporary
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNABORTED)
}

// PostRetrying is Post through Retry, every attempt within budget when it is
// positive; with none, the context's deadline is the only one. The client
// should carry no Timeout of its own, which would bound each attempt rather
// than the whole.
func PostRetrying(ctx context.Context, budget time.Duration, client *http.Client, url, contentType string,
	body []byte, userAgent string, header http.Header, limit int64,
) (Reply, error) {
	if budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}
	return Retry(ctx, func() (Reply, int, error) {
		r, err := Post(ctx, client, url, contentType, body, userAgent, header, limit)
		return r, r.Status, err
	}, nil)
}
