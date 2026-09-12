package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write creates path relative to dir, making parents as needed.
func write(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindProjectConfigPicksEitherForm(t *testing.T) {
	for _, rel := range ProjectConfigPaths {
		t.Run(rel, func(t *testing.T) {
			dir := t.TempDir()
			want := write(t, dir, rel, "# config\n")

			got, err := FindProjectConfig(dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != want {
				t.Errorf("found %q, want %q", got, want)
			}
		})
	}
}

// The refusal, and the two things the message has to carry: which files, and
// what to do about them. A message naming only one of them would leave the
// reader hunting for the other.
func TestFindProjectConfigRefusesBoth(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ProjectConfigPaths[0], "# dotfile\n")
	write(t, dir, ProjectConfigPaths[1], "# directory\n")

	got, err := FindProjectConfig(dir)
	if err == nil {
		t.Fatalf("a project with both configs must not resolve to one of them; got %q", got)
	}
	if got != "" {
		t.Errorf("returned %q alongside the error; neither file was chosen", got)
	}
	for _, want := range []string{ProjectConfigPaths[0], ProjectConfigPaths[1], "strument trust"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// TrustProject must refuse the same projects Load refuses, or `strument trust`
// would grant authority to a file that is not the one that runs.
func TestTrustProjectRefusesBoth(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ProjectConfigPaths[0], "# dotfile\n")
	write(t, dir, ProjectConfigPaths[1], "# directory\n")

	store := filepath.Join(t.TempDir(), "trust.json")
	if _, err := TrustProject(dir, store); err == nil {
		t.Fatal("trust accepted a project with two configs")
	}
	if _, err := os.Stat(store); err == nil {
		t.Error("trust wrote to the store for a project it refused")
	}
}

// The counter-arms. Each is a project that must stay unambiguous, or the check
// is reporting on "there is something under .strument/" rather than on the
// conflict it was built for.
func TestFindProjectConfigStaysQuiet(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		want  string // project-root-relative, "" for no config
	}{
		{"no config at all", nil, ""},
		{"skills but no config", []string{".strument/skills/x/SKILL.md"}, ""},
		{"dotfile beside skills", []string{".strument.star", ".strument/skills/x/SKILL.md"}, ".strument.star"},
		{"directory config beside skills", []string{".strument/config.star", ".strument/skills/x/SKILL.md"}, ".strument/config.star"},
		{"a similarly named file is not a config", []string{".strument/config.star.bak"}, ""},
		{"a config in a subdirectory does not count", []string{".strument/nested/config.star"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				write(t, dir, f, "# x\n")
			}
			got, err := FindProjectConfig(dir)
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			want := ""
			if tc.want != "" {
				want = filepath.Join(dir, filepath.FromSlash(tc.want))
			}
			if got != want {
				t.Errorf("found %q, want %q", got, want)
			}
		})
	}
}

// A directory named config.star is not a config, and must not make an otherwise
// unambiguous project refuse to start.
func TestFindProjectConfigIgnoresADirectory(t *testing.T) {
	dir := t.TempDir()
	dotfile := write(t, dir, ProjectConfigName, "# dotfile\n")
	if err := os.MkdirAll(filepath.Join(dir, ProjectConfigDir, ProjectConfigInDir), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindProjectConfig(dir)
	if err != nil {
		t.Fatalf("a directory named %s made the project ambiguous: %v", ProjectConfigInDir, err)
	}
	if got != dotfile {
		t.Errorf("found %q, want the dotfile %q", got, dotfile)
	}
}
