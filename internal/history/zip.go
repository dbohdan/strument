package history

import (
	"archive/zip"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"dbohdan.com/strument/internal/coder"
)

// Packing one run for someone else to read.
//
// A record on its own is half a record: every heavy tool result is a hash
// pointing into the project's blob store, so a segment sent by itself reads as
// a conversation about files nobody can see. The archive carries the segment
// and every payload it names, laid out as the store lays them out — log/ and
// blobs/ — so a reader resolves hashes the same way Strument does.
//
// The segment goes in byte for byte rather than re-encoded. It is the evidence;
// a round trip through Record would drop any field this build does not know.

// ZipStats is what went into an archive.
type ZipStats struct {
	// Blobs is how many payloads were packed, and Missing how many the run
	// names that the store no longer has — stripped, most likely, which is a
	// supported state rather than damage.
	Blobs, Missing int
	// Bytes is the uncompressed size of everything packed.
	Bytes int64
}

// zipLevel is Deflate's default, 6. Records and tool output are text and
// compress several-fold at any level; 9 buys a percent or two for a multiple
// of the time, which is the wrong trade for something made to be sent now.
const zipLevel = flate.DefaultCompression

// recordBlobs is every payload hash one record names: its own and its tool
// calls'. Shared with the strip scan, so "what this record refers to" has one
// answer — an archive that missed a reference strip counts would ship a
// conversation with a hole in it.
func recordBlobs(r coder.Record) []string {
	var out []string
	if r.Blob != "" {
		out = append(out, r.Blob)
	}
	for _, tc := range r.ToolCalls {
		if tc.Blob != "" {
			out = append(out, tc.Blob)
		}
	}
	return out
}

// ZipRun writes segment and the payloads it references to w as a zip archive.
//
// A segment whose last line is still being written — the live run — is packed
// as it stands, and the references are taken from the records that decode. An
// undecodable line in the middle is packed too: the archive is for looking at
// the record, damage included.
func ZipRun(projectRoot, segment string, w io.Writer) (ZipStats, error) {
	var stats ZipStats
	records, _, err := readRecords(segment)
	if err != nil {
		return stats, err
	}
	seen := map[string]bool{}
	var hashes []string
	for _, r := range records {
		for _, h := range recordBlobs(r) {
			if !seen[h] {
				seen[h] = true
				hashes = append(hashes, h)
			}
		}
	}
	slices.Sort(hashes)

	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, zipLevel)
	})
	add := func(name, src string) error {
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name, hdr.Method = name, zip.Deflate
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		n, err := io.Copy(dst, f)
		stats.Bytes += n
		return err
	}

	if err := add("log/"+filepath.Base(segment), segment); err != nil {
		return stats, err
	}
	for _, h := range hashes {
		p, err := BlobPath(projectRoot, h)
		if err != nil {
			// A name that is not a hash came from a hand-edited record. It
			// names nothing the store could hold, so it is missing, not fatal.
			stats.Missing++
			continue
		}
		if err := add("blobs/"+h, p); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				stats.Missing++
				continue
			}
			return stats, fmt.Errorf("packing payload %s: %w", h, err)
		}
		stats.Blobs++
	}
	return stats, zw.Close()
}
