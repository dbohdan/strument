package coder

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"dbohdan.com/strument/internal/llm"
)

// toolAbout reports what the running process is: its build, its clock, its
// platform and its session settings.
//
// It exists because a model cannot find these out by looking, and guesses
// when it tries. MiMo, debugging a fix that was plainly in the tree, spent a
// long stretch proving that the process serving its calls was the binary it
// had just built — inode numbers from /proc/pid/maps, `go version -m`, mtimes
// — and still ended the session doubting it. Every one of those facts is one
// the process knows about itself for free. The date in the system prompt is
// the other case: it is written once, when the prompt is built, so a session
// that crosses midnight is told the wrong day for the rest of its life.
const toolAbout = "about"

// BuildInfo is what the binary knows about how it was built. main fills it
// in, from the linker-set version and runtime/debug.ReadBuildInfo; the coder
// only reports it, so it never learns how a build is stamped.
type BuildInfo struct {
	Version    string    // "0.0.0-dev" for an unstamped build
	Commit     string    // "" when the build carried no VCS stamp
	CommitTime time.Time // zero when unknown
	Modified   bool      // the tree had uncommitted changes at build time
	GoVersion  string

	// Executable is the path this process was started from, and
	// ExecutableTime that file's modification time now, which is the nearest
	// thing a Go binary has to a build time: Go does not record one, on
	// purpose, so that builds reproduce. Both are "" and zero when the
	// executable cannot be found or read.
	Executable     string
	ExecutableTime time.Time

	// Started is when this process started.
	Started time.Time
}

func aboutTool() llm.ToolDef {
	return llm.ToolDef{
		Name: toolAbout,
		Description: "Report facts about this Strument process: its version and build, " +
			"the executable it runs from and when that file last changed, when the process " +
			"started, the current date and time, the platform, and this session's model, " +
			"mode and sandbox. Use it instead of inferring any of these, for example to " +
			"find out whether the process is running the build you expect or what time it is now.",
	}
}

// About renders the about tool's report. now is passed in so a test can fix
// it. Exported for `strument tool about`, whose Coder has no model and no
// mode: those lines are left out rather than reported empty.
func (c *Coder) About(now time.Time) string {
	b := c.Build
	var out strings.Builder
	line := func(label, value string) {
		if value != "" {
			fmt.Fprintf(&out, "%s: %s\n", label, value)
		}
	}
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format("2006-01-02 15:04:05 UTC")
	}

	out.WriteString("Strument\n")
	line("Version", b.Version)
	commit := b.Commit
	if commit != "" && b.Modified {
		commit += " (built with uncommitted changes)"
	}
	if commit == "" {
		commit = "unknown (the build carried no version-control stamp)"
	}
	line("Commit", commit)
	line("Commit time", stamp(b.CommitTime))
	line("Go", b.GoVersion)
	line("Executable", b.Executable)
	line("Executable modified", stamp(b.ExecutableTime))
	line("Process started", stamp(b.Started))
	// The one inference worth making for the model, because it is the
	// question this tool exists to settle and the answer takes two
	// timestamps it might compare the wrong way round.
	if !b.ExecutableTime.IsZero() && !b.Started.IsZero() && b.ExecutableTime.After(b.Started) {
		out.WriteString("The executable changed after this process started, so this process " +
			"is running an earlier build than the file on disk. The user has to restart " +
			"Strument for a new build to take effect.\n")
	}

	out.WriteString("\nTime\n")
	line("Now (UTC)", now.UTC().Format("2006-01-02 15:04:05 UTC, Monday"))
	zone, offset := now.Zone()
	line("Now (local)", fmt.Sprintf("%s %s (UTC%s)",
		now.Format("2006-01-02 15:04:05"), zone, utcOffset(offset)))

	out.WriteString("\nSystem\n")
	line("OS", runtime.GOOS)
	line("Architecture", runtime.GOARCH)
	line("Platform", c.Platform.Platform)
	line("CPUs", strconv.Itoa(runtime.NumCPU()))

	out.WriteString("\nSession\n")
	if c.Model != nil {
		line("Model", c.Model.QualifiedSlug())
		if c.Model.Context > 0 {
			line("Context window", fmt.Sprintf("%d tokens", c.Model.Context))
		}
	}
	line("Mode", c.modeName())
	line("Project root", c.Root)
	line("Sandbox", c.Sandbox.describe())
	return out.String()
}

// modeName is the mode as the user switches to it: ask or code.
func (c *Coder) modeName() string {
	switch c.editFormat {
	case "":
		return ""
	case "ask":
		return "ask (discussion; no edits or commands)"
	}
	return "code (edit format: " + c.editFormat + ")"
}

// utcOffset renders a zone offset in seconds as +HH:MM.
func utcOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	return fmt.Sprintf("%s%02d:%02d", sign, seconds/3600, seconds%3600/60)
}

// describe is the sandbox's state in one line, for the about tool.
func (s SandboxState) describe() string {
	switch {
	case s.Active && len(s.Writable) > 0:
		return "active; writes are permitted only under " + strings.Join(s.Writable, ", ")
	case s.Active:
		return "active"
	case s.Required:
		why := s.Unavailable
		if why == "" {
			why = "none is active"
		}
		return "required but unavailable (" + why + "); commands the model causes are refused"
	default:
		return "none"
	}
}
