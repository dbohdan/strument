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
	for _, path := range append(append([]string{}, p.Writable...), writableDevices...) {
		if rule, ok := writeRule(path); ok {
			rules = append(rules, rule)
		}
	}
	return rules
}

// writeRule grants writing under one path, choosing the rule by what the path
// actually is. It reports false for a path that is not there.
//
// The file/directory split is not cosmetic. Landlock's directory rights include
// make_dir, remove_dir and the rest, and asking for them on a regular file is
// rejected — "inconsistent access rights (using directory access rights on a
// regular file?)" — which fails the *entire* ruleset, not just that entry. So
// getting this wrong does not narrow the sandbox, it removes it: every rule is
// dropped and nothing is enforced. /dev/null is a character device, and a live
// run against the real policy is what caught it.
//
// The class is detected rather than declared, because it is not always the same
// answer: /dev/pts and /dev/shm are directories while /dev/null and /dev/ptmx
// are not, and a user's sandbox_write entry may be either.
//
// Stat rather than Lstat: a symlink should be classified as whatever it points
// at, which is also the inode Landlock will anchor the rule to.
func writeRule(path string) (landlock.FSRule, bool) {
	info, err := os.Stat(path)
	if err != nil {
		// Not there. A policy should not fail because a project has no state
		// directory yet, or this machine has no /dev/shm.
		return landlock.FSRule{}, false
	}
	if info.IsDir() {
		return landlock.RWDirs(path).WithRefer().WithIoctlDev().WithResolveUnix(), true
	}
	// No WithRefer on a file: refer is about reparenting entries within a
	// directory, and asking for it here is the same category error again.
	return landlock.RWFiles(path).WithIoctlDev(), true
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
