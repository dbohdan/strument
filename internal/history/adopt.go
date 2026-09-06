// Adopting a renamed project.
//
// A project's state directory is keyed by the absolute path of its root
// (ProjectDir), so renaming the directory orphans everything in it: transcript,
// input history, cost ledger, resume state, undo stack. Until this file existed
// the only recovery was renaming a directory under ~/.local/state by hand.
//
// The evidence used to recognize a moved project is deliberately weak and
// deliberately portable: the recorded path no longer exists, and the git root
// commit matches. Both facts are available on every platform with no syscalls
// and survive a rename, a move across filesystems, a restore from backup, and
// being carried to another machine — none of which an inode does.
//
// What that evidence cannot do is tell two clones of one repository apart: they
// share a root commit. So nothing here adopts on its own. The scan offers; a
// person types the command.

package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Candidate is one project state directory found by the scan.
type Candidate struct {
	// Dir is the state directory itself, and Root what it says about the
	// project it belongs to.
	Dir  string
	Root Root

	// Exists reports whether Root.Path is still there. An orphan is a
	// directory whose project is not.
	Exists bool

	// Turns is how many turns the transcript holds and LastUsed when the
	// directory was last written — the two facts that let a person judge
	// whether an orphan is worth adopting.
	Turns    int
	LastUsed time.Time
	Bytes    int64
}

// Matches reports whether this candidate is the same project as one with the
// given witness: an orphan whose git root commit is the same.
//
// An empty witness on either side never matches. A project with no repository,
// or one whose history has several roots, has nothing to be recognized by — it
// is listed by `strument project list` and adopted by hand, which is the honest
// answer rather than a guess dressed up as a match.
func (c Candidate) Matches(gitRootCommit string) bool {
	if gitRootCommit == "" || c.Root.GitRootCommit == "" {
		return false
	}
	return !c.Exists && c.Root.GitRootCommit == gitRootCommit
}

// projectsDir is where per-project state directories live.
func projectsDir() (string, error) {
	base, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "projects"), nil
}

// Candidates lists every project state directory, newest use first.
//
// It reads one small file per directory and stats one path. With a few hundred
// projects that is a few hundred stats — under a millisecond, and cheap enough
// to run on every startup, which is what it has to do: a session started once
// at the new path creates state there, and a scan that only ran when state was
// missing would go quiet exactly then.
//
// A directory whose root file is missing or unreadable is skipped rather than
// reported. It cannot be matched, cannot be described, and there is nothing
// useful to say about it.
func Candidates() ([]Candidate, error) {
	dir, err := projectsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var out []Candidate
	for _, e := range entries {
		if !e.IsDir() || strings.Contains(e.Name(), ".adopted-") {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		root, err := ReadRoot(sub)
		if err != nil || root.Path == "" {
			continue
		}
		c := Candidate{Dir: sub, Root: root}
		if st, err := os.Stat(root.Path); err == nil && st.IsDir() {
			c.Exists = true
		}
		c.Turns, c.LastUsed, c.Bytes = describe(sub)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	return out, nil
}

// describe reads the cheap summary of a state directory: how many turns the
// cost ledger recorded, when it was last written, and how much disk it holds.
//
// Turns come from the ledger rather than the transcript because the ledger is
// one row per turn, so counting is counting lines; the transcript would need
// parsing to say the same thing.
func describe(dir string) (turns int, last time.Time, bytes int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, time.Time{}, 0
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		bytes += info.Size()
		if info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, artifacts[artCost].name)); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.TrimSpace(line) != "" {
				turns++
			}
		}
	}
	return turns, last, bytes
}

// FindOrphan returns the single orphan matching the witness, or nil.
//
// Single is the point. Two matching orphans means two clones of one repository
// were both moved or deleted, and picking one would be picking at random —
// `strument project list` and an explicit adopt is the answer there.
func FindOrphan(gitRootCommit string) (Candidate, bool, error) {
	all, err := Candidates()
	if err != nil {
		return Candidate{}, false, err
	}
	var found Candidate
	seen := 0
	for _, c := range all {
		if !c.Matches(gitRootCommit) {
			continue
		}
		seen++
		if seen > 1 {
			return Candidate{}, false, nil
		}
		found = c
	}
	return found, seen == 1, nil
}

// Dismiss records that this project should stop being offered the orphan in
// dir. The startup hint is otherwise a line every session about a condition
// that is real but that the user has decided not to act on, and a notice with
// no way to answer it is a notice people learn to read past.
//
// It records the state directory's name rather than its recorded path: the path
// is what makes an orphan an orphan, and two projects could once have lived at
// the same one.
func Dismiss(projectRoot, dir string) error {
	p, err := artifactPath(projectRoot, artDismissed)
	if err != nil {
		return err
	}
	if IsDismissed(projectRoot, dir) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, filepath.Base(dir))
	return err
}

// IsDismissed reports whether this project was told not to be offered dir.
// A missing or unreadable file means nothing was dismissed, which is the
// reading that keeps a hint working rather than one that silences it.
func IsDismissed(projectRoot, dir string) bool {
	p, err := artifactPath(projectRoot, artDismissed)
	if err != nil {
		return false
	}
	lines, err := readLines(p)
	if err != nil {
		return false
	}
	want := filepath.Base(dir)
	for _, line := range lines {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// MergeStep is one artifact's part in an adopt, for the plan printed before
// anything moves.
type MergeStep struct {
	Name   string
	Action string // "merge", "keep newest", "skip", "rewrite"
	Why    string
}

// PlanAdopt describes what adopting srcDir into the project at projectRoot
// would do, without doing any of it.
func PlanAdopt() []MergeStep {
	steps := make([]MergeStep, 0, len(artifacts))
	for _, a := range artifacts {
		steps = append(steps, MergeStep{Name: a.name, Action: actionName(a.policy), Why: a.why})
	}
	// By file name, so the plan reads the same every time. Map order would
	// reshuffle it per run, and a plan a person is asked to approve should not
	// look different each time they are asked.
	sort.Slice(steps, func(i, j int) bool { return steps[i].Name < steps[j].Name })
	return steps
}

func actionName(p mergePolicy) string {
	switch p {
	case mergeAppend, mergeJSONLByTime:
		return "merge"
	case keepNewest:
		return "keep newest"
	case skipTransient:
		return "skip"
	case rewritten:
		return "rewrite"
	}
	return "unknown"
}

// Adopt merges the state directory srcDir into the project at projectRoot, then
// moves srcDir aside as <name>.adopted-<timestamp>.
//
// The source is preserved rather than deleted. Merging appends to files, so
// undoing one is not a matter of deleting anything — recovery means going back
// to the copy, and that only works if there is one. Nothing here deletes it
// later either; that is the user's call, and `strument project list` shows what
// is taking up room.
func Adopt(projectRoot, srcDir string, gitRootCommit string) error {
	dstDir, err := EnsureProjectDir(projectRoot, gitRootCommit)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(srcDir)
	if err != nil {
		return err
	}
	if abs == dstDir {
		return fmt.Errorf("%s is already this project's state directory", srcDir)
	}

	for _, a := range artifacts {
		src := filepath.Join(abs, a.name)
		dst := filepath.Join(dstDir, a.name)
		if err := mergeArtifact(a.policy, src, dst); err != nil {
			return fmt.Errorf("merging %s: %w", a.name, err)
		}
	}

	aside := abs + ".adopted-" + time.Now().UTC().Format("20060102T150405Z")
	return os.Rename(abs, aside)
}

// mergeArtifact applies one policy. The switch is exhaustive on purpose: a new
// policy has to be handled here, and the compiler will not say so, but the
// default case will at the first run.
func mergeArtifact(p mergePolicy, src, dst string) error {
	switch p {
	case mergeAppend:
		return appendFile(src, dst)
	case mergeJSONLByTime:
		return mergeJSONL(src, dst)
	case keepNewest:
		return keepNewestFile(src, dst)
	case skipTransient, rewritten:
		return nil
	}
	return fmt.Errorf("unhandled merge policy %d for %s", p, filepath.Base(dst))
}

// appendFile puts the source's contents before the destination's.
//
// Old first, because these are records of things that happened and the older
// session happened first. A newline is inserted when the source does not end
// with one, so two transcripts cannot be welded into one line.
func appendFile(src, dst string) error {
	older, err := os.ReadFile(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	newer, err := os.ReadFile(dst)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(older) > 0 && older[len(older)-1] != '\n' {
		older = append(older, '\n')
	}
	return writeArtifact(dst, append(older, newer...))
}

// writeArtifact writes one merged artifact back into a state directory.
//
// One choke point, so the path-traversal exemption is stated once rather than
// at each call: dst is always a file name from artifact.go joined onto a state
// directory this package computed, and merging into it is the operation the
// user asked for by naming the source.
func writeArtifact(dst string, data []byte) error {
	return os.WriteFile(dst, data, fileMode) //nolint:gosec // A state-directory path this package built from the artifact table.
}

// timestamped is the one field mergeJSONL needs out of a ledger row.
type timestamped struct {
	Time string `json:"time"`
}

// mergeJSONL interleaves two JSON Lines files by their "time" field.
//
// Sorting rather than concatenating, because an orphan is not necessarily
// older: adopting can go either way, and `cat projects/*/cost.jsonl | ...`
// already treats these rows as one stream. A row with no parsable time sorts
// first, where it is visible rather than lost at the end.
//
// Rows are moved verbatim, never re-encoded. Re-marshalling would silently
// rewrite anything a future field added and this build does not know about.
func mergeJSONL(src, dst string) error {
	older, err := readLines(src)
	if err != nil {
		return err
	}
	if len(older) == 0 {
		return nil
	}
	newer, err := readLines(dst)
	if err != nil {
		return err
	}
	all := make([]string, 0, len(older)+len(newer))
	all = append(all, older...)
	all = append(all, newer...)
	sort.SliceStable(all, func(i, j int) bool { return lineTime(all[i]) < lineTime(all[j]) })
	return writeArtifact(dst, []byte(strings.Join(all, "\n")+"\n"))
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

func lineTime(line string) string {
	var t timestamped
	if err := json.Unmarshal([]byte(line), &t); err != nil {
		return ""
	}
	return t.Time
}

// updatedAt is the one field keepNewestFile needs. Both resume.json and
// undo.json carry it, which is what makes "newest" answerable at all.
type updatedAt struct {
	Updated string `json:"updated"`
}

// keepNewestFile leaves the destination alone unless the source is newer.
//
// For state that is one value rather than a record of events: a pinned file
// list, an undo stack. There is no union to take — two undo stacks describe
// overlapping periods of one tree and cannot be ordered into a single history —
// so the question is only which one to keep, and the recorded timestamp answers
// it.
//
// Picking wrong is survivable, which is why this is allowed to be a heuristic
// at all: coder.UndoLastTurn refuses any file whose contents no longer match
// what Strument wrote, so a stale stack produces a refusal rather than
// overwriting whatever replaced it.
func keepNewestFile(src, dst string) error {
	srcData, err := os.ReadFile(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dstData, err := os.ReadFile(dst)
	if errors.Is(err, os.ErrNotExist) {
		return writeArtifact(dst, srcData)
	}
	if err != nil {
		return err
	}
	if updated(srcData) > updated(dstData) {
		return writeArtifact(dst, srcData)
	}
	return nil
}

func updated(data []byte) string {
	var u updatedAt
	if err := json.Unmarshal(data, &u); err != nil {
		return ""
	}
	return u.Updated
}
