package anchor

import (
	"strings"
	"testing"
)

// The vocabulary's properties are load bearing, so they are asserted rather
// than believed: a duplicate silently shrinks the space, and a word Parse
// rejects would mint anchors the model cannot send back.
func TestWordsAreDistinctAndParseable(t *testing.T) {
	seen := map[string]bool{}
	for _, w := range words {
		if seen[w] {
			t.Errorf("duplicate word %q: the space is smaller than it claims", w)
		}
		seen[w] = true
		if !isWord(w) {
			t.Errorf("word %q is not lowercase ASCII, so Parse would reject an anchor using it", w)
		}
	}
	if len(words) < 128 {
		t.Errorf("%d words gives only %d anchors", len(words), len(words)*len(words))
	}
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"copper-otter", true},
		{"  Copper-Otter  ", true}, // trimmed and lowercased
		{"copper", false},          // one word
		{"copper-otter-quartz", false},
		{"copper-", false},
		{"-otter", false},
		{"copper otter", false},
		{"copper-otter2", false},
		{"", false},
		{"--", false},
	} {
		got, ok := Parse(tc.in)
		if ok != tc.want {
			t.Errorf("Parse(%q) ok = %v, want %v (got %q)", tc.in, ok, tc.want, got)
		}
		if ok && got != Anchor(strings.ToLower(strings.TrimSpace(tc.in))) {
			t.Errorf("Parse(%q) = %q, want it normalized", tc.in, got)
		}
	}
}

// Minting is deterministic from a fixed supply, which is what lets the read
// format have golden tests at all.
func TestMintIsDeterministicFromAFixedSupply(t *testing.T) {
	a := Mint(8, nil, &FixedSupply{Bytes: []byte{1, 2, 3, 4, 5, 6, 7}})
	b := Mint(8, nil, &FixedSupply{Bytes: []byte{1, 2, 3, 4, 5, 6, 7}})
	if len(a) != 8 {
		t.Fatalf("minted %d, want 8", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same supply gave different anchors at %d: %q vs %q", i, a[i], b[i])
		}
		if _, ok := Parse(string(a[i])); !ok {
			t.Errorf("minted anchor %q does not parse", a[i])
		}
	}
}

// Distinctness is the whole point: two lines sharing an address would make an
// edit ambiguous again, which is the failure this format removes.
func TestMintIsDistinctAndAvoidsTaken(t *testing.T) {
	taken := map[Anchor]bool{}
	for _, a := range Mint(64, nil, CryptoSupply{}) {
		taken[a] = true
	}
	got := Mint(200, taken, CryptoSupply{})
	seen := map[Anchor]bool{}
	for _, a := range got {
		if seen[a] {
			t.Fatalf("minted %q twice", a)
		}
		if taken[a] {
			t.Fatalf("minted %q, which was already taken", a)
		}
		seen[a] = true
	}
}

// A degenerate supply must still terminate with distinct anchors rather than
// spinning: minting is called on the read path and cannot hang.
func TestMintTerminatesOnADegenerateSupply(t *testing.T) {
	got := Mint(50, nil, &FixedSupply{Bytes: []byte{0}})
	seen := map[Anchor]bool{}
	for _, a := range got {
		if seen[a] {
			t.Fatalf("minted %q twice from a constant supply", a)
		}
		seen[a] = true
	}
	if len(got) != 50 {
		t.Errorf("minted %d, want 50", len(got))
	}
}
