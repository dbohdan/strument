// Command strument is an AI pair-programming tool for the terminal — a Go
// port of aider trimmed to the essentials.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"golang.org/x/term"

	"github.com/gofrs/flock"

	"dbohdan.com/strument/internal/client"
	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/history"
	"dbohdan.com/strument/internal/httpx"
	"dbohdan.com/strument/internal/jsonlog"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/modelconfig"
	"dbohdan.com/strument/internal/render"
	"dbohdan.com/strument/internal/repl"
	"dbohdan.com/strument/internal/repomap"
	"dbohdan.com/strument/internal/sandbox"
)

var version = "0.0.0-dev"

type chatCmd struct {
	Message       string   `help:"Send one message, apply the edits, and exit (script mode)."          short:"m"`
	Continue      bool     `help:"Generate fresh notes from the previous transcript on startup."       name:"continue"                                               short:"c"`
	Model         string   `help:"Model alias from config; defaults to the config's default."          short:"M"`
	NoGit         bool     `help:"Disable git integration even inside a repository."                   name:"no-git"`
	NoColor       bool     `help:"Disable ANSI color and styling."                                     name:"no-color"`
	DarkMode      bool     `help:"Use colors suited to a dark terminal background."                    name:"dark-mode"                                              xor:"palette"`
	LightMode     bool     `help:"Use colors suited to a light terminal background."                   name:"light-mode"                                             xor:"palette"`
	NoAutoCommits bool     `help:"Keep git integration but do not auto-commit edits."                  name:"no-auto-commits"`
	NoHistory     bool     `help:"Do not write the session to the chat-history file."                  name:"no-history"`
	JSONL         string   `help:"Also record the session to this file as JSONL, one record per line." name:"jsonl"                                                  placeholder:"FILE"`
	DryRun        bool     `help:"Report edits without writing files or committing."                   name:"dry-run"`
	Yes           bool     `help:"Answer yes to confirmations (never auto-runs shell commands)."`
	YesShell      bool     `help:"Also auto-run model-suggested shell commands."                       name:"yes-shell"`
	Files         []string `arg:""                                                                     help:"Files for the model to edit (they need not exist yet)." optional:""`
}

func (c *chatCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root := cwd

	// Git is on by default inside a repository; the worktree root becomes
	// the project root, like aider (--no-git opts out).
	// Before the config is read, and so before env_set can touch PATH: see
	// gitrepo.gitBinary. Strument's own git is the one subprocess that still
	// inherits the API key, so which binary that name resolves to is settled
	// here rather than at each call.
	gitrepo.ResolveBinary()

	var repo *gitrepo.Repo
	if !c.NoGit {
		if g, err := gitrepo.Discover(root); err == nil {
			repo = g
			root = g.Root()
		}
	}

	cfg, err := config.Load(config.Options{ProjectRoot: root})
	if err != nil {
		return err
	}
	// The environment for everything Strument starts, then the zone for its own
	// clock. Two steps because setting TZ does not move Go's clock on its own —
	// ApplyTimeZone explains why, differently on each platform.
	if err := config.ApplyEnvSet(cfg.EnvSet); err != nil {
		return err
	}
	if msg := config.ApplyTimeZone(cfg.EnvSet); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	// The project's state directory, and whatever the last session left in it.
	// Resolved before the model, because a remembered alias participates in
	// choosing one.
	projectRoot, rootErr := historyRoot()
	var res history.Resume
	if rootErr == nil {
		res = history.LoadResume(projectRoot)
	}

	// -M beats a remembered alias beats the config's default.
	alias := c.Model
	fromResume := false
	if alias == "" && res.Model != "" {
		alias, fromResume = res.Model, true
	}
	if alias == "" {
		alias = cfg.Default
	}
	model, ok := cfg.Models[alias]
	if !ok && fromResume {
		// An alias can be renamed out of the config between sessions. That is
		// not the user's mistake here and must not stop them starting.
		fmt.Fprintf(os.Stderr, "strument: remembered model %q is no longer in the config; using %q.\n", alias, cfg.Default)
		alias, ok = cfg.Default, true
		model = cfg.Models[alias]
	}
	if !ok {
		return fmt.Errorf("unknown model alias %q (aliases: %s)", alias, strings.Join(slices.Sorted(maps.Keys(cfg.Models)), ", "))
	}

	cdr := coder.New(root, model)
	cdr.DryRun = c.DryRun
	// A second sink beside the terminal, not a mode: the rendered stream stays
	// exactly what it was, so an experiment can still check what the user saw.
	// See internal/coder/record.go for why that matters.
	if c.JSONL != "" {
		jl, jerr := jsonlog.Create(c.JSONL)
		if jerr != nil {
			return fmt.Errorf("cannot write the JSONL log: %w", jerr)
		}
		defer func() { _ = jl.Close() }()
		cdr.Recorder = jl
		cdr.RecordSession(alias)
	}
	cdr.Client = client.New(model.Provider)
	if cfg.MaxSteps > 0 {
		cdr.MaxSteps = cfg.MaxSteps
	}
	if cfg.ShellTimeout != 0 {
		// Seconds in the config, a Duration in the coder; -1 carries "no limit"
		// through as a negative duration, which shellTimeout reads as such.
		cdr.ShellTimeout = time.Duration(cfg.ShellTimeout) * time.Second
	}
	if cfg.MaxErrorReflections > 0 {
		cdr.MaxErrorReflections = cfg.MaxErrorReflections
	}
	cdr.DetectLoops = cfg.DetectLoops
	// The project's named checks, which the check tool runs without asking:
	// the model supplies only a name, so nothing it says can change what runs.
	cdr.Check = cfg.Check
	cdr.CheckAuto = cfg.CheckAuto
	cdr.EnvAllow = cfg.EnvAllow
	if std, ok := cdr.Out.(*coder.StdOutput); ok {
		// Script mode's output; the REPL swaps in its own and reads the setting
		// from the config it already carries.
		std.Thinking = coder.ThinkingDisplay(cfg.ReasoningDisplay)
	}
	cdr.Summarizer = coder.NewChatSummary(client.New(model.SideModel.Provider), model.SideModel, cdr.Tokens, cdr.Out, cdr.Clock)
	cdr.Confirm = coder.AutoConfirmer{Yes: c.Yes, YesShell: c.YesShell, Fallback: terminalConfirmer{}}
	// URL scraping is a non-provider egress action, so it uses the global proxy
	// (validated at load, so the error is dead; nil transport => direct). An
	// explicit `scraper` command overrides the built-in fetcher — the opt-in path
	// for JavaScript-rendered pages — and does its own networking (no proxy).
	if len(cfg.Scraper) > 0 {
		cdr.Scrape = coder.NewCommandScraper(cfg.Scraper, 60*time.Second, func() []string {
			return coder.FilterEnv(nil, cdr.EnvAllow)
		})
	} else {
		scrapeTransport, _ := httpx.ProxyTransport(cfg.Proxy)
		cdr.Scrape = coder.NewSimpleScraper(scrapeTransport, "Strument/"+version)
	}
	if model.RepoMap {
		cdr.RepoMap = repomap.New(root)
	}
	if repo != nil {
		side := model.SideModel
		repo.CommitTrailer = gitrepo.Trailer(model.ReadableName())
		repo.Message = coder.CommitMessenger(client.New(side.Provider), side,
			cdr.Platform.Language, cdr.RecordTurnSideUsage, cdr.Out, cdr.Clock)
		repo.Sign = cfg.GitSign
		cdr.Repo = repo
		cdr.AutoCommits = !c.NoAutoCommits
		cdr.Platform.InGit = true
	}

	// File arguments are relative to the invocation directory, not the git
	// root, so resolve them here — kong no longer does, now that a nonexistent
	// file is accepted (the model creates it on request). AddFile only tracks
	// the path; the file need not exist yet.
	for _, f := range c.Files {
		if !filepath.IsAbs(f) {
			f = filepath.Join(cwd, f)
		}
		// Held to the same boundary as /add. This path used to skip the check
		// entirely, so `strument ../other/file.go` produced an editable
		// out-of-root file that /add would have refused — two layers disagreeing
		// about the same rule. Reference material outside the project goes
		// through /read-only, which is the one sanctioned way in.
		//
		// The check runs before AddFile, which resolves the path itself, so it
		// must resolve here too: the project root is git's symlink-resolved path,
		// while cwd may be in the symlink namespace (a symlinked working
		// directory, or a path reached through one). Comparing the two
		// un-resolved names puts a genuine in-project file at "../../link/..." and
		// rejects it — exactly the divergence from /add this fixes.
		if !fileInProject(root, f) {
			fmt.Fprintf(os.Stderr, "strument: skipping %s: outside the project root; pin it with /read-only instead.\n", f)
			continue
		}
		cdr.AddFile(f)
	}

	// --no-history means leave no trace: no transcript, no input history, no
	// resume file, and no directory created to hold them. Reading what a past
	// session left is still fine — that writes nothing, and refusing it would
	// make the flag a bigger behavior change than its name suggests.
	keepState := !c.NoHistory && rootErr == nil
	stateDir := ""
	if keepState {
		dir, err := history.EnsureProjectDir(projectRoot)
		if err != nil {
			keepState = false
		} else {
			stateDir = dir
			// One harness per project root at a time: two copies would otherwise
			// append to, and atomic-rename over, each other's transcript, cost
			// ledger, and undo spill, silently corrupting them. The lock is held
			// for the whole session and dropped on return (Close unlocks and
			// closes the fd), so a crash or kill cannot leave a stale lock the
			// way a PID-file scheme would.
			lk, locked, err := acquireProjectLock(projectRoot)
			if err != nil {
				return fmt.Errorf("could not lock the project state directory %s: %w", dir, err)
			}
			if !locked {
				return fmt.Errorf("an instance is already running in this project (%s); exit it before starting another", dir)
			}
			defer lk.Close()
		}
	}

	// Confinement goes on here, after the state directory exists and the lock is
	// held, and before anything the model can influence has run.
	//
	// Landlock is monotonic and applies to the whole process, so this is the
	// only chance: after this line nothing — not this process, not any command
	// it spawns, not a bug in the edit tools — can write outside the set below.
	// Applying it to Strument itself rather than to a re-exec'd child per
	// command is what makes it cover the seams for free, including runCheck,
	// which reaches exec.CommandContext directly and never touches
	// CommandRunner.
	//
	// The cost of that choice, documented rather than hidden: /run is confined
	// too, even though the user typed it, because there is no way to hold back
	// a right and hand it out later.
	cdr.Sandbox = coder.SandboxState{Required: cfg.Sandbox != ""}
	if cfg.Sandbox == config.SandboxLandlock {
		writable := sandbox.DefaultWritable(projectRoot, stateDir, cfg.SandboxWrite)
		policy := sandbox.Policy{Writable: writable}
		if err := policy.Apply(); err != nil {
			cdr.Sandbox.Unavailable = err.Error()
			// In every mode, not just the banner's. This changes what the
			// session can do — the model cannot run a command at all — and a
			// scripted run would otherwise meet a wall of refusals with nothing
			// on screen to say why.
			fmt.Fprintf(os.Stderr, "strument: a sandbox is required but unavailable (%v).\n", err)
			fmt.Fprintln(os.Stderr, "strument: the model cannot run commands. /run still works, or set `sandbox = \"\"` in your config.")
		} else {
			cdr.Sandbox.Active = true
			// What was enforced, not what was asked for. Granted drops the
			// paths that did not exist to anchor a rule to, so neither
			// /sandbox nor the hint on a denied command can promise a write
			// the kernel is about to refuse.
			granted := policy.Granted()
			cdr.Sandbox.Writable = granted
			cdr.Sandbox.Skipped = missingPaths(cfg.SandboxWrite, granted)
		}
	}

	if keepState {
		// A callback, so the coder stays ignorant of where state lives. A write
		// failure is not worth interrupting a turn over: the ledger is a record
		// to read later, and the usage line has already told the user the
		// numbers.
		cdr.RecordUsage = func(u coder.TurnUsage) {
			_ = history.AppendCost(projectRoot, history.CostEntry{
				Model:        u.Model,
				TokensSent:   u.TokensSent,
				TokensRecv:   u.TokensRecv,
				CacheRead:    u.CacheRead,
				CacheWrite:   u.CacheWrite,
				Cost:         u.Cost,
				Estimated:    u.Estimated,
				Steps:        u.Steps,
				FilesChanged: u.FilesChanged,
			})
		}

		// The undo record, which is the only one there is without git. A write
		// failure is worth a word here, unlike the ledger above: the user would
		// otherwise believe a turn is undoable tomorrow when it is not.
		cdr.SaveUndo = func(stack [][]coder.TurnEdit, commits []string, last string) {
			st := history.UndoState{Commits: commits, Last: last}
			for _, turn := range stack {
				var t history.UndoTurn
				for _, e := range turn {
					t.Entries = append(t.Entries, history.UndoEntry{
						Path: e.Path, Before: e.Before, After: e.After,
						Existed: e.Existed, Mode: e.Mode,
					})
				}
				st.Turns = append(st.Turns, t)
			}
			if err := history.SaveUndo(projectRoot, st); err != nil {
				fmt.Fprintln(os.Stderr, "strument: could not save the undo record:", err)
			}
		}
	}

	var hist *history.Writer
	if keepState {
		if p, err := resolveHistoryPath(cfg, projectRoot); err == nil {
			hist = history.New(p)
		}
	}

	if c.Continue && hist != nil {
		transcript := history.ReadTranscript(hist.Path())
		if transcript != "" {
			write := coder.NotesWriter(client.New(model.SideModel.Provider), model.SideModel, cdr.RecordSideUsage, cdr.Out, cdr.Clock)
			notes := write(transcript)
			cdr.FlushSideUsage()
			if notes != "" {
				cdr.SessionNotes = notes
				cdr.SessionNotesDate = time.Now().UTC().Format("2006-01-02 15:04")
				// The notes call is paid for; say so with the same token/cost line
				// a turn ends with, rather than leaving the charge invisible.
				cdr.ReportSideUsageDone()
			}
		}
	}

	if c.Message == "" {
		// Restoring only in the REPL keeps a scripted run reproducible from its
		// own arguments: `strument -m ...` should send what it was told to, not
		// whatever an interactive session left pinned, which the user would pay
		// for without seeing it.
		note := ""
		if len(c.Files) == 0 && rootErr == nil {
			var offered, notesRestored bool
			note, offered, notesRestored = restoreSession(cdr, projectRoot, res)
			if notesRestored {
				// Announced, never silent. The notes go into every request this
				// session, so a user who never types /notes should still know
				// they are there and how to look.
				note = strings.TrimSpace(note + "\n/notes from your last session are in context.")
			}
			// Record the offer immediately rather than waiting for a command to
			// trigger a save. A session where the user pins nothing and drops
			// nothing would otherwise never write it down, and AGENTS.md would
			// be offered again next time — which is only "once" in the sense
			// that it happens once per session.
			if offered && keepState {
				res.AutoPinned = append(res.AutoPinned, coder.AgentsFileName)
				_ = history.SaveResume(projectRoot, resumeWithPins(cdr, projectRoot, res))
			}
		}
		return c.runREPL(cfg, cdr, repo, hist, alias, projectRoot, keepState, note)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	sentBefore, recvBefore := cdr.SessionTokens()
	costBefore, _ := cdr.SessionCost()
	answer := cdr.Run(ctx, c.Message)
	if hist != nil {
		sentAfter, recvAfter := cdr.SessionTokens()
		costAfter, known := cdr.SessionCost()
		if err := hist.Append(history.Turn{
			Model:          model.QualifiedSlug(),
			TokensSent:     sentAfter - sentBefore,
			TokensReceived: recvAfter - recvBefore,
			Cost:           costAfter - costBefore,
			CostKnown:      known,
			User:           c.Message,
			Assistant:      answer,
			Files:          cdr.TurnEditedFiles(),
			Tools:          cdr.TurnToolLines(),
		}); err != nil {
			fmt.Fprintln(os.Stderr, "strument: could not write chat history:", err)
		}
	}
	return nil
}

// historyRoot is the project a transcript belongs to.
//
// It is the git worktree root wherever there is one and the working directory
// otherwise, and it is deliberately independent of --no-git. That flag says how
// a turn is committed, not which project you are in: working on one repository
// sometimes with git and sometimes without should leave one transcript, not two
// scattered by a flag whose name does not hint at it.
//
// chat and history used to derive this separately — chat honoring --no-git,
// history not — so `chat --no-git` in a subdirectory filed the transcript under
// the subdirectory while `strument history` reported the repository root. Two
// paths, two hashes, and no way to find your own history. One function now, so
// they cannot drift again.
func historyRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return historyRootFrom(cwd), nil
}

func historyRootFrom(dir string) string {
	if g, err := gitrepo.Discover(dir); err == nil {
		return filepath.Clean(g.Root())
	}
	return filepath.Clean(dir)
}

// acquireProjectLock takes a non-blocking exclusive advisory lock on the
// project's state directory, so two harness copies keyed to the same root
// cannot write its transcript, cost ledger, or undo spill concurrently. The
// returned *Flock must be Closed to release the lock; Close also closes the
// underlying file descriptor, and the kernel drops the lock if the process
// exits without it. A false locked means another instance holds it; the error
// is non-nil only for genuine failures (a missing directory is not one — the
// caller has just created it).
func acquireProjectLock(projectRoot string) (*flock.Flock, bool, error) {
	p, err := history.LockPath(projectRoot)
	if err != nil {
		return nil, false, err
	}
	lk := flock.New(p)
	locked, err := lk.TryLock()
	if err != nil {
		_ = lk.Close()
		return nil, false, err
	}
	if !locked {
		_ = lk.Close()
		return nil, false, nil
	}
	return lk, true, nil
}

// fileInProject reports whether file — resolved against the invocation
// directory and possibly absolute — lies inside the project root.
//
// Both paths are normalized through EvalSymlinks before comparison, so a
// working directory reached through a symlink, or a file argument that names
// one, is judged in the same namespace the /add path uses (it joins the pattern
// with the already-resolved coder root). Without this, a real in-project file
// arrived through the symlink namespace as "../../link/..." and was refused —
// the CLI and /add disagreeing about the same rule.
//
// A not-yet-created file resolves through its deepest existing ancestor, so
// `strument newdir/file.go` is accepted when newdir/ is inside the project.
func fileInProject(root, file string) bool {
	resolved := resolvePath(filepath.Clean(file))
	rootResolved := resolvePath(root)
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolvePath follows symlinks as far as the path exists, then re-appends the
// not-yet-created tail, so a path naming a file that is not there yet still
// resolves through the directories that are. Copies the logic already in
// internal/coder and internal/workspace rather than importing it, because the
// CLI is the top layer and must not reach down into those internals for a
// containment check.
func resolvePath(abs string) string {
	rest := ""
	dir := abs
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs // reached the root without resolving anything
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// restoreSession re-pins what the last session had pinned, returning a line for
// the banner or "" when nothing was restored.
//
// Paths are project-root-relative, so they survive the --no-git case where the
// coder's root is the invocation directory and the project is the git worktree.
// A file that has since moved is skipped rather than reported: the point is to
// save retyping, not to litigate what happened to the tree.
func restoreSession(cdr *coder.Coder, projectRoot string, res history.Resume) (note string, offered, notesRestored bool) {
	abs := func(rel string) (string, bool) {
		p := filepath.FromSlash(rel)
		if !filepath.IsAbs(p) {
			p = filepath.Join(projectRoot, p)
		}
		if _, err := os.Stat(p); err != nil {
			return "", false
		}
		return p, true
	}
	// The undo record comes back before anything else, and silently: it changes
	// nothing the user can see until they type /undo, and announcing "3 turns
	// are undoable" on every start would be noise for a fact they can ask for.
	// It is safe to restore optimistically because UndoLastTurn refuses any file
	// whose contents no longer match what Strument wrote.
	if u := history.LoadUndo(projectRoot); len(u.Turns) > 0 || len(u.Commits) > 0 {
		stack := make([][]coder.TurnEdit, 0, len(u.Turns))
		for _, t := range u.Turns {
			turn := make([]coder.TurnEdit, 0, len(t.Entries))
			for _, e := range t.Entries {
				turn = append(turn, coder.TurnEdit{
					Path: e.Path, Before: e.Before, After: e.After,
					Existed: e.Existed, Mode: e.Mode,
				})
			}
			stack = append(stack, turn)
		}
		cdr.SetUndoStack(stack)
		cdr.RestoreSessionCommits(u.Commits, u.Last)
	}

	// Notes are in memory when --continue regenerated them from the transcript
	// at startup. Report them; the REPL serves /notes from the same field.
	notesRestored = strings.TrimSpace(cdr.SessionNotes) != ""

	// AGENTS.md is the cross-tool convention for a project's standing
	// instructions to a coding agent (Codex, Cursor, Amp, Gemini CLI read it;
	// Claude Code's CLAUDE.md is the outlier, and this repository's own
	// CLAUDE.md is a symlink to it). Supporting it costs one rule and adds no
	// filename of Strument's own.
	//
	// It is pinned for *editing*, not read-only, and that needs no new safety
	// story: updating it is then an ordinary edit, which already shows a diff,
	// is snapshotted before the write, is one /undo away with or without git,
	// and lands in the turn's commit where there is one. Strument already has a
	// review surface for model-authored durable state, and it is called an
	// edit. A read-only pin would instead *refuse* the update, on a file whose
	// whole purpose is being kept current.
	//
	// Never created, only noticed. On a live configuration directory with no
	// AGENTS.md, nothing happens.
	if p, ok := abs(coder.AgentsFileName); ok && !slices.Contains(res.AutoPinned, coder.AgentsFileName) {
		cdr.AddFile(p)
		offered = true
	}

	var files, readOnly int
	for _, rel := range res.Files {
		if p, ok := abs(rel); ok {
			cdr.AddFile(p)
			files++
		}
	}
	for _, rel := range res.ReadOnly {
		if p, ok := abs(rel); ok {
			cdr.AddReadOnlyFile(p)
			readOnly++
		}
	}
	// One count, then the split, so the number the user checks against /ls is the
	// first thing on the line. "2 pins: 1 read-only" also stays on one line where
	// "1 file and 1 read-only file" was already the longer half of a sentence
	// that grows with every category.
	//
	// Only the read-only half is named, matching /ls and the banner: an ordinary
	// pin has no property to report, since any file in the project is editable
	// whether pinned or not.
	//
	// An auto-pinned AGENTS.md is deliberately not counted here. This line says
	// what a *previous session* left, and the first time AGENTS.md is noticed it
	// came from the project rather than from a session. It needs no announcement
	// of its own either: the banner lists every pinned file directly below, so
	// "Pinned AGENTS.md." is already on screen, in the same words /add would have
	// used.
	switch {
	case files == 0 && readOnly == 0:
		return "", offered, notesRestored
	case readOnly == 0:
		return fmt.Sprintf("Restored %s from your last session.", plural(files, "pin", "pins")), offered, notesRestored
	case files == 0:
		return fmt.Sprintf("Restored %s from your last session, read-only.", plural(readOnly, "pin", "pins")), offered, notesRestored
	}
	return fmt.Sprintf("Restored %s from your last session, %d of them read-only.",
		plural(files+readOnly, "pin", "pins"), readOnly), offered, notesRestored
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// saveResumeFunc returns the callback the REPL calls after a command changes
// what a resume would restore, or nil when this session leaves no trace.
//
// The alias is recorded only when it differs from the config's default. That is
// a simpler rule than "explicitly chosen" and a more robust one: recording the
// default would pin a project to whatever it happened to be the first time the
// project was opened, so that later editing `default` in config.star would
// mysteriously not take effect there. It also gives an obvious way out —
// switching back to the default stops the pinning.
func saveResumeFunc(cdr *coder.Coder, cfg *config.Config, projectRoot string, keepState bool) func(alias string) {
	if !keepState {
		return nil
	}
	return func(alias string) {
		// Carry AutoPinned forward by re-reading rather than by remembering. A
		// save that dropped it would re-offer AGENTS.md next session, and the
		// first /add of any session would be enough to trigger that — so the one
		// field whose whole job is "this happened once" would be erased by
		// ordinary use. Re-reading a small JSON file per /add costs nothing and
		// cannot go out of sync with what is on disk.
		res := resumeWithPins(cdr, projectRoot, history.Resume{
			AutoPinned: history.LoadResume(projectRoot).AutoPinned,
		})
		if alias != cfg.Default {
			res.Model = alias
		}
		_ = history.SaveResume(projectRoot, res)
	}
}

// resumeWithPins fills in what is currently pinned, keeping everything else in
// base. Shared by the per-command save above and the one-off write that records
// an auto-pin at startup, so both produce the same shape — a second path that
// wrote paths differently would resume to a different set of files than the
// first, which is the kind of divergence nobody notices until it matters.
func resumeWithPins(cdr *coder.Coder, projectRoot string, base history.Resume) history.Resume {
	base.Files = toProjectPaths(cdr, projectRoot, cdr.ChatFiles())
	base.ReadOnly = toProjectPaths(cdr, projectRoot, cdr.ReadOnlyFiles())
	return base
}

// toProjectPaths rewrites coder-relative paths as project-relative where
// possible and absolute where not.
//
// A read-only reference reached outside the project has no project-relative
// form, and dropping it would silently forget the entry that took the most
// effort to find. Editable files are always inside, so this only ever produces
// an absolute path for a reference.
func toProjectPaths(cdr *coder.Coder, projectRoot string, rels []string) []string {
	out := make([]string, 0, len(rels))
	base := projectRoot
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	for _, rel := range rels {
		p := rel
		if !filepath.IsAbs(p) {
			p = filepath.Join(cdr.Root, filepath.FromSlash(rel))
		}
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		r, err := filepath.Rel(base, p)
		if err != nil || strings.HasPrefix(r, "..") {
			out = append(out, filepath.ToSlash(p))
			continue
		}
		out = append(out, filepath.ToSlash(r))
	}
	return out
}

// resolveHistoryPath is the config override (absolute, or relative to the
// history root above) or the XDG default.
func resolveHistoryPath(cfg *config.Config, projectRoot string) (string, error) {
	if cfg.HistoryFile != "" {
		p := cfg.HistoryFile
		if !filepath.IsAbs(p) {
			p = filepath.Join(projectRoot, p)
		}
		return p, nil
	}
	return history.DefaultPath(projectRoot)
}

// paletteTheme picks the color palette from the --dark-mode/--light-mode
// flags (mutually exclusive), defaulting to aider's default palette.
func (c *chatCmd) paletteTheme() render.Theme {
	switch {
	case c.DarkMode:
		return render.DarkTheme()
	case c.LightMode:
		return render.LightTheme()
	default:
		return render.DefaultTheme()
	}
}

// terminalSize reports stdout's width and height for the horizontal rules,
// falling back to 80x24 when stdout is not a terminal.
func terminalSize() (int, int) {
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w, h
	}
	return 80, 24
}

// runREPL starts the interactive session.
func (c *chatCmd) runREPL(cfg *config.Config, cdr *coder.Coder, repo *gitrepo.Repo, hist *history.Writer,
	alias, projectRoot string, keepState bool, resumeNote string,
) error {
	// Scoped to the project like the transcript, in the directory Run already
	// created — and suppressed with it when the session leaves no trace.
	var inputHistory string
	if keepState {
		inputHistory, _ = history.InputHistoryPath(projectRoot)
	}
	r, err := repl.New(repl.Options{
		Coder:      cdr,
		Config:     cfg,
		Git:        repo,
		History:    hist,
		ModelAlias: alias,
		ResumeNote: resumeNote,
		SaveResume: saveResumeFunc(cdr, cfg, projectRoot, keepState),
		MakeClient: func(m *config.Model) llm.ModelClient { return client.New(m.Provider) },
		ReloadConfig: func() (*config.Config, error) {
			return config.Load(config.Options{ProjectRoot: cdr.Root})
		},
		Notes: func() string { return cdr.SessionNotes },
		DropNotes: func() {
			cdr.SessionNotes, cdr.SessionNotesDate = "", ""
		},
		GenerateNotes: func(_ context.Context) error {
			if hist == nil {
				return errors.New("no transcript available")
			}
			side := cdr.Model.SideModel
			if side == nil {
				return errors.New("no side model configured")
			}
			write := coder.NotesWriter(client.New(side.Provider), side, cdr.RecordSideUsage, cdr.Out, cdr.Clock)
			transcript := history.ReadTranscript(hist.Path())
			if transcript == "" {
				return errors.New("transcript is empty")
			}
			notes := write(transcript)
			cdr.FlushSideUsage()
			if notes == "" {
				return errors.New("the model returned no notes")
			}
			cdr.SessionNotes = notes
			cdr.SessionNotesDate = time.Now().UTC().Format("2006-01-02 15:04")
			cdr.ReportSideUsageDone()
			return nil
		},
		Color:      !c.NoColor && stdoutIsTerminal() && os.Getenv("NO_COLOR") == "",
		IsTerminal: drivingATerminal,
		// Only stdin: `strument | tee log` still has a human to ask.
		StdinIsTerminal: func() bool { return isCharDevice(os.Stdin) },
		HistoryFile:     inputHistory,
		Version:         version,
		Theme:           c.paletteTheme(),
		GetSize:         terminalSize,
	})
	if err != nil {
		return err
	}
	defer r.Close()
	// Route confirms through readline; --yes/--yes-shell answer first. The
	// asker has no auto variant: --yes answers permission prompts, and a
	// question is the model asking for information it cannot proceed without.
	cdr.Confirm = coder.AutoConfirmer{Yes: c.Yes, YesShell: c.YesShell, Fallback: r.Confirmer()}
	cdr.Asker = r.Asker()
	return r.Run(context.Background())
}

func stdoutIsTerminal() bool { return isCharDevice(os.Stdout) }

// drivingATerminal reports whether a human is at both ends: line editing needs
// stdin, and the banner, the per-prompt rules, and the "Waiting for <model>"
// line need stdout.
//
// The REPL has had this seam since it was written — Options.IsTerminal, with
// interactive() defaulting to true when it is nil — and main never wired it, so
// the real binary always believed it was interactive. Piping its output
// therefore wrote the banner, a full-width rule before every prompt, and the
// waiting line's "\r\x1b[K" erase into the file.
//
// That last one was not merely ugly. A trial scored answers with a line anchor,
// and the stray erase sequence sat at the start of the line it was anchored to,
// so half the sessions read as unanswered and a real effect (10/12 vs 5/12)
// came back as a clean null (5/12 vs 4/12, p=1.0). See doc/experimenting.md.
//
// Gating on Color instead would have been the wrong fix: NO_COLOR=1 in a real
// terminal would then leave the waiting line on screen, unerased, forever.
// Colour and terminal-ness are different questions.
func drivingATerminal() bool {
	return isCharDevice(os.Stdin) && isCharDevice(os.Stdout)
}

// terminalConfirmer asks y/n questions on the terminal in script mode, where
// there is no REPL and so no readline and no theme.
//
// It shows the same three parts in the same order as the REPL's confirmer, and
// defaults the same way. Script mode quietly dropping the model's reasoning was
// the Stage 9 bug; a confirmation that showed less here than there, or meant
// something different by Enter, would be the same bug at the one prompt where
// being wrong costs the most.
// stdinReader is shared across confirmations. A fresh bufio.Reader per prompt
// would refill from the pipe and then be discarded along with whatever it had
// buffered past the line it used, which loses input that was never answered.
var stdinReader = bufio.NewReader(os.Stdin)

type terminalConfirmer struct{}

func (terminalConfirmer) Confirm(req coder.ConfirmRequest) coder.ConfirmResult {
	switch {
	case req.Command != "":
		if req.Purpose != "" {
			fmt.Println("‹shell›", req.Purpose)
		} else {
			fmt.Println("‹shell› (no purpose given)")
		}
		fmt.Printf("$ %s\n", req.Command)
	case req.Subject != "":
		fmt.Println(req.Subject)
	}

	// The REPL's rlConfirmer declines rather than reading when nobody is at
	// the keyboard; this surface follows, so the two mean the same thing. Only
	// stdin is consulted: redirecting output does not take the human away.
	if !isCharDevice(os.Stdin) {
		flag := "--yes"
		if req.RequiresYesShell {
			flag = "--yes-shell"
		}
		fmt.Printf("Declined: there is no terminal to ask on. Pass %s to answer this without one.\n", flag)
		return coder.ConfirmResult{}
	}

	fmt.Printf("%s (Y/n) ", req.Prompt)
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		// No answer available at all — a closed or redirected stdin. Declining
		// is the safe reading: nobody is there to have meant yes.
		return coder.ConfirmResult{}
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return coder.ConfirmResult{Yes: true}
	default:
		return coder.ConfirmResult{}
	}
}

type trustCmd struct {
	Path string `arg:"" help:"Project directory containing .strument.star (default: cwd)." optional:""`
}

func (c *trustCmd) Run() error {
	root := c.Path
	if root == "" {
		var err error
		if root, err = os.Getwd(); err != nil {
			return err
		}
	}
	absPath, err := config.TrustProject(root, "")
	if err != nil {
		return err
	}
	fmt.Printf("Trusted %s. Re-run `strument trust` after every edit to it.\n", absPath)
	return nil
}

// configCmd inspects the resolved configuration for the current project:
// the merge of the user config and a trusted project config, with the same
// root resolution a chat session gets.
type configCmd struct {
	Models  configModelsCmd  `cmd:"" help:"Print the config's model aliases, one per line."`
	Default configDefaultCmd `cmd:"" help:"Print the config's default model alias."`
}

type configModelsCmd struct{}

func (*configModelsCmd) Run() error { return runConfigSets("models") }

type configDefaultCmd struct{}

func (*configDefaultCmd) Run() error { return runConfigSets("default") }

// loadProjectConfig loads the effective config for the current project. It
// mirrors historyRoot so the answer is the one the chat session would act on,
// not the one from a different directory that happens to hold a config file.
// A missing env() variable yields "" here instead of failing the load. These
// subcommands read the config's *shape* — which aliases exist, which is the
// default — and never make a request, so a key they cannot see costs them
// nothing. Failing instead has a cost that is easy to miss: the bash
// completion calls `config models` on every Tab and discards stderr, so a
// config whose key lives in a per-project direnv made alias completion do
// nothing at all, silently, outside that project. The substitution is
// announced on stderr, where the human who typed the command sees it and the
// completion script does not.
func loadProjectConfig() (*config.Config, error) {
	root, err := historyRoot()
	if err != nil {
		return nil, err
	}
	var missing []string
	cfg, err := config.Load(config.Options{
		ProjectRoot: root,
		LookupEnv: func(name string) (string, bool) {
			if v, ok := os.LookupEnv(name); ok {
				return v, true
			}
			if !slices.Contains(missing, name) {
				missing = append(missing, name)
			}
			return "", true
		},
	})
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "strument: not set, read as empty: %s\n", strings.Join(missing, ", "))
	}
	return cfg, err
}

// runConfigSets prints one of the config's top-level sets: the keys of
// `models`, or the value of `default`. The value goes straight to stdout so
// the command composes with pipelines.
func runConfigSets(kind string) error {
	cfg, err := loadProjectConfig()
	if err != nil {
		return err
	}
	switch kind {
	case "models":
		for _, alias := range slices.Sorted(maps.Keys(cfg.Models)) {
			fmt.Println(alias)
		}
	case "default":
		fmt.Println(cfg.Default)
	}
	return nil
}

// historyCmd prints the chat-history file for the current project (the one
// XDG makes hard to discover). It resolves the same path chat mode writes.
type historyCmd struct{}

func (*historyCmd) Run() error {
	root, err := historyRoot()
	if err != nil {
		return err
	}
	// Honor a config override when the config loads; otherwise fall back to
	// the default path so "where is my history" always answers.
	if cfg, err := config.Load(config.Options{ProjectRoot: root}); err == nil {
		if p, err := resolveHistoryPath(cfg, root); err == nil {
			fmt.Println(p)
			return nil
		}
	}
	p, err := history.DefaultPath(root)
	if err != nil {
		return err
	}
	fmt.Println(p)
	return nil
}

// modelConfigCmd scaffolds model() blocks from a provider's live catalog, so
// the tedious fields (context size, costs, cache capability) don't have to be
// looked up by hand. Output is copy-pastable Starlark on stdout — the user
// reviews it and pastes it into their config.
type modelConfigCmd struct {
	Source       string   `default:"openrouter"                                                            help:"Metadata source (currently only \"openrouter\")."    short:"s"`
	ProviderName string   `default:"openrouter"                                                            help:"Provider variable name emitted in the model() call." name:"provider-name"`
	Proxy        string   `help:"SOCKS5 proxy for the catalog fetch (default: the config's global proxy)." name:"proxy"`
	Models       []string `arg:""                                                                          help:"Exact model slugs, e.g. anthropic/claude-haiku-4.5." name:"model"`
}

// openRouterKeyFromConfig returns the API key of an OpenRouter provider in the
// config, or "" when none is configured.
func openRouterKeyFromConfig(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	for _, m := range cfg.Models {
		if m.Provider.Adapter == config.AdapterOpenRouter && m.Provider.APIKey != "" {
			return m.Provider.APIKey
		}
	}
	return ""
}

func (c *modelConfigCmd) Run() error {
	if c.Source != "openrouter" {
		return fmt.Errorf("unknown source %q (only \"openrouter\" is supported)", c.Source)
	}
	// Best-effort load the config once: it supplies the OpenRouter API key and
	// the global proxy. It may not exist yet on a first run.
	var cfg *config.Config
	if loaded, err := config.Load(config.Options{}); err == nil {
		cfg = loaded
	}

	// --proxy wins, then the config's global proxy.
	proxy := c.Proxy
	if proxy == "" && cfg != nil {
		proxy = cfg.Proxy
	}
	transport, err := httpx.ProxyTransport(proxy)
	if err != nil {
		return err
	}

	// Authentication is mandatory: unauthenticated catalog requests are
	// rate-limited and can get the IP blocked. Prefer the config's OpenRouter
	// key, fall back to OPENROUTER_API_KEY.
	apiKey := openRouterKeyFromConfig(cfg)
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}
	if apiKey == "" {
		return errors.New("model-config needs an OpenRouter API key (set OPENROUTER_API_KEY); anonymous catalog requests are rate-limited and can get your IP blocked")
	}

	src := &modelconfig.OpenRouterSource{
		APIKey:    apiKey,
		UserAgent: "Strument/" + version,
		Transport: transport,
	}
	found, missing, err := src.Lookup(c.Models)
	if err != nil {
		return err
	}
	if len(found) > 0 {
		fmt.Print(modelconfig.EmitStarlark(found, c.ProviderName))
	}
	for _, m := range missing {
		fmt.Fprintf(os.Stderr, "strument: model %q not found on %s\n", m, c.Source)
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d model(s) not found", len(missing))
	}
	return nil
}

type cli struct {
	Chat        chatCmd          `cmd:""                         default:"withargs"                                                       help:"Chat with a model about the given files (default command)."`
	Trust       trustCmd         `cmd:""                         help:"Trust the project's .strument.star config file."`
	History     historyCmd       `cmd:""                         help:"Print the path to this project's chat-history file."`
	Config      configCmd        `cmd:""                         help:"Inspect the resolved config: model aliases, or the default alias."`
	ModelConfig modelConfigCmd   `cmd:""                         help:"Print copy-pastable model() config fetched from a provider."       name:"model-config"`
	Tool        toolCmd          `cmd:""                         help:"Run one observation tool and print what a model would see."`
	Shell       shellCmd         `cmd:""                         help:"Generate shell completions."`
	Version     kong.VersionFlag `help:"Print version and exit."`
}

// missingPaths reports which of want the sandbox did not grant.
//
// Used only for the user's own sandbox_write entries: a path listed there and
// silently ignored is a config that looks applied and is not, and the user
// finds out from a denied command rather than from the setting.
func missingPaths(want, granted []string) []string {
	have := make(map[string]bool, len(granted))
	for _, p := range granted {
		have[p] = true
	}
	var missing []string
	for _, p := range want {
		if !have[p] {
			missing = append(missing, p)
		}
	}
	return missing
}

func main() {
	var c cli
	ctx := kong.Parse(&c,
		kong.Name("strument"),
		kong.Description("AI pair programming in your terminal. A Go port of aider."),
		kong.Vars{"version": version},
		kong.UsageOnError(),
	)
	if err := ctx.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "strument:", err)
		os.Exit(1)
	}
}
