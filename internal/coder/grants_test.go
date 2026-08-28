package coder

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// The shape the flag promises: repeatable, comma-separated, and the two the
// same thing.
func TestParseGrantsRepeatsAndSplits(t *testing.T) {
	a, err := ParseGrants([]string{"bash", "webfetch,websearch"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseGrants([]string{"bash,webfetch,websearch"})
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(a, b) {
		t.Errorf("repeated and comma-separated forms differ:\n%v\n%v", a, b)
	}
	if len(a) != 3 || !a[GrantBash] || !a[GrantWebsearch] {
		t.Errorf("parsed = %v", a)
	}
	// Spacing and case are the user's, not the parser's business.
	c, err := ParseGrants([]string{" BASH , WebFetch "})
	if err != nil || !c[GrantBash] || !c[GrantWebfetch] {
		t.Errorf("parsed = %v, err = %v", c, err)
	}
}

// A typo is an error naming what would have worked. Silently granting nothing
// is the failure mode that matters here: the user believes a permission is in
// place and finds out at the prompt it was meant to answer.
func TestParseGrantsRejectsUnknownNames(t *testing.T) {
	_, err := ParseGrants([]string{"bash,bahs"})
	if err == nil {
		t.Fatal("an unknown name was accepted")
	}
	for _, want := range []string{"bahs", "bash", "webfetch", "all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestParseGrantsAll(t *testing.T) {
	got, err := ParseGrants([]string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(GrantNames) {
		t.Errorf("all granted %d of %d names: %v", len(got), len(GrantNames), slices.Sorted(maps.Keys(got)))
	}
}

// The point of the change: naming one permission grants exactly that one.
// Under the flags this replaced, the only choices were "everything but the
// shell" and "everything", so a user who wanted unattended search had to hand
// over the shell as well.
func TestAutoConfirmerAnswersOnlyWhatWasNamed(t *testing.T) {
	granted, err := ParseGrants([]string{"websearch"})
	if err != nil {
		t.Fatal(err)
	}
	declining := &countingConfirmer{}
	ac := AutoConfirmer{Granted: granted, Fallback: declining}

	if !ac.Confirm(ConfirmRequest{Prompt: "Search the web?", Grant: GrantWebsearch}).Yes {
		t.Error("the named permission was not granted")
	}
	for _, g := range []string{GrantBash, GrantWebfetch, GrantSteps, GrantContext} {
		if ac.Confirm(ConfirmRequest{Prompt: "?", Grant: g}).Yes {
			t.Errorf("%q was granted by --yes websearch", g)
		}
	}
	if declining.n != 4 {
		t.Errorf("fell through %d times, want 4 — an ungranted prompt still has to be asked", declining.n)
	}
}

// A prompt with no name cannot be answered by a flag, even by "all". There is
// no name a user could have typed for it, so answering it would be answering
// something they never asked about.
func TestUnnamedPromptsAreNeverAutoAnswered(t *testing.T) {
	granted, err := ParseGrants([]string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	declining := &countingConfirmer{}
	ac := AutoConfirmer{Granted: granted, Fallback: declining}
	if ac.Confirm(ConfirmRequest{Prompt: "Add command output to the chat?", Group: "add-output"}).Yes {
		t.Error("an unnamed prompt was answered by --yes all")
	}
	if declining.n != 1 {
		t.Error("the unnamed prompt did not reach the fallback")
	}
}

// With no flags and no fallback, everything is declined — the shape a
// non-interactive session has before any permission is named.
func TestNoGrantsDeclinesEverything(t *testing.T) {
	ac := AutoConfirmer{}
	for _, g := range append(slices.Clone(GrantNames), "") {
		if ac.Confirm(ConfirmRequest{Grant: g}).Yes {
			t.Errorf("%q was granted with no flags at all", g)
		}
	}
}

type countingConfirmer struct{ n int }

func (c *countingConfirmer) Confirm(ConfirmRequest) ConfirmResult { c.n++; return ConfirmResult{} }
