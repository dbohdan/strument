package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/workspace"
)

// The consult tool: the model asks another model a question and gets the
// answer back as a tool result.
//
// Between /consult and a subagent. The consulted model gets no tools and no
// conversation, only the question and the files the asking model named, which
// Strument reads and sends verbatim, so the asking model cannot paraphrase them
// on the way and does not spend its own output tokens copying them. What it can
// get wrong is what it leaves out, and the description says so plainly.
//
// It replaced /consult, where the user typed the question and then decided
// whether the answer became context. That worked, and it made the user the
// courier between two models. The turn boundary stays the user's another way:
// every call is confirmed with the question and the file list on screen, the
// answer is printed as it arrives, and its cost is its own row in the ledger.

const toolConsult = "consult"

// ConsultModel is one entry of consult_models, resolved: the model and what
// the user said to consult it for.
type ConsultModel struct {
	Model   *config.Model
	Purpose string
}

// consultTool is offered when consult_models names at least one model.
func consultTool(models map[string]ConsultModel) llm.ToolDef {
	aliases := slices.Sorted(maps.Keys(models))
	var desc strings.Builder
	desc.WriteString("Ask another model a question and get its answer back. Use it when the user " +
		"asks you to, or when one of the models below is better suited to part of the work " +
		"than you are.\n\n" +
		"The other model sees only what you send: the question, and the files you list in " +
		"\"files\", which Strument reads and includes whole. It cannot see the project, this " +
		"conversation or anything else, and it cannot use tools or ask you anything back. " +
		"Each call starts fresh. So put everything it needs in the question and the files: " +
		"what the work is, what you want from it, and the code or text it is about.\n\n" +
		"The user is shown the question and the file list and asked before each call, and " +
		"sees the answer as it arrives. Each call costs money at the other model's price.\n\n" +
		"Models:\n")
	enum := make([]any, 0, len(aliases))
	for _, a := range aliases {
		m := models[a]
		fmt.Fprintf(&desc, "- %s (%s): %s\n", a, m.Model.QualifiedSlug(), oneLine(m.Purpose))
		enum = append(enum, a)
	}
	return llm.ToolDef{
		Name:        toolConsult,
		Description: desc.String(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model": map[string]any{
					"type":        "string",
					"enum":        enum,
					"description": "Which model to consult.",
				},
				"question": strProp("The question or request, complete in itself: the other model " +
					"has no other context."),
				"files": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Paths of files to include whole, as the read tool takes them.",
				},
			},
			"required":             []string{"model", "question"},
			"additionalProperties": false,
		},
	}
}

type consultCall struct {
	callID   string
	alias    string
	question string
	files    []string
}

func parseConsultArgs(tc llm.ToolCall) (consultCall, string) {
	var a struct {
		Model    string   `json:"model"`
		Question string   `json:"question"`
		Files    []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(tc.Arguments), &a); err != nil {
		return consultCall{}, fmt.Sprintf("The arguments were not valid JSON: %v", err)
	}
	cc := consultCall{callID: tc.ID, alias: strings.TrimSpace(a.Model), question: strings.TrimSpace(a.Question)}
	if cc.alias == "" {
		return consultCall{}, "The required \"model\" argument was missing."
	}
	if cc.question == "" {
		return consultCall{}, "The required \"question\" argument was missing."
	}
	for _, f := range a.Files {
		if f = strings.TrimSpace(f); f != "" && !slices.Contains(cc.files, f) {
			cc.files = append(cc.files, f)
		}
	}
	return cc, ""
}

// consultFile is one file as the consulted model receives it.
type consultFile struct {
	path  string
	text  string
	lines int
}

// consultFiles reads the named files whole, through the same workspace the
// read tool uses: containment, the secret-file refusal and the size limit all
// apply. A file outside the project is asked about the way a read of it is.
//
// All or nothing. One unreadable file refuses the call before the user is
// asked, because a consult missing a file it was meant to have is the failure
// this tool most needs to avoid, and the model can fix the list.
func (c *Coder) consultFiles(paths []string) ([]consultFile, string) {
	out := make([]consultFile, 0, len(paths))
	for _, p := range paths {
		ft, err := c.Files.Read(p, 1, math.MaxInt32)
		var oe *workspace.OutsideError
		if errors.As(err, &oe) {
			if msg := c.askOutside(oe.Path, false); msg != "" {
				return nil, msg
			}
			ft, err = c.Files.Read(p, 1, math.MaxInt32)
		}
		if err != nil {
			return nil, fmt.Sprintf("Did not consult: could not read %s: %v. Nothing was sent.", p, err)
		}
		text := strings.Join(ft.Lines, "\n")
		if ft.EndsWithNewline && len(ft.Lines) > 0 {
			text += "\n"
		}
		out = append(out, consultFile{path: ft.Path, text: text, lines: ft.Total})
	}
	return out, ""
}

// consultIntro opens the message. Written for a reader who has never heard of
// Strument, and fixed, so repeated consults about the same files share a
// cacheable prefix: the files come next and the question last, the part most
// likely to change and the part the model should read most recently.
const consultIntro = "Another AI model, working with a user on a software project, is asking for " +
	"your help. You see only what it sent: the files below, if any, and its question at the end. " +
	"You cannot see the rest of the project or ask anything back, so if something you would " +
	"need is missing, say what it is.\n\n"

// consultPrompt is the one user message the consulted model receives.
func consultPrompt(question string, files []consultFile) string {
	var b strings.Builder
	b.WriteString(consultIntro)
	for _, f := range files {
		fmt.Fprintf(&b, "File %s:\n%s\n\n", f.path, fenced(f.text))
	}
	b.WriteString("The question:\n\n")
	b.WriteString(question)
	b.WriteString("\n")
	return b.String()
}

// consultFitShare is how much of the consulted model's window a request may
// fill, leaving the rest for its answer.
const consultFitShare = 0.75

// runConsult carries one consult call through: check, read, ask, send.
func (c *Coder) runConsult(ctx context.Context, cc consultCall) string {
	cm, ok := c.Consult[cc.alias]
	if !ok {
		return fmt.Sprintf("There is no model %q to consult. Configured: %s.",
			cc.alias, strings.Join(slices.Sorted(maps.Keys(c.Consult)), ", "))
	}
	files, msg := c.consultFiles(cc.files)
	if msg != "" {
		return msg
	}
	prompt := consultPrompt(cc.question, files)
	// Checked before asking, so the user is never asked to approve a request
	// that is then refused on size.
	if limit := cm.Model.Context; limit > 0 && c.Tokens != nil {
		est := c.Tokens.Count(prompt)
		if float64(est) > consultFitShare*float64(limit) {
			return fmt.Sprintf("Did not consult: the question and files come to about %d tokens, more than "+
				"%s's %d-token window leaves room for with an answer. Send fewer or smaller files.",
				est, cc.alias, limit)
		}
	}

	shown := make([]string, 0, len(files))
	for _, f := range files {
		shown = append(shown, fmt.Sprintf("%s (%s)", f.path, plural(f.lines, "line", "lines")))
	}
	if res := c.confirmGrouped(ConfirmRequest{
		Prompt:          fmt.Sprintf("Consult %s?", cc.alias),
		ConsultModel:    fmt.Sprintf("%s (%s)", cc.alias, cm.Model.QualifiedSlug()),
		ConsultQuestion: cc.question,
		ConsultFiles:    shown,
		Group:           toolConsult + ":" + cc.alias,
		Grant:           GrantConsult,
	}); !res.Yes && !res.Always {
		c.Out.Toolf("Did not consult %s (declined)", cc.alias)
		return declined(res, "consult "+cc.alias, GrantConsult)
	}

	c.Out.Toolf("Consulting %s (%s)", cc.alias, cm.Model.QualifiedSlug())
	client := c.Client
	if c.ConsultClient != nil {
		client = c.ConsultClient(cm.Model)
	}
	answer := c.sideCallWithin(ctx, client, cm.Model, prompt)
	if ctx.Err() != nil {
		if strings.TrimSpace(answer) == "" {
			return "The user stopped the consult before " + cc.alias + " answered."
		}
		return fmt.Sprintf("The user stopped the consult. %s's answer so far, which it did not finish:\n\n%s",
			cc.alias, fenced(answer))
	}
	if strings.TrimSpace(answer) == "" {
		return cc.alias + " gave no answer; the reason, if there was one, is on the user's screen."
	}
	return fmt.Sprintf("%s (%s) answered. The answer was written by that model, not by you or the user:\n\n%s",
		cc.alias, cm.Model.QualifiedSlug(), fenced(answer))
}

// sideState is what a side call overwrites: the stream's transient fields and
// the message-scoped usage, which a turn in progress still needs.
type sideState struct {
	client        llm.ModelClient
	model         *config.Model
	response      string
	reasoning     string
	toolCalls     []llm.ToolCall
	toolCallIndex map[int]int
	multi         string

	cost       float64
	costKnown  bool
	sent       int
	received   int
	cacheRead  int
	cacheWrite int
	estimated  bool
	sends      int
	modelTime  time.Duration
	report     string
}

func (c *Coder) saveSideState() sideState {
	return sideState{
		client: c.Client, model: c.Model,
		response: c.partialResponseContent, reasoning: c.partialReasoningContent,
		toolCalls: c.partialToolCalls, toolCallIndex: c.toolCallIndex, multi: c.multiResponseContent,
		cost: c.messageCost, costKnown: c.costKnown,
		sent: c.messageTokensSent, received: c.messageTokensReceived,
		cacheRead: c.messageCacheRead, cacheWrite: c.messageCacheWrite,
		estimated: c.messageEstimated, sends: c.messageSends, modelTime: c.messageModelTime,
		report: c.lastUsageReport,
	}
}

func (c *Coder) restoreSideState(s sideState) {
	c.Client, c.Model = s.client, s.model
	c.partialResponseContent, c.partialReasoningContent = s.response, s.reasoning
	c.partialToolCalls, c.toolCallIndex, c.multiResponseContent = s.toolCalls, s.toolCallIndex, s.multi
	c.messageCost, c.costKnown = s.cost, s.costKnown
	c.messageTokensSent, c.messageTokensReceived = s.sent, s.received
	c.messageCacheRead, c.messageCacheWrite = s.cacheRead, s.cacheWrite
	c.messageEstimated, c.messageSends, c.messageModelTime = s.estimated, s.sends, s.modelTime
	c.lastUsageReport = s.report
}

// sideCallWithin runs a side call in the middle of a turn, under another
// model, and leaves the turn as it found it.
//
// runSide streams through the same fields a turn's step does, and the tool
// calls being answered are among them: appendToolResults walks
// partialToolCalls after this returns, so a side call that cleared it would
// leave every call in the step without a result. The usage is saved for the
// same reason from the other side. The consult's own tokens and cost are
// reported as their own row, under the consulted model, and the turn's
// message totals come back untouched for the turn's own row; the run total
// keeps both.
func (c *Coder) sideCallWithin(ctx context.Context, client llm.ModelClient, model *config.Model, prompt string) string {
	saved := c.saveSideState()
	defer c.restoreSideState(saved)

	c.Client, c.Model = client, model
	c.partialToolCalls, c.toolCallIndex = nil, map[int]int{}
	c.messageCost, c.costKnown = 0, false
	c.messageTokensSent, c.messageTokensReceived = 0, 0
	c.messageCacheRead, c.messageCacheWrite = 0, 0
	c.messageEstimated, c.messageSends, c.messageModelTime = false, 0, 0
	c.lastUsageReport = ""
	return c.runSide(ctx, toolConsult, prompt)
}
