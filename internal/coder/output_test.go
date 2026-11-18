package coder

import (
	"fmt"
	"strings"

	"dbohdan.com/strument/internal/render"
)

// captureOut records the user-facing lines so a test can assert on them; the
// streaming Output methods are no-ops. ToolBlock records through the same
// renderer the real outputs use, so a test asserts on the production shape.
type captureOut struct{ lines []string }

func (o *captureOut) ToolBlock(title, body string) {
	var b strings.Builder
	render.ToolBlock(&b, title, body)
	o.lines = append(o.lines, b.String())
}
func (o *captureOut) Printf(format string, args ...any) {
	o.lines = append(o.lines, fmt.Sprintf(format, args...))
}
func (o *captureOut) Toolf(format string, args ...any)    { o.Printf(format, args...) }
func (o *captureOut) CommandOutput(text string)           { o.Printf("%s", text) }
func (o *captureOut) Warningf(format string, args ...any) { o.Printf(format, args...) }
func (o *captureOut) Errorf(format string, args ...any)   { o.Printf(format, args...) }
func (o *captureOut) Link(target string)                  { o.Printf("%s", target) }
func (o *captureOut) StreamText(string)                   {}
func (o *captureOut) StreamReasoning(string)              {}
func (o *captureOut) StreamToolCall(int, string, string)  {}
func (o *captureOut) FlushStream()                        {}

// String is everything recorded, one line per call.
func (o *captureOut) String() string {
	if len(o.lines) == 0 {
		return ""
	}
	return strings.Join(o.lines, "\n") + "\n"
}

func (o *captureOut) reset() { o.lines = nil }
