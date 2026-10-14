package coder

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// toolRecall searches the session record: every message of the session as it
// was said, including what compaction has since removed from the
// conversation.
//
// It exists because compaction loses the model's own results.
// doc/experiments/2026-10-compaction-roll found that rolling the history kept
// every user fact and none of the model's results; asked about them, the model
// said "I don't know" 24 times of 24. The results were still on disk. A search
// turns that honest gap into a lookup.
//
// Shaped like grep on purpose (doc/experiments/2026-10-recall-survey and the
// design notes there): the same pattern dialect and parameter names, so a model
// that has used grep this session already knows how to call it. Two things are
// its own. from scopes the search by speaker, because nearly all the noise is
// in tool output — the files the model read — while the model's results are
// stated in its own prose. And matches in messages still in the conversation are
// counted, not printed: the model can already see them.
const toolRecall = "recall"

const (
	recallDefaultNewest = 10
	recallMaxNewest     = 50
	// recallLinesPerHit bounds what one message contributes to a search. The
	// rest is a call away, with id.
	recallLinesPerHit = 3
	// recallLineRunes cuts a matching line that is mostly something else: a
	// minified file or a long JSON line would otherwise be the whole result.
	recallLineRunes = 300
	// recallHiddenIDs bounds how many hidden replies a search names.
	recallHiddenIDs = 10
	// recallEntryLines and recallEntryRunes bound an entry opened with id.
	recallEntryLines = 400
	recallEntryRunes = 40000
)

func recallTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolRecall,
		Description: "Search this session's record: every message of the session as it was said, " +
			"including earlier turns that have been compacted, summarized or dropped from this " +
			"conversation. Use it to find something said, read or worked out earlier that you can " +
			"no longer see. Matches in messages still in this conversation are counted, not shown. " +
			"Results are newest first; each hit has an id, which you can pass back to see that " +
			"message whole.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": strProp("A regular expression (Go syntax), as for grep. Use a|b for " +
					"alternatives. A pattern that is not a valid expression is searched for as plain text. " +
					"Required unless id is given."),
				"from": map[string]any{
					"type": "string",
					"enum": []any{"conversation", "tools", "all"},
					"description": "Which messages to search: \"conversation\" (the default) is what " +
						"the user and you said; \"tools\" is tool output, such as files read and " +
						"commands run; \"all\" is both.",
				},
				"ignore_case":   map[string]any{"type": "boolean", "description": "Match case-insensitively."},
				"context_lines": intProp("Lines to return either side of each match, like grep's -C."),
				"newest": intProp(fmt.Sprintf("How many matching messages to show, newest first. "+
					"Default %d, at most %d.", recallDefaultNewest, recallMaxNewest)),
				"id": strProp("Optional. A hit's id, such as \"t4.3\", to see that message whole; " +
					"lines matching pattern, if one is given, are marked."),
			},
		},
	}
}

type recallArgs struct {
	Pattern      string `json:"pattern"`
	From         string `json:"from"`
	IgnoreCase   bool   `json:"ignore_case"`
	ContextLines int    `json:"context_lines"`
	Newest       int    `json:"newest"`
	ID           string `json:"id"`
}

// parseRecallArgs decodes a recall call. The second return is a model-facing
// failure message, "" on success.
func parseRecallArgs(tc llm.ToolCall) (recallArgs, string) {
	var a recallArgs
	if err := json.Unmarshal([]byte(tc.Arguments), &a); err != nil {
		return a, fmt.Sprintf("The arguments were not valid JSON: %v", err)
	}
	// An id alone is a complete request: a model that has a hit's id and
	// wants the message should not have to invent a pattern to get it. A live
	// MiMo session did exactly that, with "\\d+", after being refused.
	if a.Pattern == "" && a.ID == "" {
		return a, "Give a \"pattern\" to search for, or the \"id\" of a message to see whole."
	}
	switch a.From {
	case "":
		a.From = "conversation"
	case "conversation", "tools", "all":
	default:
		return a, fmt.Sprintf("\"from\" must be \"conversation\", \"tools\" or \"all\", not %q.", a.From)
	}
	if a.ContextLines < 0 {
		return a, "\"context_lines\" must not be negative."
	}
	switch {
	case a.Newest <= 0:
		a.Newest = recallDefaultNewest
	case a.Newest > recallMaxNewest:
		a.Newest = recallMaxNewest
	}
	return a, ""
}

// recallEntry is one message of the session record, addressed by turn.
type recallEntry struct {
	turn  int    // 1-based
	index int    // 1-based within the turn, over message records
	role  string // "user", "assistant" or "tool"
	tool  string // the tool that produced a tool result
	text  string
}

func (e recallEntry) id() string { return "t" + strconv.Itoa(e.turn) + "." + strconv.Itoa(e.index) }

func (e recallEntry) speaker() string {
	if e.role == llm.RoleTool && e.tool != "" {
		return "tool " + e.tool
	}
	return e.role
}

func (e recallEntry) inScope(from string) bool {
	switch from {
	case "tools":
		return e.role == llm.RoleTool
	case "all":
		return true
	default:
		return e.role != llm.RoleTool
	}
}

// recallEntries lays the record out as messages numbered by turn, and returns
// the number of turns. A turn ends at its "turn" record; messages after the
// last one belong to the turn in progress.
//
// Earlier recall results are left out. They are copies of the record, so a
// search would find its own previous answers, and each repetition would make a
// fact look better attested than it is.
func recallEntries(records []Record) ([]recallEntry, int) {
	var out []recallEntry
	turn, index := 1, 0
	toolNames := map[string]string{}
	for _, r := range records {
		switch r.Type {
		case "turn":
			if index > 0 {
				turn++
				index = 0
			}
		case "message":
			index++
			for _, tc := range r.ToolCalls {
				toolNames[tc.ID] = tc.Name
			}
			name := toolNames[r.ToolCallID]
			if r.Role == llm.RoleTool && name == toolRecall {
				continue
			}
			if strings.TrimSpace(r.Text) == "" {
				continue
			}
			out = append(out, recallEntry{turn: turn, index: index, role: r.Role, tool: name, text: r.Text})
		}
	}
	// recall only runs inside a turn, so the turn in progress counts even
	// before its first message reaches the record. Without that, two searches in
	// one turn could disagree about how many turns there are.
	return out, turn
}

// inConversation is the set of message texts the model can currently see. A
// record entry whose text is in it is still in the conversation; a tool result
// pruning has cleared, or a turn a summary or roll replaced, is not.
func (c *Coder) inConversation() map[string]bool {
	seen := map[string]bool{}
	for _, msgs := range [][]llm.Message{c.doneMessages, c.curMessages} {
		for _, m := range msgs {
			if t := m.Text(); t != "" {
				seen[t] = true
			}
		}
	}
	return seen
}

// compileRecall compiles the pattern, falling back to a literal search when it
// does not compile. The note says so, because a model that meant an expression
// should know it was not used as one.
func compileRecall(pattern string, ignoreCase bool) (*regexp.Regexp, string) {
	prefix := ""
	if ignoreCase {
		prefix = "(?i)"
	}
	re, err := regexp.Compile(prefix + pattern)
	if err == nil {
		return re, ""
	}
	note := fmt.Sprintf("The pattern is not a valid expression (%v), so it was searched for as plain text.\n",
		strings.TrimPrefix(err.Error(), "error parsing regexp: "))
	return regexp.MustCompile(prefix + regexp.QuoteMeta(pattern)), note
}

// offersRecall reports whether this session offers recall, as a tool and
// inside run_code alike: whenever there is a record to search.
// doc/experiments/2026-10-recall-models made it the default; before that it
// was a trial flag.
func (c *Coder) offersRecall() bool { return c.SessionRecords != nil }

func (c *Coder) runRecall(a recallArgs) string {
	if c.SessionRecords == nil {
		return "This session keeps no record to search (it was started with --no-history)."
	}
	entries, turns := recallEntries(c.SessionRecords())
	if a.Pattern == "" {
		return recallOpen(entries, a.ID, nil)
	}
	if a.ID == "" {
		// A hit's id passed as the pattern, escaped or not, can only match
		// itself, and ids are not text in the record, so the search would come
		// back empty. Ling 3.1 did this three times in one turn
		// (doc/experiments/2026-10-recall-models); open the message instead.
		if m := recallIDPattern.FindStringSubmatch(a.Pattern); m != nil {
			id := "t" + m[1] + "." + m[2]
			return fmt.Sprintf("%q is a message id, not text in the record, so it was opened as id=%q.\n", a.Pattern, id) +
				recallOpen(entries, id, nil)
		}
	}
	re, note := compileRecall(a.Pattern, a.IgnoreCase)
	if a.ID != "" {
		return note + recallOpen(entries, a.ID, re)
	}
	return note + recallSearch(entries, turns, re, a, c.inConversation())
}

// recallAnnouncement is the line a recall call prints. A call by id alone
// printed `Recall ""`, which said nothing about which message was opened.
func recallAnnouncement(a recallArgs) string {
	switch {
	case a.ID == "":
		return fmt.Sprintf("Recall %q", a.Pattern)
	case a.Pattern == "":
		return "Recall message " + a.ID
	default:
		return fmt.Sprintf("Recall %q in message %s", a.Pattern, a.ID)
	}
}

// recallIDPattern matches a hit's id written as a pattern: "t4.12", or
// "t4\\.12" with the dot escaped.
var recallIDPattern = regexp.MustCompile(`^t(\d+)\\?\.(\d+)$`)

// recallSearch lists the matching messages in scope and out of the
// conversation, newest first, and says what else matched and where.
func recallSearch(entries []recallEntry, turns int, re *regexp.Regexp, a recallArgs, visible map[string]bool) string {
	var hits []recallEntry
	inView, outOfScope := 0, 0
	for _, e := range slices.Backward(entries) {
		if !re.MatchString(e.text) {
			continue
		}
		switch {
		case !e.inScope(a.From):
			outOfScope++
		case visible[e.text]:
			inView++
		default:
			hits = append(hits, e)
		}
	}

	var b strings.Builder
	shown := min(len(hits), a.Newest)
	if len(hits) == 0 {
		b.WriteString("No matches outside this conversation")
	} else {
		fmt.Fprintf(&b, "%s outside this conversation matched, newest first", plural(len(hits), "message", "messages"))
		if shown < len(hits) {
			fmt.Fprintf(&b, "; showing %d (raise newest, or narrow the pattern, for more)", shown)
		}
	}
	b.WriteString(".")
	// Replies the cutoff hid are named. A search for a word the answer used
	// matches tool output as well, and the answer can fall below the cutoff:
	// in doc/experiments/2026-10-recall-models a session that did not raise
	// newest took a line count from a tool result instead of the reply it
	// was asked about. An id says which turn a hidden reply belongs to.
	var hiddenReplies []string
	for _, e := range hits[shown:] {
		if e.role == llm.RoleAssistant {
			hiddenReplies = append(hiddenReplies, e.id())
		}
	}
	if len(hiddenReplies) > 0 {
		more := ""
		if len(hiddenReplies) > recallHiddenIDs {
			more = fmt.Sprintf(" and %d more", len(hiddenReplies)-recallHiddenIDs)
			hiddenReplies = hiddenReplies[:recallHiddenIDs]
		}
		fmt.Fprintf(&b, " Not shown, your own replies: %s%s; pass one as id to see it.", strings.Join(hiddenReplies, ", "), more)
	}
	if inView > 0 {
		fmt.Fprintf(&b, " %d more in messages still in this conversation (not shown).", inView)
	}
	if outOfScope > 0 {
		other := map[string]string{"conversation": "tool output", "tools": "the conversation", "all": ""}[a.From]
		fmt.Fprintf(&b, " %d more in %s: pass from=\"all\" to include them.", outOfScope, other)
	}
	b.WriteString("\n")
	for _, e := range hits[:shown] {
		fmt.Fprintf(&b, "\n%s · turn %d of %d · %s\n", e.id(), e.turn, turns, e.speaker())
		b.WriteString(matchingLines(e.text, re, a.ContextLines, recallLinesPerHit, e.id()))
	}
	return b.String()
}

// matchingLines renders a message's matching lines with their context, at most
// limit matches, each line numbered within the message.
func matchingLines(text string, re *regexp.Regexp, context, limit int, id string) string {
	lines := strings.Split(text, "\n")
	var matches []int
	for i, l := range lines {
		if re.MatchString(l) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		// The pattern spans lines; show where it starts.
		loc := re.FindStringIndex(text)
		matches = []int{strings.Count(text[:loc[0]], "\n")}
	}
	var b strings.Builder
	last := -1
	for _, m := range matches[:min(len(matches), limit)] {
		from, to := max(m-context, last+1), min(m+context, len(lines)-1)
		if last >= 0 && from > last+1 {
			b.WriteString("  ...\n")
		}
		for i := from; i <= to; i++ {
			fmt.Fprintf(&b, "  %d: %s\n", i+1, clipLine(lines[i], re))
		}
		last = to
	}
	if len(matches) > limit {
		fmt.Fprintf(&b, "  (%d more matching lines: recall with id=%q)\n", len(matches)-limit, id)
	}
	return b.String()
}

// clipLine shortens a long line around its first match.
func clipLine(line string, re *regexp.Regexp) string {
	r := []rune(line)
	if len(r) <= recallLineRunes {
		return line
	}
	start := 0
	if loc := re.FindStringIndex(line); loc != nil {
		start = max(0, len([]rune(line[:loc[0]]))-recallLineRunes/3)
	}
	end := min(len(r), start+recallLineRunes)
	out := string(r[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}

// recallOpen shows one entry whole, lines matching re marked with ">"; a nil
// re marks nothing.
func recallOpen(entries []recallEntry, id string, re *regexp.Regexp) string {
	for _, e := range entries {
		if e.id() != id {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s · turn %d · %s\n", e.id(), e.turn, e.speaker())
		lines := strings.Split(e.text, "\n")
		runes := 0
		for i, l := range lines {
			if i >= recallEntryLines || runes > recallEntryRunes {
				fmt.Fprintf(&b, "(%d more lines not shown)\n", len(lines)-i)
				break
			}
			mark := " "
			if re != nil && re.MatchString(l) {
				mark = ">"
			}
			fmt.Fprintf(&b, "%s %d: %s\n", mark, i+1, l)
			runes += len(l)
		}
		return b.String()
	}
	return fmt.Sprintf("No message has the id %q. Ids look like \"t4.3\" and come from a search's results.", id)
}

// runRecallData is recall inside a program: the matching lines as data, so a
// program can filter, count or join them. Matches still in the conversation are
// included and marked: a program is post-processing, and leaving them out would
// make its counts disagree with what the model can see.
func runRecallData(c *Coder, call *bridgedCall) (any, error) {
	raw, err := json.Marshal(call.Args)
	if err != nil {
		return nil, err
	}
	a, msg := parseRecallArgs(llm.ToolCall{Arguments: string(raw)})
	if msg != "" {
		return nil, errors.New(msg)
	}
	if a.Pattern == "" {
		return nil, errors.New("recall in a program needs a \"pattern\"")
	}
	entries, _ := recallEntries(c.SessionRecords())
	re, _ := compileRecall(a.Pattern, a.IgnoreCase)
	visible := c.inConversation()
	var out []map[string]any
	messages := 0
	for _, e := range slices.Backward(entries) {
		if !e.inScope(a.From) || !re.MatchString(e.text) {
			continue
		}
		if messages++; messages > a.Newest {
			break
		}
		lines := strings.Split(e.text, "\n")
		last := -1
		for i, l := range lines {
			if !re.MatchString(l) {
				continue
			}
			for j := max(i-a.ContextLines, last+1); j <= min(i+a.ContextLines, len(lines)-1); j++ {
				out = append(out, map[string]any{
					"id": e.id(), "turn": e.turn, "speaker": e.speaker(),
					"in_conversation": visible[e.text], "line": j + 1,
					"text": clipLine(lines[j], re), "match": j == i,
				})
				last = j
			}
		}
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, nil
}
