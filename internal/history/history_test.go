package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultPathKeying(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	p1, err := DefaultPath("/home/user/myproj")
	if err != nil {
		t.Fatal(err)
	}
	p1again, _ := DefaultPath("/home/user/myproj")
	p2, _ := DefaultPath("/home/user/other")

	if p1 != p1again {
		t.Errorf("same root gave different paths: %q vs %q", p1, p1again)
	}
	if p1 == p2 {
		t.Errorf("different roots gave the same path: %q", p1)
	}
	if base := filepath.Base(p1); !strings.HasPrefix(base, "myproj-") || !strings.HasSuffix(base, ".md") {
		t.Errorf("filename = %q, want myproj-<hash>.md", base)
	}
	if !strings.Contains(p1, filepath.Join("strument", "history")) {
		t.Errorf("path not under strument/history: %q", p1)
	}
}

func TestInputHistoryPathIsGlobal(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, err := InputHistoryPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "input-history" {
		t.Errorf("input history basename = %q", filepath.Base(p))
	}
	// Global: directly under strument/, not under history/.
	if filepath.Base(filepath.Dir(p)) != "strument" {
		t.Errorf("input history not global: %q", p)
	}
}

func TestAppendFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "chat.md")
	w := New(path)

	when := time.Date(2026, 7, 17, 14, 30, 5, 0, time.UTC)
	if err := w.Append(Turn{
		Time:           when,
		Model:          "flash",
		TokensSent:     2600,
		TokensReceived: 433,
		Cost:           0.00046,
		CostKnown:      true,
		User:           "change the greeting",
		Assistant:      "Sure, here is the change.",
	}); err != nil {
		t.Fatal(err)
	}
	// Second turn, cost unknown.
	if err := w.Append(Turn{
		Time:           when.Add(time.Minute),
		Model:          "flash",
		TokensSent:     10,
		TokensReceived: 5,
		User:           "thanks",
		Assistant:      "You're welcome.",
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	for _, want := range []string{
		"# Strument chat history",
		"## 2026-07-17 14:30:05 — flash",
		"2600 tokens sent, 433 received · $0.0005",
		"### Prompt\n\nchange the greeting",
		"### Response\n\nSure, here is the change.",
		"## 2026-07-17 14:31:05 — flash",
		"_10 tokens sent, 5 received_", // no cost suffix when unknown
		"You're welcome.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("history missing %q:\n%s", want, got)
		}
	}
	// The title header appears exactly once across appends.
	if n := strings.Count(got, "# Strument chat history"); n != 1 {
		t.Errorf("title header appears %d times, want 1", n)
	}
}

func TestAppendSkipsEmptyAssistant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.md")
	w := New(path)
	if err := w.Append(Turn{User: "hi", Assistant: "   \n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("empty-answer turn should not create the file: err=%v", err)
	}
}
