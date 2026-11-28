package secretfile

import "testing"

func TestDefaults(t *testing.T) {
	m, err := New("/home/u", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"/home/u/.ssh/id_ed25519":                "",
		"/home/u/.aws/credentials":               ".aws/credentials",
		"/proj/.env":                             ".env",
		"/proj/sub/.env.local":                   ".env.*",
		"/proj/.envrc":                           ".envrc",
		"/home/u/.netrc":                         ".netrc",
		"/home/u/.config/gcloud/creds/adc.json":  ".config/gcloud/",
		"/home/u/.gnupg/private-keys-v1.d/x.key": ".gnupg/",
		"/home/u/.docker/config.json":            ".docker/config.json",
		"/proj/backup/id_rsa":                    "id_rsa",
	} {
		got, ok := m.Match(path)
		if !ok {
			t.Errorf("%s: not matched", path)
			continue
		}
		if want != "" && got != want {
			t.Errorf("%s: matched %q, want %q", path, got, want)
		}
	}
	for _, path := range []string{
		"/proj/.env.example",
		"/proj/.env.sample",
		"/home/u/.ssh/id_ed25519.pub",
		"/home/u/.ssh/known_hosts",
		"/home/u/.ssh/config",
		"/proj/credentials.go", // the case a basename-prefix rule gets wrong
		"/proj/internal/credentials/store.go",
		"/home/u/.config/strument/config.star",
		"/proj/environment.go",
		"/proj/.envoy.yaml",
		"/home/u/.docker/daemon.json",
	} {
		if p, ok := m.Match(path); ok {
			t.Errorf("%s: matched %q, want no match", path, p)
		}
	}
}

func TestAddAndExempt(t *testing.T) {
	m, err := New("/home/u", []string{"~/.config/strument/", "*.pem", "/etc/secret"},
		[]string{".env.test", "fixtures/*.pem"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/home/u/.config/strument/config.star", "/proj/tls/server.pem", "/etc/secret"} {
		if _, ok := m.Match(path); !ok {
			t.Errorf("%s: an added pattern should match", path)
		}
	}
	for _, path := range []string{
		"/proj/.env.test", "/proj/fixtures/a.pem",
		"/other/.config/strument/config.star", // ~ anchors to this home only
		"/srv/etc/secret",                     // a leading / anchors to the root
	} {
		if p, ok := m.Match(path); ok {
			t.Errorf("%s: matched %q, want no match", path, p)
		}
	}
}

func TestBadPatterns(t *testing.T) {
	for _, p := range []string{"", "  ", "!.env"} {
		if err := Validate([]string{p}); err == nil {
			t.Errorf("%q: want an error", p)
		}
	}
	if _, err := New("", []string{"~/.x"}, nil); err == nil {
		t.Error("~ with no home: want an error")
	}
	var m *Matcher
	if _, ok := m.Match("/proj/.env"); ok {
		t.Error("a nil matcher matched")
	}
}

// A copy of a secret file is as secret as the file, and a copy of an exempt
// one as exempt; a suffix on a name that matches nothing changes nothing.
func TestBackupCopies(t *testing.T) {
	m, err := New("/home/u", []string{"deploy/token"}, []string{".env.test"})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"/home/u/proj/id_rsa.bak":          true,
		"/home/u/backup/id_ed25519.orig":   true,
		"/home/u/id_ecdsa.old":             true,
		"/srv/keys/id_rsa.pem":             true,
		"/home/u/.aws/credentials.bak":     true,
		"/home/u/.netrc.old":               true,
		"/home/u/proj/.npmrc.bak":          true,
		"/home/u/proj/.npmrc~":             true,
		"/home/u/id_rsa.old.bak":           true,
		"/home/u/proj/deploy/token.save":   true, // a user's pattern too
		"/home/u/.ssh/id_rsa.pub.bak":      false,
		"/home/u/proj/id_ed25519.pub.orig": false,
		"/home/u/proj/.env.example.bak":    false,
		"/home/u/proj/.env.test.bak":       false, // the user's exemption
		"/home/u/proj/server.key":          false,
		"/home/u/proj/notes.bak":           false,
		"/home/u/proj/.bak":                false,
		"/home/u/proj/~":                   false,
	} {
		if _, got := m.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}
