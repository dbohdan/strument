// Package render is a Go port of thetarnav/streaming-markdown (smd.js),
// MIT License, Copyright 2024 Damian Tarnawski,
// https://github.com/thetarnav/streaming-markdown — a streaming markdown
// parser: feed it chunks as they arrive from the model and it emits
// add/end-token, text, and attribute events to a Renderer without waiting
// for the document (or even the current line) to finish.
//
// The parser is a character state machine transliterated from smd.js; the
// oracle is smd.js's own test suite, extracted to
// testdata/transliterated/render/smd-cases.json and replayed both as a
// single write and char by char.
package render

// Token identifies a node kind in the streamed markdown tree. Values match
// smd.js's Token enum so the transliterated fixtures compare directly.
type Token int32

// Tree node kinds (smd.js Token values 1..31).
const (
	Document       Token = 1
	Paragraph      Token = 2
	Heading1       Token = 3
	Heading2       Token = 4
	Heading3       Token = 5
	Heading4       Token = 6
	Heading5       Token = 7
	Heading6       Token = 8
	CodeBlock      Token = 9
	CodeFence      Token = 10
	CodeInline     Token = 11
	ItalicAst      Token = 12
	ItalicUnd      Token = 13
	StrongAst      Token = 14
	StrongUnd      Token = 15
	Strike         Token = 16
	Link           Token = 17
	RawURL         Token = 18
	Image          Token = 19
	Blockquote     Token = 20
	LineBreak      Token = 21
	Rule           Token = 22
	ListUnordered  Token = 23
	ListOrdered    Token = 24
	ListItem       Token = 25
	Checkbox       Token = 26
	Table          Token = 27
	TableRow       Token = 28
	TableCell      Token = 29
	EquationBlock  Token = 30
	EquationInline Token = 31
)

// Parser-internal states (smd.js values 101..105). They live in the same
// space as Token because the state machine stores them in Parser.token, but
// they are never sent to a Renderer.
const (
	tokNewline    Token = 101
	tokMaybeURL   Token = 102
	tokMaybeTask  Token = 103
	tokMaybeBR    Token = 104
	tokMaybeEqBlk Token = 105
)

// String returns the smd.js token_to_string name (used in test diffs).
func (t Token) String() string {
	switch t {
	case Document:
		return "Document"
	case Blockquote:
		return "Blockquote"
	case Paragraph:
		return "Paragraph"
	case Heading1:
		return "Heading_1"
	case Heading2:
		return "Heading_2"
	case Heading3:
		return "Heading_3"
	case Heading4:
		return "Heading_4"
	case Heading5:
		return "Heading_5"
	case Heading6:
		return "Heading_6"
	case CodeBlock:
		return "Code_Block"
	case CodeFence:
		return "Code_Fence"
	case CodeInline:
		return "Code_Inline"
	case ItalicAst:
		return "Italic_Ast"
	case ItalicUnd:
		return "Italic_Und"
	case StrongAst:
		return "Strong_Ast"
	case StrongUnd:
		return "Strong_Und"
	case Strike:
		return "Strike"
	case Link:
		return "Link"
	case RawURL:
		return "Raw URL"
	case Image:
		return "Image"
	case LineBreak:
		return "Line_Break"
	case Rule:
		return "Rule"
	case ListUnordered:
		return "List_Unordered"
	case ListOrdered:
		return "List_Ordered"
	case ListItem:
		return "List_Item"
	case Checkbox:
		return "Checkbox"
	case Table:
		return "Table"
	case TableRow:
		return "Table_Row"
	case TableCell:
		return "Table_Cell"
	case EquationBlock:
		return "Equation_Block"
	case EquationInline:
		return "Equation_Inline"
	default:
		return "Unknown"
	}
}

// Attr identifies an attribute set on the current node. Values match
// smd.js's Attr enum.
type Attr int32

// Attribute kinds.
const (
	AttrHref    Attr = 1
	AttrSrc     Attr = 2
	AttrLang    Attr = 4
	AttrChecked Attr = 8
	AttrStart   Attr = 16
)

// HTMLName returns the HTML attribute name for a (smd.js attr_to_html_attr;
// Lang maps to "class" because the default DOM renderer styles code fences
// with a language class).
func (a Attr) HTMLName() string {
	switch a {
	case AttrHref:
		return "href"
	case AttrSrc:
		return "src"
	case AttrLang:
		return "class"
	case AttrChecked:
		return "checked"
	case AttrStart:
		return "start"
	default:
		return "unknown"
	}
}

// headingFromLevel maps a "#" count to a heading token; 6+ clamps to h6.
func headingFromLevel(level int) Token {
	switch level {
	case 1:
		return Heading1
	case 2:
		return Heading2
	case 3:
		return Heading3
	case 4:
		return Heading4
	case 5:
		return Heading5
	default:
		return Heading6
	}
}
