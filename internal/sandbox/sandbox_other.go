//go:build !linux

package sandbox

import "errors"

// Probe reports no support. Landlock is a Linux LSM; there is no equivalent to
// degrade to, and pretending otherwise is what a warn-only sandbox mode would
// do.
func Probe() Availability {
	return Availability{Err: errors.New("Landlock is Linux-only")}
}
