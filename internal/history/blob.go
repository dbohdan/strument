package history

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// The blob store: a project's heavy tool-result payloads, addressed by the
// SHA-256 of their contents.
//
// It exists so that history can be pruned without being forgotten. Nothing
// here is ever deleted on Strument's own initiative, which makes a record that
// holds every tool result verbatim a growing pile of whatever the model read
// out of the project — an .env, an SSH config, a customer's data. Separating
// the payload from the record means the timeline can stay forever while the
// payloads can go: the record keeps the hash and a one-line description, and a
// reader that finds no blob degrades to that description rather than failing.
//
// The shape is Connectome's Chronicle (read at 013f138, 2026-09-20), which
// never deletes a record, appends rather than rewrites when it compacts, and
// keeps heavy payloads in a content-addressed store with an individual delete.
// Strument keeps the description Chronicle does not, so a stripped record still
// reads as a conversation.
//
// Addressing by content, not by tool call id, although the ids are unique and
// were the obvious candidate. Two calls that read the same unchanged file
// store one copy; more to the point, deleting a secret that was read five
// times is one unlink rather than five, and the fifth is the one a
// call-id-keyed store would leave behind.

// blobNamePattern is what a blob file may be called. Used to check a hash that
// came out of a record before it is joined onto a path, since a record is a
// file a user can edit and `..` in a name would be a path traversal.
var blobNamePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// BlobsDir is a project's blob store.
func BlobsDir(projectRoot string) (string, error) {
	return artifactPath(projectRoot, artBlobs)
}

// BlobHash is the name a payload is stored under: the hex SHA-256 of its
// bytes, whole and unprefixed.
//
// No `ab/cdef…` sharding. Measured rather than assumed: a flat directory of
// 200k files reads back in 214ms and lookup does not degrade across a 20×
// range of sizes (3.5ms to 4.4ms per thousand). Sharding would buy nothing and
// cost every reader two string operations and a wrong guess about how many
// characters the prefix has.
//
// SHA-256 rather than BLAKE3, which is eight times faster. On the largest
// payload this store will ever hold, that difference is 153µs — the project
// has standardized on SHA-256 for the project-directory key and the fixture
// digests, and one hash is worth more than a microsecond.
func BlobHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// BlobPath is where a hash is stored. It rejects a name that is not a hash,
// because the name reaches it from a record the user can edit.
func BlobPath(projectRoot, hash string) (string, error) {
	if !blobNamePattern.MatchString(hash) {
		return "", fmt.Errorf("%q is not a blob hash", hash)
	}
	dir, err := BlobsDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, hash), nil
}

// PutBlob stores a payload and returns its hash.
//
// A blob that is already there is already right — the name is the content — so
// this returns early rather than rewriting it. That is not only an
// optimization: rewriting would briefly truncate a file another process may be
// reading, to replace it with the same bytes.
//
// The write goes through a temporary file and a rename, so a blob is never
// half-written under a name that claims its hash. An interrupted put leaves a
// stray temporary file, which is the one thing in here worth cleaning up and
// the one thing that costs nothing to leave.
func PutBlob(projectRoot string, data []byte) (string, error) {
	hash := BlobHash(data)
	p, err := BlobPath(projectRoot, hash)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err == nil {
		return hash, nil
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // a no-op once the rename has succeeded
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return "", err
	}
	return hash, nil
}

// GetBlob returns a payload, or false when it is not there.
//
// Gone is an ordinary answer, not an error: pruning is a supported operation,
// so a record that outlives its payload is the design working. Callers show
// the record's description in its place.
func GetBlob(projectRoot, hash string) ([]byte, bool) {
	p, err := BlobPath(projectRoot, hash)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	// The name is a claim about the contents, and a store that is copied
	// between machines, restored from a backup or edited by hand can have that
	// claim be false. Checking costs one pass over bytes that were just read.
	if BlobHash(data) != hash {
		return nil, false
	}
	return data, true
}

// DeleteBlob removes one payload. A blob that is already gone is a success:
// the caller wanted it gone.
func DeleteBlob(projectRoot, hash string) error {
	p, err := BlobPath(projectRoot, hash)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ListBlobs returns every hash the store holds.
//
// Temporary files from an interrupted put, and anything else whose name is not
// a hash, are skipped rather than reported: this answers "what payloads are
// here", and a half-written one is not a payload.
func ListBlobs(projectRoot string) ([]string, error) {
	dir, err := BlobsDir(projectRoot)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && blobNamePattern.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
