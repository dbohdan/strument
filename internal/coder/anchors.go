package coder

import (
	"crypto/sha256"
	"os"
	"strings"
	"sync"

	"dbohdan.com/strument/internal/anchor"
)

// The anchor registry: which identity names which line of which file.
//
// Anchors are minted when a file is read and kept for as long as the lines they
// name are unchanged. That is the property the format exists for — an edit
// elsewhere in the file, including one the model just made, does not move an
// anchor — and it is what makes an anchored edit unambiguous by construction:
// an anchor names one line, so "the text appears three times" cannot happen.
//
// The registry is per session and lives in memory. yoneda keeps its own in the
// XDG cache so anchors survive between runs; Strument does not need that,
// because an anchor is only ever used inside the turn that read the file, and a
// durable store would add an invalidation surface for no reach.
//
// (Unrelated to anchorOf in the webfetch outline code, which names a heading in
// a fetched page. These anchor lines of files the model edits.)
//
// A hash per line is what makes an external change visible. On a re-read, a
// line whose hash still matches keeps its anchor and one that changed gets a
// fresh one, so the model is never handed an identity that points at content it
// has not seen.

// lineHash identifies a line's content. Truncated to 8 bytes: this distinguishes
// versions of one line of one file, not contents of the world, and a collision
// costs a stale anchor rather than a wrong write — the edit still checks that
// the anchor resolves to a line, and the caller still stamps the file.
type lineHash [8]byte

func hashLine(s string) lineHash {
	sum := sha256.Sum256([]byte(s))
	var h lineHash
	copy(h[:], sum[:8])
	return h
}

// fileAnchors is one file's registry: anchors and hashes aligned with its lines.
type fileAnchors struct {
	ids    []anchor.Anchor
	hashes []lineHash
}

// anchorRegistry holds every file the session has anchored.
type anchorRegistry struct {
	mu     sync.Mutex
	files  map[string]*fileAnchors
	supply anchor.Supply
}

func newAnchorRegistry(s anchor.Supply) *anchorRegistry {
	if s == nil {
		s = anchor.CryptoSupply{}
	}
	return &anchorRegistry{files: map[string]*fileAnchors{}, supply: s}
}

// sync brings rel's registry into line with lines and returns the anchors, one
// per line. A line whose content is unchanged from the last sync keeps its
// anchor; every other line gets a fresh one.
//
// Matching is positional, then by content: a line that moved because an edit
// above it inserted or removed lines is found by its hash, so an anchor follows
// its line rather than its offset. That is what an anchor is for, and doing it
// any other way would make the identity a line number with extra steps.
func (r *anchorRegistry) sync(rel string, lines []string) []anchor.Anchor {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	prev := r.files[rel]
	out := make([]anchor.Anchor, len(lines))
	hashes := make([]lineHash, len(lines))
	taken := map[anchor.Anchor]bool{}

	// Index the previous version by content hash. A hash occurring several
	// times (blank lines, closing braces) hands out its anchors in order, so
	// identical lines keep distinct identities and nothing is reused twice.
	byHash := map[lineHash][]anchor.Anchor{}
	if prev != nil {
		for i, h := range prev.hashes {
			byHash[h] = append(byHash[h], prev.ids[i])
		}
	}

	var need []int
	for i, line := range lines {
		h := hashLine(line)
		hashes[i] = h
		if q := byHash[h]; len(q) > 0 {
			out[i] = q[0]
			byHash[h] = q[1:]
			taken[out[i]] = true
			continue
		}
		need = append(need, i)
	}
	for i, a := range anchor.Mint(len(need), taken, r.supply) {
		out[need[i]] = a
	}

	r.files[rel] = &fileAnchors{ids: out, hashes: hashes}
	return out
}

// resolve reports the 0-based line an anchor names, and whether the registry
// knows it at all. An anchor from another file, or from before a re-mint, is
// not found — which the caller turns into "read the file again" rather than a
// guess.
func (r *anchorRegistry) resolve(rel string, a anchor.Anchor) (int, bool) {
	if r == nil {
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.files[rel]
	if f == nil {
		return 0, false
	}
	for i, id := range f.ids {
		if id == a {
			return i, true
		}
	}
	return 0, false
}

// known reports whether rel has been anchored this session, so the caller can
// tell "you sent an anchor for a file you never read" from "that anchor is
// stale".
func (r *anchorRegistry) known(rel string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.files[rel] != nil
}

// renderAnchored is the read format: anchor, a tab, the line verbatim.
//
// A tab rather than yoneda's ║. The heavy bar is three tokens against a tab's
// one, twice a row, and measured over this repository it was more than half the
// format's whole overhead — for a character that carries no information
// (doc/experiments/2026-09-anchored-edit-phase0.md). Indentation stays in the
// content: naming it in words costs more than the whitespace does, because any
// run of whitespace is already a single token.
func renderAnchored(ids []anchor.Anchor, lines []string) string {
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(string(ids[i]))
		b.WriteByte('\t')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// anchorRows renders one read window as anchored rows, or "" when anchored
// edits are off — in which case the caller keeps the numbered format.
//
// It syncs the registry against the *whole* file, not the window, and then
// slices. An anchor is an identity within a file: reading lines 40-60 twice has
// to give those lines the same anchors, and reading the whole file afterwards
// has to agree with both. Syncing per window would re-mint the rest of the file
// every time and make a windowed read a way to lose anchors the model is
// holding.
func (c *Coder) anchorRows(rel string, start, count int) string {
	if !c.AnchoredEdits {
		return ""
	}
	data, err := os.ReadFile(c.fullPath(rel))
	if err != nil {
		return "" // fall back to the numbered format rather than failing a read
	}
	all := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	ids := c.anchors.sync(rel, all)
	if start < 0 || start >= len(ids) {
		return ""
	}
	end := min(start+count, len(ids))
	return renderAnchored(ids[start:end], all[start:end])
}

// resolveEdit turns an anchored edit into the new file content.
//
// There is no matching here and that is the point. The anchors name a line
// range; the range is replaced. An anchor that the registry does not know is
// refused with a message saying so, because the alternative — finding the
// nearest plausible lines — is exactly the guessing this format was built to
// remove, and it would be guessing with more confidence than a search does.
func (c *Coder) resolveEdit(e plannedEdit, content string) (newContent, failure string) {
	a, ok := anchor.Parse(e.anchor)
	if !ok {
		return "", "Not an anchor: " + quoteToolArg(e.anchor) + ". An anchor is two " +
			"dash-joined words as read printed them, like copper-otter.\n"
	}
	end := a
	if e.endAnchor != "" {
		if end, ok = anchor.Parse(e.endAnchor); !ok {
			return "", "Not an anchor: " + quoteToolArg(e.endAnchor) + ".\n"
		}
	}

	if !c.anchors.known(e.path) {
		return "", "No anchors for " + quoteToolArg(e.path) + " in this session: read it first, " +
			"and edit using the anchors that read prints.\n"
	}
	from, ok := c.anchors.resolve(e.path, a)
	if !ok {
		return "", "Anchor " + quoteToolArg(string(a)) + " does not name a line in " +
			quoteToolArg(e.path) + " any more, so nothing was changed.\n" +
			"Either that line changed, or the anchor is from another file. Read it again " +
			"and use the anchors from that read.\n"
	}
	to, ok := c.anchors.resolve(e.path, end)
	if !ok {
		return "", "Anchor " + quoteToolArg(string(end)) + " does not name a line in " +
			quoteToolArg(e.path) + " any more, so nothing was changed.\nRead it again.\n"
	}
	if to < from {
		return "", "The range runs backwards: " + quoteToolArg(string(a)) + " comes after " +
			quoteToolArg(string(end)) + " in " + quoteToolArg(e.path) + ".\n"
	}

	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if from >= len(lines) || to >= len(lines) {
		// The registry and the file disagree, which means the file moved
		// between the read and now. Say that rather than splicing on stale
		// coordinates.
		return "", "The anchors for " + quoteToolArg(e.path) + " no longer fit the file, " +
			"so nothing was changed. Read it again.\n"
	}

	var b strings.Builder
	for _, l := range lines[:from] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	// An empty replacement deletes the range. The trailing newline is the
	// caller's convention: every line of the file carries one.
	if e.replace != "" {
		b.WriteString(strings.TrimSuffix(e.replace, "\n"))
		b.WriteByte('\n')
	}
	for _, l := range lines[to+1:] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String(), ""
}

// anchorDigest is the tool result for an applied anchored edit: the identities
// the edit produced, and nothing else.
//
// No content is echoed. The model knows what it wrote; what it does not know is
// how to address the lines it just made, and without that its next edit to the
// same file has to re-read. That round trip is most of what anchors are for, so
// spending a few tokens here to avoid it is the trade the format is making.
func (c *Coder) anchorDigest(rel, newContent string) string {
	lines := strings.Split(strings.TrimSuffix(newContent, "\n"), "\n")
	ids := c.anchors.sync(rel, lines)
	var fresh []string
	for i, a := range ids {
		if i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			fresh = append(fresh, string(a))
		}
	}
	if len(fresh) > 8 {
		fresh = fresh[:8]
	}
	return "Applied. New anchors for " + quoteToolArg(rel) + ": " +
		strings.Join(fresh, ", ") + " (read again for the rest).\n"
}

// forget drops rel's anchors. Called when the harness put a file into a state
// the registry does not describe — a rolled-back batch, an undo — so the next
// anchored edit is told to read again rather than splicing on coordinates for
// content that is no longer there. Bounds checks in resolveEdit would catch the
// grossest of those; this catches the rest.
func (r *anchorRegistry) forget(rel string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.files, rel)
}

// forgetAll drops every file's anchors.
func (r *anchorRegistry) forgetAll() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.files)
}
