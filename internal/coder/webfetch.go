package coder

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/origin"
)

// Fetching a page, as a tool the model calls rather than a guess about what the
// user meant by typing a URL.
//
// What this replaces was aider's: a regex over the input, prompting once per
// URL found, before the turn began. It could not work. The prompts arrived
// while the model had not yet seen the message, so nothing in the loop knew
// whether a URL was reading material or an example — and only the user did,
// which is the one party the design never asked. Pasting three URLs into a
// question cost three prompts. rejectedUrls, a session-long map of "never offer
// this again", was the mute button that grew on top.
//
// The name is the field's, not this project's: Claude Code has WebFetch,
// OpenCode and MiMo Code webfetch, DeepSeek Harness web_fetch. A model's prior
// about what "webfetch" does is worth more than a better name, and the same
// argument is already written at the top of tools.go. Bare "fetch" was the
// tempting short one and is the one to avoid: in a coding agent that word
// means git.

// webfetchTool is offered whenever the Scrape port is set, which the binary
// always does: a `scraper` command if one is configured, the built-in fetcher
// otherwise. The nil case is for a Coder built directly, in a test or as a
// library; runWebfetch answers it too. What the `scraper` setting changes is
// not whether the tool exists but whether it runs a subprocess, and so
// whether the sandbox gates it — see runWebfetch.
//
// purpose is required for the reason bashTool requires it: the prompt is worth
// only what the user reads off it. It matters more here than there. A shell
// command largely explains itself; a URL does not, and a long query string is
// exactly the shape worth being suspicious of.
func webfetchTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolWebfetch,
		Description: "Fetch a web page and return its content as text. Use it to read " +
			"documentation, a specification, or an issue the work depends on. " +
			"The user is asked before an unfamiliar host is fetched, so give a " +
			"purpose they can judge.\n\n" +
			"A URL fragment fetches just that section, so " +
			"https://docs.python.org/3/library/stdtypes.html#string-methods returns the " +
			"string methods rather than the whole page. On a page too large to return " +
			"whole, ask for its outline first and fetch a section by the anchor it lists. " +
			"A plain-text page — source code, Markdown — has no anchors; its outline " +
			"instead lists line numbers or definitions with their line ranges, and " +
			"range (\"412-470\") fetches those lines.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": strProp("The absolute URL to fetch, including the scheme (http or https)."),
				"purpose": strProp("Why this page is needed, in a few words — a claim the user can " +
					"weigh, not a label. \"Check the Go 1.26 release notes for the loop change\" " +
					"tells them something; \"read documentation\" does not."),
				"outline": map[string]any{
					"type": "boolean",
					"description": "Return the page's headings and their anchors instead of its content. " +
						"Use it on a page too large to read whole, then fetch the section you want.",
				},
				"range": map[string]any{
					"type": "string",
					"description": "Fetch only the given lines of a plain-text page, 1-based and " +
						"inclusive — \"412-470\". An outline of a text page lists line numbers; " +
						"this fetches the lines it points at.",
				},
			},
			"required": []any{"url", "purpose"},
		},
	}
}

// toolFetch is one parsed webfetch call.
type toolFetch struct {
	callID  string
	url     string
	purpose string
	outline bool
	lineLo  int // range fetch: 1-based, inclusive; 0 when no range was given
	lineHi  int
}

func parseFetchArgs(tc llm.ToolCall) (toolFetch, string) {
	var a struct {
		URL     string `json:"url"`
		Purpose string `json:"purpose"`
		Outline bool   `json:"outline"`
		Range   string `json:"range"`
	}
	if err := json.Unmarshal([]byte(tc.Arguments), &a); err != nil {
		return toolFetch{}, fmt.Sprintf("The arguments were not valid JSON: %v", err)
	}
	raw := strings.TrimSpace(a.URL)
	if raw == "" {
		return toolFetch{}, "The required \"url\" argument was missing."
	}
	// Parsed here rather than at the fetch, so a malformed URL is a reflection
	// the model can fix rather than a confirmation prompt for a string that was
	// never going to work. The rule is origin's; the sentence is this package's,
	// because it is the one a model reads.
	if _, err := origin.Of(raw); err != nil {
		return toolFetch{}, "That URL cannot be fetched: " + err.Error() + "."
	}
	lo, hi, err := parseLineRange(a.Range)
	if err != nil {
		return toolFetch{}, "The range argument was not usable: " + err.Error() +
			" Give it as two line numbers, first and last, as in \"80-120\"."
	}
	return toolFetch{callID: tc.ID, url: raw, purpose: strings.TrimSpace(a.Purpose),
		outline: a.Outline, lineLo: lo, lineHi: hi}, ""
}

// parseLineRange reads "80-120" (1-based, inclusive) or a single "80".
func parseLineRange(s string) (lo, hi int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	first, second, _ := strings.Cut(s, "-")
	lo, err = strconv.Atoi(strings.TrimSpace(first))
	if err != nil || lo < 1 {
		return 0, 0, fmt.Errorf("%q is not a line number", first)
	}
	hi = lo
	if second != "" {
		hi, err = strconv.Atoi(strings.TrimSpace(second))
		if err != nil {
			return 0, 0, fmt.Errorf("%q is not a line number", second)
		}
	} else if strings.HasSuffix(s, "-") {
		return 0, 0, fmt.Errorf("the range %s has no end", s)
	}
	if hi < lo {
		return 0, 0, fmt.Errorf("the range %s ends before it starts", s)
	}
	return lo, hi, nil
}

// runWebfetch confirms and fetches, returning the page as the tool result.
func (c *Coder) runWebfetch(ctx context.Context, f toolFetch) string {
	if c.Scrape == nil {
		return "Fetching is not available in this session."
	}
	// A configured `scraper` command is a subprocess the model caused, so it is
	// gated like bash and check. The built-in fetcher is an in-process HTTP GET
	// with nothing to confine, and refusing it for want of a kernel feature
	// would be applying a rule about subprocesses to something that spawns
	// none.
	if c.ScrapeRunsCommand && c.Sandbox.blocksExecution() {
		return c.Sandbox.refusal()
	}

	org, err := origin.Of(f.url)
	if err != nil {
		return "That URL cannot be fetched: " + err.Error() + "."
	}
	// asked records whether the user saw this fetch as a question, which is
	// what decides below whether it has to be announced instead.
	asked := false
	// An allowlisted origin skips the prompt entirely — which is also why it
	// needs no --yes flag. The flag question only ever arises for an origin the
	// user never named.
	if !origin.Allowed(org, c.WebfetchAllow) {
		// Scoped to the origin rather than to the turn. bash ties its "all this
		// turn" to the sandbox, because a sandbox bounds what an unseen command
		// can do; nothing bounds an unseen *URL*, so the bound has to come from
		// the answer itself. "Everything on go.dev" is the workflow people
		// actually want, and it still stops the model pivoting to a host the
		// user never saw.
		group := "webfetch:" + org
		granted := c.sessionAutoApprove[group]
		asked = !granted
		if !c.confirmGrouped(ConfirmRequest{
			Prompt:       "Fetch this page?",
			URL:          f.url,
			Origin:       org,
			Purpose:      f.purpose,
			Group:        group,
			GroupSession: true,
			Grant:        GrantWebfetch,
		}) {
			return "The user chose not to fetch that page."
		}
		// Said once, when the grant is made. A turn boundary used to give this
		// visibility for free: a grant that expired on its own never needed
		// announcing. One that outlives the topic it was made for does, and the
		// line is also where the way out gets named — a permission with no
		// visible escape is one whose only recourse is restarting.
		if !granted && c.sessionAutoApprove[group] {
			c.Out.Printf("Fetching %s without asking for the rest of this session.", org)
		}
	}

	// A fetch nobody was asked about still has to be seen. Every other
	// observation tool announces itself — "Read x.go", "Searched for y" — and
	// webfetch was the one whose visibility came from its permission prompt
	// instead, so an allowlisted fetch has always been silent. Session grants
	// make that the common case rather than the rare one, and a grant
	// announced once cannot cover for uses nobody can see. Skipped when we did
	// prompt, since the prompt drew all of this and asked about it.
	if !asked {
		if f.purpose != "" {
			c.Out.Toolf("\u2039webfetch\u203a %s", f.purpose)
		} else {
			c.Out.Warningf("\u2039webfetch\u203a (no purpose given)")
		}
		c.Out.Link(f.url)
	}

	content, err := c.Scrape(ctx, f.url, ScrapeOptions{Outline: f.outline, Range: rangeArg(f)})
	if err != nil {
		// The model gets the reason, so it can try a different URL rather than
		// conclude the page said nothing.
		return fmt.Sprintf("Could not fetch %s: %v", f.url, err)
	}
	if f.outline {
		return truncateResult(content) // an outline that overruns has no map of its own
	}
	return truncateFetch(content, f.url)
}

// rangeArg is the fetch's range as the ScrapeOptions field, "" for none.
func rangeArg(f toolFetch) string {
	if f.lineLo == 0 {
		return ""
	}
	if f.lineLo == f.lineHi {
		return strconv.Itoa(f.lineLo)
	}
	return fmt.Sprintf("%d-%d", f.lineLo, f.lineHi)
}

// truncateFetch cuts an oversized page and hands back its map.
//
// The note this replaces said to "fetch a more specific page", which assumes a
// page that may not exist and asks a model to find it while holding a quarter
// of the one it has. The predictable next move is to give up on the tool and
// reach for curl.
//
// So the page arrives with its own outline instead. Every heading and its
// anchor, computed from the markdown that is about to be cut rather than from
// a second request, and the anchors are fetchable — which turns "this is too
// big" into a two-step navigation the model can finish on its own.
//
// The outline is reserved out of the budget rather than added to it, or the
// result would exceed the cap it exists to respect.
// outlineSwitchRatio is how many times over the cap a page has to be before it
// answers with its outline instead of a prefix of itself.
//
// The ratio, not the excess, is what matters. A page 20% over the cap returns
// 83% of itself and the answer is probably in hand; trading that for a map
// would buy a guaranteed extra round trip. A page six times over returns 14%,
// and on the page this was measured against that 14% was the navigation
// sidebar and the table of contents — nothing anyone asked for.
//
// Four is a judgment call. The evidence covers 6x, where the outline wins
// clearly, and reasoning covers 1.2x, where the prefix obviously wins; nobody
// has measured where they cross. The note below names the multiple, so a badly
// chosen threshold announces itself — "the first 83% of it" would read absurdly
// and be the signal to raise this number.
const outlineSwitchRatio = 4

func truncateFetch(content, pageURL string) string {
	if len(content) <= maxToolOutputBytes {
		return content
	}
	if len(content) > outlineSwitchRatio*maxToolOutputBytes {
		// Far more page than one result can carry, so send the map. Four of
		// six models with an order-independent preference chose this over a
		// prefix, in a blind pairwise test; the other two answered by position
		// rather than content and were counted as saying nothing.
		return fmt.Sprintf(
			"(This page is %d KB, about %d times what one tool result carries, so its outline "+
				"follows instead of the first %d%% of it. Fetch any section by adding its "+
				"anchor to the URL.)\n\n",
			len(content)/1024, len(content)/maxToolOutputBytes,
			100*maxToolOutputBytes/len(content)) + outlineOf(content, pageURL)
	}
	outline := outlineOf(content, pageURL)
	// The map gets a budget of its own. Without one a page of four thousand
	// headings produced an outline twice the cap, and the result overran the
	// limit it exists to respect — found by a test written for the opposite
	// case, which is the usual way.
	if maxOutline := maxToolOutputBytes / 2; len(outline) > maxOutline {
		cut := strings.LastIndexByte(outline[:maxOutline], '\n') + 1
		outline = outline[:cut] +
			"\n(Outline cut short: this page has more sections than one result can list.)\n"
	}
	pct := 100 * maxToolOutputBytes / len(content)
	// At the top, not only at the bottom.
	//
	// The note used to sit after the content, which is after 60 KB of reading —
	// by which point a conclusion has been drawn. A model asked to find a
	// string, handed something that says "Here is the content of <url>" and is
	// in fact a tenth of it, searches, fails, and reports the string is not on
	// the page. That is a wrong answer rather than a slow one, and it is the
	// worst thing this tool can produce. The header says what this is before
	// any of it is read.
	head := fmt.Sprintf(
		"(Partial page: what follows is the first %d%% of it. The page is %d KB and one tool "+
			"result carries %d KB. If what you need is not in the text below, it may still be "+
			"on the page — the outline of the whole page is at the end of this result.)\n\n",
		pct, len(content)/1024, maxToolOutputBytes/1024)
	note := "\n\n(End of the partial page. Outline of the whole of it follows; fetch a section " +
		"by adding its anchor to the URL rather than fetching this page again, which returns " +
		"this same prefix.)\n\n"

	room := max(maxToolOutputBytes-len(head)-len(note)-len(outline), 0)
	room = min(room, len(content))
	return head + content[:room] + note + outline
}

// SessionOrigins lists the origins approved by an "a" answer this session,
// sorted. The config allowlist is not folded in: they have different
// lifetimes and different ways of being undone, and a list that blurs the two
// would be answering "what may be fetched" when the question a caller has is
// "what did I grant, and how do I take it back".
func (c *Coder) SessionOrigins() []string {
	out := make([]string, 0, len(c.sessionAutoApprove))
	for group := range c.sessionAutoApprove {
		if org, ok := strings.CutPrefix(group, "webfetch:"); ok {
			out = append(out, org)
		}
	}
	// Host first, then port *numerically*. A plain string sort puts go.dev:443
	// above go.dev:80, which reads as a bug in a list whose whole job is to be
	// checked at a glance.
	slices.SortFunc(out, func(a, b string) int {
		ha, pa, _ := net.SplitHostPort(a)
		hb, pb, _ := net.SplitHostPort(b)
		if ha != hb {
			return cmp.Compare(ha, hb)
		}
		na, _ := strconv.Atoi(pa)
		nb, _ := strconv.Atoi(pb)
		return cmp.Compare(na, nb)
	})
	return out
}

// AllowOrigin approves an allowlist entry for the session without waiting to be
// asked, reporting the origins it granted that were not granted already. An
// entry is validated and expanded by the same rules the config file uses, so a
// bare host grants both default ports there and here alike.
func (c *Coder) AllowOrigin(entry string) ([]string, bool) {
	if !origin.ValidEntry(entry) {
		return nil, false
	}
	var added []string
	for _, org := range origin.Origins(entry) {
		group := "webfetch:" + org
		if !c.sessionAutoApprove[group] {
			c.sessionAutoApprove[group] = true
			added = append(added, org)
		}
	}
	return added, true
}

// DropOrigin withdraws one session grant, returning the origins it withdrew.
// ok is false for an entry that is not an origin at all; an empty result for a
// well-formed entry means it was not granted — which the caller distinguishes,
// because "never approved" and "approved in the config" are different answers
// and only one of them means the user still has something to do.
func (c *Coder) DropOrigin(entry string) ([]string, bool) {
	if !origin.ValidEntry(entry) {
		return nil, false
	}
	var dropped []string
	for _, org := range origin.Origins(entry) {
		group := "webfetch:" + org
		if c.sessionAutoApprove[group] {
			delete(c.sessionAutoApprove, group)
			dropped = append(dropped, org)
		}
	}
	return dropped, true
}

// ForgetOrigins drops every session grant, returning how many there were. The
// config allowlist is untouched: it is a file the user wrote, and a command
// that silently disagreed with the file would be the worse surprise.
func (c *Coder) ForgetOrigins() int {
	n := len(c.sessionAutoApprove)
	c.sessionAutoApprove = map[string]bool{}
	return n
}
