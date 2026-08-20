//go:build linux

package sandbox

import (
	"fmt"
	"os"

	"github.com/landlock-lsm/go-landlock/landlock"
)

// rules builds the Landlock ruleset for a policy.
//
// Three modifiers are attached, and each is here because a live probe on a real
// kernel said so rather than because the documentation suggested it:
//
//   - WithRefer, or renaming a file between two directories fails inside the
//     writable root. The denial arrives as EXDEV ("invalid cross-device link"),
//     not EACCES, which also means mv(1) quietly falls back to copy-and-unlink
//     instead of reporting a permission problem.
//   - WithIoctlDev, handled from ABI 5, or an ioctl on a device file is denied.
//     That is every terminal-size query on a pty the sandbox just permitted
//     opening, so the two go together or neither is worth having.
//   - WithResolveUnix, handled from ABI 9, or connecting to a pathname Unix
//     socket is denied. It is granted *now*, on kernels that do not yet have
//     it, precisely because they do not: a policy that omits it keeps working
//     until the day the kernel is upgraded and then starts failing every test
//     suite that talks to a local database, with no change on our side. A
//     latent break scheduled for someone else's kernel upgrade is worse than
//     an outright one.
//
// BestEffort drops modifiers the running kernel does not handle, so requesting
// the newest set is safe. What BestEffort must never be trusted for is whether
// Landlock exists at all — see Probe.
func (p Policy) rules() []landlock.Rule {
	// Read and execute everywhere. RODirs grants execution as well as reading,
	// which is what makes this policy cheap: every binary on the machine —
	// /usr/bin, ~/.local/bin, ~/go/bin, ~/.cargo/bin — works with nothing
	// enumerated and nothing to keep current.
	rules := make([]landlock.Rule, 0, 1+len(p.Writable)+len(writableDevices))
	rules = append(rules, landlock.RODirs("/").WithResolveUnix())
	for _, dir := range p.existing(p.Writable) {
		rules = append(rules, landlock.RWDirs(dir).WithRefer().WithIoctlDev().WithResolveUnix())
	}
	for _, dev := range p.existing(writableDevices) {
		rules = append(rules, landlock.RWDirs(dev).WithIoctlDev().WithResolveUnix())
	}
	return rules
}

// existing drops paths that are not there. Landlock refuses a rule naming a
// missing path, and a policy should not fail because a project has no state
// directory yet or because this machine has no /dev/shm.
func (p Policy) existing(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			out = append(out, path)
		}
	}
	return out
}

// Apply confines the calling process, and everything it later spawns, to the
// policy. It cannot be undone: Landlock is monotonic by design, which is what
// makes it safe to apply in a process that goes on to run untrusted commands.
//
// The caller must have probed first. Apply refuses rather than silently
// enforcing nothing, because a sandbox believed to be present and absent is
// worse than one known to be absent.
func (p Policy) Apply() error {
	if av := Probe(); !av.Supported() {
		return fmt.Errorf("landlock unavailable: %w", av.Err)
	}
	if err := landlock.V9.BestEffort().RestrictPaths(p.rules()...); err != nil {
		return fmt.Errorf("landlock: %w", err)
	}
	return nil
}
