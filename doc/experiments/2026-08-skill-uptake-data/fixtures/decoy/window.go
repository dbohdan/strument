// Package window provides a fixed-size sliding window over a series of
// samples.
package window

// Window holds the most recent samples, oldest first.
type Window struct {
	buf  []float64
	size int
}

// New returns a window that keeps at most size samples.
func New(size int) *Window {
	return &Window{size: size}
}

// Push appends a sample, discarding the oldest if the window is full.
func (w *Window) Push(v float64) {
	w.buf = append(w.buf, v)
	if len(w.buf) > w.size {
		w.buf = w.buf[1:]
	}
}

// Len reports how many samples the window currently holds.
func (w *Window) Len() int { return len(w.buf) }

// Slice returns the n most recent samples, oldest first. If the window holds
// fewer than n, it returns everything it has.
func (w *Window) Slice(n int) []float64 {
	if n >= len(w.buf) {
		return w.buf
	}
	return w.buf[len(w.buf)-n-1:]
}
