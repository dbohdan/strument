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
			"purpose they can judge.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": strProp("The absolute URL to fetch, including the scheme (http or https)."),
				"purpose": strProp("Why this page is needed, in a few words — a claim the user can " +
					"weigh, not a label. \"Check the Go 1.26 release notes for the loop change\" " +
					"tells them something; \"read documentation\" does not."),
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
}

func parseFetchArgs(tc llm.ToolCall) (toolFetch, string) {
	var a struct {
		URL     string `json:"url"`
		Purpose string `json:"purpose"`
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
	return toolFetch{callID: tc.ID, url: raw, purpose: strings.TrimSpace(a.Purpose)}, ""
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

	content, err := c.Scrape(ctx, f.url)
	if err != nil {
		// The model gets the reason, so it can try a different URL rather than
		// conclude the page said nothing.
		return fmt.Sprintf("Could not fetch %s: %v", f.url, err)
	}
	return truncateFetch(content)
}

// truncateFetch is truncateResult with a note a model can act on.
//
// The generic one says only that the result was cut short, which on a fetch
// leaves the model to guess whether the page ended or the harness stopped it.
// A live pass against docs.python.org/3/library/stdtypes.html made the case:
// 228 KB of real content against a 60 KB cap, and the model reported the page
// as read while having seen a quarter of it. Saying how much there was, and
// that a narrower page is the way to the rest, turns a silent gap into a next
// step.
func truncateFetch(content string) string {
	if len(content) <= maxToolOutputBytes {
		return content
	}
	return content[:maxToolOutputBytes] + fmt.Sprintf(
		"\n\n(Cut off here: the page is %d KB and one tool result carries %d KB, so this is "+
			"the first %d%% of it. If what you need is further down, fetch a more specific page "+
			"rather than this one again — the same fetch returns the same prefix.)\n",
		len(content)/1024, maxToolOutputBytes/1024, 100*maxToolOutputBytes/len(content))
}
