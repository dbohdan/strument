package allowance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func answering(status int, body string, seen *http.Request) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if seen != nil {
			*seen = *r
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
}

const goAnswer = `{"usage":{
	"rolling":{"status":"ok","percent":23.4,"resetsAt":"2026-11-19T23:12:00Z"},
	"weekly":{"status":"ok","percent":41,"resetsAt":1795651200000},
	"monthly":{"status":"ok","percent":12.5,"resetsAt":"1796256000"}}}`

func TestFetchReadsTheAllowance(t *testing.T) {
	var seen http.Request
	r, err := Fetch(context.Background(), answering(200, goAnswer, &seen), "https://x/zen/go/v1/usage", "k", "strument/test")
	if err != nil {
		t.Fatal(err)
	}
	if seen.Header.Get("Authorization") != "Bearer k" || seen.Header.Get("User-Agent") != "strument/test" {
		t.Errorf("request headers: %v", seen.Header)
	}
	if r.Rolling.Percent != 23.4 || r.Rolling.Limited || !r.Rolling.ResetsAt.Equal(time.Date(2026, 11, 19, 23, 12, 0, 0, time.UTC)) {
		t.Errorf("rolling = %+v", r.Rolling)
	}
	if !r.Weekly.ResetsAt.Equal(time.UnixMilli(1795651200000)) || !r.Monthly.ResetsAt.Equal(time.Unix(1796256000, 0)) {
		t.Errorf("epoch reset times: weekly %v, monthly %v", r.Weekly.ResetsAt, r.Monthly.ResetsAt)
	}
}

// A refusal or an answer of another shape means there is no allowance, for
// good; a server error or a busy answer is worth asking again next turn.
func TestFetchTellsNoneFromNotNow(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		none   bool
	}{
		{403, `{"error":"not subscribed"}`, true},
		{404, `not found`, true},
		{401, ``, true},
		{200, `{"data":{}}`, true},
		{200, `<html>`, true},
		{503, ``, false},
		{429, ``, false},
	} {
		_, err := Fetch(context.Background(), answering(tc.status, tc.body, nil), "https://x/usage", "k", "ua")
		if err == nil || errors.Is(err, ErrNone) != tc.none {
			t.Errorf("HTTP %d %q: err = %v, want none=%v", tc.status, tc.body, err, tc.none)
		}
	}
}

func TestLine(t *testing.T) {
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	win := func(p float64, in time.Duration) Window {
		return Window{Percent: p, ResetsAt: now.Add(in), Known: true}
	}
	for _, tc := range []struct {
		name string
		cur  Reading
		want string
	}{
		{"whole percents", Reading{Rolling: win(9, 2*time.Hour+37*time.Minute)}, "5h 9% used, resets in 2h 37m"},
		{"a fraction", Reading{Rolling: win(23.4, 40*time.Second)}, "5h 23.4% used, resets in 1m"},
		{"long windows nearly spent", Reading{Rolling: win(10, time.Hour), Weekly: win(80, 50*time.Hour), Monthly: win(30, 0)},
			"5h 10% used, resets in 1h 0m; week 80% used"},
		{"limited", Reading{Rolling: Window{Percent: 100, Limited: true, ResetsAt: now.Add(62 * time.Minute), Known: true},
			Monthly: Window{Percent: 100, Limited: true, ResetsAt: now.Add(9 * 24 * time.Hour), Known: true}},
			"5h limit reached, resets in 1h 2m; month limit reached, resets in 9d 0h"},
		{"no reset time", Reading{Rolling: Window{Percent: 5, Known: true}}, "5h 5% used"},
	} {
		if got := Line(tc.cur, now); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}
