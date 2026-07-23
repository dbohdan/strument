package render

import "strings"

// Renderer receives parse events as markdown streams through the Parser.
// AddToken opens a node as a child of the current one and makes it current;
// EndToken closes the current node, returning to its parent; AddText
// appends text to the current node (called any number of times); SetAttr
// sets an attribute on the current node (e.g. a link's href once the
// closing ")" arrives, which may be after the node's text was rendered).
type Renderer interface {
	AddToken(t Token)
	EndToken()
	AddText(text string)
	SetAttr(a Attr, value string)
}

// Parser is the streaming state machine, a transliteration of smd.js's
// parser. One Parser renders one document into one Renderer.
type Parser struct {
	renderer      Renderer
	text          string  // text to be added to the last token in the next flush
	pending       string  // characters not yet identified as a token
	tokens        []Token // current token and its parents (a slice of the tree)
	depth         int     // number of open tokens, without the root
	token         Token   // last token in the tree, or an internal state
	spaces        []int   // per-level indent widths for list nesting
	indent        string
	indentLen     int
	fenceStart    int // for CodeFence and CodeInline parsing
	fenceEnd      int
	blockquoteIdx int
	hrChar        byte
	hrChars       int
	tableState    int
	underBefore   rune // the character just before the current "_" run (0 = line/token boundary)
}

const tokenArrayCap = 24

// NewParser returns a Parser that streams events into renderer. The
// implicit root is Document; the renderer never sees an event for it.
func NewParser(renderer Renderer) *Parser {
	p := &Parser{
		renderer: renderer,
		tokens:   make([]Token, tokenArrayCap),
		spaces:   make([]int, tokenArrayCap),
		token:    Document,
	}
	p.tokens[0] = Document
	return p
}

// Write parses and renders another chunk of markdown.
func (p *Parser) Write(chunk string) {
	for _, char := range chunk {
		p.writeChar(char)
	}
	p.addText()
}

// End finishes rendering: any pending characters are flushed as if the
// stream ended with a newline.
func (p *Parser) End() {
	if len(p.pending) > 0 {
		p.Write("\n")
	}
}

// AtLineStart reports whether the renderer's cursor is at the start of a fresh
// line. Meaningful after End (once pending text is flushed); renderers that
// don't track it are assumed to be at a line start.
func (p *Parser) AtLineStart() bool {
	if a, ok := p.renderer.(interface{ AtLineStart() bool }); ok {
		return a.AtLineStart()
	}
	return true
}

func (p *Parser) addText() {
	if len(p.text) == 0 {
		return
	}
	p.renderer.AddText(p.text)
	p.text = ""
}

func (p *Parser) endToken() {
	p.depth--
	p.token = p.tokens[p.depth]
	p.renderer.EndToken()
}

func (p *Parser) addToken(token Token) {
	// If a list doesn't start with a list item there was a blank line after
	// the list, so the list is over:
	//	1. foo
	//	2. bar
	//	<empty line>
	//	<not a list item>
	if (p.tokens[p.depth] == ListOrdered || p.tokens[p.depth] == ListUnordered) && token != ListItem {
		p.endToken()
	}

	p.depth++
	if p.depth == len(p.tokens) {
		p.tokens = append(p.tokens, 0)
		p.spaces = append(p.spaces, 0)
	}
	p.tokens[p.depth] = token
	p.token = token
	p.renderer.AddToken(token)
}

func (p *Parser) idxOfToken(token Token, startIdx int) int {
	for ; startIdx <= p.depth; startIdx++ {
		if p.tokens[startIdx] == token {
			return startIdx
		}
	}
	return -1
}

// endTokensToLen ends tokens until the parser is depth tokens deep.
func (p *Parser) endTokensToLen(depth int) {
	p.fenceStart = 0
	for p.depth > depth {
		p.endToken()
	}
}

// endTokensToIndent ends tokens that the given indent no longer reaches and
// returns the leftover indent (possibly negative), which the caller replays
// as literal spaces.
func (p *Parser) endTokensToIndent(indent int) int {
	idx := 0
	for i := 0; i <= p.depth; i++ {
		indent -= p.spaces[i]
		if indent < 0 {
			break
		}
		switch p.tokens[i] {
		case CodeBlock, CodeFence, Blockquote, ListItem:
			idx = i
		}
	}

	for p.depth > idx {
		p.endToken()
	}
	return indent
}

// continueOrAddList creates a new list inside the last item when the indent
// is greater than that item's (with prefix), or rewinds to an existing
// compatible list. Reports whether a new list was added.
//
//  1. foo
//     - bar      <- new nested ul
//     - baz   <- new nested ul
//  12. qux    <- cannot nest in "baz" or "bar": new list in "foo"
func (p *Parser) continueOrAddList(listToken Token) bool {
	listIdx := -1
	itemIdx := -1

	for i := p.blockquoteIdx + 1; i <= p.depth; i++ {
		if p.tokens[i] == ListItem {
			if p.indentLen < p.spaces[i] {
				itemIdx = -1
				break
			}
			itemIdx = i
		} else if p.tokens[i] == listToken {
			listIdx = i
		}
	}

	if itemIdx == -1 {
		if listIdx == -1 {
			p.endTokensToLen(p.blockquoteIdx)
			p.addToken(listToken)
			return true
		}
		p.endTokensToLen(listIdx)
		return false
	}
	p.endTokensToLen(itemIdx)
	p.addToken(listToken)
	return true
}

func (p *Parser) addListItem(prefixLen int) {
	p.addToken(ListItem)
	p.spaces[p.depth] = p.indentLen + prefixLen
	p.clearRootPending()
	p.token = tokMaybeTask
}

func (p *Parser) clearRootPending() {
	p.indent = ""
	p.indentLen = 0
	p.pending = ""
}

func isDigit(c rune) bool { return c >= '0' && c <= '9' }

func isDelimiter(c rune) bool {
	switch c {
	case ' ', ':', ';', ')', ',', '!', '.', '?', ']', '\n':
		return true
	default:
		return false
	}
}

func isDelimiterOrNumber(c rune) bool { return isDigit(c) || isDelimiter(c) }

func isAlnum(c rune) bool {
	return isDigit(c) || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// jsLen counts UTF-16 code units, matching JavaScript's String.length for
// the length checks whose strings can hold arbitrary text.
func jsLen(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// charAtIs reports whether s has the rune c (ASCII) at byte index i,
// matching JS's out-of-range-index-is-undefined semantics.
func charAtIs(s string, i int, c rune) bool {
	return i < len(s) && rune(s[i]) == c
}

func (p *Parser) writeChar(char rune) {
	// Newline after a code fence header or inside a fence: count the
	// indent, unwind tokens it no longer reaches, replay the excess.
	if p.token == tokNewline {
		switch char {
		case ' ':
			p.indentLen++
			return
		case '\t':
			p.indentLen += 4
			return
		}

		indent := p.endTokensToIndent(p.indentLen)
		p.indentLen = 0
		p.token = p.tokens[p.depth]
		if indent > 0 {
			p.Write(strings.Repeat(" ", indent))
		}
	}

	pendingWithChar := p.pending + string(char)

	if p.tokenChecks(char, pendingWithChar) {
		return
	}
	if p.commonChecks(char, pendingWithChar) {
		return
	}

	//	foo http://...
	//	    ^
	if p.token != Image && p.token != Link &&
		p.token != EquationBlock && p.token != EquationInline &&
		char == 'h' &&
		(p.pending == " " || p.pending == "") {
		p.text += p.pending
		p.pending = string(char)
		p.token = tokMaybeURL
		return
	}

	// No check hit.
	if char == '_' {
		// Capture the character before a "_" run so emphasis parsing can apply
		// CommonMark's intraword rule. Captured here (as the "_" enters
		// pending) it survives chunk splits like "ansi" | "_text".
		p.underBefore = lastRune(p.pending)
	}
	p.text += p.pending
	p.pending = string(char)
}

// lastRune returns the final rune of s, or 0 if s is empty.
func lastRune(s string) rune {
	last := rune(0)
	for _, r := range s {
		last = r
	}
	return last
}

// tokenChecks runs the token-specific arm of the state machine and reports
// whether the character was consumed.
func (p *Parser) tokenChecks(char rune, pendingWithChar string) bool {
	switch p.token {
	case LineBreak, Document, Blockquote, ListOrdered, ListUnordered:
		if !p.rootPending(char, pendingWithChar) {
			toWrite := pendingWithChar

			switch {
			case p.token == LineBreak:
				// Add a line break and continue in the previous token.
				p.token = p.tokens[p.depth]
				p.renderer.AddToken(LineBreak)
				p.renderer.EndToken()
			case p.indentLen >= 4:
				// Code block. There may be extra spaces after the indent:
				//	_________________________
				//	       code
				//	^^^^----indent
				//	    ^^^-part of code
				//	_________________________
				//	 \t   code
				//	^^-----indent
				//	   ^^^-part of code
				codeStart := 0
				for ; codeStart < 4; codeStart++ {
					if codeStart < len(p.indent) && p.indent[codeStart] == '\t' {
						codeStart++
						break
					}
				}
				codeStart = min(codeStart, len(p.indent))
				toWrite = p.indent[codeStart:] + pendingWithChar
				p.addToken(CodeBlock)
			default:
				p.addToken(Paragraph)
			}

			p.clearRootPending()
			p.Write(toWrite)
		}
		return true
	case Table:
		if p.tableState == 1 {
			switch char {
			case '-', ' ', '|', ':':
				p.pending = pendingWithChar
				return true
			case '\n':
				p.tableState = 2
				p.pending = ""
				return true
			default:
				p.endToken()
				p.tableState = 0
			}
		} else {
			switch p.pending {
			case "|":
				p.addToken(TableRow)
				p.pending = ""
				p.Write(string(char))
				return true
			case "\n":
				p.endToken()
				p.pending = ""
				p.tableState = 0
				p.Write(string(char))
				return true
			}
		}
		return false
	case TableRow:
		switch p.pending {
		case "":
			return false
		case "|":
			p.addToken(TableCell)
			p.endToken()
			p.pending = ""
			p.Write(string(char))
		case "\n":
			p.endToken()
			p.tableState = min(p.tableState+1, 2)
			p.pending = ""
			p.Write(string(char))
		default:
			p.addToken(TableCell)
			p.Write(string(char))
		}
		return true
	case TableCell:
		if p.pending == "|" {
			p.addText()
			p.endToken()
			p.pending = ""
			p.Write(string(char))
			return true
		}
		return false
	case CodeBlock:
		switch pendingWithChar {
		case "\n    ", "\n   \t", "\n  \t", "\n \t", "\n\t":
			p.text += "\n"
			p.pending = ""
		case "\n", "\n ", "\n  ", "\n   ":
			p.pending = pendingWithChar
		default:
			if len(p.pending) != 0 {
				p.addText()
				p.endToken()
				p.pending = string(char)
			} else {
				p.text += string(char)
			}
		}
		return true
	case CodeFence:
		switch char {
		case '`':
			//	```\n<code>\n``??
			//	                ^
			p.pending = pendingWithChar
			return true
		case '\n':
			//	```\n<code>\n```\n
			//	                  ^
			if jsLen(pendingWithChar) == p.fenceStart+p.fenceEnd+1 {
				p.addText()
				p.endToken()
				p.pending = ""
				p.fenceStart = 0
				p.fenceEnd = 0
				p.token = tokNewline
				return true
			}
			p.token = tokNewline
		case ' ':
			//	```\n<code>\n ??
			//	             ^   (space after newline is allowed)
			if len(p.pending) > 0 && p.pending[0] == '\n' {
				p.pending = pendingWithChar
				p.fenceEnd++
				return true
			}
		}
		// Any other char.
		p.text += p.pending
		p.pending = string(char)
		p.fenceEnd = 1
		return true
	case CodeInline:
		switch char {
		case '`':
			space := 0
			if len(p.pending) > 0 && p.pending[0] == ' ' {
				space = 1
			}
			if jsLen(pendingWithChar) == p.fenceStart+space {
				p.addText()
				p.endToken()
				p.pending = ""
				p.fenceStart = 0
			} else {
				p.pending = pendingWithChar
			}
		case '\n':
			p.text += p.pending
			p.pending = ""
			p.token = LineBreak
			p.blockquoteIdx = 0
			p.addText()
		case ' ':
			// Trim the space before a closing `.
			p.text += p.pending
			p.pending = string(char)
		default:
			p.text += pendingWithChar
			p.pending = ""
		}
		return true
	case tokMaybeTask: // checkboxes
		switch len(p.pending) {
		case 0:
			if char == '[' {
				p.pending = pendingWithChar
				return true
			}
		case 1:
			if char == ' ' || char == 'x' {
				p.pending = pendingWithChar
				return true
			}
		case 2:
			if char == ']' {
				p.pending = pendingWithChar
				return true
			}
		case 3:
			if char == ' ' {
				p.renderer.AddToken(Checkbox)
				if p.pending[1] == 'x' {
					p.renderer.SetAttr(AttrChecked, "")
				}
				p.renderer.EndToken()
				p.pending = " "
				return true
			}
		}
		// Not a task item after all: replay as the list item's content.
		p.token = p.tokens[p.depth]
		p.pending = ""
		p.Write(pendingWithChar)
		return true
	case StrongAst, StrongUnd:
		symbol, italic := "*", ItalicAst
		if p.token == StrongUnd {
			symbol, italic = "_", ItalicUnd
		}

		if p.pending == symbol {
			p.addText()
			//	**Bold**
			//	       ^
			if string(char) == symbol {
				p.endToken()
				p.pending = ""
				return true
			}
			//	**Bold*Bold->Em*
			//	       ^
			p.addToken(italic)
			p.pending = string(char)
			return true
		}
		return false
	case ItalicAst, ItalicUnd:
		symbol, strong := "*", StrongAst
		if p.token == ItalicUnd {
			symbol, strong = "_", StrongUnd
		}

		switch p.pending {
		case symbol:
			if string(char) == symbol {
				// Decide between ***bold>em**em* and **bold*bold>em***
				// with the help of the next character.
				if p.tokens[p.depth-1] == strong {
					p.pending = pendingWithChar
				} else {
					//	*em**bold
					//	    ^
					p.addText()
					p.addToken(strong)
					p.pending = ""
				}
			} else {
				//	*em*foo
				//	    ^
				p.addText()
				p.endToken()
				p.pending = string(char)
			}
			return true
		case symbol + symbol:
			italic := p.token
			p.addText()
			p.endToken()
			p.endToken()
			//	***bold>em**em* or **bold*bold>em***
			//	            ^                      ^
			if string(char) != symbol {
				p.addToken(italic)
				p.pending = string(char)
			} else {
				p.pending = ""
			}
			return true
		}
		return false
	case Strike:
		if pendingWithChar == "~~" {
			p.addText()
			p.endToken()
			p.pending = ""
			return true
		}
		return false
	case tokMaybeEqBlk:
		//	\[?  or  $$?
		//	  ^        ^
		if char == '\n' {
			p.addText()
			p.addToken(EquationBlock)
			p.pending = ""
		} else {
			p.token = p.tokens[p.depth]
			if p.pending[0] == '\\' {
				p.text += "["
			} else {
				p.text += "$$"
			}
			p.pending = ""
			p.Write(string(char))
		}
		return true
	case EquationBlock:
		if pendingWithChar == "\\]" || pendingWithChar == "$$" {
			p.addText()
			p.endToken()
			p.pending = ""
			return true
		}
		return false
	case EquationInline:
		if pendingWithChar == "\\)" || (len(p.pending) > 0 && p.pending[0] == '$') {
			p.addText()
			p.endToken()
			if char == ')' {
				p.pending = ""
			} else {
				p.pending = string(char)
			}
			return true
		}
		return false
	case tokMaybeURL:
		switch {
		case pendingWithChar == "http://" || pendingWithChar == "https://":
			p.addText()
			p.addToken(RawURL)
			p.pending = pendingWithChar
			p.text = pendingWithChar
		case charAtIs("http:/", len(p.pending), char) || charAtIs("https:/", len(p.pending), char):
			p.pending = pendingWithChar
		default:
			p.token = p.tokens[p.depth]
			p.Write(string(char))
		}
		return true
	case Link, Image:
		if p.pending == "]" {
			//	[Link](url)
			//	     ^
			p.addText()
			if char == '(' {
				p.pending = pendingWithChar
			} else {
				p.endToken()
				p.pending = string(char)
			}
			return true
		}
		if len(p.pending) >= 2 && p.pending[0] == ']' && p.pending[1] == '(' {
			//	[Link](url)
			//	          ^
			if char == ')' {
				attr := AttrHref
				if p.token == Image {
					attr = AttrSrc
				}
				p.renderer.SetAttr(attr, p.pending[2:])
				p.endToken()
				p.pending = ""
			} else {
				p.pending += string(char)
			}
			return true
		}
		return false
	case RawURL:
		//	http://example.com?
		//	                  ^
		if char == ' ' || char == '\n' || char == '\\' {
			p.renderer.SetAttr(AttrHref, p.pending)
			p.addText()
			p.endToken()
			p.pending = string(char)
		} else {
			p.text += string(char)
			p.pending = pendingWithChar
		}
		return true
	case tokMaybeBR:
		if strings.HasPrefix(pendingWithChar, "<br") {
			if len(pendingWithChar) == 3 || // "<br"
				char == ' ' || // "<br "
				// "<br/" | "<br /"
				(char == '/' && (len(pendingWithChar) == 4 || p.pending[len(p.pending)-1] == ' ')) {
				p.pending = pendingWithChar
				return true
			}

			// "<br>" | "<br/>"
			if char == '>' {
				p.addText()
				p.token = p.tokens[p.depth]
				p.renderer.AddToken(LineBreak)
				p.renderer.EndToken()
				p.pending = ""
				return true
			}
		}
		// Fail: replay without the "<".
		p.token = p.tokens[p.depth]
		p.text += "<"
		p.pending = p.pending[1:]
		p.Write(string(char))
		return true
	default:
		return false
	}
}

// rootPending handles the first pending character at the root of a line
// (Document, Blockquote, list, or after a LineBreak). It reports whether
// the character was consumed; on false the caller starts a paragraph or
// code block from the accumulated pending text.
func (p *Parser) rootPending(char rune, pendingWithChar string) bool {
	if p.pending == "" {
		p.pending = string(char)
		return true
	}

	switch p.pending[0] {
	case ' ':
		p.pending = string(char)
		p.indent += " "
		p.indentLen++
		return true
	case '\t':
		p.pending = string(char)
		p.indent += "\t"
		p.indentLen += 4
		return true
	case '\n':
		// Lists can have one empty line between items:
		//	1. foo
		//	<empty>
		//	2. bar
		if p.tokens[p.depth] == ListItem && p.token == LineBreak {
			p.endToken()
			p.clearRootPending()
			p.pending = string(char)
			return true
		}
		// Exit tokens and ignore newlines in root.
		p.endTokensToLen(p.blockquoteIdx)
		p.clearRootPending()
		p.blockquoteIdx = 0
		p.fenceStart = 0
		p.pending = string(char)
		return true
	case '#': // heading
		switch char {
		case '#':
			if len(p.pending) < 6 {
				p.pending = pendingWithChar
				return true
			}
		case ' ':
			p.endTokensToIndent(p.indentLen)
			p.addToken(headingFromLevel(len(p.pending)))
			p.clearRootPending()
			return true
		}
		return false
	case '>': // blockquote
		// Only when there is no blockquote to the right of blockquoteIdx
		// can a new blockquote be created.
		nextBlockquoteIdx := p.idxOfToken(Blockquote, p.blockquoteIdx+1)
		if nextBlockquoteIdx == -1 {
			p.endTokensToLen(p.blockquoteIdx)
			p.blockquoteIdx++
			p.fenceStart = 0
			p.addToken(Blockquote)
		} else {
			p.blockquoteIdx = nextBlockquoteIdx
		}

		p.clearRootPending()
		p.pending = string(char)
		return true
	case '-', '*', '_':
		// Horizontal rule: "-- - --- - --".
		if p.hrChars == 0 {
			p.hrChars = 1
			p.hrChar = p.pending[0]
		}
		switch char {
		case rune(p.hrChar):
			p.hrChars++
			p.pending = pendingWithChar
			return true
		case ' ':
			p.pending = pendingWithChar
			return true
		case '\n':
			if p.hrChars >= 3 {
				p.endTokensToIndent(p.indentLen)
				p.renderer.AddToken(Rule)
				p.renderer.EndToken()
				p.clearRootPending()
				p.hrChars = 0
				return true
			}
		}
		p.hrChars = 0

		// Unordered list:
		//	* foo
		//	* *bar*
		if p.pending[0] != '_' && len(p.pending) > 1 && p.pending[1] == ' ' {
			p.continueOrAddList(ListUnordered)
			p.addListItem(2)
			p.Write(pendingWithChar[2:])
			return true
		}
		return false
	case '`': // code fence
		//	``?
		//	  ^
		if len(p.pending) < 3 {
			if char == '`' {
				p.pending = pendingWithChar
				p.fenceStart = len(pendingWithChar)
				return true
			}
			p.fenceStart = 0
			return false
		}

		switch char {
		case '`':
			if len(p.pending) == p.fenceStart {
				//	````?
				//	    ^
				p.pending = pendingWithChar
				p.fenceStart = len(pendingWithChar)
			} else {
				//	```code`
				//	       ^
				p.addToken(Paragraph)
				p.clearRootPending()
				p.fenceStart = 0
				p.Write(pendingWithChar)
			}
			return true
		case '\n':
			//	```lang\n
			//	        ^
			p.endTokensToIndent(p.indentLen)
			p.addToken(CodeFence)
			if len(p.pending) > p.fenceStart {
				p.renderer.SetAttr(AttrLang, p.pending[p.fenceStart:])
			}
			p.clearRootPending()
			p.token = tokNewline
			return true
		default:
			//	```lang\n
			//	    ^
			p.pending = pendingWithChar
			return true
		}
	case '+': // unordered list ('-' and '*' are handled with the rule)
		if char != ' ' {
			return false
		}
		p.continueOrAddList(ListUnordered)
		p.addListItem(2)
		return true
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9': // ordered list
		//	12. foo
		//	  ^
		if p.pending[len(p.pending)-1] == '.' {
			if char != ' ' {
				return false
			}
			if p.continueOrAddList(ListOrdered) && p.pending != "1." {
				p.renderer.SetAttr(AttrStart, p.pending[:len(p.pending)-1])
			}
			p.addListItem(len(p.pending) + 1)
			return true
		}
		if char == '.' || isDigit(char) {
			p.pending = pendingWithChar
			return true
		}
		return false
	case '|': // table
		p.endTokensToLen(p.blockquoteIdx)
		p.addToken(Table)
		p.addToken(TableRow)
		p.pending = ""
		p.Write(string(char))
		return true
	default:
		return false
	}
}

// commonChecks runs the pending-prefix checks shared by most token states
// and reports whether the character was consumed.
func (p *Parser) commonChecks(char rune, pendingWithChar string) bool {
	if p.pending == "" {
		return false
	}

	switch p.pending[0] {
	case '\\': // escape character
		if p.token == Image || p.token == EquationBlock || p.token == EquationInline {
			return false
		}

		switch char {
		case '(':
			p.addText()
			p.addToken(EquationInline)
			p.pending = ""
		case '[':
			p.token = tokMaybeEqBlk
			p.pending = pendingWithChar
		case '\n':
			// An escaped newline acts like an unescaped one.
			p.pending = string(char)
		default:
			// Keep the backslash only before alphanumerics.
			p.pending = ""
			if isAlnum(char) {
				p.text += pendingWithChar
			} else {
				p.text += string(char)
			}
		}
		return true
	case '\n': // newline
		switch p.token {
		case Image, EquationBlock, EquationInline:
			return false
		case Heading1, Heading2, Heading3, Heading4, Heading5, Heading6:
			p.addText()
			p.endTokensToLen(p.blockquoteIdx)
			p.blockquoteIdx = 0
			p.pending = string(char)
		default:
			p.addText()
			p.pending = string(char)
			p.token = LineBreak
			p.blockquoteIdx = 0
		}
		return true
	case '<': // <br>
		if p.token == Image || p.token == EquationBlock || p.token == EquationInline {
			return false
		}
		p.addText()
		p.pending = pendingWithChar
		p.token = tokMaybeBR
		return true
	case '`': // `code inline`
		if p.token == Image {
			return false
		}

		if char == '`' {
			p.fenceStart++
			p.pending = pendingWithChar
		} else {
			p.fenceStart++ // started at 0, and the first ` wasn't counted
			p.addText()
			p.addToken(CodeInline)
			if char == ' ' || char == '\n' {
				p.text = "" // trim the leading space
			} else {
				p.text = string(char)
			}
			p.pending = ""
		}
		return true
	case '_', '*':
		if p.token == Image || p.token == EquationBlock ||
			p.token == EquationInline || p.token == StrongAst {
			return false
		}

		italic, strong := ItalicAst, StrongAst
		symbol := rune(p.pending[0])
		if symbol == '_' {
			italic, strong = ItalicUnd, StrongUnd
		}

		// An intraword "_" is not emphasis (CommonMark: "_" needs a
		// non-word char before it to open). "*" has no such restriction, so
		// this gates "_" only. `ansi_text.go` stays literal.
		underword := symbol == '_' && isAlnum(p.underBefore)

		if len(p.pending) == 1 {
			//	**Strong**
			//	 ^
			if symbol == char {
				p.pending = pendingWithChar
				return true
			}
			//	*Em*
			//	 ^
			if char != ' ' && char != '\n' {
				if underword {
					p.text += p.pending // the "_" is literal text
					p.pending = string(char)
					return true
				}
				p.addText()
				p.addToken(italic)
				p.pending = string(char)
				return true
			}
		} else {
			//	***Strong->Em***
			//	  ^
			if symbol == char {
				if underword {
					p.text += pendingWithChar // "___" literal
					p.pending = ""
					return true
				}
				p.addText()
				p.addToken(strong)
				p.addToken(italic)
				p.pending = ""
				return true
			}
			//	**Strong**
			//	  ^
			if char != ' ' && char != '\n' {
				if underword {
					p.text += p.pending // "__" literal
					p.pending = string(char)
					return true
				}
				p.addText()
				p.addToken(strong)
				p.pending = string(char)
				return true
			}
		}
		return false
	case '~': // ~~strike~~
		if p.token != Image && p.token != Strike {
			if p.pending == "~" {
				//	~~Strike~~
				//	 ^
				if char == '~' {
					p.pending = pendingWithChar
					return true
				}
			} else if char != ' ' && char != '\n' {
				//	~~Strike~~
				//	  ^
				p.addText()
				p.addToken(Strike)
				p.pending = string(char)
				return true
			}
		}
		return false
	case '$': // $eq$ | $$eq$$
		if p.token != Image && p.token != Strike && p.pending == "$" {
			//	$$EQUATION_BLOCK$$
			//	 ^
			if char == '$' {
				p.token = tokMaybeEqBlk
				p.pending = pendingWithChar
				return true
			}
			//	$123
			//	 ^
			if isDelimiterOrNumber(char) {
				return false
			}
			//	$EQUATION_INLINE$
			//	 ^
			p.addText()
			p.addToken(EquationInline)
			p.pending = string(char)
			return true
		}
		return false
	case '[': // [Link](url)
		if p.token != Image && p.token != Link &&
			p.token != EquationBlock && p.token != EquationInline &&
			char != ']' {
			p.addText()
			p.addToken(Link)
			p.pending = string(char)
			return true
		}
		return false
	case '!': // ![Image](url)
		if p.token != Image && char == '[' {
			p.addText()
			p.addToken(Image)
			p.pending = ""
			return true
		}
		return false
	case ' ': // collapse runs of spaces
		return len(p.pending) == 1 && char == ' '
	default:
		return false
	}
}
