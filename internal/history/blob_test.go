package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
)

func TestPutBlobIsContentAddressed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	payload := []byte("main.go (3 lines)\n1\tpackage main\n")
	h1, err := PutBlob(project, payload)
	if err != nil {
		t.Fatal(err)
	}
	// The same bytes from a second call store one copy under one name, which
	// is what makes deleting a secret that was read five times one unlink.
	h2, err := PutBlob(project, payload)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("the same payload got two names: %q and %q", h1, h2)
	}
	other, err := PutBlob(project, []byte("something else"))
	if err != nil {
		t.Fatal(err)
	}
	if other == h1 {
		t.Error("two different payloads got one name")
	}

	got, ok := GetBlob(project, h1)
	if !ok {
		t.Fatal("the payload just written is not there")
	}
	if string(got) != string(payload) {
		t.Errorf("round trip changed the payload: %q", got)
	}

	blobs, err := ListBlobs(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != 2 {
		t.Errorf("store holds %d blobs, want the 2 distinct payloads: %v", len(blobs), blobs)
	}
}

// A record that outlives its payload is the design working, not a failure:
// pruning is supported, so the reader degrades to the record's description.
func TestGetBlobReportsAMissingPayloadRatherThanFailing(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	h, err := PutBlob(project, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteBlob(project, h); err != nil {
		t.Fatal(err)
	}
	if _, ok := GetBlob(project, h); ok {
		t.Error("a deleted payload came back")
	}
	// Deleting it again is what the caller wanted either way.
	if err := DeleteBlob(project, h); err != nil {
		t.Errorf("deleting a gone blob should succeed: %v", err)
	}
}

// The name is a claim about the contents. A store copied between machines,
// restored from a backup or edited by hand can have that claim be false, and
// handing back a payload that is not what the record points at would be worse
// than handing back nothing.
func TestGetBlobRejectsAPayloadThatDoesNotMatchItsName(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	h, err := PutBlob(project, []byte("the real payload"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := BlobPath(project, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not the real payload"), fileMode); err != nil {
		t.Fatal(err)
	}
	if _, ok := GetBlob(project, h); ok {
		t.Error("a payload that does not hash to its name was returned")
	}
}

// A hash reaches BlobPath out of a record, and a record is a file the user can
// edit — `strument history edit` opens it. A name that is not a hash must not
// be joined onto a path.
func TestBlobPathRejectsANameThatIsNotAHash(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	for _, bad := range []string{
		"../../../etc/passwd",
		"",
		"ABCDEF", // short, and the store is lower case
		strings.Repeat("g", 64),
		strings.Repeat("a", 63),
		strings.Repeat("A", 64),
	} {
		if p, err := BlobPath(project, bad); err == nil {
			t.Errorf("BlobPath(%q) = %q, want an error", bad, p)
		}
		if _, ok := GetBlob(project, bad); ok {
			t.Errorf("GetBlob(%q) returned a payload", bad)
		}
	}
}

// An interrupted put leaves a temporary file. It is not a payload, so it is
// not listed — and a sweep that treated it as one would be a sweep that could
// delete a blob still being written.
func TestListBlobsSkipsAnInterruptedPut(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	if _, err := PutBlob(project, []byte("real")); err != nil {
		t.Fatal(err)
	}
	dir, err := BlobsDir(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tmp-123456"), []byte("half"), fileMode); err != nil {
		t.Fatal(err)
	}
	blobs, err := ListBlobs(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != 1 {
		t.Errorf("ListBlobs = %v, want only the finished payload", blobs)
	}
}

// The whole round trip: a record whose payload went to the store comes back
// whole, and the same record with the payload pruned comes back readable.
func TestResolvePutsPayloadsBackAndDegradesWhenTheyAreGone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	result := strings.Repeat("a line of output\n", 200)
	args := `{"path":"big.txt"}` + strings.Repeat(" ", 2000)
	resultHash, err := PutBlob(project, []byte(result))
	if err != nil {
		t.Fatal(err)
	}
	argsHash, err := PutBlob(project, []byte(args))
	if err != nil {
		t.Fatal(err)
	}

	rec := coder.Record{
		Type: "message", Role: "tool", ToolCallID: "call_1",
		Blob: resultHash, Bytes: len(result), Summary: "a line of output",
		ToolCalls: []coder.RecordToolCall{{
			ID: "call_1", Name: "read",
			Blob: argsHash, Bytes: len(args), Summary: `{"path":"big.txt"}`,
		}},
	}

	got, whole := Resolve(project, rec)
	if !whole {
		t.Error("every payload was there; Resolve reported otherwise")
	}
	if got.Text != result {
		t.Errorf("the result did not come back whole: %q", got.Text)
	}
	if got.ToolCalls[0].Arguments != args {
		t.Errorf("the arguments did not come back whole: %q", got.ToolCalls[0].Arguments)
	}

	// Now prune it, which is the operation the separation exists for.
	if err := DeleteBlob(project, resultHash); err != nil {
		t.Fatal(err)
	}
	got, whole = Resolve(project, rec)
	if whole {
		t.Error("a pruned payload was reported as present")
	}
	for _, want := range []string{"no longer stored", "a line of output", "3400 bytes"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("the stand-in does not mention %q: %q", want, got.Text)
		}
	}
	// The arguments were not pruned, so they are still whole. Losing one
	// payload must not lose the rest of the record.
	if got.ToolCalls[0].Arguments != args {
		t.Errorf("pruning the result also lost the arguments: %q", got.ToolCalls[0].Arguments)
	}
	// The hash stays, so a payload restored from a backup is found again.
	if got.Blob != resultHash {
		t.Errorf("Resolve dropped the hash: %q", got.Blob)
	}
}

// A record with no blobs is returned untouched. Resolve runs over every
// record a reader sees, and most of them have nothing to resolve.
func TestResolveLeavesAnInlineRecordAlone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()

	rec := coder.Record{Type: "message", Role: "tool", ToolCallID: "c", Text: "small enough"}
	got, whole := Resolve(project, rec)
	if !whole {
		t.Error("a record with nothing to resolve was reported incomplete")
	}
	if got.Text != "small enough" {
		t.Errorf("text = %q, want it untouched", got.Text)
	}
}

// Pruned arguments cannot take a note where they stood: they are replayed as
// the call's JSON, and a provider refuses a call whose arguments do not parse.
// The wire used to swap the note for "{}" silently, so a restored call read as
// one that took no arguments. Now Arguments is "{}" on purpose and the note is
// carried beside it, for the restore to put in front of the call's result.
func TestResolveMarksPrunedArgumentsAndKeepsThemJSON(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	args := `{"path":"big.txt","content":"` + strings.Repeat("x", 3000) + `"}`
	hash, err := PutBlob(project, []byte(args))
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteBlob(project, hash); err != nil {
		t.Fatal(err)
	}
	rec := coder.Record{Type: "message", Role: "assistant", ToolCalls: []coder.RecordToolCall{{
		ID: "call_1", Name: "write", Blob: hash, Bytes: len(args), Summary: `{"path":"big.txt",…`,
	}}}

	got, whole := Resolve(project, rec)
	if whole {
		t.Error("pruned arguments were reported as present")
	}
	tc := got.ToolCalls[0]
	if tc.Arguments != "{}" {
		t.Errorf("Arguments = %q, want an empty object a provider accepts", tc.Arguments)
	}
	for _, want := range []string{"This call's arguments are no longer stored", "They were 3031 bytes", `big.txt`} {
		if !strings.Contains(tc.Pruned, want) {
			t.Errorf("Pruned = %q, want it to say %q", tc.Pruned, want)
		}
	}
	if rec.ToolCalls[0].Arguments != "" || rec.ToolCalls[0].Pruned != "" {
		t.Error("Resolve wrote into the caller's record")
	}

	// An assistant message's own text is a message, not a result.
	textHash, _ := PutBlob(project, []byte("a long answer"))
	_ = DeleteBlob(project, textHash)
	got, _ = Resolve(project, coder.Record{Type: "message", Role: "assistant", Blob: textHash, Bytes: 13})
	if !strings.HasPrefix(got.Text, "[strument] This message is no longer stored.") {
		t.Errorf("Text = %q, want it called a message", got.Text)
	}
}
