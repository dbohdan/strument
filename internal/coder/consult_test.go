package coder

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// consultStub records the request it was sent and answers with a fixed string.
type consultStub struct {
	req llm.Request
}

func (s *consultStub) Send(_ context.Context, req llm.Request) iter.Seq2[llm.StreamEvent, error] {
	s.req = req
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "an opinion"}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// consultFixture is a session with one pinned file and one turn of history, each
// carrying a token that appears nowhere else. Both directions are checked at
// every rung, which is what makes the ladder a measurement rather than a claim:
// an assertion that only ever looks for presence passes for a scope that sends
// everything.
func consultFixture(t *testing.T) (*Coder, *consultStub) {
	t.Helper()
	c := testCoder(t)
	stub := &consultStub{}
	c.Client = stub

	path := filepath.Join(c.Root, "pinned.go")
	if err := os.WriteFile(path, []byte("package p // FILE-NEEDLE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.AddFile(path)
	c.doneMessages = []llm.Message{
		llm.TextMessage(llm.RoleUser, "the earlier turn said CHAT-NEEDLE"),
		llm.TextMessage(llm.RoleAssistant, "understood"),
	}
	return c, stub
}

func TestConsultScopeDecidesWhatTheAdvisorSees(t *testing.T) {
	tests := []struct {
		name     string
		scope    ConsultScope
		wantFile bool
		wantChat bool
	}{
		{"none", ConsultNothing, false, false},
		{"files", ConsultFiles, true, false},
		{"chat", ConsultChat, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, stub := consultFixture(t)

			c.RunConsult(context.Background(), nil, nil, "what do you think?", tt.scope)

			if len(stub.req.Messages) != 1 {
				t.Fatalf("advisor got %d messages, want 1", len(stub.req.Messages))
			}
			sent := stub.req.Messages[0].Text()
			if got := strings.Contains(sent, "FILE-NEEDLE"); got != tt.wantFile {
				t.Errorf("pinned file content present = %v, want %v:\n%s", got, tt.wantFile, sent)
			}
			if got := strings.Contains(sent, "CHAT-NEEDLE"); got != tt.wantChat {
				t.Errorf("conversation present = %v, want %v:\n%s", got, tt.wantChat, sent)
			}
			// The question is the point and rides at every rung.
			if !strings.Contains(sent, "what do you think?") {
				t.Errorf("the question is missing from the prompt:\n%s", sent)
			}
		})
	}
}

// The advisor's own slug and price are what the ledger records, so a session can
// say what the habit costs. A server-side advisor tool is exactly the design
// that cannot do this, and it is most of why /consult is not one.
func TestRunConsultBillsTheAdvisorAndRestoresTheSessionModel(t *testing.T) {
	c, _ := consultFixture(t)
	sessionModel := c.Model

	advisor := &config.Model{
		Provider:   config.Provider{Adapter: config.AdapterOpenRouter},
		Slug:       "advisor-model",
		EditFormat: "tool",
	}
	advisor.SideModel = advisor
	advisorStub := &consultStub{}

	var billed []string
	c.RecordUsage = func(u TurnUsage) { billed = append(billed, u.Model) }

	c.RunConsult(context.Background(), advisorStub, advisor, "what do you think?", ConsultNothing)

	if advisorStub.req.Model != "advisor-model" {
		t.Errorf("request went out as model %q, want advisor-model", advisorStub.req.Model)
	}
	if len(billed) != 1 || !strings.Contains(billed[0], "advisor-model") {
		t.Errorf("usage recorded for %v, want one row naming advisor-model", billed)
	}
	if c.Model != sessionModel {
		t.Errorf("the session model was not restored: %q", c.Model.Slug)
	}
	if _, ok := c.Client.(*consultStub); !ok || c.Client == advisorStub {
		t.Error("the session client was not restored")
	}
}

// Like /btw and unlike /ask: the exchange is not part of the conversation. What
// reaches the chat is what the REPL adds afterwards, labelled, if the user says
// yes.
func TestRunConsultAddsNothingToTheConversation(t *testing.T) {
	c, _ := consultFixture(t)
	c.curMessages = []llm.Message{llm.TextMessage(llm.RoleUser, "the current turn")}

	if got := c.RunConsult(context.Background(), nil, nil, "what do you think?", ConsultChat); got != "an opinion" {
		t.Errorf("answer = %q, want %q", got, "an opinion")
	}

	if len(c.curMessages) != 1 {
		t.Errorf("curMessages changed: len %d, want 1", len(c.curMessages))
	}
	if len(c.doneMessages) != 2 {
		t.Errorf("doneMessages changed: len %d, want 2", len(c.doneMessages))
	}
}

func TestParseConsultScopeRoundTrips(t *testing.T) {
	for _, name := range ConsultScopeNames {
		scope, ok := ParseConsultScope(name)
		if !ok {
			t.Fatalf("ParseConsultScope(%q) was not recognized", name)
		}
		if got := scope.String(); got != name {
			t.Errorf("%q parsed to a scope that renders as %q", name, got)
		}
	}
	if _, ok := ParseConsultScope("everything"); ok {
		t.Error("ParseConsultScope accepted a name that is not a scope")
	}
}
