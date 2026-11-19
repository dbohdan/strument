// Package allowance reads opencode Go's subscription meter and words it for
// the turn's usage line.
//
// Go limits spending in three dollar-denominated windows (5 hours, a week, a
// month) and serves them as whole percentages at <base_url>/usage, which a
// plain API key may read. What a request spent comes with the request
// (llm.OpencodeBilling); this is the other half, how much of each window is
// left and when it comes back.
package allowance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Window is one of the allowance's meters.
type Window struct {
	Percent  float64 // of the window's limit used, 0-100
	Limited  bool    // the window is exhausted and requests are refused
	ResetsAt time.Time
	Known    bool // the response carried this window
}

// Reading is the allowance at one moment.
type Reading struct {
	Rolling, Weekly, Monthly Window
}

// ErrNone means the endpoint has no allowance to read: it is not Go, or the
// key is not on a Go subscription. It is final for the session, unlike a
// timeout, which the next turn tries again.
var ErrNone = errors.New("no Go allowance at this endpoint")

// Fetch reads the allowance from url with key. An answer that is not the
// allowance's shape, or a refusal, is ErrNone.
func Fetch(ctx context.Context, hc *http.Client, url, key, userAgent string) (Reading, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Reading{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return Reading{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return Reading{}, fmt.Errorf("usage endpoint: HTTP %d", resp.StatusCode)
	default:
		return Reading{}, fmt.Errorf("%w: HTTP %d", ErrNone, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return Reading{}, err
	}
	return Parse(body)
}

// Parse reads the endpoint's answer: {"usage": {"rolling": {"status",
// "percent", "resetsAt"}, "weekly": …, "monthly": …}}. Without the rolling
// window it is not an answer this package can use.
func Parse(body []byte) (Reading, error) {
	var doc struct {
		Usage map[string]struct {
			Status   string          `json:"status"`
			Percent  *float64        `json:"percent"`
			ResetsAt json.RawMessage `json:"resetsAt"` //nolint:tagliatelle // the endpoint's own name
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return Reading{}, fmt.Errorf("%w: %w", ErrNone, err)
	}
	window := func(name string) Window {
		w, ok := doc.Usage[name]
		if !ok || w.Percent == nil {
			return Window{}
		}
		return Window{
			Percent:  min(max(*w.Percent, 0), 100),
			Limited:  w.Status != "" && w.Status != "ok",
			ResetsAt: parseTime(w.ResetsAt),
			Known:    true,
		}
	}
	r := Reading{Rolling: window("rolling"), Weekly: window("weekly"), Monthly: window("monthly")}
	if !r.Rolling.Known {
		return Reading{}, fmt.Errorf("%w: no rolling window in the answer", ErrNone)
	}
	return r, nil
}

// parseTime reads a reset time as an ISO 8601 string or as epoch seconds or
// milliseconds. Unreadable is the zero time, which the line leaves out.
func parseTime(raw json.RawMessage) time.Time {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return epoch(n)
		}
		return time.Time{}
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		return epoch(n)
	}
	return time.Time{}
}

func epoch(n float64) time.Time {
	if n > 1e12 {
		return time.UnixMilli(int64(n))
	}
	return time.Unix(int64(n), 0)
}

// longWindowShown is where the week and the month join the line: below it the
// five-hour window binds first and is the only one worth a reader's glance.
const longWindowShown = 75

// Line words a reading for the usage line: "5h 23% used, resets in 3h 12m",
// with the week and the month after it when they are nearly spent or
// exhausted. What a turn took is not read from here: the meter moves in whole
// percents, which a turn rarely fills, and each response says what it cost the
// allowance to the cent.
func Line(cur Reading, now time.Time) string {
	var b strings.Builder
	r := cur.Rolling
	if r.Limited {
		b.WriteString("5h limit reached")
	} else {
		b.WriteString("5h " + percent(r.Percent) + " used")
	}
	if in := until(r.ResetsAt, now); in != "" {
		b.WriteString(", resets in " + in)
	}
	for _, w := range []struct {
		name string
		w    Window
	}{{"week", cur.Weekly}, {"month", cur.Monthly}} {
		switch {
		case !w.w.Known:
		case w.w.Limited:
			b.WriteString("; " + w.name + " limit reached")
			if in := until(w.w.ResetsAt, now); in != "" {
				b.WriteString(", resets in " + in)
			}
		case w.w.Percent >= longWindowShown:
			b.WriteString("; " + w.name + " " + percent(w.w.Percent) + " used")
		}
	}
	return b.String()
}

// percent shows a share to one decimal, without a trailing ".0".
func percent(p float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(math.Round(p*10)/10, 'f', 1, 64), ".0") + "%"
}

// until is the time left to t as the largest two units, or "" when t is
// unknown or past.
func until(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := t.Sub(now)
	if d <= 0 {
		return ""
	}
	days, hours, minutes := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", max(minutes, 1))
	}
}
