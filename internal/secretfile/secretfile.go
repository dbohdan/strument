// Package secretfile names the files a model may not read on its own say-so:
// the well-known homes of credentials. It is a list of gitignore-style
// patterns, built in and extended by `secret_files_add`, with
// `secret_files_exempt` for the user to take a pattern back.
//
// What a name list can and cannot do decides how it is used. It stops the
// places a generic injection would ask for — "read ~/.ssh/id_ed25519" — and
// says nothing about a secret embedded in an ordinary file. The confirmation
// for reads outside the project is what covers those; this is the second
// layer, for the targets everyone knows. Inside the project, .gitignore is the
// first layer, since a project's .env is almost always ignored already.
//
// The panel survey that shaped it (October 2026): Kimi Code asks before reading
// a short denylist (.env*, SSH private keys, credentials, .aws/credentials);
// OpenCode asks for *.env and *.env.*; Pi, Codex, and DeepSeek Harness have no
// such list. The defaults here are Kimi's, matched by path rather than by
// basename prefix, plus the other files credentials are conventionally kept in.
// By November Kimi Code refused rather than asked, and caught backup copies
// (id_rsa.bak, credentials.old, …) that these patterns do not
// (doc/experiments/2026-11-secrets-survey).
package secretfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dbohdan.com/strument/internal/gitignore"
)

// Defaults are the built-in patterns. A pattern with no leading "/" or "~/"
// matches at any depth, so ".aws/credentials" finds ~/.aws/credentials and a
// copy inside a project alike. A trailing "/" names a directory and everything
// under it.
var Defaults = []string{
	".env",
	".env.*",
	".envrc", // direnv: exported variables, usually tokens
	".netrc",
	"_netrc",
	".pgpass",
	".git-credentials",
	".npmrc",
	".pypirc",
	".vault-token",
	"id_rsa",
	"id_rsa_*",
	"id_dsa",
	"id_ecdsa",
	"id_ecdsa_*",
	"id_ed25519",
	"id_ed25519_*",
	".ssh/id_*",
	".aws/credentials",
	".config/gcloud/",
	".azure/",
	".docker/config.json",
	".kube/config",
	".config/gh/hosts.yml",
	".cargo/credentials",
	".cargo/credentials.toml",
	".terraform.d/credentials.tfrc.json",
	".gnupg/",
	".password-store/",
	".local/share/keyrings/",
}

// DefaultExempt are the names the defaults would otherwise catch and should
// not: templates committed precisely because they hold no values, and public
// keys.
var DefaultExempt = []string{
	".env.example",
	".env.sample",
	".env.template",
	"*.pub",
}

// BackupSuffixes are the endings a copy of a secret file is left with: by
// hand (id_rsa.bak, credentials.old), by an editor (.orig, .save, a trailing
// ~), or by converting a key (id_rsa.pem). Kimi Code's list, plus "~", which
// is not dot-separated. A path ending in one is judged as the name without it,
// repeatedly, so id_rsa.old.bak is id_rsa.
var BackupSuffixes = []string{
	".bak", ".backup", ".copy", ".disabled", ".key", ".old", ".orig", ".pem", ".save", ".tmp", "~",
}

// Matcher reports whether a path is secret-shaped. The zero value and nil
// match nothing.
type Matcher struct {
	deny   []rule
	exempt []rule
}

type rule struct {
	text string
	p    gitignore.Pattern
}

// New builds a matcher from the defaults plus add, less anything exempt
// matches. home expands a leading "~/"; an empty home makes such a pattern an
// error rather than one that silently matches nothing.
func New(home string, add, exempt []string) (*Matcher, error) {
	m := &Matcher{}
	for _, list := range []struct {
		pats []string
		dst  *[]rule
	}{
		{Defaults, &m.deny}, {add, &m.deny},
		{DefaultExempt, &m.exempt}, {exempt, &m.exempt},
	} {
		for _, p := range list.pats {
			r, err := compile(home, p)
			if err != nil {
				return nil, err
			}
			*list.dst = append(*list.dst, r)
		}
	}
	return m, nil
}

// Default is the built-in matcher, for a workspace no config has spoken to
// yet: `strument tool`, and every session before ApplyConfig runs. The
// defaults hold no "~/" pattern, so an unknown home cannot fail it.
func Default() *Matcher {
	home, _ := os.UserHomeDir()
	m, err := New(home, nil, nil)
	if err != nil {
		panic("secretfile: the built-in patterns do not compile: " + err.Error())
	}
	return m
}

// Validate checks patterns the way New will, for the config loader to report
// a bad one at load rather than at first use.
func Validate(patterns []string) error {
	for _, p := range patterns {
		if _, err := compile("/home", p); err != nil {
			return err
		}
	}
	return nil
}

func compile(home, text string) (rule, error) {
	p := strings.TrimSpace(text)
	switch {
	case p == "":
		return rule{}, errors.New("an empty secret-file pattern matches nothing")
	case strings.HasPrefix(p, "!"):
		// The gitignore negation would work, but only by position in a list
		// the user does not see the whole of. Exemption is its own key so
		// that taking a pattern back is a visible, separate decision.
		return rule{}, fmt.Errorf("secret-file pattern %q: use secret_files_exempt instead of a leading \"!\"", text)
	case p == "~" || strings.HasPrefix(p, "~/"):
		if home == "" {
			return rule{}, fmt.Errorf("secret-file pattern %q: the home directory is unknown", text)
		}
		p = filepath.ToSlash(home) + p[1:]
	case strings.HasPrefix(p, "/"):
	default:
		p = "**/" + p
	}
	return rule{text: text, p: gitignore.ParsePattern(p, nil)}, nil
}

// Match reports the pattern an absolute path matches, or ok false. A path an
// exemption matches is not secret whatever else it matches.
//
// A copy is judged as its original: the name is matched again with each
// backup suffix taken off (see BackupSuffixes), and an exemption of any of
// those names holds, so ~/.ssh/id_rsa.pub.bak is a public key and
// .env.example.bak a template. The copy itself can be exempted by its own
// name.
func (m *Matcher) Match(abs string) (pattern string, ok bool) {
	if m == nil {
		return "", false
	}
	parts := components(abs)
	if len(parts) == 0 {
		return "", false
	}
	forms := [][]string{parts}
	for last := parts[len(parts)-1]; ; {
		base := stripBackup(last)
		if base == last {
			break
		}
		forms = append(forms, append(parts[:len(parts)-1:len(parts)-1], base))
		last = base
	}
	for _, f := range forms {
		for _, r := range m.exempt {
			if r.p.Match(f, false) == gitignore.Exclude {
				return "", false
			}
		}
	}
	for _, f := range forms {
		for _, r := range m.deny {
			if r.p.Match(f, false) == gitignore.Exclude {
				return r.text, true
			}
		}
	}
	return "", false
}

// stripBackup takes one backup suffix off a file name, or returns it as it
// is. A name that would be left empty or a bare dot keeps its suffix: ".bak"
// is a file called .bak, not a copy of nothing.
func stripBackup(name string) string {
	for _, s := range BackupSuffixes {
		if base, ok := strings.CutSuffix(name, s); ok && base != "" && base != "." {
			return base
		}
	}
	return name
}

// components splits an absolute path for the matcher, relative to the
// filesystem root: an anchored pattern ("/etc/x", or "~/" expanded) is
// anchored there, since the matcher skips a pattern's leading empty segment.
// The volume of a Windows path is dropped.
func components(abs string) []string {
	abs = filepath.ToSlash(filepath.Clean(abs))
	abs = strings.TrimPrefix(abs, filepath.ToSlash(filepath.VolumeName(abs)))
	if !strings.HasPrefix(abs, "/") || abs == "/" {
		return nil
	}
	return strings.Split(abs[1:], "/")
}
