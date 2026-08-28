// The webfetch tool: what it asks, when it does not, and what it says back.

package coder

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

// recordingConfirmer (tools_test.go) is reused rather than reimplemented: it
// already captures the request, which is what these tests assert on.

func fetchCoder(t *testing.T, yes bool) (*Coder, *recordingConfirmer, *int) {
	t.Helper()
	c := testCoder(t)
	cf := &recordingConfirmer{answer: yes}
	c.Confirm = cf
	fetches := 0
	c.Scrape = func(_ context.Context, url string) (string, error) {
		fetches++
		return "Here is the content of " + url + ":\n\nthe page", nil
	}
	return c, cf, &fetches
}

func fetchCall(url, purpose string) llm.ToolCall {
	return llm.ToolCall{
		ID:        "call_1",
		Name:      toolWebfetch,
		Arguments: `{"url":` + jsonString(url) + `,"purpose":` + jsonString(purpose) + `}`,
	}
}

func jsonString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func runFetch(t *testing.T, c *Coder, tc llm.ToolCall) string {
	t.Helper()
	f, msg := parseFetchArgs(tc)
	if msg != "" {
		return msg
	}
	return c.runWebfetch(context.Background(), f)
}

// The prompt carries the whole URL and the origin the answer would cover.
// Scoping an "all" answer to something the prompt never printed is asking the
// user to agree to a scope they cannot see.
func TestWebfetchPromptShowsURLAndOrigin(t *testing.T) {
	c, cf, fetches := fetchCoder(t, true)

	out := runFetch(t, c, fetchCall("https://go.dev/doc/go1.26?x=1", "check the loop change"))

	if len(cf.got) != 1 {
		t.Fatalf("asked %d times, want 1", len(cf.got))
	}
	req := cf.got[0]
	if req.URL != "https://go.dev/doc/go1.26?x=1" {
		t.Errorf("URL = %q — the query string must survive to the prompt", req.URL)
	}
	if req.Origin != "go.dev:443" {
		t.Errorf("Origin = %q, want go.dev:443", req.Origin)
	}
	if req.Purpose != "check the loop change" {
		t.Errorf("Purpose = %q", req.Purpose)
	}
	if !req.RequiresYesShell {
		t.Error("a fetch of an unlisted origin must not be answerable by plain --yes")
	}
	if req.Group != "webfetch:go.dev:443" {
		t.Errorf("Group = %q — the turn-scoped answer must be scoped to the origin", req.Group)
	}
	if *fetches != 1 || !strings.Contains(out, "the page") {
		t.Errorf("fetches = %d, out = %q", *fetches, out)
	}
}

// Declining is not failing, and the model has to be able to tell them apart:
// one means try something else, the other means the user said no.
func TestWebfetchDeclinedSaysSo(t *testing.T) {
	c, _, fetches := fetchCoder(t, false)

	out := runFetch(t, c, fetchCall("https://go.dev/doc", "read the docs"))

	if *fetches != 0 {
		t.Error("a declined fetch still fetched")
	}
	if !strings.Contains(out, "chose not to") {
		t.Errorf("result = %q, want it to say the user declined", out)
	}
}

func TestWebfetchFailureSaysWhy(t *testing.T) {
	c, _, _ := fetchCoder(t, true)
	c.Scrape = func(context.Context, string) (string, error) {
		return "", errors.New("HTTP 404")
	}

	out := runFetch(t, c, fetchCall("https://go.dev/nope", "read the docs"))

	if !strings.Contains(out, "404") {
		t.Errorf("result = %q, want the reason so the model can try another URL", out)
	}
	if strings.Contains(out, "chose not to") {
		t.Error("a failed fetch was reported as a refusal")
	}
}

// An allowlisted origin is not asked about at all — which is also why it needs
// no --yes flag: the flag question only arises for an origin never named.
func TestWebfetchAllowlistSkipsThePrompt(t *testing.T) {
	c, cf, fetches := fetchCoder(t, false)
	c.WebfetchAllow = []string{"go.dev", "localhost:3000"}

	for _, u := range []string{"https://go.dev/doc/go1.26", "http://localhost:3000/api"} {
		if out := runFetch(t, c, fetchCall(u, "read it")); !strings.Contains(out, "the page") {
			t.Errorf("fetch of an allowed origin %s did not happen: %q", u, out)
		}
	}
	if len(cf.got) != 0 {
		t.Errorf("asked %d times about allowed origins, want 0", len(cf.got))
	}
	if *fetches != 2 {
		t.Errorf("fetches = %d, want 2", *fetches)
	}

	// The counter-half: a neighbouring port on the same host is a different
	// place and is still asked about.
	if out := runFetch(t, c, fetchCall("http://localhost:8080/api", "read it")); !strings.Contains(out, "chose not to") {
		t.Errorf("localhost:8080 was not confirmed: %q", out)
	}
	if len(cf.got) != 1 {
		t.Errorf("asked %d times about the unlisted port, want 1", len(cf.got))
	}
}

// A URL the model cannot fetch is a reflection it can fix, not a prompt the
// user has to answer for a string that was never going to work.
func TestWebfetchRefusesBadURLsWithoutAsking(t *testing.T) {
	c, cf, fetches := fetchCoder(t, true)

	for _, u := range []string{"file:///etc/passwd", "example.com/page", "ftp://x/y"} {
		out := runFetch(t, c, fetchCall(u, "read it"))
		if !strings.Contains(out, "cannot be fetched") {
			t.Errorf("%s: result = %q", u, out)
		}
	}
	if len(cf.got) != 0 || *fetches != 0 {
		t.Errorf("asked %d times and fetched %d — a URL that cannot be fetched should do neither", len(cf.got), *fetches)
	}
}

// The sandbox gates a fetch only when the scraper is an external command. The
// built-in fetcher spawns nothing, and refusing it for want of a kernel feature
// would apply a rule about subprocesses to something that has none.
func TestWebfetchSandboxGateFollowsTheScraper(t *testing.T) {
	c, _, fetches := fetchCoder(t, true)
	c.Sandbox = SandboxState{Required: true, Active: false, Unavailable: "no Landlock on this kernel"}

	if out := runFetch(t, c, fetchCall("https://go.dev/doc", "read it")); !strings.Contains(out, "the page") {
		t.Errorf("the in-process fetcher was refused for want of a sandbox: %q", out)
	}

	c.ScrapeRunsCommand = true
	out := runFetch(t, c, fetchCall("https://go.dev/doc", "read it"))
	if !strings.Contains(out, "Refused") {
		t.Errorf("a scraper *command* ran without the sandbox it requires: %q", out)
	}
	if *fetches != 1 {
		t.Errorf("fetches = %d, want 1 — only the in-process one should have run", *fetches)
	}
}

// The tool is offered only when there is something to fetch with, and it is
// offered in ask mode, where reading a specification is the point.
func TestWebfetchToolOffering(t *testing.T) {
	c := testCoder(t)
	if hasTool(c.toolDefs(), toolWebfetch) {
		t.Error("webfetch offered with no scraper configured")
	}
	c.Scrape = func(context.Context, string) (string, error) { return "", nil }
	if !hasTool(c.toolDefs(), toolWebfetch) {
		t.Error("webfetch not offered with a scraper configured")
	}
	c.editFormat = "ask"
	if !hasTool(c.toolDefs(), toolWebfetch) {
		t.Error("webfetch not offered in ask mode, where it mutates nothing")
	}
}

func hasTool(defs []llm.ToolDef, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}
