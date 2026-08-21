//go:build !linux

package sandbox

import "errors"

// Apply always fails off Linux. There is no partial confinement to offer and
// no warning mode: a sandbox that cannot enforce says so, and the caller
// refuses to run what it cannot confine.
func (p Policy) Apply() error {
	return errors.New("no Landlock here: it is a Linux LSM and this is not Linux")
}
