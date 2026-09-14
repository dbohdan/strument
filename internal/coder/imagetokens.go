package coder

import (
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/llm"
)

// Image token estimates.
//
// There is no counting an image the way there is counting text: the number is
// the provider's, computed from dimensions by a formula each one documents
// differently, and Strument has no tokenizer for it. So this is declared rather
// than measured, and the declaration is the point — llm.Money's rule about
// never fabricating an unknown cost applies to tokens too, and an image silently
// worth zero was the bug that made this necessary at all.
//
// Anthropic documents width x height / 750. OpenAI bills a base plus one charge
// per 512-pixel tile. Both are approximations here: a provider that resizes
// before counting will disagree, and OpenRouter routes to whichever backend it
// likes. Treat the number as the right order of magnitude, which is what a
// context guard needs, and not as a bill.
const (
	// anthropicPixelsPerToken is the divisor Anthropic documents.
	anthropicPixelsPerToken = 750

	// openAITileTokens and openAIBaseTokens are the per-tile and fixed parts
	// of the tile formula.
	openAITileTokens = 170
	openAIBaseTokens = 85
	openAITilePixels = 512

	// unknownImageTokens stands in when the dimensions could not be read --
	// a WebP variant this harness does not parse, say. Deliberately on the
	// high side: a guard that under-counts lets a request through that the
	// provider then refuses, which is the failure with a cost attached.
	unknownImageTokens = 1600

	// maxImageTokens caps any single estimate. Providers downscale very large
	// images before counting, so the raw formula runs away where they do not.
	maxImageTokens = 2600
)

// imageTokens estimates what one image adds to a request.
func imageTokens(src llm.ImageSource, adapter string) int {
	if src.Width <= 0 || src.Height <= 0 {
		return unknownImageTokens
	}
	var n int
	switch adapter {
	case config.AdapterAnthropic, config.AdapterOpenCodeAnthropic:
		n = src.Width * src.Height / anthropicPixelsPerToken
	default:
		// Tiles, rounded up in both directions, which is how the tiling works.
		wide := (src.Width + openAITilePixels - 1) / openAITilePixels
		tall := (src.Height + openAITilePixels - 1) / openAITilePixels
		n = openAIBaseTokens + openAITileTokens*wide*tall
	}
	return min(n, maxImageTokens)
}

// countImages estimates the image tokens in a message slice.
//
// Kept apart from countMessages so /tokens can show attachments on a row of
// their own. The reasoning is already written into TokensReport for the tool
// schemas: a cost that is not part of any message's text disappears when it is
// folded into a sum over message text, and the schemas were invisible for
// exactly as long as that was where they lived.
func (c *Coder) countImages(msgs []llm.Message) int {
	adapter := ""
	if c.Model != nil {
		adapter = c.Model.Provider.Adapter
	}
	n := 0
	for _, m := range msgs {
		for _, img := range m.Content.Images() {
			n += imageTokens(img, adapter)
		}
	}
	return n
}
