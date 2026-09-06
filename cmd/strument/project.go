package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dbohdan.com/strument/internal/history"
	"dbohdan.com/strument/internal/render"
)

// projectCmd groups the commands for the per-project state directories under
// $XDG_STATE_HOME/strument/projects.
//
// The directory is keyed by the project's absolute path, so renaming a project
// orphans its transcript, input history, cost ledger, resume state and undo
// stack. Before these commands the only way back was renaming a directory under
// ~/.local/state by hand. `list` makes the state visible and `adopt` re-binds an
// orphan to the project's new path.
type projectCmd struct {
	List   projectListCmd   `cmd:"" help:"List the recorded projects and their state directories."`
	Adopt  projectAdoptCmd  `cmd:"" help:"Merge a renamed project's recorded history into this one."`
	Ignore projectIgnoreCmd `cmd:"" help:"Stop offering a renamed project's history at startup."`
}

type projectListCmd struct {
	All bool `help:"Include projects whose directory still exists (default: orphans first, then the rest)." short:"a"`
}

// Run prints one line per project. Orphans — the ones whose directory is gone —
// come first and are marked, because they are the only ones anybody runs this
// command to find.
func (c *projectListCmd) Run() error {
	all, err := history.Candidates()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		fmt.Println("No projects recorded yet.")
		return nil
	}

	var orphans, live []history.Candidate
	for _, p := range all {
		if p.Exists {
			live = append(live, p)
		} else {
			orphans = append(orphans, p)
		}
	}

	if len(orphans) > 0 {
		fmt.Println("Projects whose directory is gone (renamed, moved, or deleted):")
		for _, p := range orphans {
			printProject(p)
		}
		fmt.Println()
		fmt.Println("To re-bind one, run this from the project's new location:")
		fmt.Println("  strument project adopt <old path>")
		if len(live) > 0 {
			fmt.Println()
		}
	}
	if len(live) > 0 && (c.All || len(orphans) == 0) {
		fmt.Println("Projects still at their recorded path:")
		for _, p := range live {
			printProject(p)
		}
	} else if len(live) > 0 {
		fmt.Printf("\n%s still at the recorded path; pass --all to list them.\n",
			render.Plural(len(live), "project", "projects"))
	}
	return nil
}

func printProject(p history.Candidate) {
	// "no turns" rather than "0 turns": zero reads as an answer here, where a
	// measurement would read as one more number to compare.
	turns := "no turns"
	if p.Turns > 0 {
		turns = render.Plural(p.Turns, "turn", "turns")
	}
	last := "never used"
	if !p.LastUsed.IsZero() {
		last = "last used " + humanAge(time.Since(p.LastUsed))
	}
	witness := ""
	if p.Root.GitRootCommit == "" {
		// Said out loud rather than left blank: this is exactly the project
		// that will not be offered automatically after a rename, and knowing
		// that in advance is worth a few characters.
		witness = ", no git witness"
	}
	fmt.Printf("  %s\n      %s, %s, %s%s\n", p.Root.Path, turns, last, humanBytes(p.Bytes), witness)
	fmt.Printf("      %s\n", p.Dir)
}

// humanAge is a coarse age. Precision would be false comfort here — the
// question a reader has is "is this the one I renamed last week".
func humanAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "under an hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

type projectAdoptCmd struct {
	Source string `arg:""                          help:"The project's old path, or its state directory."`
	Yes    bool   `help:"Do not ask; for scripts." short:"y"`
}

// Run merges the named orphan's state into the current project.
//
// It asks first, and prints what it would do to each file before asking,
// because the interesting half of an adopt is the half that is *not* a merge:
// resume.json and undo.json are single values where the newer one wins, and a
// person approving this should see that rather than discover it.
func (c *projectAdoptCmd) Run() error {
	projectRoot, err := historyRoot()
	if err != nil {
		return err
	}
	src, err := resolveStateDir(c.Source)
	if err != nil {
		return err
	}
	dstDir, err := history.ProjectDir(projectRoot)
	if err != nil {
		return err
	}
	if src == dstDir {
		return fmt.Errorf("%s is already this project's state directory", c.Source)
	}

	srcRoot, err := history.ReadRoot(src)
	if err != nil {
		return fmt.Errorf("%s does not look like a project state directory: %w", src, err)
	}

	fmt.Printf("Adopt into %s\n", projectRoot)
	fmt.Printf("  from %s\n", src)
	fmt.Printf("  recorded as %s\n\n", srcRoot.Path)
	for _, s := range history.PlanAdopt() {
		fmt.Printf("  %-14s %-11s %s\n", s.Name, s.Action, s.Why)
	}
	fmt.Printf("\nThe source directory is kept as %s.adopted-<timestamp>; nothing is deleted.\n",
		filepath.Base(src))

	if !c.Yes && !confirmAdopt() {
		fmt.Println("Nothing was changed.")
		return nil
	}

	// Both directories are locked for the merge. The destination may be a
	// project someone has open in another window, and the source may be too if
	// it was never really orphaned — appending to either from under a running
	// session is how a transcript gets interleaved with itself.
	dstLock, locked, err := acquireProjectLock(projectRoot)
	if err != nil {
		return fmt.Errorf("could not lock this project's state directory: %w", err)
	}
	if !locked {
		return errors.New("an instance is already running in this project; exit it before adopting")
	}
	defer dstLock.Close()

	srcLock, locked, err := lockStateDir(src)
	if err != nil {
		return fmt.Errorf("could not lock %s: %w", src, err)
	}
	if !locked {
		return fmt.Errorf("an instance is running in %s; exit it before adopting", srcRoot.Path)
	}
	defer srcLock.Close()

	if err := history.Adopt(projectRoot, src, projectRootCommit(projectRoot)); err != nil {
		return err
	}
	fmt.Printf("Adopted. This project's history now includes what was recorded under %s.\n", srcRoot.Path)
	return nil
}

// resolveStateDir accepts either a project's old path or its state directory,
// because a user who has been digging around in ~/.local/state has the second
// one in their clipboard and a user reading the startup hint has the first.
func resolveStateDir(arg string) (string, error) {
	if arg == "" {
		return "", errors.New("name the project's old path, or its state directory")
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	// A state directory: it has a root file.
	if _, err := history.ReadRoot(abs); err == nil {
		return abs, nil
	}
	// Otherwise treat it as a project path and find the directory keyed to it.
	// Deliberately not requiring the path to still exist — the whole premise is
	// that it does not.
	dir, err := history.ProjectDir(abs)
	if err != nil {
		return "", err
	}
	if _, err := history.ReadRoot(dir); err != nil {
		return "", fmt.Errorf("no recorded history for %s; `strument project list` shows what there is", arg)
	}
	return dir, nil
}

func confirmAdopt() bool {
	if !isCharDevice(os.Stdin) {
		fmt.Println("\nDeclined: there is no terminal to ask on. Pass --yes to adopt without one.")
		return false
	}
	fmt.Print("\nAdopt? (y/N) ")
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		return false
	}
	// Defaulting to no, unlike the shell prompt: this one rewrites records
	// rather than running something the user just read.
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

type projectIgnoreCmd struct {
	Source string `arg:"" help:"The project's old path, or its state directory."`
}

// Run silences the startup hint for one orphan.
//
// The hint names a real condition, so it repeats until something answers it —
// and a notice with no way to answer is one people learn to read past, which
// costs the notice its value for every other project too. Adopting answers it
// one way; this answers it the other. Nothing is deleted either way: the orphan
// stays on disk and stays in `strument project list`.
func (c *projectIgnoreCmd) Run() error {
	projectRoot, err := historyRoot()
	if err != nil {
		return err
	}
	src, err := resolveStateDir(c.Source)
	if err != nil {
		return err
	}
	if err := history.Dismiss(projectRoot, src); err != nil {
		return err
	}
	fmt.Printf("Not offering %s here again. Its history is still on disk:\n  %s\n", c.Source, src)
	fmt.Println("`strument project list` still shows it, and `strument project adopt` still works.")
	return nil
}
