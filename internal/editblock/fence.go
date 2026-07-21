package editblock

// Fence is an open/close pair wrapping file content in prompts and model
// replies.
type Fence struct {
	Open, Close string
}

// DefaultFence is triple backticks.
var DefaultFence = Fence{"```", "```"}

// AllFences is the 7-entry escalation list from base_coder.py, in order.
// chooseFence walks it; the golden parser
// test uses it to pick a fence per section.
var AllFences = []Fence{
	{"```", "```"},
	{"````", "````"},
	{"<source>", "</source>"},
	{"<code>", "</code>"},
	{"<pre>", "</pre>"},
	{"<codeblock>", "</codeblock>"},
	{"<sourcecode>", "</sourcecode>"},
}
