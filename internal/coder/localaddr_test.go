package coder

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
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
		if got := namesLocalHost(host); got != want {
			t.Errorf("namesLocalHost(%q) = %v, want %v", host, got, want)
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
	scrape := NewSimpleScraper(nil, "")

	_, err := scrape(context.Background(), srv.URL, ScrapeOptions{LocalOK: func(string) bool { return false }})
	var local *localAddressError
	if !errors.As(err, &local) || local.Addr != "127.0.0.1" {
		t.Fatalf("err = %v, want a localAddressError for 127.0.0.1", err)
	}
	if hits.Load() != 0 {
		t.Error("the refused request reached the server")
	}
	for name, ok := range map[string]func(string) bool{
		"cleared": func(string) bool { return true },
		"no rule": nil,
	} {
		if _, err := scrape(context.Background(), srv.URL, ScrapeOptions{LocalOK: ok}); err != nil {
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
	var local *localAddressError
	if !errors.As(err, &local) {
		t.Fatalf("err = %v, want the dialer's localAddressError", err)
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

func localFetchCoder(t *testing.T, confirm Confirmer) *Coder {
	t.Helper()
	c := testCoder(t)
	c.Scrape = NewSimpleScraper(nil, "")
	c.Confirm = confirm
	return c
}

func webfetchGranted(t *testing.T, fallback Confirmer) Confirmer {
	t.Helper()
	granted, err := ParseGrants([]string{"webfetch"})
	if err != nil {
		t.Fatal(err)
	}
	return AutoConfirmer{Granted: StaticGrants(granted), Fallback: fallback}
}

// --yes webfetch approves fetches no one sees, so it does not reach a local
// address: it asks, and with no one to ask it says how to allow it.
func TestWebfetchGrantDoesNotReachALocalAddress(t *testing.T) {
	srv, hits := localServer(t)
	c := localFetchCoder(t, webfetchGranted(t, unattendedConfirmer{}))

	out := runFetch(t, c, fetchCall(srv.URL+"/admin", "read it"))
	if hits.Load() != 0 {
		t.Fatalf("the granted fetch reached the local server:\n%s", out)
	}
	if !strings.Contains(out, "127.0.0.1, a local address") || !strings.Contains(out, "webfetch_allow") {
		t.Errorf("the model should hear why and how to allow it:\n%s", out)
	}
}

// Asked the second time, with the address in the question, and no --yes
// can answer that question.
func TestWebfetchGrantAsksAboutALocalAddress(t *testing.T) {
	srv, hits := localServer(t)
	person := &recordingConfirmer{answer: true}
	c := localFetchCoder(t, webfetchGranted(t, person))

	out := runFetch(t, c, fetchCall(srv.URL+"/", "read it"))
	if hits.Load() != 1 || !strings.Contains(out, "the local page") {
		t.Fatalf("approved at the second prompt, the fetch should go ahead:\n%s", out)
	}
	if len(person.got) != 1 || !strings.Contains(person.got[0].Prompt, "127.0.0.1") || person.got[0].Grant != "" {
		t.Errorf("the one prompt should name the address and carry no grant: %+v", person.got)
	}
}

func TestWebfetchLocalAddressAllowedWhenSeen(t *testing.T) {
	srv, _ := localServer(t)
	org, _ := origin.Of(srv.URL)

	t.Run("listed in webfetch_allow", func(t *testing.T) {
		c := localFetchCoder(t, webfetchGranted(t, unattendedConfirmer{}))
		c.WebfetchAllow = []string{org}
		if out := runFetch(t, c, fetchCall(srv.URL+"/", "read it")); !strings.Contains(out, "the local page") {
			t.Errorf("an allowlisted local origin was not fetched:\n%s", out)
		}
	})
	t.Run("approved at a prompt", func(t *testing.T) {
		person := &recordingConfirmer{answer: true}
		c := localFetchCoder(t, person)
		if out := runFetch(t, c, fetchCall(srv.URL+"/", "read it")); !strings.Contains(out, "the local page") {
			t.Errorf("a fetch a person approved was not made:\n%s", out)
		}
		if len(person.got) != 1 {
			t.Errorf("%d prompts, want the one", len(person.got))
		}
	})
	t.Run("session grant for an origin written as local", func(t *testing.T) {
		c := localFetchCoder(t, unattendedConfirmer{})
		c.sessionAutoApprove["webfetch:"+org] = true
		if out := runFetch(t, c, fetchCall(srv.URL+"/", "read it")); !strings.Contains(out, "the local page") {
			t.Errorf("an origin approved as 127.0.0.1 is local as seen:\n%s", out)
		}
	})
}
