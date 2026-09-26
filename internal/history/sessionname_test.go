package history

import (
	"strings"
	"testing"
)

func TestNextSessionName(t *testing.T) {
	long := strings.Repeat("a", 63)
	for _, tc := range []struct {
		current  string
		existing []string
		want     string
	}{
		{"foo", []string{"foo"}, "foo-2"},
		{"foo-2", []string{"foo", "foo-2"}, "foo-3"},
		// A deleted foo-2 is not reused: the highest plus one.
		{"foo-3", []string{"foo", "foo-3"}, "foo-4"},
		// Clearing an older member continues the sequence, not a branch of it.
		{"foo-2", []string{"foo", "foo-2", "foo-7"}, "foo-8"},
		// A number over 99 is part of the name.
		{"release-2026", []string{"release-2026"}, "release-2026-2"},
		// No base beside it: a hand-picked name, not a sequence.
		{"spike-2", []string{"spike-2"}, "spike-2-2"},
		{"api_v2", []string{"api_v2"}, "api_v2-2"},
		{"default", []string{"default"}, "default-2"},
		// Case-insensitive filesystems: FOO-2 takes foo-2's place.
		{"foo", []string{"foo", "FOO-2"}, "foo-3"},
		// Zero-padded and non-numeric neighbours do not count.
		{"foo", []string{"foo", "foo-02", "foo-x"}, "foo-2"},
		// Length: the base gives way, and no trailing dash is left at the cut.
		{long, []string{long}, strings.Repeat("a", 62) + "-2"},
		{"ab-" + long[:59], []string{"ab-" + long[:59]}, "ab-" + long[:59] + "-2"},
		{strings.Repeat("a", 61) + "-b", []string{strings.Repeat("a", 61) + "-b"}, strings.Repeat("a", 61) + "-2"},
	} {
		got := nextSessionName(tc.current, tc.existing)
		if got != tc.want {
			t.Errorf("next(%q, %v) = %q, want %q", tc.current, tc.existing, got, tc.want)
		}
		if err := ValidSessionName(got); err != nil {
			t.Errorf("next(%q) = %q, which is not a valid name: %v", tc.current, got, err)
		}
	}
}

func TestCompareNatural(t *testing.T) {
	ordered := []string{"foo", "foo-2", "foo-9", "foo-10", "foo-010", "foo-11", "foo-a", "foo2", "zeta"}
	for i := range ordered {
		for j := range ordered {
			want := cmpInt(i, j)
			if got := compareNatural(ordered[i], ordered[j]); got != want {
				t.Errorf("compareNatural(%q, %q) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}
