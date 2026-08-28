// What an origin is, and what an allowlist entry covers.

package origin

import (
	"strings"
	"testing"
)

func TestOfReducesToHostPort(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"https://docs.python.org/3/library/net.html", "docs.python.org:443"},
		{"http://example.com/a", "example.com:80"},
		// An explicit default port normalizes away, so the two spellings of one
		// place are one origin.
		{"https://example.com:443/a", "example.com:443"},
		{"http://example.com:80/a", "example.com:80"},
		{"https://example.com:8443/a", "example.com:8443"},
		{"http://localhost:3000/api", "localhost:3000"},
		{"http://127.0.0.1:8080/", "127.0.0.1:8080"},
		{"http://[::1]:8080/", "[::1]:8080"},
		// Case in a host means nothing; case in a path means everything, and
		// only the host is what this returns.
		{"https://DOCS.Python.ORG/A", "docs.python.org:443"},
	} {
		got, err := Of(tc.url)
		if err != nil {
			t.Errorf("Of(%q): %v", tc.url, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Of(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestOfRefusesWhatCannotBeFetched(t *testing.T) {
	for _, url := range []string{
		"example.com/page",        // no scheme
		"file:///etc/passwd",      // not http(s)
		"ftp://example.com/x",     //
		"https:///just/a/path",    // no host
		"http://exa\x1bmple.com/", // control character; url.Parse rejects it
	} {
		if got, err := Of(url); err == nil {
			t.Errorf("Of(%q) = %q, want an error", url, got)
		}
	}
}

// The port is the point. "localhost" is not one place: a dev server on :3000
// and whatever else answers on :8080 have nothing to do with each other, and an
// allowlist that could not tell them apart would be useless exactly where a
// coding agent needs it most.
func TestAllowedIsPortSpecific(t *testing.T) {
	allow := []string{"docs.python.org", "localhost:3000", "[::1]:9229"}

	for _, o := range []string{
		"docs.python.org:443", // a bare entry covers the defaults,
		"docs.python.org:80",  // both of them
		"localhost:3000",
		"[::1]:9229",
	} {
		if !Allowed(o, allow) {
			t.Errorf("Allowed(%q) = false, want true", o)
		}
	}
	for _, o := range []string{
		"docs.python.org:8443", // a bare entry does not cover other ports
		"localhost:8080",       // the neighbouring dev server is not the same place
		"localhost:443",
		"[::1]:3000",
		"127.0.0.1:3000", // not the same host as "localhost", by name
		"evil.com:443",
	} {
		if Allowed(o, allow) {
			t.Errorf("Allowed(%q) = true, want false", o)
		}
	}
}

// No subdomains, and no prefix widening. On *.github.io, *.pages.dev, and
// *.s3.amazonaws.com the subdomain is whoever signed up, so a rule that
// admitted them would hand an attacker the host the user vouched for.
func TestAllowedDoesNotWiden(t *testing.T) {
	allow := []string{"example.com", "docs.rust-lang.org"}
	for _, o := range []string{
		"evil.example.com:443",
		"sub.docs.rust-lang.org:443",
		"example.com.evil.net:443",
		"notexample.com:443",
		"example.co:443",
	} {
		if Allowed(o, allow) {
			t.Errorf("Allowed(%q) = true — the allowlist widened past what was written", o)
		}
	}
	if Allowed("example.com:443", nil) {
		t.Error("an empty allowlist allowed something")
	}
}

// Case in a host is not information, in the config any more than in the URL.
// Of lowercases what it parses, so an entry that is not folded the same way
// would silently never match — the failure that looks like the allowlist being
// ignored.
func TestAllowedFoldsEntryCase(t *testing.T) {
	for _, entry := range []string{"Docs.Python.Org", "LOCALHOST:3000"} {
		o, err := Of("https://" + strings.ToLower(strings.SplitN(entry, ":", 2)[0]) + "/x")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(entry, ":") {
			o = strings.ToLower(entry)
		}
		if !Allowed(o, []string{entry}) {
			t.Errorf("Allowed(%q, [%q]) = false — entry case was not folded", o, entry)
		}
	}
}

func TestValidEntry(t *testing.T) {
	for _, e := range []string{"example.com", "docs.python.org", "localhost:3000", "[::1]:9229", "127.0.0.1", "[fe80::1]"} {
		if !ValidEntry(e) {
			t.Errorf("ValidEntry(%q) = false, want true", e)
		}
	}
	// A URL written where an origin belongs is a typo with a plausible shape,
	// which is the kind worth refusing at load rather than at 3 a.m.
	for _, e := range []string{
		"", "  ", "https://example.com", "example.com/docs", "example.com:",
		":443", "user@example.com", "example.com?x=1", "::1", "exa mple.com",
	} {
		if ValidEntry(e) {
			t.Errorf("ValidEntry(%q) = true, want false", e)
		}
	}
}
