package coder

import (
	"strings"

	"dbohdan.com/strument/internal/llm"
)

// An assistant message is read by the next model as its own past self, so a
// conversation that passed between models needs a seam where it did, or the
// model will defend choices it never made. markModelSeams puts one there.
//
// The seams are computed for each request from the model each assistant
// message records, never stored. That is what makes them right in the cases a
// stored note got wrong. A /model switch and back with no turn in between
// leaves nothing to mark. A live switch and a resumed session read the same
// way. A turn compacted or rewound away is not there to be marked. And the
// note says which turns, where a single note after the history said "the
// turns above" of a session whose recent turns were the reader's own.
//
// The notes go in the user message that opens each stretch, as a text part of
// their own: on the user's side, so no words are put in the model's mouth,
// and in the same message, so no two user messages sit together. They name no
// model. The fact that matters is "not you", and a name invites weighing what
// a turn says by whose it was.

const (
	seamOther = "The replies from here on were made by a different model. They are a record " +
		"of what happened, not decisions you made — where you have reason to disagree with them, " +
		"say so rather than defending them."
	seamYours = "The replies from here on are yours."
)

// markModelSeams returns msgs with a note at each change between replies by
// the model with this slug and replies by any other. It copies only what it
// changes, so the history it reads is left as it was.
func markModelSeams(msgs []llm.Message, slug string) []llm.Message {
	var out []llm.Message
	other := false
	for i, m := range msgs {
		if m.Role != llm.RoleUser {
			continue
		}
		model, known := replyModel(msgs, i, slug)
		if !known {
			continue
		}
		if o := !sameModel(model, slug); o != other {
			other = o
			if out == nil {
				out = append([]llm.Message(nil), msgs...)
			}
			out[i] = withSeam(m, map[bool]string{true: seamOther, false: seamYours}[o])
		}
	}
	if out == nil {
		return msgs
	}
	return out
}

// replyModel is the model that answered the user message at i: the model of
// the first assistant message after it, before the next user message. The
// last user message, which nothing has answered yet, is answered by the
// current model. Anything else unanswered, or answered by a message that
// records no model, is unknown and changes nothing.
func replyModel(msgs []llm.Message, i int, slug string) (string, bool) {
	for _, m := range msgs[i+1:] {
		switch m.Role {
		case llm.RoleUser:
			return "", false
		case llm.RoleAssistant:
			return m.Model, m.Model != ""
		}
	}
	return slug, true
}

// sameModel reports whether a recorded model is the one with this slug. A
// record names the slug, or, written before the slug was recorded on its own,
// the provider and the slug; renaming a provider is not changing models.
func sameModel(recorded, slug string) bool {
	return recorded == slug || strings.HasSuffix(recorded, "/"+slug)
}

// withSeam puts the note before the message's own content, as a text part.
func withSeam(m llm.Message, note string) llm.Message {
	blocks := []llm.ContentBlock{llm.TextBlock(llm.HarnessMarker + " " + note + "\n\n")}
	if m.Content.Text != nil {
		blocks = append(blocks, llm.TextBlock(*m.Content.Text))
	} else {
		blocks = append(blocks, m.Content.Blocks...)
	}
	m.Content = llm.BlocksContent(blocks...)
	return m
}
