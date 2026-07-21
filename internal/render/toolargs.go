package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ArgScanner incrementally decodes a tool call's JSON arguments object and
// streams the decoded value of each top-level string field to emit(field,
// chunk) as fragments arrive. It tolerates arguments split at any byte
// boundary — mid-escape or mid-UTF-8. Nested and non-string values are
// skipped; the edit tools' arguments are flat string fields
// (path/search/replace/content/command), which is what the diff renderer
// needs. Best-effort: malformed JSON just stops producing sensible output,
// and the authoritative parse happens elsewhere (json.Unmarshal on the whole
// arguments) before anything is applied.
type ArgScanner struct {
	emit func(field, chunk string)

	pending string // incomplete trailing UTF-8 bytes between Writes

	depth     int
	inStr     bool
	esc       bool
	inUni     bool
	uniHex    string
	keyBuf    strings.Builder
	curKey    string
	isKey     bool
	expectVal bool
	field     string // non-empty while streaming a captured value
}

// NewArgScanner returns a scanner that reports decoded field values to emit.
func NewArgScanner(emit func(field, chunk string)) *ArgScanner {
	return &ArgScanner{emit: emit}
}

// Write feeds the next raw arguments fragment.
func (s *ArgScanner) Write(frag string) {
	buf := s.pending + frag
	s.pending = ""
	for len(buf) > 0 {
		if !utf8.FullRuneInString(buf) {
			s.pending = buf // hold an incomplete char until the next fragment
			return
		}
		r, size := utf8.DecodeRuneInString(buf)
		s.step(r)
		buf = buf[size:]
	}
}

func (s *ArgScanner) step(ch rune) {
	if s.inStr {
		s.inString(ch)
		return
	}
	switch ch {
	case '"':
		s.inStr = true
		s.keyBuf.Reset()
		switch {
		case s.depth == 1 && s.expectVal:
			s.isKey = false
			s.field = s.curKey // stream this value under its key
		case s.depth == 1:
			s.isKey = true
			s.field = ""
		default:
			s.isKey = false
			s.field = ""
		}
	case '{', '[':
		s.depth++
	case '}', ']':
		s.depth--
	case ':':
		if s.depth == 1 {
			s.expectVal = true
		}
	case ',':
		if s.depth == 1 {
			s.expectVal = false
		}
	}
}

func (s *ArgScanner) inString(ch rune) {
	if s.inUni {
		s.uniHex += string(ch)
		if len(s.uniHex) == 4 {
			if n, err := strconv.ParseUint(s.uniHex, 16, 32); err == nil && n <= unicode.MaxRune {
				s.put(rune(n))
			}
			s.inUni = false
			s.uniHex = ""
		}
		return
	}
	if s.esc {
		s.esc = false
		switch ch {
		case 'n':
			s.put('\n')
		case 't':
			s.put('\t')
		case 'r':
			s.put('\r')
		case 'b':
			s.put('\b')
		case 'f':
			s.put('\f')
		case 'u':
			s.inUni = true
		default:
			s.put(ch) // ", \, /, and anything else: literal
		}
		return
	}
	switch ch {
	case '\\':
		s.esc = true
	case '"':
		if s.isKey {
			s.curKey = s.keyBuf.String()
		}
		s.inStr = false
		s.field = ""
	default:
		s.put(ch)
	}
}

func (s *ArgScanner) put(r rune) {
	if s.isKey {
		s.keyBuf.WriteRune(r)
	} else if s.field != "" {
		s.emit(s.field, string(r))
	}
}

// ToolDiff renders a streaming edit tool call as a red-green Git-style diff:
// the path on a header line, then search lines as red "-" and replace/content
// lines as green "+". Feed it raw JSON argument fragments; it decodes and
// line-buffers internally. Colors use plain 31/32 (diff convention), gated on
// color.
type ToolDiff struct {
	w     io.Writer
	color bool
	tool  string

	scan        *ArgScanner
	path        strings.Builder
	line        strings.Builder
	curField    string
	wroteHeader bool
}

// NewToolDiff builds a diff renderer for one edit tool call writing to w.
func NewToolDiff(w io.Writer, color bool, tool string) *ToolDiff {
	d := &ToolDiff{w: w, color: color, tool: tool}
	d.scan = NewArgScanner(d.onArg)
	return d
}

// Write feeds the next raw arguments fragment.
func (d *ToolDiff) Write(frag string) { d.scan.Write(frag) }

// Flush emits any buffered partial line; call once the tool call is complete.
func (d *ToolDiff) Flush() {
	d.flushLine()
	if !d.wroteHeader && d.path.Len() > 0 {
		d.header() // an empty create/replace still names the file
	}
}

// isLineField reports whether a field's value renders as diff/command lines.
func isLineField(field string) bool {
	switch field {
	case "search", "replace", "content", "command":
		return true
	default:
		return false
	}
}

func (d *ToolDiff) onArg(field, chunk string) {
	if field != d.curField {
		d.flushLine()
		d.curField = field
	}
	if field == "path" {
		d.path.WriteString(chunk)
		return
	}
	if !isLineField(field) {
		return // purpose/reason/etc. don't render
	}
	for _, r := range chunk {
		if r == '\n' {
			d.emitLine(field, d.line.String())
			d.line.Reset()
		} else {
			d.line.WriteRune(r)
		}
	}
}

func (d *ToolDiff) flushLine() {
	if d.line.Len() > 0 && isLineField(d.curField) {
		d.emitLine(d.curField, d.line.String())
		d.line.Reset()
	}
}

func (d *ToolDiff) header() {
	d.wroteHeader = true
	label := d.path.String()
	if d.tool == "create_file" {
		label += " (new file)"
	}
	fmt.Fprintf(d.w, "%s\n", label)
}

func (d *ToolDiff) emitLine(field, text string) {
	// A path-bearing edit tool prints a header first; command lines
	// (suggest_command) have no path and print on their own.
	if !d.wroteHeader && d.path.Len() > 0 {
		d.header()
	}
	prefix, color := "-", "31" // search: removed, red
	switch field {
	case "replace", "content":
		prefix, color = "+", "32" // added, green
	case "command":
		prefix, color = "$", "36" // suggested command, cyan
	}
	if d.color {
		fmt.Fprintf(d.w, "\x1b[%sm%s %s\x1b[0m\n", color, prefix, text)
	} else {
		fmt.Fprintf(d.w, "%s %s\n", prefix, text)
	}
}

// ToolDiffSet fans a send's streamed tool-call fragments out to a ToolDiff
// per call index, so an Output can forward fragments without tracking indexes
// itself. A non-edit tool (no path/search/replace/content fields) simply
// renders nothing.
type ToolDiffSet struct {
	w     io.Writer
	color bool
	order []int
	diffs map[int]*ToolDiff
}

// NewToolDiffSet builds a diff fan-out writing to w.
func NewToolDiffSet(w io.Writer, color bool) *ToolDiffSet {
	return &ToolDiffSet{w: w, color: color, diffs: map[int]*ToolDiff{}}
}

// Write forwards an argument fragment for the tool call at index, opening a
// fresh diff the first time an index is seen. name is read from the first
// fragment (later fragments carry only args).
func (s *ToolDiffSet) Write(index int, name, frag string) {
	d, ok := s.diffs[index]
	if !ok {
		d = NewToolDiff(s.w, s.color, name)
		s.diffs[index] = d
		s.order = append(s.order, index)
	}
	d.Write(frag)
}

// Flush closes every open diff, in call order, and resets the set.
func (s *ToolDiffSet) Flush() {
	for _, i := range s.order {
		s.diffs[i].Flush()
	}
	s.order = nil
	clear(s.diffs)
}
