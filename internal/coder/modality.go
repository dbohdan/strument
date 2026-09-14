package coder

import (
	"fmt"

	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// projectForModel replaces content blocks the active model cannot accept with
// text that says what was there and what became of it.
//
// A projection rather than a refusal, and the reason is /model. Strument
// switches models inside a session, so an image attached under a model that can
// see it is still sitting in doneMessages after a switch to one that cannot.
// Refusing at attach time — the obvious design, and the one this started as —
// does nothing for that case: the conversation is already carrying the image,
// and refusing to send it means the session is stuck until the user clears the
// history. Projecting keeps every conversation sendable to every model, at the
// cost of the model being told, in band, what it is missing.
//
// Every surveyed harness that handles this at all does it this way. OpenCode
// rewrites the part to "ERROR: Cannot read ... (this model does not support
// image input). Inform the user."; DeepSeek Harness has projectImagesForTextModel,
// deterministic and computable from message lengths alone.
//
// Pure: same messages and same model give the same result, with no I/O and no
// state. The slice is returned unchanged — the identical backing array — when
// nothing needs projecting, so the common case allocates nothing and the
// request prefix stays byte-identical for the prompt cache.
func projectForModel(msgs []llm.Message, m *config.Model) []llm.Message {
	if !needsProjection(msgs, m) {
		return msgs
	}
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	for i, msg := range out {
		if len(msg.Content.Blocks) == 0 {
			continue
		}
		blocks := make([]llm.ContentBlock, 0, len(msg.Content.Blocks))
		changed := false
		for _, b := range msg.Content.Blocks {
			if accepts(m, b.Type) {
				blocks = append(blocks, b)
				continue
			}
			changed = true
			// The cache breakpoint rides on the replacement, not on the block
			// that left: dropping it would move every breakpoint after this
			// one and silently cost a cache write.
			text := llm.TextBlock(unavailableText(b))
			text.CacheControl = b.CacheControl
			blocks = append(blocks, text)
		}
		if changed {
			out[i].Content = llm.Content{Blocks: blocks}
		}
	}
	return out
}

// needsProjection reports whether any block would be replaced, so the common
// case can return the caller's own slice.
func needsProjection(msgs []llm.Message, m *config.Model) bool {
	for _, msg := range msgs {
		for _, b := range msg.Content.Blocks {
			if !accepts(m, b.Type) {
				return true
			}
		}
	}
	return false
}

// accepts asks the model, treating an absent model as text-only. A nil Model
// means there is nothing to send to; assuming it can see images would be the
// one guess with a wire error at the end of it.
func accepts(m *config.Model, kind string) bool {
	if m == nil {
		return kind == llm.BlockText
	}
	return m.Accepts(kind)
}

// unavailableText is what the model reads in place of the block.
//
// It names the thing, says plainly that this model cannot take it, and asks for
// the user to be told. The last clause is there because the failure it prevents
// is a confident description of an image nobody sent: a model with a filename
// and no picture can write a plausible paragraph about "the screenshot", and
// the user has no way to tell that apart from vision.
func unavailableText(b llm.ContentBlock) string {
	what := b.String()
	if b.Type == llm.BlockImage {
		return fmt.Sprintf(
			"%s [strument: this model does not accept image input, so the image is not here. "+
				"Tell the user it could not be seen; do not guess at what it showed.]", what)
	}
	return fmt.Sprintf("%s [strument: this model does not accept %s input.]", what, b.Type)
}
