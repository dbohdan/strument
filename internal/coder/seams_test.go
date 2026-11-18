package coder

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/llm"
)

func said(model, text string) llm.Message {
	m := llm.TextMessage(llm.RoleAssistant, text)
	m.Model = model
	return m
}

// seamsOf is which user messages carry which note, as "i:other" or "i:yours".
func seamsOf(msgs []llm.Message) []string {
	var out []string
	for i, m := range msgs {
		switch t := m.Text(); {
		case strings.Contains(t, seamOther):
			out = append(out, strconv.Itoa(i)+":other")
		case strings.Contains(t, seamYours):
			out = append(out, strconv.Itoa(i)+":yours")
		}
	}
	return out
}

// The case that misfired: a session switched to another model for a turn and
// back. The other model's stretch is bookended, and the turns on either side
// stay the reader's own. A single note after the history used to say all of
// it was another model's.
func TestSeamsBookendAnotherModelsStretch(t *testing.T) {
	msgs := []llm.Message{
		llm.TextMessage(llm.RoleUser, "q1"), said("x/mimo", "a1"),
		llm.TextMessage(llm.RoleUser, "q2"), said("o/terra", "a2"),
		llm.TextMessage(llm.RoleUser, "q3"), said("x/mimo", "a3"),
		llm.TextMessage(llm.RoleUser, "q4"),
	}
	got := markModelSeams(msgs, "x/mimo")
	if want := "2:other 4:yours"; strings.Join(seamsOf(got), " ") != want {
		t.Errorf("seams %v, want %s", seamsOf(got), want)
	}
	if !strings.HasSuffix(got[2].Text(), "q2") || !strings.HasPrefix(got[2].Text(), llm.HarnessMarker) {
		t.Errorf("the note is not a harness part before the user's own text: %q", got[2].Text())
	}
	if strings.Contains(msgs[2].Text(), llm.HarnessMarker) {
		t.Error("marking the seams changed the history it read")
	}
}

// Switching away and back with no turn in between changes nothing: the seams
// come from who wrote each reply, not from switch events.
func TestNoSeamsWhenEveryReplyIsTheReaders(t *testing.T) {
	msgs := []llm.Message{
		llm.TextMessage(llm.RoleUser, "q1"), said("x/mimo", "a1"),
		llm.TextMessage(llm.RoleUser, "q2"),
	}
	if got := seamsOf(markModelSeams(msgs, "x/mimo")); len(got) != 0 {
		t.Errorf("seams %v in a conversation that never changed hands", got)
	}
}

// A whole history by another model is marked once at the start, and the
// turn in progress is the reader's.
func TestSeamsAfterASwitch(t *testing.T) {
	msgs := []llm.Message{
		llm.TextMessage(llm.RoleUser, "q1"), said("o/terra", "a1"),
		llm.TextMessage(llm.RoleUser, "q2"), said("o/terra", "a2"),
		llm.TextMessage(llm.RoleUser, "q3"),
	}
	if want := "0:other 4:yours"; strings.Join(seamsOf(markModelSeams(msgs, "x/mimo")), " ") != want {
		t.Errorf("seams %v, want %s", seamsOf(markModelSeams(msgs, "x/mimo")), want)
	}
}

// A row written before the slug was recorded names provider and slug, and a
// provider renamed in the config since is still the same model.
func TestSameModelIgnoresTheProviderName(t *testing.T) {
	if !sameModel("openrouter/xiaomi/mimo", "xiaomi/mimo") || !sameModel("xiaomi/mimo", "xiaomi/mimo") {
		t.Error("the same slug under a provider name was taken for another model")
	}
	if sameModel("openrouter/openai/gpt-6.1-terra", "xiaomi/mimo") {
		t.Error("another model was taken for this one")
	}
}

// Live, through the real request path: a /model switch marks the switch on
// the next request, and the note is never stored in the history.
func TestALiveSwitchIsMarkedOnTheNextRequest(t *testing.T) {
	c := testCoder(t)
	var reqs []llm.Request
	c.Client = &fixture.StreamStub{
		Turns: []fixture.Turn{answer("first"), answer("second")},
		OnRequest: func(_ int, req llm.Request, _ *fixture.Request) error {
			reqs = append(reqs, req)
			return nil
		},
	}
	c.Run(context.Background(), "one")
	other := *c.Model
	other.Slug = "another-model"
	c.SetModel(&other)
	c.Run(context.Background(), "two")

	sent := reqs[len(reqs)-1].Messages
	var notes []string
	for _, m := range sent {
		if m.Role == llm.RoleUser {
			switch t := m.Text(); {
			case strings.Contains(t, seamOther):
				notes = append(notes, "other:"+strings.TrimSpace(t[strings.LastIndex(t, "\n")+1:]))
			case strings.Contains(t, seamYours):
				notes = append(notes, "yours:"+strings.TrimSpace(t[strings.LastIndex(t, "\n")+1:]))
			}
		}
	}
	if want := "other:one yours:two"; strings.Join(notes, " ") != want {
		t.Errorf("notes %v, want %s", notes, want)
	}
	for _, m := range append(c.doneMessages, c.curMessages...) {
		if strings.Contains(m.Text(), "replies from here on") {
			t.Errorf("a seam note was stored in the history: %q", m.Text())
		}
	}
}
