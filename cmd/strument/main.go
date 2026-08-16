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

	"dbohdan.com/strument/internal/client"
	"dbohdan.com/strument/internal/coder"
	"dbohdan.com/strument/internal/config"
	"dbohdan.com/strument/internal/gitrepo"
	"dbohdan.com/strument/internal/history"
	"dbohdan.com/strument/internal/httpx"
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/modelconfig"
	"dbohdan.com/strument/internal/render"
	"dbohdan.com/strument/internal/repl"
	"dbohdan.com/strument/internal/repomap"
)

var version = "0.0.0-dev"

type chatCmd struct {
	Message       string   `help:"Send one message, apply the edits, and exit (script mode)."    short:"m"`
	Model         string   `help:"Model alias from config; defaults to the config's default."    short:"M"`
	NoGit         bool     `help:"Disable git integration even inside a repository."             name:"no-git"`
	NoColor       bool     `help:"Disable ANSI color and styling."                               name:"no-color"`
	DarkMode      bool     `help:"Use colors suited to a dark terminal background."              name:"dark-mode"                                              xor:"palette"`
	LightMode     bool     `help:"Use colors suited to a light terminal background."             name:"light-mode"                                             xor:"palette"`
	NoAutoCommits bool     `help:"Keep git integration but do not auto-commit edits."            name:"no-auto-commits"`
	NoHistory     bool     `help:"Do not write the session to the chat-history file."            name:"no-history"`
	DryRun        bool     `help:"Report edits without writing files or committing."             name:"dry-run"`
	Yes           bool     `help:"Answer yes to confirmations (never auto-runs shell commands)."`
	YesShell      bool     `help:"Also auto-run model-suggested shell commands."                 name:"yes-shell"`
	Files         []string `arg:""                                                               help:"Files for the model to edit (they need not exist yet)." optional:""`
}

func (c *chatCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root := cwd

	// Git is on by default inside a repository; the worktree root becomes
	// the project root, like aider (--no-git opts out).
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
	cdr.Client = client.New(model.Provider)
	// The project's named checks, which the verify tool runs without asking:
	// the model supplies only a name, so nothing it says can change what runs.
	cdr.Verify = cfg.Verify
	cdr.VerifyAuto = cfg.VerifyAuto
	if std, ok := cdr.Out.(*coder.StdOutput); ok {
		// Script mode's output; the REPL swaps in its own and reads the setting
		// from the config it already carries.
		std.Thinking = coder.ThinkingDisplay(cfg.ReasoningDisplay)
	}
	cdr.Summarizer = coder.NewChatSummary(client.New(model.WeakModel.Provider), model.WeakModel, cdr.Tokens)
	cdr.Confirm = coder.AutoConfirmer{Yes: c.Yes, YesShell: c.YesShell, Fallback: terminalConfirmer{}}
	// URL scraping is a non-provider egress action, so it uses the global proxy
	// (validated at load, so the error is dead; nil transport => direct). An
	// explicit `scraper` command overrides the built-in fetcher — the opt-in path
	// for JavaScript-rendered pages — and does its own networking (no proxy).
	if len(cfg.Scraper) > 0 {
		cdr.Scrape = coder.NewCommandScraper(cfg.Scraper, 60*time.Second)
	} else {
		scrapeTransport, _ := httpx.ProxyTransport(cfg.Proxy)
		cdr.Scrape = coder.NewSimpleScraper(scrapeTransport, "Strument/"+version)
	}
	if model.RepoMap {
		cdr.RepoMap = repomap.New(root)
	}
	if repo != nil {
		weak := model.WeakModel
		repo.CommitTrailer = gitrepo.Trailer(model.ReadableName())
		repo.Message = coder.CommitMessenger(client.New(weak.Provider), weak,
			cdr.Platform.Language, cdr.RecordSideUsage)
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
		if rel, err := filepath.Rel(root, f); err != nil || strings.HasPrefix(rel, "..") {
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
	if keepState {
		if _, err := history.EnsureProjectDir(projectRoot); err != nil {
			keepState = false
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
				note = strings.TrimSpace(note + " Notes from your last session are in context; /notes to read them.")
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

	// The previous session's notes. Loaded once here rather than read per turn:
	// they describe the session *before* this one, and refreshing them as they
	// are regenerated would show the model a summary of turns already sitting in
	// its own history.
	if notes := history.LoadNotes(projectRoot); strings.TrimSpace(notes) != "" {
		cdr.SessionNotes = notes
		cdr.SessionNotesDate = "date unknown"
		if p, err := history.NotesPath(projectRoot); err == nil {
			if info, err := os.Stat(p); err == nil {
				cdr.SessionNotesDate = info.ModTime().Format("2006-01-02 15:04")
			}
		}
		notesRestored = true
	}

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
	// first thing on the line. "2 pins: 1 for editing, 1 read-only" also stays on
	// one line where "1 file and 1 read-only file" was already the longer half of
	// a sentence that grows with every category.
	//
	// An auto-pinned AGENTS.md is deliberately not counted here. This line says
	// what a *previous session* left, and the first time AGENTS.md is noticed it
	// came from the project rather than from a session. It needs no announcement
	// of its own either: the banner lists every pinned file directly below, so
	// "Pinned AGENTS.md for editing." is already on screen, in the same words
	// /add would have used.
	switch {
	case files == 0 && readOnly == 0:
		return "", offered, notesRestored
	case readOnly == 0:
		return fmt.Sprintf("Restored %s from your last session, for editing.", plural(files, "pin", "pins")), offered, notesRestored
	case files == 0:
		return fmt.Sprintf("Restored %s from your last session, read-only.", plural(readOnly, "pin", "pins")), offered, notesRestored
	}
	return fmt.Sprintf("Restored %s from your last session: %d for editing, %d read-only.",
		plural(files+readOnly, "pin", "pins"), files, readOnly), offered, notesRestored
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

// notesTurnInterval is how many turns pass between regenerations.
//
// Debounced rather than written at exit, because sessions do not reliably end
// with /exit: Ctrl-C, a closed terminal and a dropped connection are all
// ordinary, and those are exactly the times you want the notes. Debounced
// rather than every turn, because each regeneration is a weak-model call — one
// per three turns is roughly 3% of a turn's cost by the commit-message
// measurement, where one per turn would be closer to 9%.
const notesTurnInterval = 3

// notesDropped is set by /notes drop and read by the per-turn updater.
//
// Deleting the file is not enough on its own: the debounce would regenerate it
// two or three turns later, and the user would watch a thing they discarded
// come back — the same "will not take no for an answer" shape the AGENTS.md
// offer-once rule exists to avoid. Session-scoped, so the next session starts
// writing them again, which is right: dropping says "these are wrong now", not
// "never again".
var notesDropped bool

// notesUpdater returns the per-turn hook that refreshes a project's session
// notes, or nil when the session leaves no trace.
//
// Regenerated from the transcript rather than from the previous notes. Folding
// a summary into a summary is what makes every iterative compaction scheme
// degrade — the documented failure of compaction in every harness surveyed —
// and the transcript is a durable record that makes the fold avoidable.
func notesUpdater(cdr *coder.Coder, cfg *config.Config, hist *history.Writer,
	projectRoot string, keepState bool,
) func() {
	if !keepState || hist == nil || cfg == nil {
		return nil
	}
	weak := cdr.Model.WeakModel
	if weak == nil {
		return nil
	}
	write := coder.NotesWriter(client.New(weak.Provider), weak, cdr.RecordSideUsage)
	turns := 0
	return func() {
		if notesDropped {
			return
		}
		turns++
		if turns%notesTurnInterval != 0 {
			return
		}
		transcript := history.ReadTranscript(hist.Path())
		if transcript == "" {
			return
		}
		if notes := write(transcript); notes != "" {
			_ = history.SaveNotes(projectRoot, notes)
		}
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
		UpdateNotes: notesUpdater(cdr, cfg, hist, projectRoot, keepState),
		Notes:       func() string { return history.LoadNotes(projectRoot) },
		DropNotes: func() {
			notesDropped = true
			cdr.SessionNotes, cdr.SessionNotesDate = "", ""
			_ = history.SaveNotes(projectRoot, "")
		},
		Color:       !c.NoColor && stdoutIsTerminal() && os.Getenv("NO_COLOR") == "",
		IsTerminal:  drivingATerminal,
		HistoryFile: inputHistory,
		Version:     version,
		Theme:       c.paletteTheme(),
		GetSize:     terminalSize,
	})
	if err != nil {
		return err
	}
	defer r.Close()
	// Route confirms through readline; --yes/--yes-shell answer first.
	cdr.Confirm = coder.AutoConfirmer{Yes: c.Yes, YesShell: c.YesShell, Fallback: r.Confirmer()}
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

	fmt.Printf("%s (Y/n) ", req.Prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
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
	Chat        chatCmd          `cmd:""                         default:"withargs"                                                 help:"Chat with a model about the given files (default command)."`
	Trust       trustCmd         `cmd:""                         help:"Trust the project's .strument.star config file."`
	History     historyCmd       `cmd:""                         help:"Print the path to this project's chat-history file."`
	ModelConfig modelConfigCmd   `cmd:""                         help:"Print copy-pastable model() config fetched from a provider." name:"model-config"`
	Tool        toolCmd          `cmd:""                         help:"Run one observation tool and print what a model would see."`
	Version     kong.VersionFlag `help:"Print version and exit."`
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
