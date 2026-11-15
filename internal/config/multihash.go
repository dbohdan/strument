package config

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
)

// The trust store's digests are multihashes: a varint hash-function code, a
// varint digest length, then the digest. This is the part of the format the
// store uses, copied rather than imported. github.com/multiformats/go-multihash
// brought in five modules — BLAKE3 among them, whose cached copy once broke a
// local build — to compute a SHA2-256 that crypto/sha256 already provides.
// The bytes are the same: multihash_test.go pins them against go-multihash's
// own output.

// Multihash codes, from the multicodec table.
const (
	multihashSHA2256 uint64 = 0x12 // the default for new records
	// multihashSHA2512 is not used for new records. It is here because the
	// standard library computes it, and the store's self-description needs a
	// second algorithm to be tested against.
	multihashSHA2512 uint64 = 0x13
	// multihashBLAKE3 is reserved for a later default. Records under it are
	// recognized and refused until the standard library can compute BLAKE3;
	// the store then re-verifies nothing it could not before.
	multihashBLAKE3 uint64 = 0x1e
)

var errMultihashUnsupported = errors.New("unsupported multihash function")

// multihashSum is the multihash of data under code.
func multihashSum(data []byte, code uint64) ([]byte, error) {
	var digest []byte
	switch code {
	case multihashSHA2256:
		d := sha256.Sum256(data)
		digest = d[:]
	case multihashSHA2512:
		d := sha512.Sum512(data)
		digest = d[:]
	default:
		return nil, fmt.Errorf("%w: code %#x", errMultihashUnsupported, code)
	}
	out := binary.AppendUvarint(nil, code)
	out = binary.AppendUvarint(out, uint64(len(digest)))
	return append(out, digest...), nil
}

// multihashCode is the hash-function code of a multihash, checking that the
// length it states is the length it has.
func multihashCode(mh []byte) (uint64, error) {
	code, n := binary.Uvarint(mh)
	if n <= 0 {
		return 0, errors.New("multihash: bad function code")
	}
	length, m := binary.Uvarint(mh[n:])
	if m <= 0 {
		return 0, errors.New("multihash: bad digest length")
	}
	rest := len(mh[n+m:])
	if uint64(rest) != length {
		return 0, fmt.Errorf("multihash: states %d digest bytes, holds %d", length, rest)
	}
	return code, nil
}
