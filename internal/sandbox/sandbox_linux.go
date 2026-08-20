//go:build linux

package sandbox

import (
	llsys "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

// Probe asks the kernel which Landlock ABI it supports.
//
// Asked explicitly rather than inferred from a BestEffort() call, which is the
// library's documented footgun: on a kernel with no Landlock at all, a
// best-effort restriction succeeds and enforces nothing. A sandbox that
// silently enforces nothing is worse than none, because the user believes it is
// there. The version also decides which rules are worth requesting.
//
// A modern kernel is not sufficient. Landlock must be compiled in and enabled,
// and it is absent on plenty of container and VM kernels — Claude Code's own
// development container runs 6.18 and returns ENOSYS.
func Probe() Availability {
	v, err := llsys.LandlockGetABIVersion()
	if err != nil {
		return Availability{Err: err}
	}
	return Availability{ABI: v}
}
