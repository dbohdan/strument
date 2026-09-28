package history

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// A zipped run is the segment byte for byte and every payload it names, under
// the store's own layout: both places a hash can sit are followed, a payload
// named twice is packed once, a stripped one is counted rather than failing,
// and a torn last line — the live run — does not stop the packing.
func TestZipRunPacksTheRecordAndItsPayloads(t *testing.T) {
	project := newStripProject(t)
	payloads := []string{"result one\n", "call arguments\n", "result three\n"}
	hashes := stripFixture(t, project, "s", 0, payloads...)

	segs, err := LogSegments(project, "s")
	if err != nil || len(segs) != 1 {
		t.Fatalf("segments = %v, %v", segs, err)
	}
	seg := segs[0]
	gone := strings.Repeat("ab", 32) // a well-formed hash the store never had
	extra := `{"type":"message","role":"tool","tool_call_id":"d","blob":"` + hashes[0] + `","bytes":1,"summary":"s"}` + "\n" +
		`{"type":"message","role":"tool","tool_call_id":"e","blob":"` + gone + `","bytes":1,"summary":"stripped"}` + "\n" +
		`{"type":"message","role":"assis` // torn: the run is still being written
	f, err := os.OpenFile(seg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(extra); err != nil {
		t.Fatal(err)
	}
	f.Close()
	logBytes, err := os.ReadFile(seg)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	stats, err := ZipRun(project, seg, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Blobs != 3 || stats.Missing != 1 {
		t.Errorf("stats = %+v; want 3 packed (one named twice) and 1 missing", stats)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var names []string
	for _, zf := range zr.File {
		if zf.Method != zip.Deflate {
			t.Errorf("%s is stored with method %d, want Deflate", zf.Name, zf.Method)
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		got[zf.Name] = string(b)
		names = append(names, zf.Name)
	}
	logName := "log/" + seg[strings.LastIndex(seg, string(os.PathSeparator))+1:]
	if got[logName] != string(logBytes) {
		t.Errorf("the segment in the archive is not the segment on disk:\n%q\nwant\n%q", got[logName], logBytes)
	}
	for i, h := range hashes {
		if got["blobs/"+h] != payloads[i] {
			t.Errorf("blobs/%s = %q, want %q", h, got["blobs/"+h], payloads[i])
		}
	}
	if len(names) != 1+len(hashes) || slices.Contains(names, "blobs/"+gone) {
		t.Errorf("archive holds %v; want the segment and the %d payloads that exist", names, len(hashes))
	}
}
