// Package jsonlog writes a session's records as JSONL.
//
// One JSON object per line. The format is chosen over an XML-ish stream for
// three reasons, in increasing order of how expensive they are to get wrong:
//
// Escaping is total and automatic. encoding/json escapes every byte that could
// close a string; a hand-written tag stream escapes whatever its author
// remembered. Strument has already shipped one forgeable delimiter — the
// "# ROLE" headers renderForSummary writes into the summarizer's input, which a
// file containing that line can counterfeit — and the two-form reasoning
// delimiter cost a day of scoring. A format whose escaping is somebody else's
// solved problem removes the whole class.
//
// A record stream has no root element, so it is not well-formed XML anyway.
// That means no standard parser, which means a hand-rolled one, which is the
// previous paragraph again.
//
// And a truncated JSONL file loses its last line. A truncated XML document
// cannot be parsed at all. Experiments get interrupted; containers get
// reclaimed.
//
// The honest cost is readability: a long answer becomes one enormous line with
// escaped newlines, which is genuinely unpleasant to read, and that is the one
// place a tag stream would win. It is a *view* problem — `jq -r` fixes it in a
// line — and views are cheap where ambiguous parsing is not. Derivation runs
// one way: readable text falls out of JSONL, safe parsing does not fall out of
// hand-escaped markup.
package jsonlog

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"

	"dbohdan.com/strument/internal/coder"
)

// Writer records a session to a file. It satisfies coder.Recorder.
type Writer struct {
	mu  sync.Mutex
	w   *bufio.Writer
	f   *os.File
	enc *json.Encoder
	// err keeps the first write failure. A log that cannot be written must not
	// take the session down with it — the user asked for a coding session, and
	// the log is instrumentation.
	err error
}

// Create opens path for writing, truncating an existing file.
func Create(path string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	bw := bufio.NewWriter(f)
	enc := json.NewEncoder(bw)
	// No HTML escaping: these records carry source code, and turning every <
	// into < makes the file unreadable for no benefit. Nothing here is
	// interpolated into a web page.
	enc.SetEscapeHTML(false)
	return &Writer{w: bw, f: f, enc: enc}, nil
}

// Record writes one record. Safe to call from any goroutine, because the send
// path and the REPL both reach it.
func (w *Writer) Record(r coder.Record) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return
	}
	if err := w.enc.Encode(r); err != nil {
		w.err = err
		return
	}
	// Flushed per record rather than buffered to the end: a session that is
	// killed — the common way an experiment ends — must still leave every
	// record it emitted before the kill.
	w.err = w.w.Flush()
}

// Err reports the first write failure, if any.
func (w *Writer) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Close flushes and closes the file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.w.Flush(); err != nil && w.err == nil {
		w.err = err
	}
	return w.f.Close()
}

var _ coder.Recorder = (*Writer)(nil)
var _ io.Closer = (*Writer)(nil)
