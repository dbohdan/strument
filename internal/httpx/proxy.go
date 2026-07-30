// Package httpx builds HTTP transports for Strument's outbound calls. Its one
// job today is turning a proxy URL from config into an http.RoundTripper, kept
// in a leaf package so config (which validates the URL at load) and client
// (which dials through it) share one definition without an import cycle.
package httpx

import (
	"fmt"
	"net/http"
	"net/url"
)

// ProxyTransport builds a RoundTripper that dials through the given proxy URL.
// An empty string returns (nil, nil); callers treat a nil transport as "use the
// default". Only socks5:// and socks5h:// are supported for now — Go's net/http
// dials both natively and reads user:pass straight from the URL, so no extra
// dependency is needed. The transport is cloned from http.DefaultTransport so
// its TLS, timeout, and HTTP/2 defaults stand.
func ProxyTransport(raw string) (http.RoundTripper, error) {
	if raw == "" {
		return nil, nil //nolint:nilnil // No proxy: the caller uses the default transport.
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("proxy %q: %w", raw, err)
	}
	switch u.Scheme {
	case "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("proxy %q: unsupported scheme %q (only socks5:// and socks5h:// are supported)", raw, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy %q: missing host", raw)
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	t := base.Clone()
	t.Proxy = http.ProxyURL(u)
	return t, nil
}
