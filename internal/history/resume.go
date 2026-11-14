package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// resumeVersion is the schema version of resume.json.
const resumeVersion = 1

// Resume is the cheap half of a session: what you would otherwise retype after
// a restart.
//
// Deliberately not the conversation, and still not — but for a narrower reason
// than when this was written. The conversation is durable now: it is in the
// session record, which `--continue` rebuilds it from. What would be wrong is
// keeping a *second* copy here, where it could disagree with the record and
// where nothing could prune it. This file stays the cheap half, written after
// every command; the record is the expensive half, written as it happens.
type Resume struct {
	Version int    `json:"version"`
	Updated string `json:"updated"`

	// Model is recorded only when the alias was *chosen* — -M or /model — and
	// never when it fell back to the config's default. Recording the fallback
	// would silently pin a project to whatever the default was the first time
	// it was opened, so that later editing `default` in config.star would
	// mysteriously not take effect there.
	Model string `json:"model,omitempty"`
}

// ResumePath is the resume file for a project root.
func ResumePath(projectRoot, session string) (string, error) {
	return sessionArtifactPath(projectRoot, session, sartResume)
}

// LoadResume reads a project's resume file. A missing, unreadable, malformed,
// or unrecognized-version file yields the zero value and no error.
//
// Never an error, on purpose. This is a convenience, not a record: the fixture
// loader fails loudly on a version mismatch because a wrong fixture invalidates
// a test, while a stale resume file should cost nothing more than retyping. It
// is overwritten on the next change either way.
func LoadResume(projectRoot, session string) Resume {
	p, err := ResumePath(projectRoot, session)
	if err != nil {
		return Resume{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Resume{}
	}
	var r Resume
	if err := json.Unmarshal(data, &r); err != nil || r.Version != resumeVersion {
		return Resume{}
	}
	return r
}

// SaveResume writes the resume file atomically, owner-only.
//
// Atomic because it is rewritten on every /add, /drop, and /model rather than
// at exit — the crash this exists to survive is exactly when a partial write
// would land. Note the temp file is created 0600 rather than chmod'd after:
// rename replaces the destination's inode, so a mode set on the original does
// not survive, which is the same trap writeAtomically and the vendored readline
// each had to be taught.
func SaveResume(projectRoot, session string, r Resume) error {
	p, err := ResumePath(projectRoot, session)
	if err != nil {
		return err
	}
	r.Version = resumeVersion
	r.Updated = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), fileMode); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
