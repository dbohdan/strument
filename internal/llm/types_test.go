package llm

import (
	"strings"
	"testing"
)

// The accounting fix, stated as a test. Three separate sums in this codebase —
// countMessages in assemble.go, ChatSummary.count, and checkTokens — go through
// Message.Text(), and renderForSummary renders through it. A block that
// stringified to "" was therefore a zero in three accountings and an invisible
// drop in the fourth, all at once. A label is not the image, but it is true and
// it has a length.
func TestImageBlockStringifiesToALabel(t *testing.T) {
	c := BlocksContent(
		TextBlock("before"),
		ImageBlock(ImageSource{MediaType: "image/png", Label: "diagram.png", Width: 1920, Height: 1080}),
		TextBlock("after"),
	)
	got := c.String()
	for _, want := range []string{"before", "diagram.png", "1920x1080", "after"} {
		if !strings.Contains(got, want) {
			t.Errorf("Content.String() = %q, missing %q", got, want)
		}
	}
	if n := len(Message{Role: RoleUser, Content: c}.Text()); n == 0 {
		t.Error("a message carrying an image counted as zero characters")
	}
}

// Dimensions are optional; a label alone still has to read as an image.
func TestImageWithoutDimensions(t *testing.T) {
	got := ImageSource{MediaType: "image/png", Label: "shot.png"}.String()
	if got != "[image: shot.png]" {
		t.Errorf("got %q", got)
	}
	// With no label at all the media type stands in, so the phrase is never
	// "[image: ]".
	if got := (ImageSource{MediaType: "image/webp"}).String(); got != "[image: image/webp]" {
		t.Errorf("unlabelled image rendered as %q", got)
	}
}

func TestImagesReturnsPayloadsInOrder(t *testing.T) {
	c := BlocksContent(
		ImageBlock(ImageSource{Label: "one"}),
		TextBlock("text"),
		ImageBlock(ImageSource{Label: "two"}),
	)
	got := c.Images()
	if len(got) != 2 || got[0].Label != "one" || got[1].Label != "two" {
		t.Errorf("Images() = %+v", got)
	}
	// Plain-text content has none, and must not panic reaching for blocks that
	// are not there.
	if n := len(TextContent("just text").Images()); n != 0 {
		t.Errorf("plain text reported %d images", n)
	}
}
