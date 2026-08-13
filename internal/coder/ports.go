// Package coder is the orchestration spine: assemble -> stream -> reflect ->
// apply -> shell -> commit -> cost.
package coder

import (
	"context"
	"time"
)

// TokenCounter estimates token counts. Consumers treat counts as
// advisory-conservative and never gate irreversibly on an estimate.
type TokenCounter interface {
	Count(text string) int
}

// RuneCounter is the default estimator: code points / 4. Measured
// against the phase-0 captures, runes/4 lands within ~0.5% of the
// provider's real prompt_tokens for the primary model (DeepSeek: 3.99–4.02
// chars/token; est/real ≈ 1.00 over five requests).
// It can under-count unusually code-dense payloads (code runs closer to
// 3.3 chars/token), so the count is advisory, not a guarantee; consumers
// never gate irreversibly on it (margin: unknown-max→always-add).
type RuneCounter struct{}

func (RuneCounter) Count(text string) int {
	n := 0
	for range text {
		n++
	}
	return n / 4
}

// Confirmer asks the user yes/no questions.
type Confirmer interface {
	Confirm(req ConfirmRequest) ConfirmResult
}

// ConfirmResult is the user's answer to a confirmation prompt.
//
// There used to be a Never ("d", don't ask again) alongside these, offered by
// the prompt whenever AllowNever was set — and honored by nothing. Each caller
// treated it as a plain decline: the URL check rejects the URL either way, the
// command-output check reads only Yes, and the shell gate goes through
// confirmTurn, which never looked at it. The one caller that might have meant
// it left with the file-mention flow. A prompt advertising an option that does
// nothing is worse than one without it, so the option is gone rather than
// implemented — session-scoped silence on shell commands is the last thing this
// gate should grow.
type ConfirmResult struct {
	Yes            bool // the user approved this one action
	AlwaysThisTurn bool // "a" — turn-scoped auto-approve for this Group
}

// ConfirmRequest mirrors aider's confirm_ask surface.
type ConfirmRequest struct {
	Prompt              string
	Subject             string
	ExplicitYesRequired bool   // --yes must NOT auto-answer (model shell)
	Group               string // ConfirmGroup key ("all"/"skip" scope)
}

// AutoConfirmer implements --yes / --yes-shell (--yes never auto-runs
// model shell; that needs YesShell).
type AutoConfirmer struct {
	Yes      bool
	YesShell bool
	// Fallback handles prompts the flags don't answer; nil declines.
	Fallback Confirmer
}

func (a AutoConfirmer) Confirm(req ConfirmRequest) ConfirmResult {
	if req.ExplicitYesRequired {
		if a.YesShell {
			return ConfirmResult{Yes: true}
		}
		if a.Fallback != nil {
			return a.Fallback.Confirm(req)
		}
		return ConfirmResult{}
	}
	if a.Yes {
		return ConfirmResult{Yes: true}
	}
	if a.Fallback != nil {
		return a.Fallback.Confirm(req)
	}
	return ConfirmResult{}
}

// CommandRunner executes one accepted shell block through a single shell.
// Output merges stdout and stderr.
type CommandRunner interface {
	Run(ctx context.Context, block string, cwd string) (exitCode int, output string, err error)
}

// Repo is the git port; nil Repo means no git integration this session.
// Implemented by shelling out in phase 8.
type Repo interface {
	Root() string
	TrackedFiles() []string // repo-root-relative
	PathInRepo(rel string) bool
	IsDirty(rel string) bool
	GitIgnored(rel string) bool
	HeadSHA() string
	// Commit commits fnames with a generated message; returns hash and
	// message, or ok=false when there was nothing to commit. attributed
	// marks auto-commits of model edits, which get the trailer;
	// dirty commits of user changes stay unattributed.
	Commit(fnames []string, context string, attributed bool) (hash, message string, ok bool, err error)
}

// Clock injects time so retry/continuation tests don't sleep.
type Clock interface {
	Sleep(d time.Duration)
	Now() time.Time
}

// RealClock is the production clock.
type RealClock struct{}

func (RealClock) Sleep(d time.Duration) { time.Sleep(d) }
func (RealClock) Now() time.Time        { return time.Now() }

// Output is where the coder talks to the user. StreamText receives answer
// deltas as they arrive; StreamReasoning receives reasoning deltas
// (display-only, never parsed or persisted).
type Output interface {
	Printf(format string, args ...any)
	Warningf(format string, args ...any)
	Errorf(format string, args ...any)
	// Toolf reports what a tool just did — the file that was read, the search
	// that matched, the check that passed. Most of a turn's scroll is now this,
	// so it gets a recessive color of its own: the harness narrating its own
	// work should sit behind the diffs and the answer, not compete with them.
	Toolf(format string, args ...any)
	StreamText(delta string)
	StreamReasoning(delta string)
	// StreamToolCall receives a streamed tool-call argument fragment for the
	// call at index (name on the first fragment for an index), so edit tools
	// render as a live red-green diff. FlushStream closes any open render.
	StreamToolCall(index int, name, argsFragment string)
	// FlushStream marks the end of a send's live rendering.
	FlushStream()
}

// Scraper fetches URL content for check_for_urls; injectable for tests.
type Scraper func(ctx context.Context, url string) (string, error)
