package repomap

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The cache is proved by making it wrong on purpose: rewrite a file's contents
// while restoring the stamp the tags were extracted under, and the stale tags
// coming back is the only possible explanation for the answer. A counter would
// only show that some code did not run.

// alphaSrc and gammaSrc differ in content and agree in length, so a rewrite
// between them moves nothing but the modification time.
const (
	alphaSrc = "def alpha():\n    pass\n"
	gammaSrc = "def gamma():\n    pass\n"
)

func defNames(tags []Tag) []string {
	var out []string
	for _, t := range tags {
		if t.Kind == Def {
			out = append(out, t.Name)
		}
	}
	slices.Sort(out)
	return out
}

// rewrite replaces the file's contents and then forces its modification time,
// so a test can vary content and stamp independently.
func rewrite(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestTagCacheServesAnUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.py")
	if err := os.WriteFile(path, []byte(alphaSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stamp := fi.ModTime()

	rm := testMap(t, dir)
	if got := defNames(rm.Tags([]string{path})); !slices.Contains(got, "alpha") {
		t.Fatalf("first call should see alpha, got %v", got)
	}

	// Same size, same mtime, different contents: nothing the cache can see has
	// changed, so it must answer from what it stored.
	rewrite(t, path, gammaSrc, stamp)
	got := defNames(rm.Tags([]string{path}))
	if !slices.Contains(got, "alpha") {
		t.Errorf("second call re-extracted; the cache was not consulted, got %v", got)
	}
}

func TestTagCacheNoticesANewModTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.py")
	if err := os.WriteFile(path, []byte(alphaSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	rm := testMap(t, dir)
	rm.Tags([]string{path})

	rewrite(t, path, gammaSrc, time.Now().Add(time.Hour))
	got := defNames(rm.Tags([]string{path}))
	if !slices.Contains(got, "gamma") {
		t.Errorf("a newer modification time should re-extract, got %v", got)
	}
}

func TestTagCacheNoticesANewSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.py")
	if err := os.WriteFile(path, []byte(alphaSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stamp := fi.ModTime()

	rm := testMap(t, dir)
	rm.Tags([]string{path})

	// The same timestamp with a different size: this is the edit-twice-in-one-
	// tick case that mtime alone would miss.
	rewrite(t, path, "def delta_function():\n    pass\n", stamp)
	got := defNames(rm.Tags([]string{path}))
	if !slices.Contains(got, "delta_function") {
		t.Errorf("a changed size should re-extract even at the same mtime, got %v", got)
	}
}

func TestTagCacheSkipsTagsOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.py")
	if err := os.WriteFile(path, []byte(alphaSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	rm := testMap(t, dir)
	rm.TagsOverride = func(fname, relFname string) []Tag {
		calls++
		return []Tag{{RelFname: relFname, Fname: fname, Name: "injected", Kind: Def}}
	}

	for range 3 {
		if got := defNames(rm.Tags([]string{path})); !slices.Equal(got, []string{"injected"}) {
			t.Fatalf("override should answer every call, got %v", got)
		}
	}
	if calls != 3 {
		t.Errorf("override called %d times, want 3: the cache must not stand in front of it", calls)
	}
}
