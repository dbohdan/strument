package coder

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"dbohdan.com/strument/internal/origin"
	"dbohdan.com/strument/internal/webpage"
)

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

func localFetchCoder(t *testing.T, confirm Confirmer) *Coder {
	t.Helper()
	c := testCoder(t)
	c.Scrape = webpage.NewSimple(nil, "")
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
