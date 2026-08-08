package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// defaultReadLines is how many lines one read returns when the caller asks for
// no particular window. Large enough for most source files to arrive whole,
// small enough that a generated file cannot flood the turn.
const defaultReadLines = 2000

// FileText is one file read, with enough context for the caller to tell the
// model what it is looking at and how to see the rest.
type FileText struct {
	Path string
	// Text is the requested window, one line per line, with no numbering
	// applied — the tool layer decides how to present it.
	Lines []string
	// Start is the 1-based line number of Lines[0].
	Start int
	// Total is the file's full line count.
	Total int
	// Truncated reports that the window stops short of the end of the file.
	Truncated bool
}

// Read returns a window of a text file. offset is 1-based; 0 means the start.
// limit is a line count; 0 means defaultReadLines.
//
// Reading is deliberately line-windowed rather than whole-file. A read tool
// that can only return everything makes a large file unusable, and a model
// that receives a silently truncated file will edit against text that is not
// there. Truncated says so, and the tool layer turns it into a paging hint.
func (w *Workspace) Read(rel string, offset, limit int) (FileText, error) {
	rel = path(rel)
	if rel == "" {
		return FileText{}, errors.New("no path given")
	}
	full := filepath.Join(w.Root, filepath.FromSlash(rel))

	info, err := os.Stat(full)
	if err != nil {
		return FileText{}, err
	}
	if info.IsDir() {
		return FileText{}, fmt.Errorf("%s is a directory", rel)
	}
	if info.Size() > w.Limits.fileBytes() {
		return FileText{}, fmt.Errorf("%s is %s, larger than the %s read limit",
			rel, humanBytes(info.Size()), humanBytes(w.Limits.fileBytes()))
	}

	data, err := os.ReadFile(full)
	if err != nil {
		return FileText{}, err
	}
	if isBinary(data) {
		return FileText{}, fmt.Errorf("%s looks like a binary file", rel)
	}

	lines := splitLines(string(data))
	if offset < 1 {
		offset = 1
	}
	if limit <= 0 {
		limit = defaultReadLines
	}

	out := FileText{Path: rel, Start: offset, Total: len(lines)}
	if offset > len(lines) {
		return out, nil
	}
	end := min(offset-1+limit, len(lines))
	out.Lines = lines[offset-1 : end]
	out.Truncated = end < len(lines)
	return out, nil
}

// splitLines splits text into lines without a trailing empty element for a
// file that ends in a newline, so Total matches what an editor reports.
func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text == "" {
		return nil
	}
	text = strings.TrimSuffix(text, "\n")
	return strings.Split(text, "\n")
}

// isBinary reports whether data looks binary rather than text. A NUL byte in
// the first block is what git itself uses, and it is enough here: the cost of
// a wrong guess is a refused read, not a corrupted file.
func isBinary(data []byte) bool {
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	return !utf8.Valid(head) && len(head) > 0
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
