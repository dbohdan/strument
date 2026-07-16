package config

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/multiformats/go-multihash"
)

// The trust store records (abspath, multihash) pairs for project configs the
// user has explicitly trusted, direnv-style (config-schema §1-2). Records
// are self-describing multihashes: each is re-verified under the algorithm
// it was written with, so a future default-hash migration invalidates
// nothing.

// DefaultTrustHash is the multihash code used for new records.
const DefaultTrustHash = multihash.SHA2_256

// trustRecord is one JSONL row of the store.
type trustRecord struct {
	Path      string `json:"path"`
	Multihash string `json:"multihash"` // hex-encoded self-describing digest
}

// TrustStore is a file-backed trust database. The file lives in the user
// state dir and must not be synced between hosts (config-schema §2).
type TrustStore struct {
	path    string
	records map[string]string // abspath -> multihash hex
}

// DefaultTrustStorePath is $XDG_STATE_HOME/strument/trust, defaulting to
// ~/.local/state/strument/trust.
func DefaultTrustStorePath() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "strument", "trust"), nil
}

// OpenTrustStore loads the store at path, treating a missing file as empty.
func OpenTrustStore(path string) (*TrustStore, error) {
	ts := &TrustStore{path: path, records: map[string]string{}}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return ts, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec trustRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("%s: corrupt trust record: %w", path, err)
		}
		ts.records[rec.Path] = rec.Multihash
	}
	return ts, scan.Err()
}

// IsTrusted reports whether content at absPath matches its recorded digest.
// The recorded multihash decides the hash function (self-description), so
// records written under an older default keep verifying after a migration.
func (ts *TrustStore) IsTrusted(absPath string, content []byte) bool {
	recHex, ok := ts.records[absPath]
	if !ok {
		return false
	}
	recorded, err := hex.DecodeString(recHex)
	if err != nil {
		return false
	}
	decoded, err := multihash.Decode(recorded)
	if err != nil {
		return false
	}
	current, err := multihash.Sum(content, decoded.Code, -1)
	if err != nil {
		return false
	}
	return string(current) == string(recorded)
}

// Trust records (absPath, multihash(content)) under the current default
// hash and persists the store.
func (ts *TrustStore) Trust(absPath string, content []byte) error {
	mh, err := multihash.Sum(content, DefaultTrustHash, -1)
	if err != nil {
		return err
	}
	ts.records[absPath] = hex.EncodeToString(mh)
	return ts.save()
}

// TrustWithCode records under an explicit multihash code; used by tests to
// simulate records from an older/newer default function.
func (ts *TrustStore) TrustWithCode(absPath string, content []byte, code uint64) error {
	mh, err := multihash.Sum(content, code, -1)
	if err != nil {
		return err
	}
	ts.records[absPath] = hex.EncodeToString(mh)
	return ts.save()
}

func (ts *TrustStore) save() error {
	if err := os.MkdirAll(filepath.Dir(ts.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(ts.path), ".trust-*")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(tmp)
	for _, path := range slices.Sorted(maps.Keys(ts.records)) {
		if err := enc.Encode(trustRecord{Path: path, Multihash: ts.records[path]}); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), ts.path)
}
