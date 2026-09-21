// What leaves the record and what stays in it.

package coder

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/llm"
)

func TestBlobPolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool string
		size int
		want bool
	}{
		{"a big tool result goes to the store", toolRead, blobFloor, true},
		{"a small one stays inline", toolRead, blobFloor - 1, false},
		{"the user's own words always stay inline", toolAskUser, blobFloor * 100, false},
		{"so does the harness's own sentence", toolInterrupt, blobFloor * 100, false},
		{"an unattributed payload gets the default rule", "", blobFloor, true},
	} {
		if got := storeSeparately(tc.tool, tc.size); got != tc.want {
			t.Errorf("%s: storeSeparately(%q, %d) = %v", tc.name, tc.tool, tc.size, got)
		}
	}
}

func TestPayloadSummaryIsTheHeaderLine(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{
			"a tool result's header line",
			"main.go (3 lines)\n1\tpackage main\n2\t\n",
			"main.go (3 lines)",
		},
		{
			// orderedProps puts path first precisely so the front of the JSON
			// is the useful part; one rule reads results and arguments both.
			"the front of a call's arguments",
			`{"path":"main.go","content":"` + strings.Repeat("x", 500) + `"}`,
			`{"path":"main.go","content":"` + strings.Repeat("x", maxPayloadSummary-29) + "…",
		},
		{"trailing space goes", "  spaced  \nrest", "spaced"},
		{"a payload with no newline is its own summary", "just this", "just this"},
	} {
		if got := payloadSummary(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// A cap that lands mid-rune would print a replacement character where the
// summary is read, which is a terminal.
func TestPayloadSummaryCutsOnARuneBoundary(t *testing.T) {
	got := payloadSummary(strings.Repeat("é", maxPayloadSummary))
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("summary was not truncated: %q", got)
	}
	if !strings.ContainsRune(got, 'é') || strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("summary cut mid-rune: %q", got)
	}
}

// readThenAnswer calls read on path, then answers. The result is whatever the
// real read tool produces, so the payload under test is a real one.
type readThenAnswer struct {
	path  string
	sends int
}

func (s *readThenAnswer) Send(_ context.Context, _ llm.Request) iter.Seq2[llm.StreamEvent, error] {
	s.sends++
	first := s.sends == 1
	return func(yield func(llm.StreamEvent, error) bool) {
		if first {
			if !yield(llm.StreamEvent{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{
				Index: 0, ID: "call_1", Name: toolRead,
				Args: `{"path":"` + s.path + `"}`,
			}}, nil) {
				return
			}
			yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "tool_calls"}, nil)
			return
		}
		if !yield(llm.StreamEvent{Kind: llm.EventAnswer, Text: "Read it."}, nil) {
			return
		}
		yield(llm.StreamEvent{Kind: llm.EventFinish, FinishReason: "stop"}, nil)
	}
}

// bigFileCoder is a coder whose project holds one file well over the floor.
func bigFileCoder(t *testing.T) (*Coder, *capture) {
	t.Helper()
	c := testCoder(t)
	body := strings.Repeat("a line of output that is not especially short\n", 200)
	if err := os.WriteFile(filepath.Join(c.Root, "big.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &capture{}
	c.Recorder = rec
	c.Client = &readThenAnswer{path: "big.txt"}
	return c, rec
}

// toolResultRecord returns the last tool-result row in a capture.
func toolResultRecord(t *testing.T, rec *capture) Record {
	t.Helper()
	var out Record
	for _, r := range rec.recs {
		if r.Type == "message" && r.ToolCallID != "" {
			out = r
		}
	}
	if out.ToolCallID == "" {
		t.Fatal("no tool result was recorded")
	}
	return out
}

// The end-to-end shape: a payload over the floor leaves the record, and what
// stays behind is enough to know what was there.
func TestLargePayloadsLeaveTheRecord(t *testing.T) {
	c, rec := bigFileCoder(t)
	stored := map[string]string{}
	c.PutBlob = func(data []byte) (string, error) {
		// A stand-in for the real store: content-addressed by the same rule,
		// so the assertions below are about the policy rather than about
		// hashing.
		for k, v := range stored {
			if v == string(data) {
				return k, nil
			}
		}
		name := "blob" + string(rune('A'+len(stored)))
		stored[name] = string(data)
		return name, nil
	}

	c.runOne(context.Background(), "read the big file")

	result := toolResultRecord(t, rec)
	if result.Text != "" {
		t.Errorf("a payload over the floor stayed inline: %q", result.Text)
	}
	if result.Blob == "" {
		t.Error("the record does not say where the payload went")
	}
	if result.Bytes == 0 {
		t.Error("the record does not say how big the payload was")
	}
	if result.Summary == "" {
		t.Error("the record kept no description of the payload")
	}
	if stored[result.Blob] == "" {
		t.Errorf("nothing was stored under %q", result.Blob)
	}
}

// Without a store, nothing changes: a session that leaves no trace has
// nowhere to put a blob, and the record it does not write stays whole.
func TestWithoutAStoreEveryPayloadStaysInline(t *testing.T) {
	c, rec := bigFileCoder(t)

	c.runOne(context.Background(), "read the big file")

	for _, r := range rec.recs {
		if r.Blob != "" {
			t.Errorf("a payload was offloaded with no store wired: %+v", r)
		}
		if r.Type == "message" && r.ToolCallID != "" && r.Text == "" {
			t.Error("a tool result was recorded with neither text nor a blob")
		}
	}
}
