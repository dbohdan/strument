// Package coder is the orchestration spine: assemble -> stream -> reflect ->
// apply -> shell -> commit -> cost (basecoder-spec.md).
package coder

import (
	"context"
	"time"
)

// TokenCounter estimates token counts. Consumers treat counts as
// advisory-conservative and never gate irreversibly on an estimate
// (basecoder-spec §10).
type TokenCounter interface {
	Count(text string) int
}

// RuneCounter is the default estimator: code points / 4 (the non-OpenAI
// path of §10; see STATUS.md on the tiktoken question).
type RuneCounter struct{}

func (RuneCounter) Count(text string) int {
	n := 0
	for range text {
		n++
	}
	return n / 4
}

// Confirmer asks the user yes/no questions. Never=true means "never ask
// this again" — the caller tracks what that applies to.
type Confirmer interface {
	Confirm(req ConfirmRequest) (yes bool, never bool)
}

// ConfirmRequest mirrors aider's confirm_ask surface.
type ConfirmRequest struct {
	Prompt              string
	Subject             string
	ExplicitYesRequired bool // --yes must NOT auto-answer (model shell, §6.4)
	AllowNever          bool
	Group               string // ConfirmGroup key ("all"/"skip" scope)
}

// AutoConfirmer implements --yes / --yes-shell (basecoder-spec §6.4:
// --yes never auto-runs model shell; that needs YesShell).
type AutoConfirmer struct {
	Yes      bool
	YesShell bool
	// Fallback handles prompts the flags don't answer; nil declines.
	Fallback Confirmer
}

func (a AutoConfirmer) Confirm(req ConfirmRequest) (bool, bool) {
	if req.ExplicitYesRequired {
		if a.YesShell {
			return true, false
		}
		if a.Fallback != nil {
			return a.Fallback.Confirm(req)
		}
		return false, false
	}
	if a.Yes {
		return true, false
	}
	if a.Fallback != nil {
		return a.Fallback.Confirm(req)
	}
	return false, false
}

// CommandRunner executes one accepted shell block through a single shell
// (basecoder-spec §6.3). Output merges stdout and stderr.
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
	// message, or ok=false when there was nothing to commit.
	Commit(fnames []string, context string) (hash, message string, ok bool, err error)
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
// (display-only, never parsed or persisted, §4).
type Output interface {
	Print(format string, args ...any)
	Warning(format string, args ...any)
	Error(format string, args ...any)
	StreamText(delta string)
	StreamReasoning(delta string)
	// FlushStream marks the end of a send's live rendering.
	FlushStream()
}

// Scraper fetches URL content for check_for_urls; injectable for tests.
type Scraper func(ctx context.Context, url string) (string, error)
