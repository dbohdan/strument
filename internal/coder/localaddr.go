package coder

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
)

// Local addresses under webfetch.
//
// A fetch the user approved at a prompt goes where the prompt said. The
// others — an origin approved earlier with "a", `--yes webfetch`, a redirect
// followed on the strength of either — were approved without anyone seeing
// the address they connect to, and those are the ones a page's injected
// instruction can steer: at the metadata service on 169.254.169.254, a
// router's admin page, a dev server with no authentication. So an unseen
// approval does not reach a local address. It falls back to asking, and the
// question names the address; webfetch_allow names the ones that are fine.
//
// maki (4cefcff) blocks the same ranges and pins each request to the address
// its guard vetted, because curl resolves the name again on its own and a
// TTL-0 answer can change in between. Go needs no pin: the check sits in the
// dialer's Control hook, which sees the address actually being connected to.

// localAddressError is a connection to a local address that was not approved.
type localAddressError struct {
	Origin string // host:port as the URL named it
	Addr   string // the address it resolved to
}

func (e *localAddressError) Error() string {
	return fmt.Sprintf("%s connects to %s, a local address", e.Origin, e.Addr)
}

// isLocalAddr reports whether an address is one a fetch should not reach
// unseen: loopback, private, link-local (the cloud metadata services live
// there), shared carrier-grade NAT (Alibaba Cloud's metadata service is on
// 100.100.100.200), unique-local IPv6, and the unspecified address, which
// reaches the local host.
func isLocalAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() || a.IsUnspecified() ||
		cgnat.Contains(a)
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// namesLocalHost reports whether a URL's host is itself local as written: an
// address literal in the local ranges, or localhost. An origin like that was
// seen as local by whoever approved it, which a name that resolves locally
// was not.
func namesLocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	a, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && isLocalAddr(a)
}

// guardedTransport returns a transport that refuses to connect to a local
// address unless localOK approves the origin, checked twice:
//
//   - before the request, by resolving the URL's host here. That is the only
//     check that works through a proxy, which resolves the name itself; it
//     is not proof against a name that changes its answer, and the next one
//     is.
//   - at connect time, in the dialer's Control hook, against the address
//     actually dialed.
//
// A connection to the proxy itself is never refused: the user configured
// it, and it is often on localhost.
func guardedTransport(base http.RoundTripper, localOK func(origin string) bool) (http.RoundTripper, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	t, ok := base.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("cannot check where a %T connects", base)
	}
	t = t.Clone()
	proxy := t.Proxy
	dialer := &net.Dialer{}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if proxy != nil && isProxyAddr(ctx, addr) {
			return dialer.DialContext(ctx, network, addr)
		}
		origin := strings.ToLower(addr)
		d := *dialer
		d.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				// Refused rather than waved through: an address this cannot
				// read is one it cannot vouch for.
				return fmt.Errorf("cannot tell where %s connects: %w", origin, err)
			}
			if isLocalAddr(ap.Addr()) && !localOK(origin) {
				return &localAddressError{Origin: origin, Addr: ap.Addr().Unmap().String()}
			}
			return nil
		}
		return d.DialContext(ctx, network, addr)
	}
	return &precheckTransport{base: t, localOK: localOK}, nil
}

// proxyAddrKey carries the proxy's host:port for one request, so the dialer
// can tell a connection to the proxy from one to the target.
type proxyAddrKey struct{}

func isProxyAddr(ctx context.Context, addr string) bool {
	p, _ := ctx.Value(proxyAddrKey{}).(string)
	return p != "" && strings.EqualFold(p, addr)
}

// precheckTransport resolves the request's host before sending it, and
// records the proxy the request will go through, if any.
type precheckTransport struct {
	base    *http.Transport
	localOK func(origin string) bool
}

func (p *precheckTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	origin := strings.ToLower(req.URL.Host)
	if req.URL.Port() == "" {
		port := "80"
		if req.URL.Scheme == "https" {
			port = "443"
		}
		origin = net.JoinHostPort(strings.ToLower(req.URL.Hostname()), port)
	}
	if !p.localOK(origin) {
		host := req.URL.Hostname()
		if a, err := netip.ParseAddr(host); err == nil {
			if isLocalAddr(a) {
				return nil, &localAddressError{Origin: origin, Addr: a.Unmap().String()}
			}
		} else if addrs, err := net.DefaultResolver.LookupNetIP(req.Context(), "ip", host); err == nil {
			// A failed lookup is not a verdict: behind a proxy the name may
			// only resolve on the far side. The dial check still stands.
			for _, a := range addrs {
				if isLocalAddr(a) {
					return nil, &localAddressError{Origin: origin, Addr: a.Unmap().String()}
				}
			}
		}
	}
	if p.base.Proxy != nil {
		if u, err := p.base.Proxy(req); err == nil && u != nil {
			port := u.Port()
			if port == "" {
				port = "80"
				if u.Scheme == "https" {
					port = "443"
				}
			}
			ctx := context.WithValue(req.Context(), proxyAddrKey{}, net.JoinHostPort(u.Hostname(), port))
			req = req.WithContext(ctx)
		}
	}
	return p.base.RoundTrip(req)
}
