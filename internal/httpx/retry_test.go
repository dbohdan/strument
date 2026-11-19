package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func quickRetries(t *testing.T) {
	t.Helper()
	saved := RetryDelays
	RetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { RetryDelays = saved })
}

// timeoutError is what a stalled TLS handshake returns: a net.Error that says
// it timed out.
type timeoutError struct{}

func (timeoutError) Error() string   { return "net/http: TLS handshake timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func wrapped(err error) error {
	return &url.Error{Op: "Post", URL: "https://example.com", Err: err}
}

// The failures a second try can fix are retried; the rest, a caller's own
// refusals among them, are not.
func TestRetryErrorClassifies(t *testing.T) {
	refusal := errors.New("redirected to https://elsewhere.example, a different origin")
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{wrapped(timeoutError{}), true},
		{wrapped(io.EOF), true},
		{wrapped(io.ErrUnexpectedEOF), true},
		{wrapped(&net.OpError{Op: "read", Err: syscall.ECONNRESET}), true},
		{wrapped(&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}), true},
		{wrapped(&net.DNSError{Err: "server misbehaving", IsTemporary: true}), true},
		{wrapped(&net.DNSError{Err: "no such host", IsNotFound: true}), false},
		{wrapped(context.DeadlineExceeded), false},
		{wrapped(context.Canceled), false},
		{wrapped(refusal), false},
		{errors.New("x509: certificate signed by unknown authority"), false},
	} {
		if got := RetryError(tc.err); got != tc.want {
			t.Errorf("RetryError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// Retry tries a transient failure again, returns anything else at once, and
// says how many attempts it took when it gives up.
func TestRetry(t *testing.T) {
	quickRetries(t)
	ctx := context.Background()

	n := 0
	v, err := Retry(ctx, func() (string, int, error) {
		n++
		if n < 3 {
			return "", 0, wrapped(timeoutError{})
		}
		return "ok", 200, nil
	}, nil)
	if v != "ok" || err != nil || n != 3 {
		t.Errorf("transient then fine: v=%q err=%v n=%d", v, err, n)
	}

	n = 0
	_, err = Retry(ctx, func() (string, int, error) { n++; return "", 0, wrapped(timeoutError{}) }, nil)
	if n != len(RetryDelays)+1 || !strings.Contains(fmt.Sprint(err), "5 attempts, the last: ") ||
		!errors.As(err, new(net.Error)) {
		t.Errorf("always timing out: err=%v n=%d", err, n)
	}

	// A failure that is not transient ends the trying, and still counts.
	n = 0
	refusal := errors.New("refused")
	_, err = Retry(ctx, func() (string, int, error) {
		n++
		if n == 1 {
			return "", 0, wrapped(io.EOF)
		}
		return "", 0, refusal
	}, nil)
	if !errors.Is(err, refusal) || n != 2 || !strings.Contains(err.Error(), "2 attempts") {
		t.Errorf("transient then refused: err=%v n=%d", err, n)
	}

	// A response saying to come back is released and asked again; still
	// saying it at the end, it is returned for the caller to report.
	n, released := 0, 0
	v, err = Retry(ctx, func() (string, int, error) { n++; return "busy", http.StatusServiceUnavailable, nil },
		func(string) { released++ })
	if v != "busy" || err != nil || n != len(RetryDelays)+1 || released != len(RetryDelays) {
		t.Errorf("always 503: v=%q err=%v n=%d released=%d", v, err, n, released)
	}

	// Any other status is an answer.
	n = 0
	_, _ = Retry(ctx, func() (string, int, error) { n++; return "", http.StatusNotFound, nil }, nil)
	if n != 1 {
		t.Errorf("404 was asked %d times", n)
	}

	// The caller's deadline ends the trying.
	cctx, cancel := context.WithCancel(ctx)
	n = 0
	_, err = Retry(cctx, func() (string, int, error) { n++; cancel(); return "", 0, wrapped(timeoutError{}) }, nil)
	if n != 1 || err == nil {
		t.Errorf("cancelled: err=%v n=%d", err, n)
	}
}

func TestQuickHandshake(t *testing.T) {
	for _, in := range []http.RoundTripper{nil, http.DefaultTransport, &http.Transport{}} {
		got, ok := QuickHandshake(in).(*http.Transport)
		if !ok || got.TLSHandshakeTimeout != HandshakeTimeout {
			t.Errorf("QuickHandshake(%T) = %+v", in, got)
		}
	}
	if d, ok := http.DefaultTransport.(*http.Transport); !ok || d.TLSHandshakeTimeout == HandshakeTimeout {
		t.Error("the default transport was changed, not cloned")
	}
}
