// Package origin reduces a URL to the host:port that webfetch_allow, the
// confirmation prompt, and the "all on this origin" answer all speak in.
//
// Its own package because two others need the identical answer and neither can
// import the other: the config loader validates an entry at load, and the coder
// matches a fetch against it at run time. A rule stated in two packages is a
// rule that drifts, which this codebase has now watched happen twice.
package origin

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
)

// Of reduces a URL to the host:port the allowlist and the prompt both
// speak in, or says why it cannot.
//
// Port, not just host, because "localhost" is not one place: a dev server on
// :3000 and whatever else is listening on :8080 have nothing to do with each
// other, and an allowlist that could not tell them apart would be useless
// exactly where a coding agent needs it most. The same reasoning applies to a
// staging host that also runs something private on another port.
//
// A URL with no port takes the scheme's default, and an entry with no port
// matches those defaults. So `example.com` covers http and https on 80 and 443
// and nothing else; `example.com:8443` covers only that. Writing
// `example.com:443` is how to insist on https, since a plain entry admits the
// plaintext port too.
func Of(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("not a URL Strument can parse: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	case "":
		return "", errors.New("the URL needs a scheme: write https://example.com/page, not example.com/page")
	default:
		return "", fmt.Errorf("only http and https URLs can be fetched, not %q", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("the URL has no host")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(host, port), nil
}

// Allowed reports whether an origin needs no confirmation.
//
// Exact matching, no prefix or wildcard expansion, which is the rule
// envallow.go already argues for: a config that writes half a name almost
// certainly means one specific whole one, and a silent prefix match would widen
// permissions past what was written. Subdomains are not covered for a sharper
// version of the same reason — on `*.github.io`, `*.pages.dev`, and
// `*.s3.amazonaws.com` the subdomain is whoever signed up, so a rule that
// admitted them would hand an attacker the host the user vouched for. Claude
// Code, whose syntax is the most permissive in the field, still requires
// `*.github.com` to be written out and still does not let it match bare
// github.com.
//
// "Needs no confirmation", not "may be reached". The difference is the whole
// honesty of the setting: bash can curl anywhere, and Landlock does not touch
// the network, so a host restriction here would be a boundary the tool beside
// it steps over. Codex's allowed_domains genuinely restricts because its search
// is a hosted service the model cannot route around; Strument is not in that
// position and should not imply it is.
func Allowed(origin string, allow []string) bool {
	for _, entry := range allow {
		if slices.Contains(Origins(entry), origin) {
			return true
		}
	}
	return false
}

// Origins expands one allowlist entry into the origins it covers: just itself
// when it names a port, and both default ports when it does not. Exported
// because "/web allow" grants by the same rule the config file does — two ways
// of naming an origin that disagreed about what one covers would be a bug
// nobody could see.
func Origins(entry string) []string {
	e := strings.ToLower(strings.TrimSpace(entry))
	if e == "" {
		return nil
	}
	if host, port, err := net.SplitHostPort(e); err == nil && host != "" && port != "" {
		return []string{net.JoinHostPort(host, port)}
	}
	// No port, so both defaults. A bare IPv6 literal arrives bracketed —
	// SplitHostPort rejects it for having too many colons — and JoinHostPort
	// puts the brackets back.
	host := strings.TrimSuffix(strings.TrimPrefix(e, "["), "]")
	return []string{net.JoinHostPort(host, "80"), net.JoinHostPort(host, "443")}
}

// ValidEntry reports whether a webfetch_allow entry is well formed,
// so the config loader can refuse a typo at load rather than let it silently
// never match. An entry is an origin, not a URL: a bare host, a host:port, or a
// bracketed IPv6 literal, with no scheme, no path, and no credentials.
func ValidEntry(entry string) bool {
	e := strings.TrimSpace(entry)
	if e == "" || e != strings.TrimSpace(entry) {
		return false
	}
	if strings.ContainsAny(e, " \t/@?#") {
		return false
	}
	host := e
	if h, p, err := net.SplitHostPort(e); err == nil {
		if h == "" || p == "" {
			return false
		}
		host = h
	} else if strings.Contains(e, ":") && !strings.HasPrefix(e, "[") {
		// A bare IPv6 literal must be bracketed, or host:port is ambiguous.
		return false
	}
	return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]") != ""
}
