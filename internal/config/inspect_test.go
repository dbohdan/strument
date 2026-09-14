package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// everySetting sets every key a project config can set, so the assertions below
// can compare against the classification table whole rather than against a list
// that would go stale. A key added to projectKeys and not to this file fails the
// first test with the name of what is missing.
//
// {{writable}} is filled in by inspect() with a real temporary directory,
// because `sandbox_write` is validated and a literal cannot satisfy it
// everywhere: "/tmp" is not absolute on Windows, which is where this first
// failed. It is substituted forward-slashed for a second reason that bites
// before the first — a raw Windows path is `C:\Users\RUNNER~1\…`, and `\U` is
// a Starlark escape, so the fixture would fail to parse before the
// absolute-path check ever saw it. Windows accepts `C:/Users/…` as absolute.
const everySetting = `
router = provider("openrouter", api_key = env("OR_KEY"))
corp = provider("openai", base_url = "https://llm.corp.internal:8443/v1", api_key = env("CORP_TOKEN"))

models = {
    "fast": model(router, "xiaomi/mimo-v2.5"),
    "big": model(corp, "corp/big"),
}
default = "fast"

history_file = env("SHARE_DIR") + "/history.md"
proxy = "socks5://" + env("PROXY_HOST") + ":1080"
scraper = ["curl", "-sS"]
check = {"test": ["task", "test"]}
check_auto = ["test"]
reasoning_display = "off"
max_steps = 40
max_error_reflections = 2
webfetch_allow = ["docs.example.com"]
websearch = search("searxng", url = "http://localhost:8888")
loop_detection = False
shell = True
anchored_edits = True
indent_column = True
sandbox = ""
sandbox_write = ["{{writable}}"]
shell_timeout = 30
git_sign = "ABCD1234"
env_allow = ["PATH", "HOME"]
auto_approve = ["websearch"]
env_set = {"TZ": "UTC", "CORP_TOKEN": env("CORP_TOKEN")}
example_messages = [["user", "hi"], ["assistant", "hello"]]
prompt_system_prefix = "House rules."
prompt_code = "Be terse."
prompt_ask = "Answer briefly."
prompt_commit = "One line."
prompt_read_only = "Look only."
chat_language = "en"
`

func inspect(t *testing.T, body string, env map[string]string) *ProjectInspection {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, ProjectConfigName, strings.ReplaceAll(body, "{{writable}}", filepath.ToSlash(t.TempDir())))
	for k, v := range env {
		t.Setenv(k, v)
	}
	insp, err := InspectProjectConfig(dir)
	if err != nil {
		t.Fatalf("inspecting the config: %v", err)
	}
	if insp == nil {
		t.Fatal("no inspection for a project that has a config")
	}
	return insp
}

func keys(caps []Capability) []string {
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, c.Key)
	}
	return out
}

// A config that sets everything must report everything, split the way the
// table says. Compared against projectKeys rather than against a written list:
// the point of the split is that it is exhaustive, and a test with its own copy
// of the answer could not tell.
func TestInspectReportsEveryClassifiedKey(t *testing.T) {
	insp := inspect(t, everySetting, map[string]string{
		"OR_KEY": "sk-test-key", "CORP_TOKEN": "corp-token-value",
		"SHARE_DIR": "/home/someone/private", "PROXY_HOST": "proxy.secret.internal",
	})

	var wantCaps, wantPrefs []string
	for _, f := range projectKeyOrder {
		k := projectKeys[f]
		if k.detail == nil {
			wantPrefs = append(wantPrefs, k.name)
		} else {
			wantCaps = append(wantCaps, k.name)
		}
	}
	gotCaps := keys(insp.Capabilities)
	if !slices.Equal(gotCaps, wantCaps) {
		t.Errorf("capabilities:\n got %v\nwant %v\n"+
			"A key in projectKeys that everySetting does not set looks exactly like a key "+
			"the inspection missed. Set it there.", gotCaps, wantCaps)
	}
	if !slices.Equal(insp.Preferences, wantPrefs) {
		t.Errorf("preferences:\n got %v\nwant %v", insp.Preferences, wantPrefs)
	}
	for _, c := range insp.Capabilities {
		if strings.TrimSpace(c.Detail) == "" {
			t.Errorf("%s is announced with no detail; naming the key without the specifics "+
				"is the silence this command replaced", c.Key)
		}
	}
}

// The counter-arm: a config of nothing but preferences announces nothing. Without
// it the test above would pass just as well on an inspection that called every
// key a capability.
func TestInspectAnnouncesNothingForPreferencesAlone(t *testing.T) {
	insp := inspect(t, "max_steps = 40\nchat_language = \"en\"\n", nil)
	if len(insp.Capabilities) != 0 {
		t.Errorf("a config setting only preferences announced %v", keys(insp.Capabilities))
	}
	if !slices.Equal(insp.Preferences, []string{"max_steps", "chat_language"}) {
		t.Errorf("preferences = %v", insp.Preferences)
	}
	if insp.Empty() {
		t.Error("Empty() is true for a config that sets two things")
	}
}

func TestInspectEmptyConfig(t *testing.T) {
	insp := inspect(t, "# nothing\n", nil)
	if !insp.Empty() {
		t.Errorf("a config that sets nothing is not Empty(): %+v", insp)
	}
}

func TestInspectNoConfigIsNotAnError(t *testing.T) {
	insp, err := InspectProjectConfig(t.TempDir())
	if err != nil || insp != nil {
		t.Errorf("a project with no config: insp=%+v err=%v; want nil, nil", insp, err)
	}
}

// Nothing the summary prints may contain a value that came out of the
// environment. env() resolves when the file runs, so a proxy, a path, or a base
// URL can carry a secret the file does not literally hold — which is the
// concern that ruled out storing config content in the trust store in the first
// place, and it would be ironic to reintroduce it on the way to the screen.
func TestInspectHidesEnvironmentValues(t *testing.T) {
	secrets := map[string]string{
		"OR_KEY":     "sk-or-v1-sentinel-key",
		"CORP_TOKEN": "sentinel-corp-token",
		"SHARE_DIR":  "/home/sentinel-user/private",
		"PROXY_HOST": "sentinel.proxy.internal",
	}
	insp := inspect(t, everySetting, secrets)

	var all strings.Builder
	for _, c := range insp.Capabilities {
		all.WriteString(c.Key + " " + c.Detail + "\n")
	}
	rendered := all.String()
	for name, value := range secrets {
		if strings.Contains(rendered, value) {
			t.Errorf("the value of %s reached the summary:\n%s", name, rendered)
		}
	}

	// The counter-arm. "The secret is absent" is also true of an inspection that
	// rendered nothing at all, so the fields that carried one have to be shown
	// to be present, with the variable named in place of its value.
	for _, want := range []string{
		"${SHARE_DIR}/history.md", // history_file
		"socks5://${PROXY_HOST}",  // proxy
		"llm.corp.internal:8443",  // models: a base URL that is not a secret stays
		"CORP_TOKEN",              // env_set names the variable...
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the summary does not contain %q, so the redaction check above "+
				"may have passed by rendering nothing:\n%s", want, rendered)
		}
	}
	// ...and never its value, which is the one place redaction cannot help: a
	// literal token in env_set never went through env() at all.
	if strings.Contains(rendered, "env_set") && strings.Contains(rendered, "UTC") {
		t.Errorf("env_set printed a value, not just names:\n%s", rendered)
	}
}

// A config written for someone else's machine is still summarisable, and the
// names of what could not be read are part of the summary: a reader who cannot
// see a value should at least know which one they are not seeing.
func TestInspectReportsMissingEnv(t *testing.T) {
	insp := inspect(t, "proxy = env(\"NOT_SET_ANYWHERE_XYZ\")\n", nil)
	if !slices.Contains(insp.MissingEnv, "NOT_SET_ANYWHERE_XYZ") {
		t.Errorf("MissingEnv = %v", insp.MissingEnv)
	}
	if len(insp.Capabilities) != 1 || insp.Capabilities[0].Key != "proxy" {
		t.Errorf("a missing variable stopped the inspection: %v", keys(insp.Capabilities))
	}
}

// A config that does not execute is an error, not an empty summary. The whole
// value of the step is that the user sees what the file grants, and a file
// nobody could parse grants nothing anyone can describe — so `strument trust`
// has something to refuse on rather than a blank listing to show.
func TestInspectFailsOnAConfigThatWillNotLoad(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, ProjectConfigName, "shell = )\n")
	_, err := InspectProjectConfig(dir)
	if err == nil {
		t.Fatal("a config with a syntax error inspected cleanly")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error does not name the file it is about:\n%v", err)
	}

	// A config that is merely *wrong for this machine* fails the same way, and
	// that is the case Windows CI found: a path this platform does not consider
	// absolute stops the file loading at all.
	dir2 := t.TempDir()
	write(t, dir2, ProjectConfigName, "sandbox_write = [\"relative/path\"]\n")
	if _, err := InspectProjectConfig(dir2); err == nil {
		t.Error("a sandbox_write entry that is not an absolute path inspected cleanly")
	}
}

// The path is the file the summary is about, and a project that names its
// config either way must inspect either way — the same two forms
// FindProjectConfig accepts.
func TestInspectFindsBothConfigForms(t *testing.T) {
	for _, rel := range ProjectConfigPaths {
		t.Run(rel, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, rel, "shell = True\n")
			insp, err := InspectProjectConfig(dir)
			if err != nil {
				t.Fatal(err)
			}
			if insp.Path != filepath.Join(dir, filepath.FromSlash(rel)) {
				t.Errorf("Path = %q", insp.Path)
			}
		})
	}
}
