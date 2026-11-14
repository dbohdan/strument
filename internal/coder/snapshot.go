package coder

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// errNothingToUndo is what /undo reports when this session has changed nothing.
var errNothingToUndo = errors.New("no Strument edits to undo in this session")

// A turn's edits are recoverable whether or not there is a repository. Git
// records them as a commit; the snapshot here records them as bytes, which is
// what makes Strument usable on a live configuration directory or under another
// SCM. The two are kept in step: an undo pops the stack either way.
//
// The contents come free. writeAtomically already reads every file it is about
// to write so it can roll a failed batch back; a snapshot is that same reading,
// kept instead of discarded once the batch succeeds.

// snapEntry is one file's before and after within a turn. before is the state
// at the turn's *first* write — a later batch touching the same file must not
// overwrite it, or an undo would only unwind the last step. after is the
// content of the most recent write, which is what tells an undo whether
// something else has changed the file since.
type snapEntry struct {
	before  []byte
	existed bool
	mode    os.FileMode
	after   []byte
}

// turnSnapshot is what one turn did to the working tree, in first-touch order.
type turnSnapshot struct {
	order   []string
	entries map[string]*snapEntry
	// commits are the short hashes of the commits made while this snapshot
	// was open — by the commit tool, the separate commit of the user's
	// uncommitted changes before it, or a git commit the model ran — oldest
	// first. /undo moves HEAD back past them. See undoRepo.
	commits []string
}

func newTurnSnapshot() *turnSnapshot {
	return &turnSnapshot{entries: map[string]*snapEntry{}}
}

// record notes one write. The first call for a path fixes its before-state;
// every call updates the after-state.
func (s *turnSnapshot) record(path string, before snapEntry, after string) {
	e, ok := s.entries[path]
	if !ok {
		e = &snapEntry{before: before.before, existed: before.existed, mode: before.mode}
		s.entries[path] = e
		s.order = append(s.order, path)
	}
	e.after = []byte(after)
}

func (s *turnSnapshot) empty() bool { return s == nil || (len(s.order) == 0 && len(s.commits) == 0) }

// paths lists what has been written since the last settle, in first-touch
// order. It is the set a commit should name — turnEditedFiles accumulates
// across the whole turn, because the end-of-turn history record wants every
// file the turn touched.
func (s *turnSnapshot) paths() []string {
	if s == nil {
		return nil
	}
	return slices.Clone(s.order)
}

// wrote reports whether this turn has already written path.
func (s *turnSnapshot) wrote(path string) bool {
	if s == nil {
		return false
	}
	_, ok := s.entries[path]
	return ok
}

// createdKey reports the key under which this turn's snapshot holds a file it
// created, matching the path however it is spelled: the snapshot keys on the
// spelling of the write, "sim/x.go", and a later command may say "./sim/x.go".
// A file that existed before the turn's first write to it is not one.
func (s *turnSnapshot) createdKey(root, path string) (string, bool) {
	if s == nil {
		return "", false
	}
	want := absUnder(root, path)
	for _, k := range s.order {
		if e := s.entries[k]; !e.existed && absUnder(root, k) == want {
			return k, true
		}
	}
	return "", false
}

// forget drops a path from the snapshot: what the turn did to it no longer
// needs undoing or committing.
func (s *turnSnapshot) forget(path string) {
	if s == nil {
		return
	}
	delete(s.entries, path)
	s.order = slices.DeleteFunc(s.order, func(p string) bool { return p == path })
}

func absUnder(root, p string) string {
	p = filepath.FromSlash(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}

// recordWrites folds a completed batch into the current turn's snapshot. It is
// called only after the whole batch landed: a batch that rolled back changed
// nothing and must leave no trace here.
func (c *Coder) recordWrites(plan writePlan, before map[string]snapEntry) {
	if c.turnSnap == nil {
		c.turnSnap = newTurnSnapshot()
	}
	for _, rel := range plan.WriteOrder {
		c.turnSnap.record(rel, before[rel], plan.Writes[rel])
	}
}

// pushTurnSnapshot closes the turn's snapshot and puts it on the undo stack.
// An empty turn leaves the stack alone, so /undo always reaches the last turn
// that actually changed something.
func (c *Coder) pushTurnSnapshot() {
	if c.turnSnap.empty() {
		c.turnSnap = nil
		return
	}
	c.undoStack = append(c.undoStack, c.turnSnap)
	c.turnSnap = nil
	c.trimUndoStack()
	c.saveUndo()
}

// trimUndoStack drops the oldest turns beyond the retention depth.
//
// The reasoning is the one history's own retention comment already gives for
// the persisted copy: "the stack is bounded from the bottom because only the
// top is reachable: /undo pops, so a turn buried under twenty others is not
// something anyone is about to restore, while its bytes are as heavy as the top
// one's." That was worked out for disk and never carried across, so the
// in-memory stack grew for the life of the session — holding every turn's
// before *and* after contents for every file it touched, while the writer
// discarded all but the newest twenty on every save.
//
// Two costs, and the second is the quiet one. Memory grew without bound. And
// UndoStack() walks the whole stack on every turn to build the slice the writer
// then trims, so the per-turn work grew with the session: linear each turn,
// quadratic across one. Neither is visible in a session short enough to fit a
// person's patience, which is why it survived this long.
//
// The evicted slots are nil'd rather than left in the backing array, so the
// snapshots are released when the trim happens rather than whenever the next
// append outgrows the array and copies only the live elements forward.
//
// That difference is deliberate but small, and it is untested: reslicing alone
// is also eventually bounded, by exactly that reallocation, so the two are
// indistinguishable to anything a test can observe here. Two attempts at a
// guard — inspecting the slots past the length, then a weak reference to an
// evicted turn — both passed against the implementation they were written to
// reject. The bound itself is covered; this line rests on the argument above.
func (c *Coder) trimUndoStack() {
	depth := c.undoDepth()
	n := len(c.undoStack)
	if n <= depth {
		return
	}
	copy(c.undoStack, c.undoStack[n-depth:])
	for i := depth; i < n; i++ {
		c.undoStack[i] = nil
	}
	c.undoStack = c.undoStack[:depth]
}

// undoDepth is how many turns the in-memory stack keeps. Zero means the
// default, so a Coder built literally — as tests do — is bounded too. An
// unbounded default is the bug this exists to fix.
func (c *Coder) undoDepth() int {
	if c.MaxUndoTurns > 0 {
		return c.MaxUndoTurns
	}
	return defaultUndoDepth
}

// HasTurnSnapshot reports whether there is a turn to undo.
func (c *Coder) HasTurnSnapshot() bool { return len(c.undoStack) > 0 }

// TurnEdit is one file's before and after, in the shape a caller can persist.
// snapEntry stays unexported: the coder owns what an undo *means* — first-touch
// before-state, latest after-state, all-or-nothing restore — and a caller that
// could build one directly could build an incoherent one.
type TurnEdit struct {
	Path    string
	Before  []byte
	After   []byte
	Existed bool
	Mode    os.FileMode
}

// UndoTurn is one entry of the undo stack in the shape a caller can persist:
// the files' before and after, and the commits made while it was open.
type UndoTurn struct {
	Edits   []TurnEdit
	Commits []string
}

// UndoStack exports the stack, oldest turn first, for a caller that stores it.
func (c *Coder) UndoStack() []UndoTurn {
	out := make([]UndoTurn, 0, len(c.undoStack))
	for _, snap := range c.undoStack {
		out = append(out, snap.export())
	}
	return out
}

func (s *turnSnapshot) export() UndoTurn {
	turn := UndoTurn{Edits: make([]TurnEdit, 0, len(s.order)), Commits: slices.Clone(s.commits)}
	for _, rel := range s.order {
		e := s.entries[rel]
		turn.Edits = append(turn.Edits, TurnEdit{
			Path: rel, Before: e.before, After: e.after,
			Existed: e.existed, Mode: e.mode,
		})
	}
	return turn
}

// SetUndoStack replaces the stack with one a caller loaded. Called once at
// startup, before any turn runs.
//
// A stack restored from a previous session cannot do damage, and that is a
// property of UndoLastTurn rather than of this function: it refuses any file
// whose contents no longer match what Strument wrote, so a stale entry produces
// the refusal "<file> has changed since Strument wrote it" instead of
// overwriting whatever replaced it. The load is therefore allowed to be
// optimistic — the guard is downstream and already exists.
func (c *Coder) SetUndoStack(stack []UndoTurn) {
	c.undoStack = nil
	for _, turn := range stack {
		snap := newTurnSnapshot()
		snap.commits = slices.Clone(turn.Commits)
		for _, e := range turn.Edits {
			snap.order = append(snap.order, e.Path)
			snap.entries[e.Path] = &snapEntry{
				before: e.Before, existed: e.Existed, mode: e.Mode, after: e.After,
			}
		}
		if !snap.empty() {
			c.undoStack = append(c.undoStack, snap)
		}
	}
	// A stack loaded from disk is already within the writer's retention, but
	// this session's depth is what governs from here — and a file written by a
	// build with a larger depth must not put the session back above its own.
	c.trimUndoStack()
}

// RestoreLastCommit puts back the short hash of the session's last commit,
// which the undo record carries across a restart.
func (c *Coder) RestoreLastCommit(last string) { c.lastCommitHash = last }

// Pending is the model's uncommitted work in the shape a caller can persist:
// the paths it has written since its last commit, and the user's contents of
// those of them that had uncommitted changes before it began (nil: deleted).
type Pending struct {
	Edits    []string
	Baseline map[string][]byte
}

// PendingWork exports the record for saveUndo and the startup recovery. Only the baseline of pending paths
// is kept: the rest describes files the model has not touched, which the next
// run's turn start reads afresh.
func (c *Coder) PendingWork() Pending {
	p := Pending{Edits: slices.Sorted(maps.Keys(c.pendingEdits))}
	for _, rel := range p.Edits {
		if data, ok := c.userBaseline[rel]; ok {
			if p.Baseline == nil {
				p.Baseline = map[string][]byte{}
			}
			p.Baseline[rel] = data
		}
	}
	return p
}

// RestorePending puts back what the last run left uncommitted, at startup. The
// record is open from here, so the next turn does not retake a baseline that
// would read the model's own edits as the user's; the user's other dirty files
// are then not separated from a model commit, which only matters for files the
// model also touches before committing, and those it will have read first.
func (c *Coder) RestorePending(p Pending) {
	c.pendingEdits, c.pendingStaged, c.userBaseline, c.pendingOpen = nil, nil, nil, false
	c.dropStartMark()
	if len(p.Edits) == 0 {
		return
	}
	c.pendingEdits = map[string]bool{}
	for _, rel := range p.Edits {
		c.pendingEdits[rel] = true
	}
	c.userBaseline = maps.Clone(p.Baseline)
	c.pendingOpen = true
}

// saveUndo hands the stack to whoever stores it. A callback rather than a
// writer, for the same reason RecordUsage is one: the coder knows nothing about
// where state lives, and a session that leaves no trace passes nil.
func (c *Coder) saveUndo() {
	if c.SaveUndo == nil {
		return
	}
	c.SaveUndo(c.UndoStack(), c.lastCommitHash, c.PendingWork())
}

// UndoResult is what /undo did: the files it put back and the commits it took
// off the branch, oldest first.
type UndoResult struct {
	Restored []string
	Removed  []string
}

// undoRepo is the part of a Repo /undo needs to take a turn's commits back
// off the branch. gitrepo implements it.
type undoRepo interface {
	// LastShorts lists the n newest commits' short hashes, newest first.
	LastShorts(n int) ([]string, error)
	MergesSince(base string) (bool, error)
	Published(sha string) bool
	ChangedInRange(rev string) ([]string, error)
	InCommit(commitish, rel string) bool
	IsDirty(rel string) bool
	CheckoutFileFrom(rev, rel string) error
	ResetSoft(rev string) error
	// ResetPaths sets the index entries of paths to rev's, leaving the
	// working tree alone.
	ResetPaths(rev string, paths []string) error
}

// UndoLastTurn puts back what the last turn wrote, and when the turn made
// commits, takes them back off the branch.
//
// The snapshot is the record either way. It used to be only the no-git
// undo, and with a repository /undo moved HEAD back one commit, because every
// turn ended in exactly one. That stopped being true when committing became
// the model's: a turn can now end uncommitted, or commit twice, or commit
// work an earlier turn wrote. So the files come back from the snapshot, and
// the commits the snapshot recorded go with it — provided they are still the
// newest on the branch, unpushed and not merges. If someone committed on top,
// /undo refuses rather than unwinding their commit along with the model's.
//
// Nothing is restored unless everything is: a file whose contents no longer
// match what Strument wrote has been changed by someone else, and quietly
// overwriting that is the one outcome an undo must never produce. A file the
// undo cannot write — read-only, or a directory now standing where the file
// was — is the same problem arriving later, so a failure part of the way
// through puts the turn's own result back rather than leaving the tree in a
// state neither the user nor Strument asked for.
//
// It restores contents and not modes, deliberately. writeAtomically preserves a
// file's mode through an edit, so the turn never changed one; a chmod between
// the turn and the undo belongs to whoever ran it.
func (c *Coder) UndoLastTurn() (UndoResult, error) {
	n := len(c.undoStack)
	if n == 0 {
		return UndoResult{}, errNothingToUndo
	}
	snap := c.undoStack[n-1]

	// The git half is checked first and done last: every refusal comes before
	// anything is touched.
	var r undoRepo
	var base string
	var gitOnly, gitAlso []string
	if len(snap.commits) > 0 {
		var ok bool
		if r, ok = c.Repo.(undoRepo); !ok || c.Repo == nil {
			return UndoResult{}, errors.New("the last turn made commits, and there is no repository to undo them in")
		}
		var err error
		if base, gitOnly, gitAlso, err = c.checkTurnCommits(r, snap); err != nil {
			return UndoResult{}, err
		}
	}

	// Undo rewrites files behind the harness's back, so every stamp is now
	// about a version that no longer exists. Dropping them returns to
	// fail-open rather than reporting the user's own undo as staleness.
	c.shown.forget()

	for _, rel := range snap.order {
		e := snap.entries[rel]
		full := c.fullPath(rel)
		current, err := os.ReadFile(full)
		if err != nil {
			// Gone from disk. Whatever happened to it, putting the previous
			// contents back cannot destroy work that is no longer there.
			continue
		}
		if string(current) != string(e.after) {
			return UndoResult{}, fmt.Errorf("%s has changed since Strument wrote it; undo would discard that", rel)
		}
		if e.existed {
			// Fail before touching anything rather than halfway through. The
			// rollback below is the backstop, not the plan.
			f, err := os.OpenFile(full, os.O_WRONLY, 0)
			if err != nil {
				return UndoResult{}, fmt.Errorf("cannot write %s to undo it: %w", rel, err)
			}
			f.Close()
		}
	}

	var res UndoResult
	for _, rel := range snap.order {
		if err := c.restoreFile(rel, snap.entries[rel]); err != nil {
			c.redoFiles(snap, res.Restored)
			return UndoResult{}, fmt.Errorf("could not restore %s, so nothing was undone: %w", rel, err)
		}
		res.Restored = append(res.Restored, rel)
	}

	if r != nil {
		// Files only git touched — a git mv, a staged formatter run — come
		// back from the base commit; the snapshot's own files are already
		// back, and only their index entries need to follow HEAD down.
		for _, rel := range gitOnly {
			if err := r.CheckoutFileFrom(base, rel); err != nil {
				return res, fmt.Errorf("restored the files, but could not restore %s from before the commits: %w", rel, err)
			}
			res.Restored = append(res.Restored, rel)
		}
		if err := r.ResetSoft(base); err != nil {
			return res, fmt.Errorf("restored the files, but could not move the branch back: %w", err)
		}
		if err := r.ResetPaths(base, gitAlso); err != nil {
			return res, fmt.Errorf("moved the branch back, but could not reset the index for %s: %w",
				strings.Join(gitAlso, ", "), err)
		}
		res.Removed = slices.Clone(snap.commits)
		c.lastCommitHash = ""
	}

	c.undoStack = c.undoStack[:n-1]
	c.saveUndo()
	c.repend(res.Restored)
	return res, nil
}

// checkTurnCommits is /undo's gate on taking a turn's commits off the branch,
// and works out what the git half has to do: base, the commit before the
// turn's first; gitOnly, the files the commits changed that the snapshot does
// not hold; gitAlso, those it does.
func (c *Coder) checkTurnCommits(r undoRepo, snap *turnSnapshot) (base string, gitOnly, gitAlso []string, err error) {
	k := len(snap.commits)
	newest, err := r.LastShorts(k + 1)
	if err != nil {
		return "", nil, nil, fmt.Errorf("could not read the branch: %w", err)
	}
	if len(newest) < k+1 {
		return "", nil, nil, errors.New("the last turn's first commit is the repository's first; undo it with git")
	}
	for i := range k {
		if newest[i] != snap.commits[k-1-i] {
			return "", nil, nil, fmt.Errorf("the last turn's commits are no longer the newest on the branch "+
				"(%s is on top of them); undo would remove that too", newest[0])
		}
	}
	base = newest[k]
	if merges, err := r.MergesSince(base); err != nil || merges {
		return "", nil, nil, errors.New("the last turn's commits include a merge; undo them with git")
	}
	if r.Published(snap.commits[k-1]) {
		return "", nil, nil, errors.New("the last turn's commits have already been pushed; undoing them would rewrite published history")
	}
	changed, err := r.ChangedInRange(base)
	if err != nil {
		return "", nil, nil, fmt.Errorf("could not read what the commits changed: %w", err)
	}
	for _, rel := range changed {
		if snap.wrote(rel) {
			gitAlso = append(gitAlso, rel)
			continue
		}
		if r.IsDirty(rel) {
			return "", nil, nil, fmt.Errorf("%s has uncommitted changes; commit or stash them first", rel)
		}
		if !r.InCommit(base, rel) {
			return "", nil, nil, fmt.Errorf("%s did not exist before the commits; undo them with git", rel)
		}
		gitOnly = append(gitOnly, rel)
	}
	return base, gitOnly, gitAlso, nil
}

// repend brings the pending record in line with files an undo just put back.
// They are no longer the model's work as far as a commit is concerned, and
// what they hold now, if it differs from HEAD, is treated as the user's: it is
// committed on its own before a later model commit touching the file, rather
// than inside it. That can credit an earlier turn's uncommitted edit to the
// user, which is the safer of the two mistakes — the other puts the user's
// work in a commit with the model's name on it.
func (c *Coder) repend(paths []string) {
	for _, rel := range paths {
		delete(c.pendingEdits, rel)
		delete(c.pendingStaged, rel)
		if c.Repo == nil || !c.Repo.IsDirty(rel) {
			delete(c.userBaseline, rel)
			continue
		}
		data, err := os.ReadFile(c.fullPath(rel))
		if err != nil {
			data = nil // gone: the user's deletion, as readUncommitted records it
		}
		if c.userBaseline == nil {
			c.userBaseline = map[string][]byte{}
		}
		c.userBaseline[rel] = data
	}
}

// restoreFile puts one file back the way the turn found it.
func (c *Coder) restoreFile(rel string, e *snapEntry) error {
	full := c.fullPath(rel)
	if !e.existed {
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	// The mode argument applies only if the file has to be created — if it is
	// still there, its current mode is kept, which is the point.
	return os.WriteFile(full, e.before, e.mode)
}

// redoFiles puts the turn's own result back on the files an aborted undo had
// already reverted, so a failure leaves the tree where the undo found it.
func (c *Coder) redoFiles(snap *turnSnapshot, paths []string) {
	for _, rel := range paths {
		e := snap.entries[rel]
		full := c.fullPath(rel)
		mode := e.mode
		if !e.existed {
			mode = newFileMode // the turn created it; recreate it as the turn did
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
		}
		_ = os.WriteFile(full, e.after, mode)
	}
}
