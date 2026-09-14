package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/config"
)

// The editor is a command, not a program name, and the whole reason it goes
// through the shell-word splitter is the cases in this table: an editor that
// takes a flag, and one whose path has a space in it.
func TestEditorArgv(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		want   []string
		reason string
	}{
		{
			name: "neither is set",
			want: []string{"vi", "/f"},
		},
		{
			name: "EDITOR alone",
			env:  map[string]string{"EDITOR": "nano"},
			want: []string{"nano", "/f"},
		},
		{
			name:   "VISUAL wins",
			env:    map[string]string{"VISUAL": "emacsclient", "EDITOR": "nano"},
			want:   []string{"emacsclient", "/f"},
			reason: "age-edit's order, and the convention VISUAL exists for",
		},
		{
			name: "arguments are kept",
			env:  map[string]string{"EDITOR": "code --wait"},
			want: []string{"code", "--wait", "/f"},
		},
		{
			name:   "a quoted path with a space",
			env:    map[string]string{"EDITOR": `"/opt/My Editor/bin/ed" --wait`},
			want:   []string{"/opt/My Editor/bin/ed", "--wait", "/f"},
			reason: "strings.Fields would have split this into three broken pieces",
		},
		{
			name:   "whitespace only is unset",
			env:    map[string]string{"VISUAL": "   ", "EDITOR": "nano"},
			want:   []string{"nano", "/f"},
			reason: "somebody's stray quoting, not a request to run a nameless program",
		},
		{
			name: "both empty falls back",
			env:  map[string]string{"VISUAL": "", "EDITOR": ""},
			want: []string{"vi", "/f"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := editorArgv("/f", func(k string) string { return tc.env[k] })
			if !slices.Equal(got, tc.want) {
				t.Errorf("editorArgv = %q, want %q\n%s", got, tc.want, tc.reason)
			}
		})
	}
}

// Which of the two project config spellings `--project` acts on. An existing
// file always wins; the choice only ever decides where a *first* config goes.
func TestProjectConfigPathForEdit(t *testing.T) {
	t.Run("nothing yet", func(t *testing.T) {
		root := t.TempDir()
		got, err := projectConfigPathForEdit(root)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(root, config.ProjectConfigName); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})

	t.Run("a project with a .strument directory keeps its files together", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, config.ProjectConfigDir, "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := projectConfigPathForEdit(root)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(root, config.ProjectConfigDir, filepath.FromSlash(config.ProjectConfigInDir))
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})

	// An existing config wins over the rule above, in both spellings, or
	// `config edit` would open a new empty file beside the real one.
	for _, rel := range config.ProjectConfigPaths {
		t.Run("existing "+rel+" wins", func(t *testing.T) {
			root := t.TempDir()
			// The .strument directory exists either way here, so the "keep
			// them together" branch would fire if the existing file did not
			// take precedence.
			if err := os.MkdirAll(filepath.Join(root, config.ProjectConfigDir), 0o755); err != nil {
				t.Fatal(err)
			}
			want := writeAt(t, root, rel, "shell = True\n")
			got, err := projectConfigPathForEdit(root)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}

	// The refusal comes from config.FindProjectConfig, which is the point of
	// going through it: this command and the loader cannot disagree about what
	// a conflict is.
	t.Run("both spellings is a refusal", func(t *testing.T) {
		root := t.TempDir()
		writeAt(t, root, config.ProjectConfigPaths[0], "# one\n")
		writeAt(t, root, config.ProjectConfigPaths[1], "# two\n")
		if got, err := projectConfigPathForEdit(root); err == nil {
			t.Errorf("a project with both configs resolved to %s", got)
		}
	})
}

func writeAt(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func captureStdout(t *testing.T, f func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	runErr := f()
	_ = w.Close()
	os.Stdout = saved
	var out bytes.Buffer
	if _, err := io.Copy(&out, r); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return out.String(), runErr
}

// `config path` answers for a file that is not there, which is most of the
// point: someone who has not written a config yet is exactly who needs to be
// told where it goes.
func TestConfigPathAnswersForAMissingFile(t *testing.T) {
	writeTempUserConfig(t, "# empty\n")
	userPath, err := config.DefaultUserConfigPath()
	if err != nil {
		t.Fatal(err)
	}

	out, err := captureStdout(t, func() error { return (&configPathCmd{}).Run(&configCmd{}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != userPath {
		t.Errorf("config path = %q, want the user config %q", strings.TrimSpace(out), userPath)
	}

	// --project, in a project with nothing written yet.
	out, err = captureStdout(t, func() error { return (&configPathCmd{}).Run(&configCmd{Project: true}) })
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(out)
	if filepath.Base(got) != config.ProjectConfigName {
		t.Errorf("config --project path = %q, want a path ending in %s", got, config.ProjectConfigName)
	}
	if got == userPath {
		t.Error("--project returned the user config")
	}
}

// The scope flags select one file, so the two commands that print the *merged*
// config refuse them rather than accepting them and doing something else.
func TestConfigScopeFlagsRefusedWhereTheyMeanNothing(t *testing.T) {
	for _, scope := range []configCmd{{User: true}, {Project: true}} {
		if err := scope.refuseScope("models"); err == nil {
			t.Errorf("config models accepted a scope flag: %+v", scope)
		}
	}
	// The counter-arm: with no scope flag the same call has to pass, or the
	// check above is refusing everything.
	if err := (&configCmd{}).refuseScope("models"); err != nil {
		t.Errorf("config models refused the no-flag case: %v", err)
	}
}

// `config edit` and `history edit` open the path their `path` sibling prints.
// Two commands that disagreed about which file they mean would be worse than
// either one being wrong.
func TestEditOpensThePathThatPathPrints(t *testing.T) {
	writeTempUserConfig(t, "# empty\n")

	var opened string
	var seamErr error
	seam := func(p string) error { opened = p; return seamErr }

	for _, tc := range []struct {
		name string
		run  func() error
		want func() (string, error)
	}{
		{
			name: "config --user",
			run:  func() error { return (&configEditCmd{edit: seam}).Run(&configCmd{}) },
			want: config.DefaultUserConfigPath,
		},
		{
			name: "config --project",
			run:  func() error { return (&configEditCmd{edit: seam}).Run(&configCmd{Project: true}) },
			want: (&configCmd{Project: true}).scopedFile,
		},
		{
			name: "history",
			run:  func() error { return (&historyEditCmd{edit: seam}).Run() },
			want: historyPath,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened = ""
			want, err := tc.want()
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.run(); err != nil {
				t.Fatal(err)
			}
			if opened != want {
				t.Errorf("opened %q, want %q", opened, want)
			}
		})
	}

	// An editor that exits badly must fail the command. Someone whose $EDITOR
	// is a typo must not be told the file was edited.
	seamErr = errors.New("exec: no such editor")
	if err := (&configEditCmd{edit: seam}).Run(&configCmd{}); err == nil {
		t.Error("config edit exited 0 after the editor failed")
	}
	if err := (&historyEditCmd{edit: seam}).Run(); err == nil {
		t.Error("history edit exited 0 after the editor failed")
	}
}

// Editing a project config untrusts it, and a session then silently stops
// honouring the file the user just changed. The reminder is the whole reason
// `config --project edit` is worth having over `$EDITOR .strument.star`.
func TestProjectEditRemindsToTrust(t *testing.T) {
	writeTempUserConfig(t, "# empty\n")
	root, err := historyRoot()
	if err != nil {
		t.Fatal(err)
	}

	restore := captureStderr(t)
	err = (&configEditCmd{edit: func(p string) error {
		return os.WriteFile(p, []byte("shell = True\n"), 0o644)
	}}).Run(&configCmd{Project: true})
	got := restore()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "strument trust") {
		t.Errorf("no re-trust reminder after editing a project config:\n%s", got)
	}

	// The counter-arm: trusting it and then "editing" it without changing
	// anything must say nothing, or the reminder is unconditional and people
	// will learn to read past it.
	if err := (&trustCmd{Path: root, Yes: true}).Run(); err != nil {
		t.Fatal(err)
	}
	restore = captureStderr(t)
	err = (&configEditCmd{edit: func(string) error { return nil }}).Run(&configCmd{Project: true})
	got = restore()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "strument trust") {
		t.Errorf("an unchanged, trusted config was still nagged about:\n%s", got)
	}
}

func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	return func() string {
		_ = w.Close()
		os.Stderr = saved
		var out bytes.Buffer
		if _, err := io.Copy(&out, r); err != nil {
			t.Fatal(err)
		}
		_ = r.Close()
		return out.String()
	}
}
