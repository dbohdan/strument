package render

import (
	"bytes"
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
	theme Theme
	tool  string

	scan        *ArgScanner
	path        strings.Builder
	line        strings.Builder
	curField    string
	wroteHeader bool
	pending     []string // diff lines held until the header (path) is known
	sawSearch   bool     // the search field has begun streaming
	addedBuf    []string // "+" lines held back until after the "-" lines
}

// NewToolDiff builds a diff renderer for one edit tool call writing to w.
func NewToolDiff(w io.Writer, color bool, theme Theme, tool string) *ToolDiff {
	d := &ToolDiff{w: w, color: color, theme: theme, tool: tool}
	d.scan = NewArgScanner(d.onArg)
	return d
}

// Write feeds the next raw arguments fragment.
func (d *ToolDiff) Write(frag string) { d.scan.Write(frag) }

// Flush emits any buffered partial line, then the held "+" lines; call once
// the tool call is complete.
func (d *ToolDiff) Flush() {
	d.flushLine()
	if !d.wroteHeader && (d.path.Len() > 0 || len(d.pending) > 0 || len(d.addedBuf) > 0) {
		d.header() // resolve the header (path may have streamed after the diff)
	}
	for _, line := range d.addedBuf {
		fmt.Fprint(d.w, line)
	}
	d.addedBuf = nil
}

// expectsPath reports whether the tool has a path/header, so its diff lines
// must wait for it. suggest_command has none — its command line streams live.
func (d *ToolDiff) expectsPath() bool {
	return d.tool == "replace_in_file" || d.tool == "create_file"
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
		// The header can go out once the path field is complete — which is
		// when a later field begins — so diff lines that streamed before the
		// path (some providers order arguments that way) stay under it.
		if d.curField == "path" && !d.wroteHeader && d.path.Len() > 0 {
			d.header()
		}
		d.curField = field
	}
	if field == "path" {
		d.path.WriteString(chunk)
		return
	}
	if field == "search" {
		d.sawSearch = true
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
		// "whole file" is honest whether the file is new or overwritten; the
		// stream can't tell (no filesystem access). The coder's outcome line
		// and tool result carry the created-vs-overwrote truth.
		label += " (whole file)"
	}
	fmt.Fprintf(d.w, "%s\n", label)
	for _, line := range d.pending {
		fmt.Fprint(d.w, line)
	}
	d.pending = nil
}

func (d *ToolDiff) emitLine(field, text string) {
	line := d.formatLine(field, text)
	// A replace field that streams before search must wait so the removed (-)
	// lines still print first, whatever order the provider sent the fields in.
	// This is held separately from the header's pending buffer and appended
	// after every "-" line on Flush.
	if d.holdsAdded(field) {
		d.addedBuf = append(d.addedBuf, line)
		return
	}
	if !d.wroteHeader && d.path.Len() > 0 {
		d.header()
	}
	// An edit tool's diff lines wait for the header even if the path streams
	// after them; a command line (no header) prints straight away.
	if !d.wroteHeader && d.expectsPath() {
		d.pending = append(d.pending, line)
		return
	}
	fmt.Fprint(d.w, line)
}

// holdsAdded reports whether an added ("+") line must be buffered because it
// arrived before the search field it should follow. Only replace_in_file
// reverses this way; create_file's content is the whole diff and streams live.
func (d *ToolDiff) holdsAdded(field string) bool {
	return field == "replace" && d.tool == "replace_in_file" && !d.sawSearch
}

// formatLine renders one diff/command line with its prefix and themed color.
func (d *ToolDiff) formatLine(field, text string) string {
	prefix, color := "-", d.theme.DiffRemoved // search: removed
	switch field {
	case "replace", "content":
		prefix, color = "+", d.theme.DiffAdded // added
	case "command":
		prefix, color = "$", d.theme.Command // suggested command
	}
	if d.color && color != "" {
		return fmt.Sprintf("\x1b[%sm%s %s\x1b[0m\n", color, prefix, text)
	}
	return fmt.Sprintf("%s %s\n", prefix, text)
}

// ToolDiffSet fans a send's streamed tool-call fragments out to a ToolDiff
// per call index, so an Output can forward fragments without tracking indexes
// itself. A non-edit tool (no path/search/replace/content fields) simply
// renders nothing.
//
// Providers may stream several tool calls' arguments interleaved, so only the
// first call renders live; later calls buffer and are appended, each
// contiguous, in first-seen order on Flush. This keeps each diff whole
// instead of interleaving their lines.
type ToolDiffSet struct {
	w     io.Writer
	color bool
	theme Theme
	order []int
	diffs map[int]*ToolDiff
	bufs  map[int]*bytes.Buffer // set for indexes that buffer instead of streaming live
	live  int                   // the index streaming to w; -1 until the first is seen
}

// NewToolDiffSet builds a diff fan-out writing to w.
func NewToolDiffSet(w io.Writer, color bool, theme Theme) *ToolDiffSet {
	return &ToolDiffSet{w: w, color: color, theme: theme, diffs: map[int]*ToolDiff{}, bufs: map[int]*bytes.Buffer{}, live: -1}
}

// Write forwards an argument fragment for the tool call at index, opening a
// fresh diff the first time an index is seen. name is read from the first
// fragment (later fragments carry only args).
func (s *ToolDiffSet) Write(index int, name, frag string) {
	d, ok := s.diffs[index]
	if !ok {
		var out io.Writer
		if s.live == -1 {
			s.live = index
			out = s.w
		} else {
			buf := &bytes.Buffer{}
			s.bufs[index] = buf
			out = buf
		}
		d = NewToolDiff(out, s.color, s.theme, name)
		s.diffs[index] = d
		s.order = append(s.order, index)
	}
	d.Write(frag)
}

// Flush closes every open diff and appends the buffered ones after the live
// one, each whole, in first-seen order; then resets the set.
func (s *ToolDiffSet) Flush() {
	for _, i := range s.order {
		s.diffs[i].Flush()
	}
	for _, i := range s.order {
		if buf := s.bufs[i]; buf != nil {
			_, _ = s.w.Write(buf.Bytes())
		}
	}
	s.order = nil
	clear(s.diffs)
	clear(s.bufs)
	s.live = -1
}
