package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// defaultReadBinBytes is how many bytes one ReadBytes window returns when the
// caller asks for no particular window. Sized so a window's JSON encoding
// (~3 bytes per byte through the run_code bridge) stays well under the tool
// result cap.
const defaultReadBinBytes = 4096

// maxReadBinBytes caps one ReadBytes window. A larger appetite is paged, not
// granted, for the same reason read is line-windowed: a silently truncated
// result reads as "nothing else exists", and an uncapped window lets one call
// eat the result cap.
const maxReadBinBytes = 64 << 10

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
	// Link is the target when Path is a symlink, so a reader given both the
	// link and the target can tell it is holding one file rather than two.
	Link string
}

// Read returns a window of a text file. offset is 1-based; 0 means the start.
// limit is a line count; 0 means defaultReadLines. An absolute path under the
// standard temporary directory is also allowed.
//
// Reading is deliberately line-windowed rather than whole-file. A read tool
// that can only return everything makes a large file unusable, and a model
// that receives a silently truncated file will edit against text that is not
// there. Truncated says so, and the tool layer turns it into a paging hint.
func (w *Workspace) Read(rel string, offset, limit int) (FileText, error) {
	raw := rel
	full, rel, reason := w.contain(raw)
	if reason != "" {
		return FileText{}, errors.New(reason)
	}
	if rel == "" {
		return FileText{}, errors.New("no path given")
	}
	// Temporary-directory paths are outside the project, so project ignore rules
	// do not apply to them. The path was already restricted to the standard temp
	// directories by contain. A project itself may also live under /tmp, so the
	// original spelling must be absolute before this exception applies.
	absolute := filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`)
	if !absolute || !UnderTempDir(full) {
		// The ignore rules bind here too. They always bound ls, glob, and grep,
		// which is what made this easy to miss: a gitignored .env was invisible to
		// every way of finding it and one guessed filename away from being read.
		if err := w.refuseIgnored(rel, full); err != nil {
			return FileText{}, err
		}
	}

	info, err := os.Stat(full)
	if err != nil {
		return FileText{}, err
	}
	if info.IsDir() {
		return FileText{}, fmt.Errorf("%s is a directory", rel)
	}
	if info.Size() > w.Limits.fileBytes() {
		return FileText{}, fmt.Errorf("%s is %s, larger than the %s read limit",
			rel, HumanBytes(info.Size()), HumanBytes(w.Limits.fileBytes()))
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
	if li, err := os.Lstat(full); err == nil && li.Mode()&os.ModeSymlink != 0 {
		out.Link, _ = os.Readlink(full)
	}
	if offset > len(lines) {
		return out, nil
	}
	end := min(offset-1+limit, len(lines))
	out.Lines = lines[offset-1 : end]
	out.Truncated = end < len(lines)
	return out, nil
}

// FileBytes is one binary read: a window of raw bytes plus enough context for
// the caller to page and to know the whole size.
type FileBytes struct {
	Path string
	// Data is the requested window, raw — no encoding applied; the caller
	// decides how to present it.
	Data []byte
	// Offset is where Data starts, 0-based.
	Offset int64
	// Size is the file's full size.
	Size int64
	// Truncated reports that the window stops short of the end of the file.
	Truncated bool
}

// ReadBytes returns a window of a file's raw bytes. offset is 0-based, unlike
// Read's 1-based line offset — bytes have no line numbers to be 1-based from —
// and limit is a byte count; 0 means defaultReadBinBytes. The containment,
// ignore, and size rules are Read's exactly: the same contain, the same
// refuseIgnored, the same fileBytes limit. Deliberately no isBinary check —
// refusing binaries is Read's job; this is the way in when Read has refused.
//
// It exists for the run_code bridge's read_bin function, not as a tool: the
// observation surface is text-shaped, and this returns data a program computes
// over rather than prose a model reads.
func (w *Workspace) ReadBytes(rel string, offset, limit int64) (FileBytes, error) {
	full, rel, info, err := w.openable(rel, w.Limits.fileBytes())
	if err != nil {
		return FileBytes{}, err
	}

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = defaultReadBinBytes
	}
	if limit > maxReadBinBytes {
		limit = maxReadBinBytes
	}
	if offset > info.Size() {
		offset = info.Size()
	}

	f, err := os.Open(full)
	if err != nil {
		return FileBytes{}, err
	}
	defer f.Close()

	n := min(limit, info.Size()-offset)
	data := make([]byte, n)
	if _, err := io.ReadFull(io.NewSectionReader(f, offset, n), data); err != nil && err != io.EOF {
		return FileBytes{}, err
	}
	return FileBytes{
		Path:      rel,
		Data:      data,
		Offset:    offset,
		Size:      info.Size(),
		Truncated: offset+n < info.Size(),
	}, nil
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

// HumanBytes formats a byte count for a message a person reads. Exported for
// the coder's attachment limits, which refuse in the same units this package
// refuses an oversized read in.
func HumanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// openable runs the checks every whole-file read shares — containment, the
// ignore rules, that the path is a file, and a size ceiling — and returns the
// absolute path, the project-relative one, and the stat.
//
// Extracted rather than copied because the checks are a gate, and a gate with
// two implementations is a gate with one hole in it. ReadImage is the second
// caller and the reason this exists.
func (w *Workspace) openable(rel string, maxBytes int64) (string, string, os.FileInfo, error) {
	raw := rel
	full, rel, reason := w.contain(raw)
	if reason != "" {
		return "", "", nil, errors.New(reason)
	}
	if rel == "" {
		return "", "", nil, errors.New("no path given")
	}
	// Same temp-directory exception as Read, and for the same reason: a
	// project itself may live under /tmp, so the original spelling must be
	// absolute before the exception applies.
	absolute := filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`)
	if !absolute || !UnderTempDir(full) {
		if err := w.refuseIgnored(rel, full); err != nil {
			return "", "", nil, err
		}
	}

	info, err := os.Stat(full)
	if err != nil {
		return "", "", nil, err
	}
	if info.IsDir() {
		return "", "", nil, fmt.Errorf("%s is a directory", rel)
	}
	if info.Size() > maxBytes {
		return "", "", nil, fmt.Errorf("%s is %s, larger than the %s read limit",
			rel, HumanBytes(info.Size()), HumanBytes(maxBytes))
	}
	return full, rel, info, nil
}

// maxImageBytes caps an image the read tool will hand back. The same ceiling
// /attach uses, and for the same reason: providers reject larger ones, and a
// local refusal names the file and the limit where a provider error names
// neither.
const maxImageBytes = 5 << 20

// ReadImage returns an image file whole, with the media type and dimensions
// sniffed from its content.
//
// Unlike AttachFile, this path is gated: the model chooses the argument, so the
// ignore rules and containment apply exactly as they do to read and grep. That
// is the whole difference between the two routes into an image — who named the
// file.
//
// A non-image is not an error here, only a "false": the read tool calls this
// first and falls through to the text path, so "this is not an image" has to be
// cheap and unremarkable rather than something to report.
func (w *Workspace) ReadImage(rel string) (ImageInfo, []byte, bool, error) {
	full, _, _, err := w.openable(rel, maxImageBytes)
	if err != nil {
		return ImageInfo{}, nil, false, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return ImageInfo{}, nil, false, err
	}
	info, reason := SniffImage(data)
	if reason != "" {
		return ImageInfo{}, nil, false, nil
	}
	return info, data, true, nil
}
