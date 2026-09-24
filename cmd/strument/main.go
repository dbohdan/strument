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
	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/modelconfig"
	"dbohdan.com/strument/internal/render"
	"dbohdan.com/strument/internal/repl"
	"dbohdan.com/strument/internal/sandbox"
	"dbohdan.com/strument/internal/skill"
	"dbohdan.com/strument/internal/workspace"
)

var version = "0.0.0-dev"

// Flag values are written <lowercase> in angle brackets, matching what /help
// prints for slash commands — the notation is documented at the top of
// internal/repl/commands.go and the vocabulary is shared: <file>, <dir>,
// <name>, <alias>, <url>, <n>, <glob>, <text>. Kong would otherwise derive
// STRING and INT from the Go type, which says less and reads as a different
// program from the one the REPL is running.
//
// Enum flags are left alone: kong prints their default instead of a
// placeholder (--mode="files"), which already shows the shape of the value.
type chatCmd struct {
	Message string `help:"Send one message, apply the edits, and exit (script mode)."                     placeholder:"<text>" short:"m"`
	Session string `help:"Session to work in; created if it does not exist (default: the last one used)." placeholder:"<name>" short:"s"`
	// Hidden: an arm of doc/experiments/2026-09-compaction-source, removed or
	// promoted when that trial reports. A flag in --help is a supported
	// feature, and this is a question.
	CompactionSource string   `default:"fold"                                                                                                                                                    enum:"fold,record"                                        help:"Where a compaction summary is built from."        hidden:""`
	Continue         bool     `help:"Continue the session's previous conversation."                                                                                                              name:"continue"                                           short:"c"`
	Model            string   `help:"Model alias to use; defaults to the alias set in the config."                                                                                               placeholder:"<alias>"                                     short:"M"`
	NoGit            bool     `help:"Disable git integration even inside a repository."                                                                                                          name:"no-git"`
	NoColor          bool     `help:"Disable ANSI color and styling."                                                                                                                            name:"no-color"`
	DarkMode         bool     `help:"Use colors suited to a dark terminal background."                                                                                                           name:"dark-mode"                                          xor:"palette"`
	LightMode        bool     `help:"Use colors suited to a light terminal background."                                                                                                          name:"light-mode"                                         xor:"palette"`
	NoAutoCommits    bool     `help:"Keep git integration but do not auto-commit edits."                                                                                                         name:"no-auto-commits"`
	NoHistory        bool     `help:"Do not save this session's history, undo record, or resume state."                                                                                          name:"no-history"`
	DryRun           bool     `help:"Show edits without writing files or committing them."                                                                                                       name:"dry-run"`
	NoShell          bool     `help:"Disable the model's bash tool."                                                                                                                             name:"no-shell"`
	Yes              []string `help:"Automatically approve prompts of these types: bash, webfetch, websearch, steps, context, add-output, all. Repeat the option or use a comma-separated list." placeholder:"<name>"`
	ConsultScope     string   `default:"files"                                                                                                                                                   enum:"none,files,chat"                                    help:"Session context to include in /consult requests." name:"consult-scope"`
	Files            []string `arg:""                                                                                                                                                            help:"Files to pin for editing; they need not exist yet." optional:""`
}

func (c *chatCmd) Run() error {
	// First, because it validates nothing but what the user typed. A mistyped
	// permission name reported after a config or alias error is a mistyped
	// permission name the user fixes second.
	grants, err := coder.ParseGrants(c.Yes)
	if err != nil {
		return err
	}

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

	cfg, err := config.Load(config.Options{ProjectRoot: root, Warn: warnNoticef})
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
		noticef("%s", msg)
	}
	// The project's state directory, and whatever the last session left in it.
	// Resolved before the model, because a remembered alias participates in
	// choosing one.
	projectRoot, rootErr := historyRoot()
	// Which conversation this run belongs to: whatever --session names,
	// otherwise whichever `current` names, otherwise the default.
	//
	// --session selects; it does not resume. -c resumes whatever was
	// selected, so `--session review -c` picks that conversation up and
	// `--session review` alone starts a fresh one in it. The plan had
	// --session resuming, which is wrong for the reason a resuming default is
	// wrong anywhere: doc/experiments/ runs hundreds of scripted invocations
	// per trial, and an arm that wanted its own cost rows would have replayed
	// a conversation into every one of them.
	session := history.DefaultSession
	var res history.Resume
	if rootErr == nil {
		session = history.CurrentSession(projectRoot)
	}
	if c.Session != "" {
		if err := history.ValidSessionName(c.Session); err != nil {
			return err
		}
		session = c.Session
	}
	if rootErr == nil {
		res = history.LoadResume(projectRoot, session)
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
		noticef("remembered model %q is no longer in the config; using %q.", alias, cfg.Default)
		alias, ok = cfg.Default, true
		model = cfg.Models[alias]
	}
	if !ok {
		return fmt.Errorf("unknown model alias %q (aliases: %s)", alias, strings.Join(slices.Sorted(maps.Keys(cfg.Models)), ", "))
	}

	cdr := coder.New(root, model)
	cdr.Session = session
	cdr.DryRun = c.DryRun
	cdr.Client = client.ForProvider(model.Provider)
	// Every config-to-coder assignment lives in ApplyConfig, which /reload also
	// calls. Adding one here instead is the bug that made three reloads look
	// like they had worked; internal/repl's reload test guards against it.
	cdr.ShellWithheld = c.NoShell
	// Before ApplyConfig, which fills in the config half. The first version of
	// this set it *after*, so auto_approve was written to a nil Grants and
	// silently dropped -- found by running the binary, not by a test.
	cdr.Grants = coder.NewGrants(grants)
	coder.ApplyConfig(cdr, cfg)
	if std, ok := cdr.Out.(*coder.StdOutput); ok {
		// Script mode's output; the REPL swaps in its own and reads the setting
		// from the config it already carries.
		std.Thinking = coder.ThinkingDisplay(cfg.ReasoningDisplay)
	}
	cdr.Summarizer = coder.NewChatSummary(client.ForProvider(model.SideModel.Provider), model.SideModel, cdr.Tokens,
		cdr.Out, cdr.Clock, cdr.RecordSideCall)
	cdr.Confirm = coder.AutoConfirmer{Granted: cdr.Grants.Effective, Fallback: terminalConfirmer{}}
	applyEgressConfig(cdr, cfg)
	cdr.Skills = discoverSkills(root)
	if repo != nil {
		side := model.SideModel
		repo.CommitTrailer = gitrepo.Trailer(model.ReadableName())
		repo.Message = coder.CommitMessenger(client.ForProvider(side.Provider), side,
			cdr.Platform.Language, cdr.RecordTurnSideUsage, cdr.Out, cdr.Clock, cdr.PromptCommit, cdr.RecordSideCall)
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
			noticef("skipping %s: outside the project root; pin it with /read-only instead.", f)
			continue
		}
		cdr.AddFile(f)
	}

	// --no-history means leave no trace: no transcript, no input history, no
	// resume file, and no directory created to hold them. Reading what a past
	// session left is still fine — that writes nothing, and refusing it would
	// make the flag a bigger behavior change than its name suggests.
	keepState := !c.NoHistory && rootErr == nil
	// On every startup, not only when this project has no state yet. That is
	// the load-bearing half, and a control confirms it: making the scan
	// conditional on a missing state directory takes the hint from 1 to 0 in
	// the case the feature exists for — a user who renamed a project, did not
	// notice the history was gone, and had a session anyway. That session
	// creates state at the new path, and a conditional scan would go quiet
	// exactly then.
	//
	// It also runs before EnsureProjectDir, so nothing has been written when
	// the hint prints. That ordering is a sequencing preference and not a
	// behavioural one: the same control, moved, changes no output, because
	// creating *this* project's directory does not touch the orphan whose path
	// is gone. Said plainly because an earlier draft of this comment claimed
	// otherwise.
	if keepState {
		hintAtRenamedProject(projectRoot)
	}
	// The record segment for whichever conversation is active. nil when the
	// session leaves no trace.
	var slog *sessionLog

	stateDir := ""
	if keepState {
		dir, err := history.EnsureProjectDir(projectRoot, projectRootCommit(projectRoot))
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

			// A named session is created on first use, and becomes the one a
			// bare `strument` picks up next time. Only when it was named:
			// reading `current` must not rewrite it, or every run would
			// reorder the sessions by accident.
			if _, err := history.EnsureSessionDir(projectRoot, session); err != nil {
				noticef("could not create the session directory: %v", err)
			} else if c.Session != "" {
				if err := history.SetCurrentSession(projectRoot, session); err != nil {
					noticef("could not record %s as the current session: %v", session, err)
				}
			}
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
		// The global state root is granted beside the project's directory
		// under it: the per-provider usage ledgers live there, and a ruleset
		// that names only the project directory denies every write to them
		// with no turn to show for it.
		globalState, err := history.StateDir()
		if err != nil {
			globalState = ""
		}
		writable := sandbox.DefaultWritable(projectRoot, stateDir, globalState, cfg.SandboxWrite)
		policy := sandbox.Policy{Writable: writable}
		if err := policy.Apply(); err != nil {
			cdr.Sandbox.Unavailable = err.Error()
			// In every mode, not just the banner's. This changes what the
			// session can do — the model cannot run a command at all — and a
			// scripted run would otherwise meet a wall of refusals with nothing
			// on screen to say why.
			noticeWith(fmt.Sprintf("a sandbox is required but unavailable (%v).", err),
				"The model cannot run commands. /run still works, or set `sandbox = \"\"` in your config.")
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
		// The session's durable record, one segment per process start.
		//
		// Gated on keepState, unlike the --jsonl flag it replaces: that flag
		// was independent of it, so `--no-history --jsonl x` recorded the
		// session anyway. --no-history means leave no trace, and this is now
		// the trace.
		//
		// A segment that cannot be opened is a notice rather than a failure.
		// The user asked for a coding session; the record is instrumentation,
		// and the same judgement the ledger and the undo record already make.
		slog = &sessionLog{projectRoot: projectRoot}
		if err := slog.Open(session); err != nil {
			noticef("could not open the session log, so this run is not recorded: %v", err)
			slog = nil
		} else {
			defer func() { _ = slog.Close() }()
			cdr.Recorder = slog
			cdr.RecordSession(alias)
		}

		// A callback, so the coder stays ignorant of where state lives. A write
		// failure is not worth interrupting a turn over: the ledger is a record
		// to read later, and the usage line has already told the user the
		// numbers.
		cdr.RecordUsage = func(u coder.TurnUsage) {
			_ = history.AppendCost(projectRoot, history.CostEntry{
				// The coder's own name for the conversation, not the one
				// resolved at startup: /session can change it, and a cost row
				// filed under the session the process began in would be wrong
				// about the turn it describes.
				Session:         cdr.Session,
				Model:           u.Model,
				TokensSent:      u.TokensSent,
				TokensRecv:      u.TokensRecv,
				CacheRead:       u.CacheRead,
				CacheWrite:      u.CacheWrite,
				Cost:            u.Cost,
				Estimated:       u.Estimated,
				Steps:           u.Steps,
				FilesChanged:    u.FilesChanged,
				TokensPerSecond: u.TokensPerSecond,
			})
			// The per-provider ledger, beside the per-project one. The same
			// accounting filed twice, because "what did this turn cost the
			// project" and "what is this provider costing me" are different
			// questions with different scopes: the provider's rows live in one
			// global place, whatever project the turn ran in. Gated with
			// keepState like the ledger: a session that leaves no trace leaves
			// none here either.
			//
			// A write failure is said, not swallowed. The `_ =` here once hid
			// a real bug for its whole life: under the sandbox the global
			// state root was not writable, every row failed with EACCES, and
			// nothing anywhere said so — /usage reported empty while the usage
			// line on screen showed the spend. Silent loss of a ledger is how
			// a record stops being a record.
			if u.Provider != "" {
				if err := history.AppendUsage(u.Provider, history.CostEntry{
					Model:      u.Model,
					TokensSent: u.TokensSent,
					TokensRecv: u.TokensRecv,
					CacheRead:  u.CacheRead,
					CacheWrite: u.CacheWrite,
					Cost:       u.Cost,
					Estimated:  u.Estimated,
				}); err != nil {
					noticef("could not record usage for provider %q: %v", u.Provider, err)
				}
			}
		}

		// The compaction summary's input, which the trial in
		// doc/experiments/2026-09-compaction-source is about. Gated on
		// keepState with everything else: there is no record to read in a
		// session that leaves no trace.
		if c.CompactionSource == "record" && cdr.Summarizer != nil {
			cdr.Summarizer.Record = func() string { return sessionMarkdown(projectRoot, cdr.Session) }
		}

		// Heavy tool payloads go beside the record rather than into it, so
		// that pruning them later is an unlink rather than a rewrite of the
		// timeline. Gated on keepState with everything else: there is nowhere
		// to put a blob in a session that leaves no trace.
		cdr.PutBlob = func(data []byte) (string, error) {
			return history.PutBlob(projectRoot, data)
		}

		// One retention depth for the live stack and the saved one, so /undo
		// reaches as far in this session as it will after a restart. ApplyConfig
		// has already set MaxUndoTurns from `undo_turns` (or the default), and
		// the writer is told the same number rather than enforcing its own.

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
			if err := history.SaveUndo(projectRoot, cdr.Session, st, cdr.MaxUndoTurns); err != nil {
				noticef("could not save the undo record; /undo will not be able to restore this turn's changes: %v", err)
			}
		}
	}

	// --continue restores the conversation itself.
	//
	// It used to generate session notes instead: ~300 words of third-person
	// summary, which was the best available answer while the conversation did
	// not survive the process. It does now. Summarizing a conversation that is
	// sitting right there would put a lossy paraphrase beside the thing it
	// paraphrases, competing for the model's attention and able to contradict
	// it — and the job notes are genuinely the answer to is carrying context
	// *across* sessions, which resuming one does not do.
	restoreNote := ""
	if c.Continue && keepState {
		restoreNote = restoreConversation(cdr, projectRoot, session)
	}

	if c.Message == "" {
		// Restoring only in the REPL keeps a scripted run reproducible from its
		// own arguments: `strument -m ...` should send what it was told to, not
		// whatever an interactive session left pinned, which the user would pay
		// for without seeing it.
		note := restoreNote
		if len(c.Files) == 0 && rootErr == nil {
			var offered, notesRestored bool
			var pins string
			pins, offered, notesRestored = restoreSession(cdr, projectRoot, session, res)
			note = strings.TrimSpace(note + "\n" + pins)
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
				_ = history.SaveResume(projectRoot, session, resumeWithPins(cdr, projectRoot, res))
			}
		}
		return c.runREPL(cfg, cdr, repo, slog, alias, projectRoot, keepState, note)
	}

	// Script mode has no banner, so the restore goes to stderr: a human sees
	// that a conversation was loaded and paid for, and a script reading stdout
	// is not given a line it did not ask for.
	if restoreNote != "" {
		noticef("%s", restoreNote)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, repl.UserInterruptSignal())
	defer stop()

	cdr.Run(ctx, c.Message)
	// A scripted run that got no answer must not exit 0. `strument -m …` used
	// to report success after a refused key or a dead endpoint: the diagnostic
	// went to stderr and the status said everything was fine, so a script
	// driving it could not tell a rejected request from an empty reply the
	// model meant. OutcomeFailed is the case where the send produced nothing —
	// a non-retryable error, a retry ladder run out, or an empty response.
	//
	// Only that one. A truncated answer (OutcomeOutputExhausted) is still an
	// answer, and the interrupt outcomes are the human's own doing.
	//
	// The error carries no detail because the detail was printed where it
	// happened; this line exists to make main exit non-zero, and to say that
	// the status is a consequence of the failure just above it.
	if cdr.LastOutcome() == coder.OutcomeFailed {
		return errors.New("the model gave no answer; see the message above")
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

// hintAtRenamedProject prints one line when this project's recorded history
// appears to be sitting under a path that no longer exists.
//
// It only ever prints. The evidence — the recorded path is gone and the git
// root commit matches — is portable and survives a rename, a move across
// filesystems and a restore from backup, but it cannot tell two clones of one
// repository apart, since they share a root commit. Adopting on that would
// attach the wrong project's transcript, so a person types the command.
//
// This goes to stderr rather than through Output.Toolf: the Output does not
// exist this early, and the scan has to run before the state directory is
// created. The other startup notices — an untrusted project config, untrusted
// skills — take the same route for the same reason.
func hintAtRenamedProject(projectRoot string) {
	orphan, ok, err := history.FindOrphan(projectRootCommit(projectRoot))
	if err != nil || !ok {
		return
	}
	if history.IsDismissed(projectRoot, orphan.Dir) {
		return
	}
	dir, err := history.ProjectDir(projectRoot)
	if err == nil && dir == orphan.Dir {
		return
	}
	turns := "history"
	if orphan.Turns > 0 {
		turns = render.Plural(orphan.Turns, "turn", "turns")
	}
	noticeWith(
		fmt.Sprintf("this project also has %s recorded under %s, which no longer exists.",
			turns, orphan.Root.Path),
		"Merge saved state: strument project adopt "+orphan.Root.Path,
		"Dismiss this notice: strument project ignore "+orphan.Root.Path,
	)
}

// projectRootCommit is the witness recorded in a project's state directory, so
// a renamed directory can be recognized as the same project. "" for a project
// with no repository, or one whose history has more than one root.
//
// It discovers the repository itself rather than reusing the session's, on
// purpose: --no-git withholds git from the *session* while the project root is
// still the worktree, exactly as historyRootFrom above already assumes. Reading
// the witness from the session's repo would leave --no-git projects
// unmatchable, which is a silent asymmetry between two flags that have nothing
// to do with each other. Nothing is written to the repository either way.
func projectRootCommit(projectRoot string) string {
	g, err := gitrepo.Discover(projectRoot)
	if err != nil {
		return ""
	}
	return g.RootCommit()
}

// acquireProjectLock takes a non-blocking exclusive advisory lock on the
// project's state directory, so two harness copies keyed to the same root
// cannot write its transcript, cost ledger, or undo spill concurrently. The
// returned *Flock must be Closed to release the lock; Close also closes the
// underlying file descriptor, and the kernel drops the lock if the process
// exits without it. A false locked means another instance holds it; the error
// is non-nil only for genuine failures (a missing directory is not one — the
// caller has just created it).
// lockStateDir takes the same lock as acquireProjectLock, but on a state
// directory named directly rather than derived from a project root. Adopting
// needs it: the source directory's project may not exist any more, so there is
// no root to derive its lock path from.
func lockStateDir(dir string) (*flock.Flock, bool, error) {
	lk := flock.New(filepath.Join(dir, "lock"))
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
	resolved := workspace.ResolveSymlinks(filepath.Clean(file))
	rootResolved := workspace.ResolveSymlinks(root)
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil {
		return false
	}
	return !workspace.EscapesRoot(rel)
}

// restoreSession re-pins what the last session had pinned, returning a line for
// the banner or "" when nothing was restored.
//
// Paths are project-root-relative, so they survive the --no-git case where the
// coder's root is the invocation directory and the project is the git worktree.
// A file that has since moved is skipped rather than reported: the point is to
// save retyping, not to litigate what happened to the tree.
func restoreSession(cdr *coder.Coder, projectRoot, session string, res history.Resume) (note string, offered, notesRestored bool) {
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
	if u := history.LoadUndo(projectRoot, session); len(u.Turns) > 0 || len(u.Commits) > 0 {
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
		return fmt.Sprintf("Restored %s from your last session.", render.Plural(files, "pin", "pins")), offered, notesRestored
	case files == 0:
		return fmt.Sprintf("Restored %s from your last session, read-only.", render.Plural(readOnly, "pin", "pins")), offered, notesRestored
	}
	return fmt.Sprintf("Restored %s from your last session, %d of them read-only.",
		render.Plural(files+readOnly, "pin", "pins"), readOnly), offered, notesRestored
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
//
// defaultAlias is asked at each save rather than read once, because /reload can
// change `default` mid-session. Compared against the startup value, a session
// still on the old default after the edit saved nothing, and the next start
// quietly moved it to the new one.
func saveResumeFunc(cdr *coder.Coder, defaultAlias func() string, projectRoot string, keepState bool) func(alias string) {
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
			AutoPinned: history.LoadResume(projectRoot, cdr.Session).AutoPinned,
		})
		if alias != defaultAlias() {
			res.Model = alias
		}
		_ = history.SaveResume(projectRoot, cdr.Session, res)
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

// restoreConversation rebuilds a session's conversation from its record and
// hands it back to the coder, returning the line to tell the user, or "" when
// there was nothing to restore.
//
// Everything it needs is already on disk: the record holds every message in
// order, and the blob store holds whatever was too big to keep inline. What it
// adds is the seam — a conversation made by a different model is announced as
// one, because an assistant turn is otherwise read by the next model as its
// own past self.
//
// A failure here is a notice, not an exit. The session still works without its
// history, and refusing to start because a year-old segment will not parse
// would be the wrong trade.
func restoreConversation(cdr *coder.Coder, projectRoot, session string) string {
	records, missing, err := history.ReadSessionRecords(projectRoot, session)
	if err != nil {
		noticef("could not read the session record, so this session starts empty: %v", err)
		return ""
	}
	msgs, stats := coder.MessagesFromRecords(records)
	if len(msgs) == 0 {
		return ""
	}
	cdr.RestoreHistory(msgs)
	if cdr.RestoredFromAnotherModel(stats) {
		cdr.NoteRestoredFromAnotherModel()
	}
	note := stats.RestoreNote()
	if missing > 0 {
		// The pruning this is the other half of: a payload that was deleted
		// leaves the description the record kept beside it, and the model is
		// told in words that the result is no longer stored. Say so here too,
		// because a conversation quietly missing three tool results is the
		// kind of thing that is invisible until it matters.
		note += fmt.Sprintf(" %s %s been pruned.",
			render.Plural(missing, "stored payload", "stored payloads"),
			map[bool]string{true: "has", false: "have"}[missing == 1])
	}
	return note
}

// sessionMarkdown is the session's history as the notes writer reads it: the
// record rendered by the same renderer `strument history markdown` uses.
//
// It reads every segment, so notes regenerate from the whole session rather
// than from the run that happens to be current — and, in a running session,
// from the turns of that session too, since the writer flushes per record.
//
// "" for a session with nothing in it, which is the caller's cue to say so.
func sessionMarkdown(projectRoot, session string) string {
	turns, err := history.ReadTurns(projectRoot, session)
	if err != nil || len(turns) == 0 {
		return ""
	}
	return history.Markdown(turns)
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

// consultScope maps the --consult-scope name onto the coder's ladder. An
// unrecognized name cannot reach here — kong's enum has already refused it — so
// it falls back to the bottom of the ladder rather than failing the session over
// a flag that was validated a moment ago.
func consultScope(name string) coder.ConsultScope {
	scope, _ := coder.ParseConsultScope(name)
	return scope
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
func (c *chatCmd) runREPL(cfg *config.Config, cdr *coder.Coder, repo *gitrepo.Repo, slog *sessionLog,
	alias, projectRoot string, keepState bool, resumeNote string,
) error {
	refreshCommitMessage := func(m *config.Model) {
		if repo == nil {
			return
		}
		side := m.SideModel
		repo.Message = coder.CommitMessenger(client.ForProvider(side.Provider), side,
			cdr.Platform.Language, cdr.RecordTurnSideUsage, cdr.Out, cdr.Clock, cdr.PromptCommit, cdr.RecordSideCall)
	}
	// Scoped to the project like the transcript, in the directory Run already
	// created — and suppressed with it when the session leaves no trace.
	var inputHistory string
	if keepState {
		inputHistory, _ = history.InputHistoryPath(projectRoot)
	}
	// The REPL owns the config after startup (/reload replaces it), so anything
	// built here that needs a config value later asks the REPL for it. Called
	// only once r is assigned, from commands the REPL itself dispatches.
	var r *repl.REPL
	currentDefault := func() string { return r.Config().Default }
	r, err := repl.New(repl.Options{
		Coder:                cdr,
		Config:               cfg,
		Git:                  repo,
		ModelAlias:           alias,
		ResumeNote:           resumeNote,
		SaveResume:           saveResumeFunc(cdr, currentDefault, projectRoot, keepState),
		ApplyEgress:          applyEgressConfig,
		MakeClient:           func(m *config.Model) llm.ModelClient { return client.ForProvider(m.Provider) },
		RefreshCommitMessage: refreshCommitMessage,
		// Kong's enum has already refused anything else, so the ok is never
		// false here; the parse is where the name-to-scope mapping lives.
		ConsultScope: consultScope(c.ConsultScope),
		ReloadConfig: func() (*config.Config, error) {
			return config.Load(config.Options{ProjectRoot: cdr.Root, Warn: warnNoticef})
		},
		Rediscover: func() []skill.Skill { return discoverSkills(cdr.Root) },
		Notes:      func() string { return cdr.SessionNotes },
		DropNotes: func() {
			cdr.SessionNotes, cdr.SessionNotesDate, cdr.SessionNotesSession = "", "", ""
		},
		Sessions: sessionOps(cdr, currentDefault, projectRoot, slog, keepState),
		GenerateNotes: func(_ context.Context) error {
			if !keepState {
				return errors.New("no session record available")
			}
			if cdr.Model.SideModel == nil {
				return errNoSideModel
			}
			transcript := sessionMarkdown(projectRoot, cdr.Session)
			if transcript == "" {
				return errors.New("the session record is empty")
			}
			notes, err := writeSessionNotes(cdr, transcript)
			if err != nil {
				return err
			}
			if notes == "" {
				return errors.New("the model returned no notes")
			}
			cdr.SessionNotes = notes
			cdr.SessionNotesDate = time.Now().UTC().Format("2006-01-02 15:04")
			cdr.SessionNotesSession = cdr.Session
			return nil
		},
		Color:      !c.NoColor && stdoutIsTerminal() && os.Getenv("NO_COLOR") == "",
		IsTerminal: drivingATerminal,
		// Only stdin: `strument | tee log` still has a human to ask.
		StdinIsTerminal: func() bool { return isTerminal(os.Stdin) },
		HistoryFile:     inputHistory,
		Version:         version,
		Theme:           c.paletteTheme(),
		GetSize:         terminalSize,
	})
	if err != nil {
		return err
	}
	defer r.Close()
	// Route confirms through readline; a --yes name answers first. The asker
	// has no auto variant: --yes answers permission prompts, and a question is
	// the model asking for information it cannot proceed without.
	cdr.Confirm = coder.AutoConfirmer{Granted: cdr.Grants.Effective, Fallback: r.Confirmer()}
	cdr.Asker = r.Asker()
	return r.Run(context.Background())
}

func stdoutIsTerminal() bool { return isTerminal(os.Stdout) }

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
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
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
	case req.URL != "":
		// The second confirmer, and it has to show what the first one shows.
		// It did not, and the prompt read "Fetch this page? (Y/n)" with no page
		// named — a question nobody could answer, which a pty run surfaced and
		// no unit test would have. No hyperlink here: this surface has no
		// color gate to hang one on.
		if req.Purpose != "" {
			fmt.Println("‹webfetch›", req.Purpose)
		} else {
			fmt.Println("‹webfetch› (no purpose given)")
		}
		fmt.Println(render.Sanitize(req.URL))
	}

	// The REPL's rlConfirmer declines rather than reading when nobody is at
	// the keyboard; this surface follows, so the two mean the same thing. Only
	// stdin is consulted: redirecting output does not take the human away.
	if !isTerminal(os.Stdin) {
		// The same advice the REPL gives, and for the same reason: name the
		// flag that would have answered *this* prompt rather than the nearest
		// of two.
		if req.Grant == "" {
			fmt.Println("Declined: this prompt requires an interactive terminal and cannot be approved with `--yes`.")
			return coder.ConfirmResult{}
		}
		fmt.Printf("Declined: this prompt requires an interactive terminal. Pass `--yes %s` to approve it automatically.\n", req.Grant)
		return coder.ConfirmResult{}
	}

	fmt.Printf("%s (Y/n) ", req.Prompt)
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		// No answer available at all — a closed or redirected stdin. Declining
		// is the safe reading: nobody is there to have meant yes. Said, and on
		// its own line: the prompt left the cursor after "(Y/n) ", and the next
		// message used to run on from there.
		fmt.Println()
		fmt.Println("Declined: no answer was given.")
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
	Path string `arg:""                                          help:"Project directory containing a Strument config or skills (default: current directory)." optional:""`
	Yes  bool   `help:"Proceed without asking for confirmation." short:"y"`

	// confirm is the test seam for the prompt. Unexported, so kong does not see
	// it as a flag.
	confirm func() bool
}

// trustItem is one file this command would record, with what the store already
// knows about it.
type trustItem struct {
	path    string // absolute
	content []byte
	// state is "new", "changed", or "unchanged". The trust store holds a
	// content hash and no content, so this is as much of a diff as there can
	// be — and it is the part worth having: it says whether the user is
	// looking at something they have approved before.
	state string
	// skill is set for a project skill, nil for the config.
	skill *skill.Skill
}

func (c *trustCmd) Run() error {
	root := c.Path
	if root == "" {
		var err error
		if root, err = os.Getwd(); err != nil {
			return err
		}
	}
	// Absolute, with the trailing slash doc/messages.md asks of a directory:
	// `strument trust .` should not report back about ".".
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}

	tsPath, err := config.DefaultTrustStorePath()
	if err != nil {
		return err
	}
	ts, err := config.OpenTrustStore(tsPath)
	if err != nil {
		return err
	}

	// The project's skills, whether or not they are currently trusted:
	// re-running after an edit is what re-trusts an edited one, so this is not
	// conditional on the current state.
	skills, diags := skill.Trustable(root)
	// Said before anything is trusted. A skill that cannot be read is one the
	// user thinks they just trusted, and finding out later from its absence is
	// the failure worth avoiding.
	for _, d := range diags {
		noticef("skipping %s: %s", d.Path, d.Message)
	}

	cfgPath, err := config.FindProjectConfig(root)
	if err != nil {
		return err
	}
	var items []trustItem
	if cfgPath != "" {
		item, err := newTrustItem(ts, cfgPath, nil)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	for i := range skills {
		item, err := newTrustItem(ts, skills[i].Path, &skills[i])
		if err != nil {
			return err
		}
		items = append(items, item)
	}

	// What the config would be allowed to do, read out of the file itself.
	//
	// Before the all-unchanged check below, not after, so a config that has
	// stopped loading is reported on every run rather than only on the run that
	// happens to change something — including the case where it was trusted on
	// another machine and is unchanged here.
	var insp *config.ProjectInspection
	configRefused := false
	if cfgPath != "" {
		var err error
		if insp, err = config.InspectProjectConfig(root); err != nil {
			// The config alone is refused, and the project's skills are not.
			// They are separate files that parse, and there is nothing to be
			// gained from making a repository's Unix-only config the reason a
			// Windows user cannot approve its skills.
			//
			// Refusing the config itself is not only about the summary being
			// empty. config.Load executes a project config *only* when it is
			// trusted, and returns the failure hard; an untrusted one is merely
			// warned about. So recording this file would make Strument refuse
			// to start in this directory until it was untrusted again.
			rest := append(strings.Split(err.Error(), "\n"),
				"It stays untrusted, so a session will ignore it rather than fail to start.",
				"Fix it and run `strument trust` again.")
			noticeWith(fmt.Sprintf("not trusting %s: it does not load", cfgPath), rest...)
			items = slices.DeleteFunc(items, func(it trustItem) bool { return it.skill == nil })
			configRefused = true
		}
	}

	if len(items) == 0 {
		if configRefused {
			// The project's only trustable file was the config, and it does not
			// load. The notice above said what and why; this is the status,
			// which a script chaining on the command reads instead.
			return fmt.Errorf("nothing was trusted in %s: its only trustable file is a config that does not load",
				strings.TrimRight(filepath.ToSlash(root), "/")+"/")
		}
		return fmt.Errorf("nothing to trust in %s: no %s or %s, and no skills under .strument/skills/ or .agents/skills/",
			strings.TrimRight(filepath.ToSlash(root), "/")+"/",
			config.ProjectConfigPaths[0], config.ProjectConfigPaths[1])
	}

	// Nothing here has changed since the user last approved it, so there is
	// nothing to approve. Re-running after editing one file in a project full
	// of skills is then cheap and silent about the rest.
	if !slices.ContainsFunc(items, func(it trustItem) bool { return it.state != trustUnchanged }) {
		for _, it := range items {
			fmt.Printf("Already trusted %s\n", it.path)
		}
		return nil
	}

	if insp != nil {
		printInspection(insp, stateOf(items, cfgPath))
	}
	printSkills(items)

	switch {
	case c.Yes:
		// The question is skipped; the disclosure above is not. A scripted
		// trust still leaves a record of what it granted.
	case c.confirm != nil:
		if !c.confirm() {
			fmt.Println("Nothing was trusted.")
			return nil
		}
	case isTerminal(os.Stdin):
		if !confirmTrust() {
			// Declining at a prompt exits 0, unlike the no-terminal case
			// below: a person who typed "n" knows what happened and does not
			// need a status to find out.
			fmt.Println("Nothing was trusted.")
			return nil
		}
	default:
		// Fail closed. This used to trust silently, which made `strument trust`
		// in a setup script a grant nobody read. A script that means it says so.
		return fmt.Errorf("refusing to trust %s without confirmation: this requires an interactive terminal. Pass `--yes` to trust it without one",
			strings.TrimRight(filepath.ToSlash(root), "/")+"/")
	}

	// Recorded only now. The old order trusted the config before it had even
	// looked at the skills, so a failure partway through left half a decision
	// applied.
	//
	// TrustProject rather than TrustFiles on the path already resolved above:
	// it re-checks the two-configs refusal, which internal/config/load.go keeps
	// shared with the load path so the two cannot drift on what a conflict is.
	if insp != nil {
		if _, err := config.TrustProject(root, ""); err != nil {
			return err
		}
	}
	paths := make([]string, 0, len(skills))
	for _, s := range skills {
		paths = append(paths, s.Path)
	}
	if err := config.TrustFiles(paths, ""); err != nil {
		return err
	}

	// Named one per line rather than counted. The whole risk here is a cloned
	// repository carrying skills nobody noticed, so what was just granted has
	// to be legible rather than summarised.
	fmt.Println()
	for _, it := range items {
		fmt.Printf("Trusted %s\n", it.path)
	}
	fmt.Println(config.ReTrustReminder)
	return nil
}

// The three states a trustable file can be in relative to the store.
const (
	trustNew       = "new"
	trustChanged   = "changed"
	trustUnchanged = "unchanged"
)

func newTrustItem(ts *config.TrustStore, path string, sk *skill.Skill) (trustItem, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return trustItem{}, err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return trustItem{}, err
	}
	state := trustNew
	switch {
	case ts.IsTrusted(abs, content):
		state = trustUnchanged
	case ts.Recorded(abs):
		state = trustChanged
	}
	return trustItem{path: abs, content: content, state: state, skill: sk}, nil
}

func stateOf(items []trustItem, path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	for _, it := range items {
		if it.path == abs {
			return it.state
		}
	}
	return trustNew
}

// printInspection says what trusting this config would allow, one key per line.
//
// Keys rather than a diff against the previously trusted content, and
// internal/config/inspect.go carries the reasoning: a diff would mean the trust
// store held whole config files, and that store is a plaintext state file no
// one expects to hold their project's text.
func printInspection(insp *config.ProjectInspection, state string) {
	if insp == nil {
		return
	}
	path := promptField(insp.Path, maxPromptField)
	if insp.Empty() {
		fmt.Printf("\n%s (%s) changes no settings.\n", path, state)
		return
	}
	fmt.Printf("\n%s (%s) grants:\n", path, state)
	width := 0
	for _, c := range insp.Capabilities {
		width = max(width, len(c.Key))
	}
	for _, c := range insp.Capabilities {
		// The key is ours; the detail quotes the file, so it is bounded and
		// stripped before it reaches the terminal.
		fmt.Printf("  %-*s  %s\n", width, c.Key, promptField(c.Detail, maxPromptDetail))
	}
	if len(insp.Preferences) > 0 {
		// Named without detail rather than omitted: a key the summary cannot
		// show is a key nobody classified, and silence is what this whole
		// command exists to replace.
		fmt.Printf("  and sets %s: %s\n",
			render.Plural(len(insp.Preferences), "preference", "preferences"),
			strings.Join(insp.Preferences, ", "))
	}
	if len(insp.MissingEnv) > 0 {
		// Read as empty rather than refused, so a config written for another
		// machine can still be summarised. The continuation is the part that
		// matters: the session is stricter than this — config.Load takes an
		// unset env() with no default as an error — so trusting a config is not
		// a promise that it will load.
		noticeWith(
			"not set, read as empty while reading the config: "+strings.Join(insp.MissingEnv, ", "),
			"The summary above shows the config as if "+
				render.PluralWord(len(insp.MissingEnv), "it were", "they were")+
				" empty. A session will need "+
				render.PluralWord(len(insp.MissingEnv), "it", "them")+" set.")
	}
}

// maxSkillsListed bounds the skill listing, and maxSkillDescription bounds one
// description.
//
// Both are here because this is a security prompt whose text comes from the
// repository being judged. A skill with a ten-kilobyte description, or a
// directory with two hundred skills in it, would push the config summary off
// the screen above the question — and the reader would answer a prompt whose
// first half they never saw.
const (
	maxSkillsListed = 20
	// maxPromptField bounds one quoted field, maxPromptDetail one rendered
	// capability — longer because it is a sentence naming several things.
	maxPromptField  = 100
	maxPromptDetail = 300
)

func printSkills(items []trustItem) {
	var skills []trustItem
	for _, it := range items {
		if it.skill != nil {
			skills = append(skills, it)
		}
	}
	if len(skills) == 0 {
		return
	}
	fmt.Printf("\n%s to trust. A skill is instructions the model follows:\n",
		render.Plural(len(skills), "project skill", "project skills"))
	for i, it := range skills {
		if i == maxSkillsListed {
			fmt.Printf("  … and %d more\n", len(skills)-maxSkillsListed)
			break
		}
		fmt.Printf("  %s (%s)\n", promptField(it.path, maxPromptField), it.state)
		name := promptField(it.skill.Name, maxPromptField)
		if name == "" {
			name = "(unnamed)"
		}
		desc := promptField(it.skill.Description, maxPromptField)
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Printf("    %s — %s\n", name, desc)
		if at := promptField(it.skill.AllowedTools, maxPromptField); at != "" {
			// Shown with the qualifier attached, because the field looks like a
			// permission and is not one: internal/skill/frontmatter.go explains
			// why a markdown file is never the authority on what this harness
			// may do.
			fmt.Printf("    allowed-tools: %s (advisory; Strument grants nothing from it)\n", at)
		}
	}
}

// promptField renders one piece of text quoted from an untrusted file.
//
// Three things happen and each closes a different hole. Whitespace is collapsed
// so a newline in a description cannot break the prompt's layout. Escape
// sequences are stripped, because this text is going to a terminal that obeys
// them and the cursor could otherwise be moved back over lines the user has
// already read — the review surface this whole command exists to provide.
// And the result is bounded, so a skill with a ten-kilobyte description cannot
// push the config summary off the screen above the question.
//
// internal/repl and internal/coder each have a plainer `oneLine` for listing a
// skill. This is deliberately not that one: those flatten text for a listing,
// this makes a security prompt safe to read.
func promptField(s string, limit int) string {
	s = render.Sanitize(strings.Join(strings.Fields(s), " "))
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

func confirmTrust() bool {
	fmt.Print("\nTrust these? (y/N) ")
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		return false
	}
	// Defaulting to no, unlike the in-session shell prompt: that one asks about
	// a command the user just read the model produce, while this one asks about
	// a file somebody else wrote.
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// configCmd inspects the configuration for the current project: `models` and
// `default` read the *resolved* config — the merge of the user config and a
// trusted project config, with the same root resolution a chat session gets —
// while `path` and `edit` act on one file, named by the scope flags.
//
// `--user` and `--project` rather than `--global` and `--local`. The pair they
// replaced reads as a claim about reach, and neither is true here: a "global"
// config is one user's on one machine, and "local" says nothing about which of
// the two directories it means. Git spells the same distinction `--global` and
// `--local` and means something different by it — `--local` there is the
// repository's, `--global` the user's, and there is a third, `--system`, that
// Strument has no equivalent of — so borrowing the words would import an
// expectation the third one breaks.
type configCmd struct {
	// On the parent rather than on each subcommand so `strument config --user
	// path` works, which is how someone writes it. The cost is that kong will
	// also accept them on `models` and `default`, where they mean nothing:
	// those two refuse them rather than ignoring them.
	User    bool `help:"Act on the user config (the default)." xor:"scope"`
	Project bool `help:"Act on this project's config."         xor:"scope"`

	Models  configModelsCmd  `cmd:"" help:"Print the config's model aliases, one per line."`
	Default configDefaultCmd `cmd:"" help:"Print the config's default model alias."`
	Path    configPathCmd    `cmd:"" help:"Print the path to a config file, whether or not it exists."`
	Edit    configEditCmd    `cmd:"" help:"Open a config file in $VISUAL, $EDITOR, or your platform's default editor."`
}

// scopedFile is the file the scope flags name. The same function answers for
// `path` and for `edit`, so the path printed is always the path opened.
func (c *configCmd) scopedFile() (string, error) {
	if !c.Project {
		return config.DefaultUserConfigPath()
	}
	root, err := historyRoot()
	if err != nil {
		return "", err
	}
	return projectConfigPathForEdit(root)
}

// refuseScope is what `models` and `default` say to a scope flag. They print
// the merged config, which is not one file, so a flag selecting one has no
// answer rather than a boring one.
func (c *configCmd) refuseScope(sub string) error {
	if !c.User && !c.Project {
		return nil
	}
	return fmt.Errorf("`--user` and `--project` name one config file; `config %s` prints the merged config of both", sub)
}

// projectConfigPathForEdit picks which of the project's two config spellings to
// act on.
//
// An existing one wins, via FindProjectConfig, so this command and the loader
// cannot disagree about which file is the config — including its refusal when a
// project has written both. When there is none yet, the choice follows what the
// project already looks like: a repository with a .strument/ directory (skills,
// usually) gets .strument/config.star so its Strument files stay together, and
// anything else gets .strument.star. Neither is a migration target for the
// other, so this only ever picks where a *first* config goes.
func projectConfigPathForEdit(root string) (string, error) {
	existing, err := config.FindProjectConfig(root)
	if err != nil || existing != "" {
		return existing, err
	}
	dir := filepath.Join(root, config.ProjectConfigDir)
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return filepath.Join(dir, filepath.FromSlash(config.ProjectConfigInDir)), nil
	}
	return filepath.Join(root, config.ProjectConfigName), nil
}

type configModelsCmd struct{}

func (*configModelsCmd) Run(c *configCmd) error {
	if err := c.refuseScope("models"); err != nil {
		return err
	}
	return runConfigSets("models")
}

type configDefaultCmd struct{}

func (*configDefaultCmd) Run(c *configCmd) error {
	if err := c.refuseScope("default"); err != nil {
		return err
	}
	return runConfigSets("default")
}

type configPathCmd struct{}

// Run prints the path whether or not the file is there, the way `history path`
// does: "where would my config go" is the same question as "where is it", and
// answering only one of them makes the command useless to whoever has not
// written a config yet. A file that is not there is said so on stderr, so the
// path itself stays pipeable.
func (*configPathCmd) Run(c *configCmd) error {
	path, err := c.scopedFile()
	if err != nil {
		return err
	}
	fmt.Println(path)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		noticef("does not exist yet: %s", path)
	}
	return nil
}

type configEditCmd struct {
	// edit is the test seam, unexported so kong does not see it as a flag.
	edit func(path string) error
}

func (e *configEditCmd) Run(c *configCmd) error {
	path, err := c.scopedFile()
	if err != nil {
		return err
	}
	edit := e.edit
	if edit == nil {
		edit = editFile
	}
	if err := edit(path); err != nil {
		return err
	}
	if c.Project {
		remindToTrust(path)
	}
	return nil
}

// remindToTrust says what editing a project config costs, and only when it
// costs it.
//
// Trust is over content, so saving any change untrusts the file and the session
// silently stops honouring it. Checked rather than printed unconditionally: an
// edit the user abandoned, or one they have already trusted from another
// window, should not be nagged about. config.TrustAdviceFor is the one phrasing
// of this instruction in the tree.
func remindToTrust(path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		return // no file: they opened the editor and did not save
	}
	tsPath, err := config.DefaultTrustStorePath()
	if err != nil {
		return
	}
	ts, err := config.OpenTrustStore(tsPath)
	if err != nil {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	if !ts.IsTrusted(abs, src) {
		noticeWith("this project's config is not trusted as it now stands", config.TrustAdviceFor(1, false))
	}
}

// loadProjectConfig loads the effective config for the current project. It
// mirrors historyRoot so the answer is the one the chat session would act on,
// not the one from a different directory that happens to hold a config file.
// An env() variable that is unset and has no default yields "" here instead of
// failing the load. These subcommands read the config's *shape* — which aliases
// exist, which is the default — and never make a request, so a key they cannot
// see costs them nothing. Failing instead has a cost that is easy to miss: the
// bash completion calls `config models` on every Tab and discards stderr, so a
// config whose key lives in a per-project direnv made alias completion do
// nothing at all, silently, outside that project. The substitution is
// announced on stderr, where the human who typed the command sees it and the
// completion script does not.
//
// Through Options.OnMissingEnv rather than a LookupEnv that claims everything
// is set to "". That is what this used to do, and it meant a variable with a
// default never reached it: `strument config models` reported "" where a
// session would use the default, and named a variable nobody needed to set.
func loadProjectConfig() (*config.Config, error) {
	root, err := historyRoot()
	if err != nil {
		return nil, err
	}
	var missing []string
	cfg, err := config.Load(config.Options{
		ProjectRoot: root,
		Warn:        warnNoticef,
		OnMissingEnv: func(name string) {
			if !slices.Contains(missing, name) {
				missing = append(missing, name)
			}
		},
	})
	if len(missing) > 0 {
		noticef("these variables are not set and were read as empty: %s", strings.Join(missing, ", "))
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
// historyCmd is a group with no default subcommand. A bare `strument history`
// used to print the path; it was retired so that `history` and `config` are
// asked the same way, and because "print" and "open" are two things one bare
// noun cannot name.
type historyCmd struct {
	// On the parent rather than on each subcommand, so `strument history
	// --session review markdown` works, which is how someone writes it. Same
	// shape as configCmd's scope flags and the same cost: kong accepts it on
	// `strip` too, where it means nothing because payloads are shared across
	// a project and a sweep of one session's worth would be wrong.
	Session string `help:"Session to act on (default: the last one used)." placeholder:"<name>" short:"s"`

	Path     historyPathCmd     `cmd:"" help:"Print the path to a session's record."`
	Edit     historyEditCmd     `cmd:"" help:"Open a session's record in $VISUAL, $EDITOR, or your platform's default editor."`
	Markdown historyMarkdownCmd `cmd:"" help:"Print a session's history as markdown."`
	Strip    historyStripCmd    `cmd:"" help:"Delete stored tool output that no recent session refers to. Conversation records are kept."`
}

// historySession resolves the project and the session `history` acts on: the
// one named, or the one a chat would resume.
func historySession(named string) (root, session string, err error) {
	root, err = historyRoot()
	if err != nil {
		return "", "", err
	}
	if named != "" {
		if err := history.ValidSessionName(named); err != nil {
			return "", "", err
		}
		return root, named, nil
	}
	return root, history.CurrentSession(root), nil
}

// historyPath resolves the newest record segment for the current session.
//
// Newest rather than all of them because this answers "where is what I just
// did", which is what a jq one-liner wants to be pointed at. `history
// markdown` reads every segment; someone who wants the same of the raw
// records has the directory this path sits in.
//
// A session that has never been chatted in has no segment yet. The path of the
// one the next run would open is not a useful answer — it does not exist and
// its name is a timestamp that has not happened — so this reports that there
// is nothing, and the caller says so.
func historyPath(named string) (string, error) {
	root, session, err := historySession(named)
	if err != nil {
		return "", err
	}
	segments, err := history.LogSegments(root, session)
	if err != nil {
		return "", err
	}
	if len(segments) == 0 {
		return "", errNoRecord
	}
	return segments[len(segments)-1], nil
}

// errNoRecord is a project nobody has chatted in yet, which is the ordinary
// state of a fresh checkout rather than a failure.
var errNoRecord = errors.New("this project has no session record yet")

type historyPathCmd struct{}

// Run prints the newest segment's path.
//
// Unlike `config path`, this cannot name a file that is not there: a config
// path is a fixed name whether or not anyone wrote it, while a segment is
// named after the moment it was opened. There is nothing to print for a
// project nobody has chatted in, so it says that instead.
func (*historyPathCmd) Run(parent *historyCmd) error {
	p, err := historyPath(parent.Session)
	if err != nil {
		return err
	}
	fmt.Println(p)
	return nil
}

// historyMarkdownCmd renders the record as the transcript used to look.
//
// The record is JSON Lines, which is what a program wants and not what a
// person reading back over an afternoon's work wants. This is the same
// renderer the transcript file was written with, over the same turns, so the
// document is the one that used to be on disk — now derived, and derived from
// a record that also holds the tool calls the transcript never did.
type historyMarkdownCmd struct {
	Turns int `help:"Show only the last <n> turns (default: all)." placeholder:"<n>" short:"t"`
}

func (c *historyMarkdownCmd) Run(parent *historyCmd) error {
	root, session, err := historySession(parent.Session)
	if err != nil {
		return err
	}
	turns, err := history.ReadTurns(root, session)
	if err != nil {
		return err
	}
	if len(turns) == 0 {
		return errNoRecord
	}
	fmt.Print(history.Markdown(history.LastTurns(turns, c.Turns)))
	return nil
}

type historyEditCmd struct {
	// edit is the test seam, unexported so kong does not see it as a flag.
	edit func(path string) error
}

func (e *historyEditCmd) Run(parent *historyCmd) error {
	p, err := historyPath(parent.Session)
	if err != nil {
		return err
	}
	edit := e.edit
	if edit == nil {
		edit = editFile
	}
	return edit(p)
}

// modelConfigCmd scaffolds model() blocks from a provider's live catalog, so
// the tedious fields (context size, costs, cache capability) don't have to be
// looked up by hand. Output is copy-pastable Starlark on stdout — the user
// reviews it and pastes it into their config.
type modelConfigCmd struct {
	Source       string   `default:"openrouter"                                                              help:"Metadata source (currently only \"openrouter\")."             placeholder:"<name>" short:"s"`
	ProviderName string   `default:"openrouter"                                                              help:"Provider variable name to use in the generated model() call." name:"provider-name" placeholder:"<name>"`
	Proxy        string   `help:"SOCKS5 proxy for fetching the model catalog (default: the config's proxy)." name:"proxy"                                                        placeholder:"<url>"`
	Models       []string `arg:""                                                                            help:"Exact model slugs, e.g. anthropic/claude-haiku-4.5."          name:"model"`
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
		return errors.New("model-config requires an OpenRouter API key in OPENROUTER_API_KEY: anonymous catalog requests are rate-limited and can get your IP address blocked")
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
		noticef("model %q not found on %s.", m, c.Source)
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s not found", render.Plural(len(missing), "model", "models"))
	}
	return nil
}

type cli struct {
	Chat        chatCmd          `cmd:""                         default:"withargs"                                                                        help:"Chat with a model about the given files (default command)."`
	Trust       trustCmd         `cmd:""                         help:"Trust the project's config file and its skills."`
	History     historyCmd       `cmd:""                         help:"Show, edit, or prune this project's session history."`
	Config      configCmd        `cmd:""                         help:"Inspect the resolved config, or find and edit a config file."`
	ModelConfig modelConfigCmd   `cmd:""                         help:"Fetch model metadata from a provider and print a model() configuration block."      name:"model-config"`
	Project     projectCmd       `cmd:""                         help:"List projects with saved state, or merge state from a project's previous path."`
	Session     sessionCmd       `cmd:""                         help:"List, rename, or delete this project's sessions."`
	Tool        toolCmd          `cmd:""                         help:"Run a read-only tool and print the result a model would receive."`
	Shell       shellCmd         `cmd:""                         help:"Generate shell completions."`
	Usage       usageCmd         `cmd:""                         help:"Show token usage and cost per provider for the last 24 hours, 7 days, and 30 days."`
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

// applyEgressConfig builds the fetch and search ports from a config. Called at
// startup and again on /reload, from one place rather than two, because the two
// drifting is not hypothetical: /reload used to leave both ports as they were
// built at startup, so a user who added proxy="direct" to fix a search going
// through a proxy reloaded, saw no change, and concluded the network was at
// fault. A setting that can be changed and reloaded has to actually be re-read.
func applyEgressConfig(cdr *coder.Coder, cfg *config.Config) {
	// URL scraping is a non-provider egress action, so it uses the global proxy
	// (validated at load, so the error is dead; nil transport => direct). An
	// explicit `scraper` command overrides the built-in fetcher — the opt-in
	// path for JavaScript-rendered pages — and does its own networking (no
	// proxy).
	if len(cfg.Scraper) > 0 {
		cdr.Scrape = coder.NewCommandScraper(cfg.Scraper, 60*time.Second, func() []string {
			return cdr.EnvAllow
		})
		// A subprocess the model can cause, so webfetch is gated by the sandbox
		// like bash and check. The built-in fetcher below spawns nothing.
		cdr.ScrapeRunsCommand = true
	} else {
		scrapeTransport, _ := httpx.ProxyTransport(cfg.Proxy)
		cdr.Scrape = coder.NewSimpleScraper(scrapeTransport, "Strument/"+version)
		cdr.ScrapeRunsCommand = false
	}
	// Search is another non-provider egress path, so it honors the global proxy
	// like scraping — except that a self-hosted instance is usually on localhost
	// or the LAN, where a proxy meant for external traffic has no business
	// carrying the request. proxy="direct" on search() is that opt-out, the same
	// escape hatch provider() has for a LAN-local model server.
	//
	// Set to nil when no backend is configured, so removing search() and
	// reloading withdraws the tool rather than leaving the old instance wired
	// up under a config that no longer names it.
	cdr.Search = nil
	if ws := cfg.WebSearch; ws != nil {
		// Resolved and validated at load, so the error is dead here — the same
		// shape the scraper's proxy uses above.
		searchTransport, _ := httpx.ProxyTransport(ws.Proxy)
		switch ws.Backend {
		case config.SearchAnySearch:
			cdr.Search = coder.NewAnySearch(ws.URL, ws.APIKey, searchTransport, "Strument/"+version)
		case config.SearchExa:
			cdr.Search = coder.NewExa(ws.URL, ws.APIKey, searchTransport, "Strument/"+version)
		default:
			cdr.Search = coder.NewSearxNG(ws.URL, searchTransport, "Strument/"+version)
		}
	}
}

func main() {
	// Before any request can go out: providers that ask callers to identify
	// themselves get a name and a version rather than Go's default UA.
	client.SetVersion(version)

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

// discoverSkills finds the session's skills and reports on stderr what it
// could not use.
//
// On stderr from main, in every mode, for the reason the sandbox notice above
// gives: this changes what the session can do, and (*REPL).announce returns
// early when the run is not interactive, so a scripted run would otherwise
// meet a model that never mentions a skill and have nothing on screen to say
// why. The wording mirrors what config.Load says about an untrusted project
// config, because it is the same decision about the same repository.
func discoverSkills(root string) []skill.Skill {
	tsPath, err := config.DefaultTrustStorePath()
	if err != nil {
		noticef("could not find the trust store, so project skills are disabled: %v", err)
	}
	var trust skill.Truster
	if tsPath != "" {
		ts, tsErr := config.OpenTrustStore(tsPath)
		if tsErr != nil {
			noticef("could not read the trust store, so project skills are disabled: %v", tsErr)
		} else {
			// Typed nil is not nil through an interface, so the store is
			// assigned only when there is one — Discover reads a nil Truster
			// as "trust nothing", which is the safe reading of both failures
			// above.
			trust = ts
		}
	}

	skills, diags := skill.Discover(skill.Options{ProjectRoot: root, Trust: trust})
	for _, d := range diags {
		noticef("skipping %s: %s", d.Path, d.Message)
	}
	if untrusted := skill.Untrusted(skills); len(untrusted) > 0 {
		names := make([]string, 0, len(untrusted))
		for _, s := range untrusted {
			names = append(names, s.Name)
		}
		noticeWith(
			fmt.Sprintf("ignoring %s: %s",
				render.Plural(len(names), "untrusted project skill", "untrusted project skills"),
				strings.Join(names, ", ")),
			config.TrustAdviceFor(len(names), false),
		)
	}
	return skills
}
