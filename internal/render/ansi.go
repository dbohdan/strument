package render

import (
	"io"
	"strconv"
	"strings"
)

// ANSI is a Renderer that streams markdown to a terminal with ANSI styling.
// It is the live-render half of the port: smd.js's default renderer targets
// the DOM, so the terminal texture here is Strument's own (hand-validated
// against aider's REPL feel in phase 7, per the guide's oracle table).
//
// Layout choices: block elements are separated by one blank line (a single
// newline once inside a list), blockquotes get a "│ " prefix on every
// line, list items get bullets or ordinals with a two-space hanging indent
// per level, and tables render as cells joined with " │ " (streaming rules
// out column alignment). Link targets print after the label as " (url)";
// bare URLs print once.
type ANSI struct {
	w     io.Writer
	color bool

	stack      []ansiFrame
	styles     []string
	trailingNL int  // newlines at the tail of the visible output
	wrote      bool // any visible output yet
}

type ansiFrame struct {
	tok      Token
	href     string
	checked  bool
	children int // tokens and text runs rendered into this frame
	ord      int // last ordinal for ListOrdered
	row      int // rows opened so far for Table
	cell     int // cells opened so far for TableRow
}

// NewANSI returns an ANSI renderer writing to w. With color false it emits
// plain text with the same layout and no escape codes.
func NewANSI(w io.Writer, color bool) *ANSI {
	return &ANSI{w: w, color: color, stack: []ansiFrame{{tok: Document}}}
}

// out writes visible output and tracks trailing newlines for separator
// logic.
func (r *ANSI) out(s string) {
	if s == "" {
		return
	}
	_, _ = io.WriteString(r.w, s)
	r.wrote = true

	n := 0
	for n < len(s) && s[len(s)-1-n] == '\n' {
		n++
	}
	if n == len(s) {
		r.trailingNL += n // the whole write was newlines
	} else {
		r.trailingNL = n
	}
}

// esc writes escape sequences without touching the line accounting.
func (r *ANSI) esc(s string) {
	if s != "" {
		_, _ = io.WriteString(r.w, s)
	}
}

// ensureNewlines pads the output so at least n newlines end it. Before any
// output it does nothing: documents don't start with separators.
func (r *ANSI) ensureNewlines(n int) {
	if !r.wrote {
		return
	}
	for r.trailingNL < n {
		r.out("\n")
	}
}

// prefix is what every fresh line inside the current containers starts
// with: a quote bar per blockquote and a hanging indent per list level.
func (r *ANSI) prefix() string {
	var sb strings.Builder
	for _, f := range r.stack {
		switch f.tok {
		case Blockquote:
			sb.WriteString("│ ")
		case ListItem:
			sb.WriteString("  ")
		}
	}
	return sb.String()
}

// lineStart writes the container prefix if the cursor sits on a fresh line.
func (r *ANSI) lineStart() {
	if r.trailingNL > 0 || !r.wrote {
		r.out(r.prefix())
	}
}

func (r *ANSI) sgr(codes string) string {
	if !r.color || codes == "" {
		return ""
	}
	return "\x1b[" + codes + "m"
}

func styleFor(t Token) string {
	switch t {
	case Heading1, Heading2, Heading3, Heading4, Heading5, Heading6,
		StrongAst, StrongUnd:
		return "1"
	case ItalicAst, ItalicUnd:
		return "3"
	case Strike:
		return "9"
	case CodeInline, CodeBlock, CodeFence:
		return "36"
	case Link, RawURL:
		return "4;34"
	case EquationBlock, EquationInline:
		return "2"
	default:
		return ""
	}
}

func (r *ANSI) pushStyle(codes string) {
	if codes == "" {
		return
	}
	r.styles = append(r.styles, codes)
	r.esc(r.sgr(codes))
}

func (r *ANSI) popStyle(codes string) {
	if codes == "" {
		return
	}
	r.styles = r.styles[:len(r.styles)-1]
	r.reapplyStyles()
}

// reapplyStyles resets the terminal and re-enters the still-open styles.
func (r *ANSI) reapplyStyles() {
	r.esc(r.sgr("0"))
	for _, s := range r.styles {
		r.esc(r.sgr(s))
	}
}

func isBlock(t Token) bool {
	switch t {
	case Paragraph, Heading1, Heading2, Heading3, Heading4, Heading5, Heading6,
		CodeBlock, CodeFence, Table, EquationBlock, Blockquote,
		ListUnordered, ListOrdered:
		return true
	default:
		return false
	}
}

// nested reports whether the renderer is inside a list item: nested blocks
// separate with a single newline instead of a blank line.
func (r *ANSI) nested() bool {
	for _, f := range r.stack {
		if f.tok == ListItem {
			return true
		}
	}
	return false
}

// AddToken implements Renderer.
func (r *ANSI) AddToken(t Token) {
	parent := &r.stack[len(r.stack)-1]

	if isBlock(t) {
		switch {
		case parent.children == 0:
			// First child of a fresh container: no blank separator, just a
			// clean line.
			r.ensureNewlines(1)
		case r.nested():
			r.ensureNewlines(1)
		default:
			r.ensureNewlines(2)
		}
	}
	parent.children++

	switch t {
	case LineBreak:
		r.out("\n")
	case Rule:
		r.lineStart()
		r.out(strings.Repeat("─", 40))
	case ListItem:
		r.ensureNewlines(1)
		r.lineStart()
		// parent is the list; the marker takes the item's own two columns.
		if parent.tok == ListOrdered {
			parent.ord++
			r.out(strconv.Itoa(parent.ord) + ". ")
		} else {
			r.out("• ")
		}
	case TableRow:
		r.ensureNewlines(1)
		r.lineStart()
		parent.row++
	case TableCell:
		parent.cell++
		if parent.cell > 1 {
			r.out(" │ ")
		}
	case Heading1, Heading2, Heading3, Heading4, Heading5, Heading6:
		r.lineStart()
		if n := int(t) - int(Heading1) + 1; n <= 3 {
			r.out(strings.Repeat("#", n) + " ")
		}
	default:
	}

	r.stack = append(r.stack, ansiFrame{tok: t})
	r.pushStyle(styleFor(t))
}

// EndToken implements Renderer.
func (r *ANSI) EndToken() {
	f := r.stack[len(r.stack)-1]
	r.popStyle(styleFor(f.tok))
	r.stack = r.stack[:len(r.stack)-1]

	switch f.tok {
	case Checkbox:
		// The space after the checkbox arrives as regular text.
		if f.checked {
			r.out("[x]")
		} else {
			r.out("[ ]")
		}
	case Link, Image:
		if f.href != "" {
			r.esc(r.sgr("2"))
			r.out(" (" + f.href + ")")
			r.reapplyStyles()
		}
	case TableRow:
		if top := &r.stack[len(r.stack)-1]; top.tok == Table && top.row == 1 {
			// Underline the header row.
			r.out("\n" + r.prefix() + strings.Repeat("─", 40))
		}
	case Rule, Paragraph, Heading1, Heading2, Heading3, Heading4, Heading5, Heading6,
		CodeBlock, CodeFence, Table, EquationBlock:
		r.ensureNewlines(1)
	default:
	}
}

// AddText implements Renderer.
func (r *ANSI) AddText(text string) {
	r.stack[len(r.stack)-1].children++
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			r.out("\n")
		}
		if line == "" {
			continue
		}
		r.lineStart()
		r.out(line)
	}
}

// SetAttr implements Renderer.
func (r *ANSI) SetAttr(a Attr, value string) {
	f := &r.stack[len(r.stack)-1]
	switch a {
	case AttrHref:
		if f.tok != RawURL {
			// A raw URL's text is the URL; don't print it twice.
			f.href = value
		}
	case AttrSrc:
		f.href = value
	case AttrChecked:
		f.checked = true
	case AttrLang:
		// Syntax highlighting is deliberately not v1; the fence body
		// renders in the code style regardless of language.
	case AttrStart:
		if n, err := strconv.Atoi(value); err == nil {
			f.ord = n - 1
		}
	}
}
