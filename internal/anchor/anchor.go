// Package anchor mints and validates the stable per-line identities the
// anchored read format addresses lines by.
//
// An anchor is two dash-joined lowercase words: `copper-otter`. It carries no
// line number and no content — it is a pure identity for one line of one file,
// and only the registry that minted it knows where it points.
//
// Why words rather than a random string of characters: they tokenize well.
// Every word here is common enough to be a single token in the tokenizers this
// project's model panel uses, so an anchor costs about as much as the
// right-aligned line number it replaces — measured at +0.55 tokens a line, 4.2%,
// over the numbered format (doc/experiments/2026-09-anchored-edit-m1.md). A
// random id like `ve7` looks shorter and tokenizes worse.
//
// Why random rather than derived from the line's content: anchors must be
// stable — an edit elsewhere in the file must not change them — and unique,
// because blank lines and closing braces would otherwise share an address.
// Randomness gives both by construction.
//
// Minting is pure. Randomness enters as a Supply and is consumed two bytes per
// word, so a caller with a fixed supply gets deterministic anchors, which is
// what the golden tests pin.
package anchor

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
)

// Anchor is one line's identity. The zero value is invalid; construct with
// Parse or Mint.
type Anchor string

// String renders the anchor as it appears in a read row.
func (a Anchor) String() string { return string(a) }

// Parse validates an anchor as the model wrote it: trimmed, lowercased, two
// dash-joined runs of ASCII lowercase letters. Anything else — digits, spaces,
// a leading or trailing dash, one word, three — is rejected rather than
// coerced, because a near-miss anchor is a model that has lost track of which
// line it means, and guessing on its behalf is the failure this format exists
// to remove.
func Parse(s string) (Anchor, bool) {
	t := strings.ToLower(strings.TrimSpace(s))
	first, rest, found := strings.Cut(t, "-")
	if !found || !isWord(first) || !isWord(rest) {
		return "", false
	}
	return Anchor(t), true
}

func isWord(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

// Supply is the randomness minting draws on. Read fills p and reports how much
// it filled; a supply that runs out makes Mint fall back to cycling, which
// keeps minting total rather than making it fail on an input it cannot
// influence.
type Supply interface {
	Read(p []byte) (int, error)
}

// CryptoSupply is the production supply.
type CryptoSupply struct{}

func (CryptoSupply) Read(p []byte) (int, error) { return rand.Read(p) }

// FixedSupply hands out a repeating byte sequence, so a test gets the same
// anchors every run.
type FixedSupply struct {
	Bytes []byte
	pos   int
}

func (f *FixedSupply) Read(p []byte) (int, error) {
	if len(f.Bytes) == 0 {
		return 0, nil
	}
	for i := range p {
		p[i] = f.Bytes[f.pos%len(f.Bytes)]
		f.pos++
	}
	return len(p), nil
}

// Mint returns n distinct anchors, avoiding every anchor in taken.
//
// Taking the used set rather than minting blind is what lets a file keep the
// anchors of lines an edit did not touch: the caller mints only for the lines
// it has to, and the survivors' identities are still theirs.
func Mint(n int, taken map[Anchor]bool, s Supply) []Anchor {
	out := make([]Anchor, 0, n)
	seen := make(map[Anchor]bool, len(taken)+n)
	for a := range taken {
		seen[a] = true
	}
	buf := make([]byte, 4)
	// Bounded: a collision after this many tries means the supply is degenerate
	// (a FixedSupply of one byte, say), and cycling the word list is a better
	// answer than looping forever.
	const tries = 64
	fallback := 0
	for len(out) < n {
		var a Anchor
		for range tries {
			if _, err := s.Read(buf); err != nil {
				break
			}
			a = Anchor(words[int(binary.BigEndian.Uint16(buf[0:2]))%len(words)] + "-" +
				words[int(binary.BigEndian.Uint16(buf[2:4]))%len(words)])
			if !seen[a] {
				break
			}
			a = ""
		}
		if a == "" || seen[a] {
			a = Anchor(words[fallback%len(words)] + "-" + words[(fallback/len(words))%len(words)])
			fallback++
			if seen[a] {
				continue
			}
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}
