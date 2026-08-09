package render

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"dbohdan.com/strument/internal/editblock"
)

// ArgScanner incrementally decodes a tool call's JSON arguments object and
// streams the decoded value of each top-level string field to emit(field,
// chunk) as fragments arrive. It tolerates arguments split at any byte
// boundary — mid-escape or mid-UTF-8. Nested and non-string values are
// skipped; the edit tools' arguments are flat string fields
// (path/old_string/new_string/content/command), which is what the diff renderer
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

// ToolDiff renders a tool call's streaming arguments as a Git-style diff: the
// path on a header line, then removed lines in red and added lines in green.
// Feed it raw JSON argument fragments; it decodes and line-buffers internally.
// Colors use plain 31/32 (diff convention), gated on color.
//
// write and bash stream line by line as their arguments arrive — the file's
// contents and the command are each one side of the story, so there is nothing
// to wait for. An edit is different: it carries a before and an after, and only
// with both in hand can the unchanged lines between them be shown as context
// rather than removed and added back. So an edit's two text fields accumulate
// whole and the diff is drawn in Flush. The header still prints the moment the
// path is complete, so the file being edited appears while the rest streams.
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
	noHeader    bool     // the set writes this call's header; see SuppressHeader
	pending     []string // diff lines held until the header (path) is known
	oldText     strings.Builder
	newText     strings.Builder
}

// diffContext is how many unchanged lines are shown on each side of a change,
// as in a unified diff. A longer run is elided down to this with a count, so a
// generous old_string doesn't bury the line that actually changed.
const diffContext = 3

// NewToolDiff builds a diff renderer for one tool call writing to w.
func NewToolDiff(w io.Writer, color bool, theme Theme, tool string) *ToolDiff {
	d := &ToolDiff{w: w, color: color, theme: theme, tool: tool}
	d.scan = NewArgScanner(d.onArg)
	return d
}

// Write feeds the next raw arguments fragment.
func (d *ToolDiff) Write(frag string) { d.scan.Write(frag) }

// Flush emits any buffered partial line, then an edit's diff; call once the
// tool call is complete.
func (d *ToolDiff) Flush() {
	d.flushLine()
	if !d.wroteHeader && (d.path.Len() > 0 || len(d.pending) > 0) {
		d.header() // resolve the header (path may have streamed after the diff)
	}
	if d.isEdit() {
		d.renderEdit()
	}
}

// RendersDiff reports whether a tool's streamed arguments are worth drawing as
// they arrive. Only the two edit tools and bash carry content the user wants to
// watch scroll past — a diff, or the command about to run.
//
// The observation tools must be excluded rather than merely rendering nothing:
// read and ls also take a "path" argument, and without this they would each
// print a bare path line as if it were a diff header, with no diff under it.
func RendersDiff(tool string) bool {
	switch tool {
	case "edit", "write", "bash":
		return true
	default:
		return false
	}
}

// expectsPath reports whether the tool has a path/header, so its diff lines
// must wait for it. bash has none — its command line streams live.
func (d *ToolDiff) expectsPath() bool {
	return d.tool == "edit" || d.tool == "write"
}

// isEdit reports whether this call has two sides to diff against each other.
func (d *ToolDiff) isEdit() bool { return d.tool == "edit" }

// isLineField reports whether a field's value renders as diff/command lines.
func isLineField(field string) bool {
	switch field {
	case "old_string", "new_string", "content", "command":
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
	// An edit's two sides accumulate whole; Flush diffs them. This is also why
	// the provider's field order stops mattering here — old_string arriving
	// after new_string changes nothing.
	if d.isEdit() {
		switch field {
		case "old_string":
			d.oldText.WriteString(chunk)
			return
		case "new_string":
			d.newText.WriteString(chunk)
			return
		}
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

// Label is the header line this diff belongs under, once the path field has
// completed; "" for a tool that has none.
func (d *ToolDiff) Label() string {
	if d.path.Len() == 0 {
		return ""
	}
	label := d.path.String()
	if d.tool == "write" {
		// "whole file" is honest whether the file is new or overwritten; the
		// stream can't tell (no filesystem access). The coder's outcome line
		// and tool result carry the created-vs-overwrote truth.
		label += " (whole file)"
	}
	return label
}

// SuppressHeader stops this diff from writing its own header line, leaving the
// caller to write it. A buffered call uses this: where it lands in the output
// is only settled when the set appends it, and only there can it be known
// whether the file has already been named.
func (d *ToolDiff) SuppressHeader() { d.noHeader = true }

func (d *ToolDiff) header() {
	d.wroteHeader = true
	if !d.noHeader {
		fmt.Fprintf(d.w, "%s\n", d.Label())
	}
	for _, line := range d.pending {
		fmt.Fprint(d.w, line)
	}
	d.pending = nil
}

func (d *ToolDiff) emitLine(field, text string) {
	line := d.formatLine(field, text)
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

// renderEdit draws the completed edit as a diff of its two sides: only the
// lines that actually differ get a "-" or a "+", and the surrounding lines the
// model was asked to include for a unique match print as context.
func (d *ToolDiff) renderEdit() {
	before := splitDiffLines(d.oldText.String())
	after := splitDiffLines(d.newText.String())
	if len(before) == 0 && len(after) == 0 {
		return
	}

	ops := editblock.LineOps(before, after)
	for i, op := range ops {
		switch op.Kind {
		case editblock.OpEqual:
			d.emitContext(before[op.A1:op.A2], i == 0, i == len(ops)-1)
		case editblock.OpDelete:
			d.emitSide("old_string", before[op.A1:op.A2])
		case editblock.OpInsert:
			d.emitSide("new_string", after[op.B1:op.B2])
		case editblock.OpReplace:
			// Removed before added, as a diff reads.
			d.emitSide("old_string", before[op.A1:op.A2])
			d.emitSide("new_string", after[op.B1:op.B2])
		}
	}
}

func (d *ToolDiff) emitSide(field string, lines []string) {
	for _, text := range lines {
		fmt.Fprint(d.w, d.formatLine(field, text))
	}
}

// emitContext prints an unchanged run, showing only the lines next to a
// change. A leading run has nothing above it to give context to, so only its
// tail prints; a trailing run, only its head. What is left out is named rather
// than dropped, so the rendering never reads as the whole edit.
func (d *ToolDiff) emitContext(lines []string, first, last bool) {
	head, tail := diffContext, diffContext
	if first {
		head = 0
	}
	if last {
		tail = 0
	}
	// Eliding one line is a bad trade: the marker costs the line it saves and
	// says less than the line would have. This is also why the marker never has
	// to read "1 unchanged lines".
	if hidden := len(lines) - head - tail; head+tail == 0 || hidden <= 1 {
		d.emitSide("context", lines)
		return
	}
	d.emitSide("context", lines[:head])
	fmt.Fprintf(d.w, "  … %d unchanged lines …\n", len(lines)-head-tail)
	d.emitSide("context", lines[len(lines)-tail:])
}

// splitDiffLines splits a field's text into lines, dropping the empty element a
// trailing newline leaves behind so it doesn't render as a blank added line.
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// formatLine renders one diff/command line with its prefix and themed color.
func (d *ToolDiff) formatLine(field, text string) string {
	prefix, color := "-", d.theme.DiffRemoved // old_string: removed
	switch field {
	case "new_string", "content":
		prefix, color = "+", d.theme.DiffAdded // added
	case "command":
		prefix, color = "$", d.theme.Command // suggested command
	case "context":
		// Unchanged lines carry no color, as in git: they are the backdrop the
		// changed lines stand out against.
		prefix, color = " ", ""
	}
	if d.color && color != "" {
		return fmt.Sprintf("\x1b[%sm%s %s\x1b[0m\n", color, prefix, text)
	}
	return fmt.Sprintf("%s %s\n", prefix, text)
}

// ToolDiffSet fans a send's streamed tool-call fragments out to a ToolDiff
// per call index, so an Output can forward fragments without tracking indexes
// itself. A tool RendersDiff rejects is dropped and draws nothing.
//
// Providers may stream several tool calls' arguments interleaved, so only the
// first call writes straight through; later calls buffer and are appended, each
// contiguous, in first-seen order on Flush. This keeps each diff whole
// instead of interleaving their lines.
type ToolDiffSet struct {
	w     io.Writer
	color bool
	theme Theme
	order []int
	diffs map[int]*ToolDiff
	bufs  map[int]*bytes.Buffer // set for indexes that buffer instead of streaming live
	skip  map[int]bool          // indexes whose tool draws nothing (read/grep/glob/ls/verify)
	live  int                   // the index streaming to w; -1 until the first is seen

	// lastLabel is the header most recently written, so a run of edits to one
	// file names it once. Reset per set, and a set lives for one send.
	lastLabel string
}

// NewToolDiffSet builds a diff fan-out writing to w.
func NewToolDiffSet(w io.Writer, color bool, theme Theme) *ToolDiffSet {
	return &ToolDiffSet{
		w: w, color: color, theme: theme,
		diffs: map[int]*ToolDiff{}, bufs: map[int]*bytes.Buffer{}, skip: map[int]bool{}, live: -1,
	}
}

// Write forwards an argument fragment for the tool call at index, opening a
// fresh diff the first time an index is seen. name is read from the first
// fragment (later fragments carry only args).
func (s *ToolDiffSet) Write(index int, name, frag string) {
	if s.skip[index] {
		return
	}
	d, ok := s.diffs[index]
	if !ok {
		// A tool with nothing to draw is dropped here rather than opening a
		// diff that renders nothing: an observation tool's own one-line outcome
		// is printed when it runs, and a half-rendered header would collide
		// with it.
		if name != "" && !RendersDiff(name) {
			s.skip[index] = true
			return
		}
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
		if s.live != index {
			d.SuppressHeader() // Flush writes it, once its position is settled
		}
		s.diffs[index] = d
		s.order = append(s.order, index)
	}
	d.Write(frag)
}

// Flush closes every open diff and appends the buffered ones after the live
// one, each whole, in first-seen order; then resets the set.
//
// The buffered calls' headers are written here rather than by the diffs
// themselves, because this is the first point at which a call's position in the
// output is settled — and therefore the first point at which "is this the same
// file as the diff above?" has an answer. Several edits to one file print its
// name once and are separated by a blank line, which is what the repetition was
// standing in for.
func (s *ToolDiffSet) Flush() {
	for _, i := range s.order {
		s.diffs[i].Flush()
	}
	// The live call wrote its own header as it streamed, so the file it named is
	// the one a following call has to match against.
	if s.live >= 0 {
		if d := s.diffs[s.live]; d != nil {
			s.lastLabel = d.Label()
		}
	}
	for _, i := range s.order {
		buf := s.bufs[i]
		if buf == nil {
			continue
		}
		switch label := s.diffs[i].Label(); label {
		case "":
			// A tool with no path (bash): nothing to name, nothing to repeat.
		case s.lastLabel:
			fmt.Fprintln(s.w)
		default:
			fmt.Fprintf(s.w, "%s\n", label)
			s.lastLabel = label
		}
		_, _ = s.w.Write(buf.Bytes())
	}
	s.order = nil
	s.lastLabel = ""
	clear(s.diffs)
	clear(s.bufs)
	clear(s.skip)
	s.live = -1
}
