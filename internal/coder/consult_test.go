package coder

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

func terraModel() *config.Model {
	return &config.Model{
		Provider: config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:     "openai/gpt-6.1-terra",
	}
}

// consultCoder is a coder in a project with one file and one model to consult,
// whose client is stub. The main model's client is left unset: a consult that
// reached it would fail loudly.
func consultCoder(t *testing.T, stub *fixture.StreamStub) (*Coder, *recordingConfirmer) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "help.txt"), []byte("Usage: tally [--sum] FILE\n\nAdds numbers.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := toolCoder(t, dir)
	conf := &recordingConfirmer{answer: true}
	c.Confirm = conf
	c.Consult = map[string]ConsultModel{"terra": {Model: terraModel(), Purpose: "documentation and user-facing prose"}}
	c.ConsultClient = func(*config.Model) llm.ModelClient { return stub }
	return c, conf
}

func consultArgs(t *testing.T, question string, files ...string) consultCall {
	t.Helper()
	cc, msg := parseConsultArgs(llm.ToolCall{ID: "c1", Name: toolConsult,
		Arguments: `{"model":"terra","question":` + jsonString(question) +
			`,"files":` + jsonArray(files) + `}`})
	if msg != "" {
		t.Fatal(msg)
	}
	return cc
}

func jsonArray(xs []string) string {
	parts := make([]string, 0, len(xs))
	for _, x := range xs {
		parts = append(parts, jsonString(x))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Offered only when configured, with each model's purpose in the description:
// the purpose is all the asking model has to decide by.
func TestConsultOfferedOnlyWhenConfigured(t *testing.T) {
	c := testCoder(t)
	if toolNames(c)[toolConsult] {
		t.Fatal("consult offered with nothing configured")
	}
	c.Consult = map[string]ConsultModel{"terra": {Model: terraModel(), Purpose: "documentation and user-facing prose"}}
	i := slices.IndexFunc(c.toolDefs(), func(d llm.ToolDef) bool { return d.Name == toolConsult })
	if i < 0 {
		t.Fatal("consult not offered with a model configured")
	}
	desc := c.toolDefs()[i].Description
	for _, want := range []string{"terra (openrouter/openai/gpt-6.1-terra): documentation and user-facing prose",
		"cannot see the project",
		// Measured: told only to send "the code or text it is about", MiMo
		// named no file in 18 of 30 consults and the style guide in 3, and
		// took the drafts that failed their fact checks as they came.
		"rather than quoting them", "style guide", "Check it against the code"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the description lacks %q:\n%s", want, desc)
		}
	}
}

// A program cannot consult: each call costs money at another model's price,
// and a loop would make one approval cover many.
func TestConsultNotCallableFromRunCode(t *testing.T) {
	c := testCoder(t)
	c.Consult = map[string]ConsultModel{"terra": {Model: terraModel(), Purpose: "prose"}}
	if slices.Contains(c.codeCallableTools(), toolConsult) {
		t.Error("consult is callable from run_code")
	}
}

// The consulted model gets one user message, with no system prompt and no
// tools: the fixed intro, each file whole and verbatim, and the question last.
func TestConsultSendsFilesVerbatimAndTheQuestionLast(t *testing.T) {
	var got llm.Request
	stub := &fixture.StreamStub{
		Turns: []fixture.Turn{answer("Say what --sum does.")},
		OnRequest: func(_ int, req llm.Request, _ *fixture.Request) error {
			got = req
			return nil
		},
	}
	c, _ := consultCoder(t, stub)
	out := c.runConsult(context.Background(), consultArgs(t, "Tighten this help text.", "help.txt"))

	if got.Model != "openai/gpt-6.1-terra" {
		t.Errorf("sent to %q, want the consulted model", got.Model)
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != "user" || len(got.Tools) != 0 {
		t.Fatalf("want one user message and no tools, got %d messages, %d tools", len(got.Messages), len(got.Tools))
	}
	text := got.Messages[0].Text()
	if !strings.HasPrefix(text, consultIntro) {
		t.Errorf("the message does not open with the fixed intro:\n%s", text)
	}
	if !strings.Contains(text, "File help.txt:\n```\nUsage: tally [--sum] FILE\n\nAdds numbers.\n```") {
		t.Errorf("the file is not included whole and verbatim:\n%s", text)
	}
	if !strings.HasSuffix(text, "The question:\n\nTighten this help text.\n") {
		t.Errorf("the question is not last:\n%s", text)
	}
	if !strings.Contains(out, "Say what --sum does.") || !strings.Contains(out, "written by that model") {
		t.Errorf("the result does not carry the attributed answer:\n%s", out)
	}
}

// The confirmation shows everything that leaves for the other provider, names
// the grant that answers it, and scopes "all turn" to one model.
func TestConsultConfirmationShowsWhatLeaves(t *testing.T) {
	c, conf := consultCoder(t, &fixture.StreamStub{Turns: []fixture.Turn{answer("ok")}})
	c.runConsult(context.Background(), consultArgs(t, "Tighten this.", "help.txt"))
	if len(conf.got) != 1 {
		t.Fatalf("asked %d times, want once", len(conf.got))
	}
	req := conf.got[0]
	if req.ConsultModel != "terra (openrouter/openai/gpt-6.1-terra)" || req.ConsultQuestion != "Tighten this." ||
		!slices.Equal(req.ConsultFiles, []string{"help.txt (3 lines)"}) {
		t.Errorf("the prompt shows %+v", req)
	}
	if req.Grant != GrantConsult || req.Group != "consult:terra" {
		t.Errorf("grant %q, group %q", req.Grant, req.Group)
	}
}

// Declined means nothing was sent.
func TestConsultDeclinedSendsNothing(t *testing.T) {
	stub := &fixture.StreamStub{Turns: []fixture.Turn{answer("ok")}}
	c, conf := consultCoder(t, stub)
	conf.answer = false
	out := c.runConsult(context.Background(), consultArgs(t, "Tighten this.", "help.txt"))
	if stub.Remaining() != 1 {
		t.Error("a declined consult reached the model")
	}
	if !strings.Contains(out, "consult terra") {
		t.Errorf("the decline does not say what was declined:\n%s", out)
	}
}

// A file that cannot be sent refuses the whole call before the user is asked:
// a question whose yes is followed by a refusal should not be shown, and a
// consult missing a file it was meant to have is the failure to avoid.
func TestConsultRefusesBeforeAsking(t *testing.T) {
	for _, tc := range []struct {
		name, file, want string
	}{
		{"missing", "nope.txt", "nope.txt"},
		{"secret", ".env", ".env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &fixture.StreamStub{Turns: []fixture.Turn{answer("ok")}}
			c, conf := consultCoder(t, stub)
			if tc.name == "secret" {
				if err := os.WriteFile(filepath.Join(c.Root, ".env"), []byte("KEY=x\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out := c.runConsult(context.Background(), consultArgs(t, "q", "help.txt", tc.file))
			if len(conf.got) != 0 || stub.Remaining() != 1 {
				t.Errorf("asked %d times and sent %d requests, want neither", len(conf.got), 1-stub.Remaining())
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("the refusal does not mention %q:\n%s", tc.want, out)
			}
		})
	}
}

// A request that would fill the consulted model's window is refused on size
// before the user is asked to pay for it.
func TestConsultRefusesWhatWouldNotFit(t *testing.T) {
	c, conf := consultCoder(t, &fixture.StreamStub{Turns: []fixture.Turn{answer("ok")}})
	small := terraModel()
	small.Context = 50
	c.Consult["terra"] = ConsultModel{Model: small, Purpose: "prose"}
	out := c.runConsult(context.Background(), consultArgs(t, strings.Repeat("word ", 100)))
	if len(conf.got) != 0 || !strings.Contains(out, "fewer or smaller") {
		t.Errorf("asked %d times; result:\n%s", len(conf.got), out)
	}
}

// The case the save and restore exist for: a consult in the same step as
// another call, inside a real turn. Both calls get their results, the turn
// carries on to its answer, and the ledger gets the consult as its own row
// under the consulted model, apart from the turn's.
func TestConsultInsideATurnLeavesTheStepWhole(t *testing.T) {
	advisor := &fixture.StreamStub{Turns: []fixture.Turn{answer("Use the imperative.")}}
	c, _ := consultCoder(t, advisor)
	var rows []TurnUsage
	c.RecordUsage = func(u TurnUsage) { rows = append(rows, u) }
	c.Client = &fixture.StreamStub{Turns: []fixture.Turn{
		{Events: []fixture.Event{
			{Kind: "ToolCall", ToolIndex: 0, ToolID: "call_1", ToolName: toolConsult,
				ToolArgs: `{"model":"terra","question":"How should help text read?","files":["help.txt"]}`},
			{Kind: "ToolCall", ToolIndex: 1, ToolID: "call_2", ToolName: toolLS, ToolArgs: `{"path":"."}`},
		}},
		answer("Done."),
	}}

	c.Run(context.Background(), "improve the help text")

	results := map[string]string{}
	for _, m := range c.doneMessages {
		if m.Role == llm.RoleTool {
			results[m.ToolCallID] = m.Text()
		}
	}
	if !strings.Contains(results["call_1"], "Use the imperative.") {
		t.Errorf("the consult's result is %q", results["call_1"])
	}
	if !strings.Contains(results["call_2"], "help.txt") {
		t.Errorf("the ls call in the same step lost its result: %q", results["call_2"])
	}
	if last := c.doneMessages[len(c.doneMessages)-1]; last.Text() != "Done." {
		t.Errorf("the turn did not reach its answer; last message %q", last.Text())
	}
	if c.Model.Slug != "test-model" {
		t.Errorf("the session model is %q after the consult", c.Model.Slug)
	}
	var models []string
	for _, r := range rows {
		models = append(models, r.Model)
	}
	if !slices.Equal(models, []string{"openrouter/openai/gpt-6.1-terra", "openrouter/test-model"}) {
		t.Errorf("ledger rows are for %v, want the consult's then the turn's", models)
	}
}

// A file outside the project is asked about the way a read of it is, under the
// read-outside grant, and a no there sends nothing.
func TestConsultAsksBeforeIncludingAnOutsideFile(t *testing.T) {
	stub := &fixture.StreamStub{Turns: []fixture.Turn{answer("ok")}}
	c, conf := consultCoder(t, stub)
	conf.answer = false
	// Spelled relative to the project: the temporary directory's grant
	// covers absolute spellings only, and a "../" path out of the project
	// reaches the same gate an absolute one elsewhere does.
	mkfile(t, filepath.Join(filepath.Dir(c.Root), "style.md"), "Be brief.\n")
	c.runConsult(context.Background(), consultArgs(t, "q", "../style.md"))
	if len(conf.got) != 1 || conf.got[0].Grant != GrantReadOutside {
		t.Fatalf("asked %+v, want one read-outside question", conf.got)
	}
	if stub.Remaining() != 1 {
		t.Error("a consult whose outside file was declined reached the model")
	}
}

// streamCapture is captureOut with the streamed answer kept in its place,
// so a test can see what the frame encloses.
type streamCapture struct{ captureOut }

func (o *streamCapture) StreamText(s string) { o.lines = append(o.lines, s) }

// The answer on screen is framed: an opening tag carrying the question, since
// a granted consult never shows the confirmation that would, and a named
// closer carrying the model and the cost. The usage line that used to follow
// was the only boundary, and the session model's replies end with the same.
func TestConsultAnswerIsFramedOnScreen(t *testing.T) {
	c, _ := consultCoder(t, &fixture.StreamStub{Turns: []fixture.Turn{answer("# Draft\n\nSay what --sum does.")}})
	out := &streamCapture{}
	c.Out = out
	q := "Draft help text for tally.   Keep it to one screen.\nThe second line stays in the record only."
	c.runConsult(context.Background(), consultArgs(t, q, "help.txt"))

	got := out.String()
	open := "‹consult› terra: Draft help text for tally. Keep it to one screen.\n"
	closer := "‹/consult› terra, openrouter/openai/gpt-6.1-terra: "
	i, j, k := strings.Index(got, open), strings.Index(got, "# Draft"), strings.Index(got, closer)
	if i < 0 || j < 0 || k < 0 || i >= j || j >= k {
		t.Fatalf("want the opening line, the answer, then the closer, in order:\n%s", got)
	}
	if strings.Contains(got, "second line") {
		t.Errorf("the opening line carried more than the question's first line:\n%s", got)
	}
	if strings.Contains(got[k:], "Tokens:") || !strings.Contains(got[k:], "received") {
		t.Errorf("the closer does not carry the usage in its own form:\n%s", got[k:])
	}
}

func TestConsultHeadlineCutsALongQuestion(t *testing.T) {
	h := consultHeadline(strings.Repeat("word ", 60))
	if r := []rune(h); len(r) != consultHeadlineRunes || !strings.HasSuffix(h, "…") {
		t.Errorf("headline is %d runes: %q", len(r), h)
	}
}

// /btw gets the same frame, with the session model's slug in the closer.
func TestAsideAnswerIsFramedOnScreen(t *testing.T) {
	c := testCoder(t)
	out := &streamCapture{}
	c.Out = out
	c.Client = &fixture.StreamStub{Turns: []fixture.Turn{answer("Forty-two.")}}
	c.RunAside(context.Background(), "What is the answer?")
	got := out.String()
	if !strings.HasPrefix(got, "‹btw›\n") ||
		!strings.Contains(got, "Forty-two.\n‹/btw› openrouter/test-model: ") {
		t.Errorf("the aside is not framed:\n%s", got)
	}
}

// A /btw runs between turns, on top of the message totals the last turn left.
// Its closer counts its own sends only: read as they stood, the totals put
// the turn's tokens and cost under the aside's name (seen live: 34.2k sent
// for a five-word answer).
func TestAsideCloserCountsOnlyTheAside(t *testing.T) {
	c := testCoder(t)
	out := &streamCapture{}
	c.Out = out
	c.Client = &fixture.StreamStub{Turns: []fixture.Turn{
		answer(strings.Repeat("word ", 4000)),
		answer("Forty-two."),
	}}
	c.Run(context.Background(), "Say many words.")
	out.reset()
	c.RunAside(context.Background(), "What is the answer?")
	got := out.String()
	i := strings.Index(got, "‹/btw›")
	if i < 0 {
		t.Fatalf("no closer:\n%s", got)
	}
	if line := got[i:]; strings.Contains(line, "k received") {
		t.Errorf("the closer counted the turn before it: %s", line)
	}
}
