package coder

import (
	"context"
	"encoding/json"
	"fmt"
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

// webfetchTool is offered only when a scraper is configured.
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
			"whole, ask for its outline first and fetch a section by the anchor it lists.",
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
}

func parseFetchArgs(tc llm.ToolCall) (toolFetch, string) {
	var a struct {
		URL     string `json:"url"`
		Purpose string `json:"purpose"`
		Outline bool   `json:"outline"`
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
	return toolFetch{callID: tc.ID, url: raw, purpose: strings.TrimSpace(a.Purpose), outline: a.Outline}, ""
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
	// An allowlisted origin skips the prompt entirely — which is also why it
	// needs no --yes flag. The flag question only ever arises for an origin the
	// user never named.
	if !origin.Allowed(org, c.WebfetchAllow) {
		if !c.confirmTurn(ConfirmRequest{
			Prompt:  "Fetch this page?",
			URL:     f.url,
			Origin:  org,
			Purpose: f.purpose,
			// Scoped to the origin rather than the turn. bash ties its "all this
			// turn" to the sandbox, because a sandbox bounds what an unseen
			// command can do; nothing bounds an unseen *URL*, so the bound has to
			// come from the answer itself. "Everything on go.dev this turn" is
			// the workflow people actually want, and it still stops the model
			// pivoting to a host the user never saw.
			Group:            "webfetch:" + org,
			RequiresYesShell: true,
		}) {
			return "The user chose not to fetch that page."
		}
	}

	content, err := c.Scrape(ctx, f.url, ScrapeOptions{Outline: f.outline})
	if err != nil {
		// The model gets the reason, so it can try a different URL rather than
		// conclude the page said nothing.
		return fmt.Sprintf("Could not fetch %s: %v", f.url, err)
	}
	if f.outline {
		return truncateResult(content) // an outline that overruns has no map of its own
	}
	return truncateFetch(content)
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
func truncateFetch(content string) string {
	if len(content) <= maxToolOutputBytes {
		return content
	}
	outline := outlineOf(content)
	// The map gets a budget of its own. Without one a page of four thousand
	// headings produced an outline twice the cap, and the result overran the
	// limit it exists to respect — found by a test written for the opposite
	// case, which is the usual way.
	if maxOutline := maxToolOutputBytes / 2; len(outline) > maxOutline {
		cut := strings.LastIndexByte(outline[:maxOutline], '\n') + 1
		outline = outline[:cut] +
			"\n(Outline cut short: this page has more sections than one result can list.)\n"
	}
	note := fmt.Sprintf(
		"\n\n(Cut off here: the page is %d KB and one tool result carries %d KB, so this is "+
			"the first %d%% of it. Its outline follows. Fetch a section on its own by adding "+
			"its anchor to the URL rather than fetching this page again — the same fetch "+
			"returns the same prefix.)\n\n",
		len(content)/1024, maxToolOutputBytes/1024, 100*maxToolOutputBytes/len(content))

	room := max(maxToolOutputBytes-len(note)-len(outline), 0)
	room = min(room, len(content))
	return content[:room] + note + outline
}
