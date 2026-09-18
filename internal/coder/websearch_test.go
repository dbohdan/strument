package coder

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

func searchCall(query string) llm.ToolCall {
	return llm.ToolCall{ID: "call_1", Name: toolWebsearch, Arguments: `{"query":` + jsonString(query) + `}`}
}

func runSearch(t *testing.T, c *Coder, tc llm.ToolCall) string {
	t.Helper()
	q, msg := parseSearchArgs(tc)
	if msg != "" {
		return msg
	}
	return c.runWebsearch(context.Background(), q)
}

func searchCoder(t *testing.T, res SearchResults) *Coder {
	t.Helper()
	c := testCoder(t)
	c.Confirm = &recordingConfirmer{answer: true}
	c.Search = func(context.Context, string) (SearchResults, error) { return res, nil }
	return c
}

// The finding that shaped the whole result format. On a live instance a query
// with no hits came back with three engines unresponsive — so "nothing is
// written about this" and "your search was broken" are the same response
// unless the failures are reported. A model reading only "No results" will
// tell the user the web is empty while holding a broken search.
func TestSearchWithNoResultsSaysTheEnginesFailed(t *testing.T) {
	c := searchCoder(t, SearchResults{
		Query: "obscure thing",
		Unresponsive: []UnresponsiveEngine{
			{"brave", "too many requests"}, {"duckduckgo", "CAPTCHA"}, {"startpage", "timeout"},
		},
	})
	out := runSearch(t, c, searchCall("obscure thing"))

	if strings.TrimSpace(out) == "" {
		t.Fatal("an empty result — the one answer a model cannot act on")
	}
	if !strings.Contains(out, "No results") {
		t.Errorf("did not say there were no results:\n%s", out)
	}
	for _, engine := range []string{"brave", "duckduckgo", "startpage", "CAPTCHA"} {
		if !strings.Contains(out, engine) {
			t.Errorf("did not name %q among the failures:\n%s", engine, out)
		}
	}
	// And it has to say what that means, or the list is trivia.
	if !strings.Contains(out, "broken search") {
		t.Errorf("did not distinguish a broken search from an empty subject:\n%s", out)
	}
	// The failure comes *before* "No results". A live A/B could not show that
	// this changes what a model says — both orderings scored 8/8 — so the
	// ordering is a judgement, not a measured effect, and the test pins the
	// judgement: the likeliest cause of an empty result should be stated
	// before the emptiness, not after it and in parentheses.
	if strings.Index(out, "degraded") > strings.Index(out, "No results") {
		t.Errorf("the engine failures trail the empty result:\n%s", out)
	}
}

// Three engines down on a *successful* query is the normal state, not an
// alarm, so it is reported and framed differently: the results are thin, not
// absent.
func TestSearchWithResultsCallsFailuresThinRatherThanFailed(t *testing.T) {
	c := searchCoder(t, SearchResults{
		Query:        "q",
		Results:      []SearchResult{{Title: "net/http", URL: "https://pkg.go.dev/net/http", Content: "Package http."}},
		Unresponsive: []UnresponsiveEngine{{"brave", "too many requests"}},
	})
	out := runSearch(t, c, searchCall("q"))

	if !strings.Contains(out, "thinner") || strings.Contains(out, "degraded") {
		t.Errorf("a query with hits was framed as a failure:\n%s", out)
	}
	if !strings.Contains(out, "https://pkg.go.dev/net/http") || !strings.Contains(out, "Package http.") {
		t.Errorf("the result itself did not come through:\n%s", out)
	}
}

// The "a" is turn-scoped and covers every origin, which inverts webfetch on
// both counts: the destination is pinned by config so there is nothing to
// scope per origin, and a turn holds many searches so the turn is the unit
// that pays. If this ever needs a second prompt in one turn, the group broke.
func TestSearchAlwaysCoversTheTurnAndDiesWithIt(t *testing.T) {
	c := searchCoder(t, SearchResults{Query: "q"})
	ac := &alwaysConfirmer{}
	c.Confirm = ac

	runSearch(t, c, searchCall("first"))
	runSearch(t, c, searchCall("second — a different query entirely"))
	if len(ac.got) != 1 {
		t.Errorf("asked %d times in one turn, want 1", len(ac.got))
	}
	if ac.got[0].Query != "first" {
		t.Errorf("the prompt did not carry the query: %+v", ac.got[0])
	}
	// It carries its own name, so "--yes websearch" answers it and nothing
	// else does — no flag means "everything but the scary one" any more.
	if ac.got[0].Grant != GrantWebsearch {
		t.Errorf("Grant = %q, want %q", ac.got[0].Grant, GrantWebsearch)
	}

	c.initBeforeMessage()
	runSearch(t, c, searchCall("next turn"))
	if len(ac.got) != 2 {
		t.Errorf("asked %d times, want 2 — the grant outlived its turn", len(ac.got))
	}
	if len(c.SessionOrigins()) != 0 {
		t.Error("search wrote into the session map, which only webfetch may use")
	}
}

// A search nobody was asked about still has to be seen, the same rule webfetch
// follows for an allowlisted origin: an "a" buys fewer questions, not less to
// read.
// Every way a search can be approved leaves a line on screen naming the query.
//
// This is a regression test with a story. Visibility used to be a side effect
// of the confirmation prompt: a search announced itself only when it had *not*
// been asked about, decided by reading turnAutoApprove. That covered the "a"
// answered mid-turn and nothing else. A user with auto_approve = ["websearch"]
// in their config is approved through Grants instead, which never touches
// turnAutoApprove — so the prompt did not appear, the announcement decided it
// was unnecessary, and searches ran in complete silence. The outcome line is
// now unconditional, and this table is the thing that keeps it that way: each
// row is a different route to "yes", and none of them may be quiet.
func TestEveryApprovalPathStillShowsTheSearch(t *testing.T) {
	granted, err := ParseGrants([]string{"websearch"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		confirm Confirmer
	}{
		{"asked and answered yes", &recordingConfirmer{answer: true}},
		{"answered always, turn-scoped", &alwaysConfirmer{}},
		// The one that was broken: approved by config, never prompted.
		{"granted by auto_approve", AutoConfirmer{
			Granted:  StaticGrants(granted),
			Fallback: &recordingConfirmer{answer: false},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := searchCoder(t, SearchResults{
				Query:   "q",
				Results: []SearchResult{{Title: "T", URL: "https://example.org/a"}},
			})
			c.Confirm = tc.confirm
			out := &captureOutput{}
			c.Out = out

			runSearch(t, c, searchCall("the first one"))
			if !strings.Contains(out.String(), "the first one") {
				t.Errorf("the first search left no trace:\n%s", out.String())
			}
			// And the second, which is where an "always" answer starts
			// suppressing the question. Fewer questions, not less to read.
			out.reset()
			runSearch(t, c, searchCall("the second one"))
			if !strings.Contains(out.String(), "the second one") {
				t.Errorf("a follow-up search left no trace:\n%s", out.String())
			}
			if !strings.Contains(out.String(), "1 result") {
				t.Errorf("the line does not say what came back:\n%s", out.String())
			}
		})
	}
}

// A backend that reports engine health gets it into the user's line too: "4
// results" alone reads as a thin web when what happened was three engines
// failing.
func TestSearchLineNamesUnresponsiveEngines(t *testing.T) {
	c := searchCoder(t, SearchResults{
		Query:        "q",
		Results:      []SearchResult{{Title: "T", URL: "https://example.org/a"}},
		Unresponsive: []UnresponsiveEngine{{Engine: "brave"}, {Engine: "startpage"}},
	})
	out := &captureOutput{}
	c.Out = out
	runSearch(t, c, searchCall("q"))
	if !strings.Contains(out.String(), "2 engines did not answer") {
		t.Errorf("the line hid the engine failures:\n%s", out.String())
	}
}

// A search that fails, and one the user declines, are both visible too — a
// session that quietly stopped searching otherwise looks like one that decided
// not to.
func TestFailedAndDeclinedSearchesAreVisible(t *testing.T) {
	c := searchCoder(t, SearchResults{})
	c.Search = func(context.Context, string) (SearchResults, error) {
		return SearchResults{}, errors.New("instance down")
	}
	out := &captureOutput{}
	c.Out = out
	runSearch(t, c, searchCall("broken one"))
	if !strings.Contains(out.String(), "broken one") {
		t.Errorf("a failed search left no trace:\n%s", out.String())
	}

	c2 := searchCoder(t, SearchResults{Query: "q"})
	c2.Confirm = &recordingConfirmer{answer: false}
	out2 := &captureOutput{}
	c2.Out = out2
	runSearch(t, c2, searchCall("declined one"))
	if !strings.Contains(out2.String(), "declined one") {
		t.Errorf("a declined search left no trace:\n%s", out2.String())
	}
}

// A declined search is a sentence, and a missing query is a reflection rather
// than a prompt for a string that was never going to search for anything.
func TestSearchDeclinedAndEmptyQuery(t *testing.T) {
	c := searchCoder(t, SearchResults{Query: "q"})
	c.Confirm = &recordingConfirmer{answer: false}
	if out := runSearch(t, c, searchCall("q")); !strings.Contains(out, "chose not to") {
		t.Errorf("declined search said %q", out)
	}
	if out := runSearch(t, c, llm.ToolCall{ID: "x", Name: toolWebsearch, Arguments: `{"query":"  "}`}); !strings.Contains(out, "missing") {
		t.Errorf("empty query said %q", out)
	}
}

// The tool is offered only with a backend configured — genuinely conditional,
// unlike webfetch, which always has the built-in fetcher behind it.
func TestWebsearchToolOfferedOnlyWhenConfigured(t *testing.T) {
	c := testCoder(t)
	if slices2Contains(c.toolDefs(), toolWebsearch) {
		t.Error("websearch was offered with no instance configured")
	}
	c.Search = func(context.Context, string) (SearchResults, error) { return SearchResults{}, nil }
	if !slices2Contains(c.toolDefs(), toolWebsearch) {
		t.Error("websearch was not offered with an instance configured")
	}
}

func slices2Contains(defs []llm.ToolDef, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// Ask mode keeps search, and that is the point of the mode rather than an
// exception to it: finding out what exists is what a discussion turn is for,
// and a search mutates nothing. The mutating tools stay withheld.
func TestWebsearchSurvivesAskMode(t *testing.T) {
	c := testCoder(t)
	c.Search = func(context.Context, string) (SearchResults, error) { return SearchResults{}, nil }
	c.editFormat = "ask"
	defs := c.toolDefs()
	if !slices2Contains(defs, toolWebsearch) {
		t.Error("ask mode dropped websearch")
	}
	for _, mutating := range []string{toolBash, toolEdit} {
		if slices2Contains(defs, mutating) {
			t.Errorf("ask mode offered %q", mutating)
		}
	}
}
