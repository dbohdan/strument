// The webfetch tool: what it asks, when it does not, and what it says back.

package coder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	c.Scrape = func(_ context.Context, url string, _ ScrapeOptions) (string, error) {
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
	if req.Grant != GrantWebfetch {
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
	c.Scrape = func(context.Context, string, ScrapeOptions) (string, error) {
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

// The note has to reach the model through the tool, not just exist. Swapping
// truncateFetch back for the generic truncateResult broke nothing until this
// test existed — the wiring was the untested part, which is the usual place for
// a one-line revert to hide.
func TestWebfetchLongPageCarriesTheFetchNote(t *testing.T) {
	c, _, _ := fetchCoder(t, true)
	c.Scrape = func(context.Context, string, ScrapeOptions) (string, error) {
		var b strings.Builder
		for i := range 40 {
			b.WriteString("## Section " + strconv.Itoa(i) + " {#sec-" + strconv.Itoa(i) + "}\n\n" +
				strings.Repeat("x", 4000) + "\n\n")
		}
		return b.String(), nil
	}

	out := runFetch(t, c, fetchCall("https://go.dev/doc", "read it"))

	if !strings.Contains(out, "Partial page") || !strings.Contains(out, "#sec-39") {
		t.Error("a truncated fetch did not carry the note and the outline")
	}
}

// The tool is offered only when there is something to fetch with, and it is
// offered in ask mode, where reading a specification is the point.
func TestWebfetchToolOffering(t *testing.T) {
	c := testCoder(t)
	if hasTool(c.toolDefs(), toolWebfetch) {
		t.Error("webfetch offered with no scraper configured")
	}
	c.Scrape = func(context.Context, string, ScrapeOptions) (string, error) { return "", nil }
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

// alwaysConfirmer answers "a" — always, at whatever scope the request declared.
type alwaysConfirmer struct{ got []ConfirmRequest }

func (ac *alwaysConfirmer) Confirm(req ConfirmRequest) ConfirmResult {
	ac.got = append(ac.got, req)
	return ConfirmResult{Always: true}
}

// The whole of the session scope: an "a" answered on one turn still covers the
// same origin on the next. Turn scope never paid here — a turn holds one or two
// fetches, so "a" saved a single prompt and then asked again about the host the
// user had just approved.
func TestWebfetchAlwaysOutlivesTheTurn(t *testing.T) {
	c, _, _ := fetchCoder(t, true)
	ac := &alwaysConfirmer{}
	c.Confirm = ac

	runFetch(t, c, fetchCall("https://go.dev/doc/go1.26", "read it"))
	c.initBeforeMessage()
	runFetch(t, c, fetchCall("https://go.dev/ref/spec", "read the spec"))

	if len(ac.got) != 1 {
		t.Errorf("asked %d times across two turns, want 1 — the grant did not survive the turn", len(ac.got))
	}
	// A different origin is still a different question. The grant is scoped to
	// one origin precisely so it cannot follow the model to a host the user
	// never saw.
	runFetch(t, c, fetchCall("https://example.com/x", "read it"))
	if len(ac.got) != 2 {
		t.Errorf("asked %d times, want 2 — an unrelated origin was covered by the grant", len(ac.got))
	}
	if got := c.SessionOrigins(); !slices.Equal(got, []string{"example.com:443", "go.dev:443"}) {
		t.Errorf("SessionOrigins() = %q", got)
	}
	// Ports sort numerically, not as strings. A pty run put go.dev:443 above
	// go.dev:80, which reads as a bug in a list whose job is to be checked at a
	// glance — and no unit test noticed, because none had two ports of one host.
	c.AllowOrigin("go.dev:80")
	c.AllowOrigin("go.dev:8080")
	want := []string{"example.com:443", "go.dev:80", "go.dev:443", "go.dev:8080"}
	if got := c.SessionOrigins(); !slices.Equal(got, want) {
		t.Errorf("SessionOrigins() = %q, want %q", got, want)
	}
}

// The counter-metric, and the regression the two maps exist to prevent: the
// shell gate's "a" must still die at the turn boundary. A sandbox bounds what
// an unseen command can do, which is what lets that answer be broad in *what*;
// nothing bounds an unseen URL, so only webfetch buys the longer life. If this
// ever passes with one map, session-wide silence on shell commands has arrived
// by accident — and it would arrive silently.
func TestShellAlwaysStillDiesWithTheTurn(t *testing.T) {
	c := testCoder(t)
	ac := &alwaysConfirmer{}
	c.Confirm = ac
	c.Sandbox = SandboxState{Required: true, Active: true}

	req := ConfirmRequest{Prompt: "Run shell command?", Command: "go test ./...", Group: "shell"}
	if !c.confirmGrouped(req) {
		t.Fatal(`"a" did not approve the command it was answered for`)
	}
	if c.confirmGrouped(req); len(ac.got) != 1 {
		t.Fatalf("asked %d times in one turn, want 1 — the turn grant did not hold", len(ac.got))
	}

	c.initBeforeMessage()
	c.confirmGrouped(req)
	if len(ac.got) != 2 {
		t.Errorf("asked %d times, want 2 — a shell grant outlived its turn", len(ac.got))
	}
	if got := c.SessionOrigins(); len(got) != 0 {
		t.Errorf("SessionOrigins() = %q, want none — shell wrote to the session map", got)
	}
}

// "/web allow" and a webfetch_allow entry have to agree about what an entry
// covers, or a bare host would mean one thing in the config and another at the
// prompt — a disagreement nobody could see, since both spellings look right.
func TestAllowOriginExpandsLikeTheConfig(t *testing.T) {
	c, _, _ := fetchCoder(t, true)
	c.Confirm = &recordingConfirmer{answer: false}

	added, ok := c.AllowOrigin("go.dev")
	if !ok || !slices.Equal(added, []string{"go.dev:80", "go.dev:443"}) {
		t.Fatalf("AllowOrigin(%q) = %q, %v; want both default ports", "go.dev", added, ok)
	}
	// Granted, so a fetch goes through despite the confirmer answering no.
	if out := runFetch(t, c, fetchCall("https://go.dev/doc", "read it")); !strings.Contains(out, "the page") {
		t.Errorf("an allowed origin was still refused: %q", out)
	}
	if again, _ := c.AllowOrigin("go.dev"); len(again) != 0 {
		t.Errorf("re-allowing reported %q as new", again)
	}
	if _, ok := c.AllowOrigin("https://go.dev/doc"); ok {
		t.Error("a URL was accepted as an origin")
	}

	dropped, ok := c.DropOrigin("go.dev:443")
	if !ok || !slices.Equal(dropped, []string{"go.dev:443"}) {
		t.Fatalf("DropOrigin = %q, %v", dropped, ok)
	}
	if out := runFetch(t, c, fetchCall("https://go.dev/doc", "read it")); !strings.Contains(out, "chose not to") {
		t.Errorf("a dropped origin was still fetched without asking: %q", out)
	}
	// Port 80 was granted by the same entry and is a separate origin, so
	// dropping one leaves the other standing.
	if got := c.SessionOrigins(); !slices.Equal(got, []string{"go.dev:80"}) {
		t.Errorf("SessionOrigins() = %q, want the untouched port to remain", got)
	}
	if n := c.ForgetOrigins(); n != 1 {
		t.Errorf("ForgetOrigins() = %d, want 1", n)
	}
}

// captureOutput keeps what the user would have seen, so a test can assert on
// an absence as well as on a line.
type captureOutput struct{ b strings.Builder }

func (o *captureOutput) Printf(format string, args ...any)   { fmt.Fprintf(&o.b, format+"\n", args...) }
func (o *captureOutput) Toolf(format string, args ...any)    { fmt.Fprintf(&o.b, format+"\n", args...) }
func (o *captureOutput) ToolBlock(title, body string)        { fmt.Fprintf(&o.b, "%s %s\n", title, body) }
func (o *captureOutput) Warningf(format string, args ...any) { fmt.Fprintf(&o.b, format+"\n", args...) }
func (o *captureOutput) Errorf(format string, args ...any)   { fmt.Fprintf(&o.b, format+"\n", args...) }
func (o *captureOutput) Link(target string)                  { o.Printf("%s", target) }
func (o *captureOutput) StreamText(string)                   {}
func (o *captureOutput) StreamReasoning(string)              {}
func (o *captureOutput) StreamToolCall(int, string, string)  {}
func (o *captureOutput) FlushStream()                        {}
func (o *captureOutput) String() string                      { return o.b.String() }
func (o *captureOutput) reset()                              { o.b.Reset() }

// A fetch nobody was asked about still has to be seen. webfetch was the one
// observation tool with no line of its own — read, grep, glob, ls and check all
// announce themselves, and webfetch borrowed its visibility from the permission
// prompt. That was survivable while silence took a config edit; a session grant
// buys it with one keystroke, and a grant announced once cannot cover for uses
// nobody can see. Found by reading a live transcript: turn two showed the answer
// with no trace that a page had been fetched to get it.
func TestWebfetchAnnouncesAnUnpromptedFetch(t *testing.T) {
	c, _, _ := fetchCoder(t, true)
	out := &captureOutput{}
	c.Out = out
	c.WebfetchAllow = []string{"go.dev"}

	runFetch(t, c, fetchCall("https://go.dev/doc/go1.26", "read the release notes"))
	s := out.String()
	if !strings.Contains(s, "read the release notes") || !strings.Contains(s, "https://go.dev/doc/go1.26") {
		t.Errorf("an allowlisted fetch left no trace:\n%s", s)
	}

	// A prompted fetch must not say it twice: the prompt already drew the
	// purpose and the URL, and asked about them.
	out.reset()
	c.WebfetchAllow = nil
	runFetch(t, c, fetchCall("https://example.com/x", "read it"))
	if strings.Count(out.String(), "read it") > 0 {
		t.Errorf("a prompted fetch was announced twice:\n%s", out.String())
	}
}
