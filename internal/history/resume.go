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
// Deliberately not the conversation. Storing doneMessages would make Strument
// re-send a context you pay for, assert something about what the model
// remembers, and blur the turn boundary that is the human's — while the
// transcript already exists for reading. What is left is the retyping, which is
// the part that actually annoys.
type Resume struct {
	Version int    `json:"version"`
	Updated string `json:"updated"`

	// Files and ReadOnly are relative to the *project* root — the one the state
	// directory is keyed on — not to the coder's root. Under --no-git the coder
	// works from the invocation directory while the project is still the git
	// worktree, so a coder-relative path written in one session and read in
	// another would point somewhere else entirely. The project root is the
	// stable referent, which is the property the whole layout rests on.
	Files    []string `json:"files,omitempty"`
	ReadOnly []string `json:"read_only,omitempty"`

	// Model is recorded only when the alias was *chosen* — -M or /model — and
	// never when it fell back to the config's default. Recording the fallback
	// would silently pin a project to whatever the default was the first time
	// it was opened, so that later editing `default` in config.star would
	// mysteriously not take effect there.
	Model string `json:"model,omitempty"`

	// AutoPinned records files Strument pinned on its own initiative, so it
	// does each exactly once. Today that is only AGENTS.md.
	//
	// It cannot be inferred from Files: /drop would erase the evidence and the
	// file would come back next session, which is the shape of an assistant
	// that will not take no for an answer. Offer once, then the pin set is
	// authoritative — dropped stays dropped, and /add brings it back the
	// ordinary way. The same principle as Model recording only a *chosen*
	// alias.
	AutoPinned []string `json:"auto_pinned,omitempty"`
}

// ResumePath is the resume file for a project root.
func ResumePath(projectRoot string) (string, error) {
	dir, err := ProjectDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "resume.json"), nil
}

// LoadResume reads a project's resume file. A missing, unreadable, malformed,
// or unrecognized-version file yields the zero value and no error.
//
// Never an error, on purpose. This is a convenience, not a record: the fixture
// loader fails loudly on a version mismatch because a wrong fixture invalidates
// a test, while a stale resume file should cost nothing more than retyping. It
// is overwritten on the next change either way.
func LoadResume(projectRoot string) Resume {
	p, err := ResumePath(projectRoot)
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
func SaveResume(projectRoot string, r Resume) error {
	p, err := ResumePath(projectRoot)
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
