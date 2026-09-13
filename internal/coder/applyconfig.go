package coder

import (
	"time"

	"dbohdan.com/strument/internal/config"
)

// ApplyConfig copies a loaded config onto the Coder. It is the *only* place a
// config value reaches a Coder field, and that is the whole point of it existing.
//
// There used to be two places: `chatCmd.Run` at startup and `cmdReload`
// mid-session. They drifted, three times, and each time the symptom was a
// reload that looked like it had worked. `cmdReload`'s own comments record two
// of the three — a check you could edit and reload to no effect, and egress
// ports left alone so a new proxy never took — and the third was the five
// prompt_* keys, which went in with the feature and were never added to reload
// at all. Prompt iteration is the likeliest reason anyone reaches for /reload,
// so that was the worst of the three and the quietest.
//
// A list policed by a test would have caught the next one; one function cannot
// have a next one. internal/repl's reload test asserts that this stays the only
// caller-visible path, because the failure mode is somebody adding
// `cdr.Thing = cfg.Thing` to startup code instead of here.
//
// What deliberately stays out, because mid-session is not a safe time:
//
//   - The sandbox. Landlock is monotonic — there is no call that removes a
//     ruleset — so the writable set is fixed when the process starts. /sandbox
//     says so.
//   - Egress (the scraper, the proxy, the search backend). Those are ports built
//     around closures and transports rather than plain values, so they have to be
//     rebuilt rather than assigned; the REPL's ApplyEgress hook does it.
//   - The model and its client, which /model owns.
//   - The JSONL recorder, which is a sink opened once for the session.
func ApplyConfig(c *Coder, cfg *config.Config) {
	if cfg == nil {
		return
	}

	// Guarded assignments: a zero means "the config did not say", and the
	// constructor's default has to survive a reload that says nothing either.
	c.MaxSteps = defaultMaxSteps
	if cfg.MaxSteps > 0 {
		c.MaxSteps = cfg.MaxSteps
	}
	c.MaxErrorReflections = defaultMaxErrorReflections
	if cfg.MaxErrorReflections > 0 {
		c.MaxErrorReflections = cfg.MaxErrorReflections
	}
	if cfg.ShellTimeout != 0 {
		// Seconds in the config, a Duration in the coder; -1 carries "no limit"
		// through as a negative duration, which shellTimeout reads as such.
		c.ShellTimeout = time.Duration(cfg.ShellTimeout) * time.Second
	}

	c.LoopDetection = !cfg.NoLoopDetection
	// --no-shell turns the tool off and cannot turn it on, so the flag is
	// remembered on the Coder rather than consulted here: a reload must not
	// undo a decision made on the command line, and a config saying
	// `shell = False` is a standing decision a flag must not silently reverse.
	c.SuggestShellCommands = !cfg.NoShell && !c.ShellWithheld
	c.AnchoredEdits = cfg.AnchoredEdits
	c.IndentColumn = cfg.AnchoredEdits && cfg.IndentColumn
	c.ObservationViaRunCode = cfg.ObservationViaRunCode
	c.WebfetchAllow = cfg.WebfetchAllow
	c.Check = cfg.Check
	c.CheckAuto = cfg.CheckAuto
	c.EnvAllow = cfg.EnvAllow

	// The prompt layer last, and through setPrompts rather than by hand.
	// setPrompts rebuilds the active set from the built-in before layering the
	// overrides and the examples on top, which is what makes this idempotent:
	// the old reload appended example_messages to the live set every time, so
	// reloading twice showed the model its examples twice.
	c.SystemPromptPrefix = cfg.PromptSystemPrefix
	c.PromptCode = cfg.PromptCode
	c.PromptAsk = cfg.PromptAsk
	c.PromptCommit = cfg.PromptCommit
	c.PromptReadOnly = cfg.PromptReadOnly
	c.Examples = cfg.ExampleMessages
	c.SetChatLanguage(cfg.ChatLanguage)
	c.setPrompts()
}
