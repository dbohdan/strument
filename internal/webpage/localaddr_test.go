package webpage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"

	"dbohdan.com/strument/internal/origin"
)

func TestIsLocalAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1":              true,
		"::1":                    true,
		"10.1.2.3":               true,
		"172.16.0.1":             true,
		"192.168.1.1":            true,
		"169.254.169.254":        true, // AWS, GCP and Azure metadata
		"100.100.100.200":        true, // Alibaba Cloud metadata, in CGNAT space
		"0.0.0.0":                true,
		"fd00::1":                true,
		"fe80::1":                true,
		"::ffff:169.254.169.254": true,
		"8.8.8.8":                false,
		"2606:4700::1111":        false,
		"100.128.0.1":            false, // just past CGNAT
	} {
		if got := isLocalAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("isLocalAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestNamesLocalHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "app.localhost": true, "127.0.0.1": true, "[::1]": true,
		"10.0.0.5": true, "example.com": false, "localhost.example.com": false,
	} {
		if got := NamesLocalHost(host); got != want {
			t.Errorf("NamesLocalHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// localServer is a page on a loopback address and a count of its hits.
func localServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	hits := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("the local page\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

func TestScraperRefusesALocalAddressItWasNotCleared(t *testing.T) {
	srv, hits := localServer(t)
	scrape := NewSimple(nil, "")

	_, err := scrape(context.Background(), srv.URL, Options{LocalOK: func(string) bool { return false }})
	var local *LocalAddressError
	if !errors.As(err, &local) || local.Addr != "127.0.0.1" {
		t.Fatalf("err = %v, want a LocalAddressError for 127.0.0.1", err)
	}
	if hits.Load() != 0 {
		t.Error("the refused request reached the server")
	}
	for name, ok := range map[string]func(string) bool{
		"cleared": func(string) bool { return true },
		"no rule": nil,
	} {
		if _, err := scrape(context.Background(), srv.URL, Options{LocalOK: ok}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The dial check is the one that holds when a name changes its answer
// between a lookup and the connection. Here the precheck is bypassed by
// clearing the origin for it only, so what refuses is the dialer.
func TestTheDialerRefusesWhatThePrecheckPassed(t *testing.T) {
	srv, hits := localServer(t)
	org, _ := origin.Of(srv.URL)
	calls := 0
	rt, err := guardedTransport(nil, func(o string) bool {
		calls++
		return calls == 1 && o == org // the precheck asks first
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: rt}).Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
	}
	var local *LocalAddressError
	if !errors.As(err, &local) {
		t.Fatalf("err = %v, want the dialer's LocalAddressError", err)
	}
	if hits.Load() != 0 {
		t.Error("the refused connection reached the server")
	}
}

// A proxy on localhost is the user's own configuration, and every request
// goes through it; refusing it would refuse everything.
func TestAProxyOnLocalhostIsNotRefused(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("via the proxy\n"))
	}))
	t.Cleanup(proxy.Close)
	u, _ := url.Parse(proxy.URL)
	def, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("the default transport is not an *http.Transport")
	}
	base := def.Clone()
	base.Proxy = http.ProxyURL(u)

	rt, err := guardedTransport(base, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: rt}).Get("http://example.invalid/page")
	if err != nil {
		t.Fatalf("a request through a local proxy was refused: %v", err)
	}
	_ = resp.Body.Close()
}
