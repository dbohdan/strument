package httpx

import (
	"net/http"
	"testing"
)

func TestProxyTransportEmpty(t *testing.T) {
	rt, err := ProxyTransport("")
	if err != nil {
		t.Fatalf("empty proxy: unexpected error %v", err)
	}
	if rt != nil {
		t.Errorf("empty proxy: want nil transport, got %T", rt)
	}
}

func TestProxyTransportSOCKS5(t *testing.T) {
	// A socks5 URL with credentials: the built transport must resolve to that
	// exact proxy URL, userinfo intact (net/http reads user:pass from it).
	const raw = "socks5://user:pass@127.0.0.1:1080"
	rt, err := ProxyTransport(raw)
	if err != nil {
		t.Fatalf("ProxyTransport(%q): %v", raw, err)
	}
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("want *http.Transport, got %T", rt)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	pu, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("transport.Proxy: %v", err)
	}
	if pu == nil || pu.String() != raw {
		t.Fatalf("proxy URL = %v, want %q", pu, raw)
	}
	if pu.User.Username() != "user" {
		t.Errorf("username = %q, want %q", pu.User.Username(), "user")
	}
	if pw, _ := pu.User.Password(); pw != "pass" {
		t.Errorf("password = %q, want %q", pw, "pass")
	}
}

func TestProxyTransportSOCKS5h(t *testing.T) {
	if _, err := ProxyTransport("socks5h://proxy.internal:1080"); err != nil {
		t.Errorf("socks5h should be accepted: %v", err)
	}
}

func TestProxyTransportRejects(t *testing.T) {
	cases := map[string]string{
		"http scheme":  "http://proxy:8080",
		"https scheme": "https://proxy:8080",
		"no scheme":    "://noscheme:1080",
		"missing host": "socks5://",
		"bad escape":   "socks5://%zz",
	}
	for name, raw := range cases {
		if _, err := ProxyTransport(raw); err == nil {
			t.Errorf("%s: ProxyTransport(%q) = nil error, want error", name, raw)
		}
	}
}
