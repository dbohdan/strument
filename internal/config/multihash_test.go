package config

import (
	"encoding/hex"
	"errors"
	"testing"
)

// The shim's bytes are go-multihash's, recorded from v0.2.3 before it was
// removed, so every trust record written with the library still verifies.
func TestMultihashMatchesTheLibrary(t *testing.T) {
	content := []byte("models = {}\n")
	for code, want := range map[uint64]string{
		multihashSHA2256: "1220c7ae8e775f463745dc201524442fe170f9ac10a606ecab1cb5cadf1b172cf649",
		multihashSHA2512: "13402b3c12e76c96b648fb076cf42edb27e07a98ab2c8833985943649e0034970ffb3ad2b0579d9d4" +
			"900555fb6b9cd3f1e83200bb746e0c7d3ee14083cda104c82a0",
	} {
		got, err := multihashSum(content, code)
		if err != nil {
			t.Fatalf("%#x: %v", code, err)
		}
		if hex.EncodeToString(got) != want {
			t.Errorf("%#x: got %x, want %s", code, got, want)
		}
		if c, err := multihashCode(got); err != nil || c != code {
			t.Errorf("%#x: decoded as %#x, %v", code, c, err)
		}
	}
}

// BLAKE3 is recognized and refused until the standard library computes it.
// The vector below is go-multihash's BLAKE3 of the same content as above,
// for the day it is added.
func TestMultihashRefusesWhatItCannotCompute(t *testing.T) {
	blake3, _ := hex.DecodeString("1e20dbd65743cfa4f43b51093b7256b2ec85d3ddb3f6c15f297212c7b30a2975b0da")
	code, err := multihashCode(blake3)
	if err != nil || code != multihashBLAKE3 {
		t.Fatalf("a BLAKE3 multihash decoded as %#x, %v", code, err)
	}
	if _, err := multihashSum([]byte("x"), code); !errors.Is(err, errMultihashUnsupported) {
		t.Errorf("BLAKE3 sum err = %v, want unsupported", err)
	}
	for _, bad := range []string{"", "12", "1220c7ae", "ff"} {
		b, _ := hex.DecodeString(bad)
		if _, err := multihashCode(b); err == nil {
			t.Errorf("%q decoded without error", bad)
		}
	}
}
