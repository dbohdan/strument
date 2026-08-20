// Package sandbox confines what Strument and the commands it runs may write.
//
// The guarantee is deliberately narrow: **integrity, not confidentiality.**
// Writes cannot land outside the project and a few named places; reads and
// execution stay open across the whole filesystem. A mistaken or injected
// `rm -rf ~` becomes impossible; a determined exfiltration is no harder than it
// was. The threat model is mistakes and prompt injection with a human watching,
// not a misaligned agent working patiently over hundreds of turns.
//
// Leaving reads open is what makes this affordable. Landlock's read rule also
// grants execution, so one rule over "/" covers every binary on the machine —
// /usr/bin, ~/.local/bin, ~/go/bin, ~/.cargo/bin — with nothing to enumerate
// and nothing to keep up to date. Confining reads instead is the choice that
// makes a sandbox break builds, and a sandbox that breaks builds gets removed.
//
// See doc/security.md for the threat model and what the sandbox does not cover.
package sandbox

// Availability is what a probe of the running kernel found.
type Availability struct {
	// ABI is the Landlock ABI version the kernel supports, or 0 for none.
	ABI int
	// Err is why the probe failed, when it did.
	Err error
}

// Supported reports whether the kernel can enforce anything at all.
func (a Availability) Supported() bool { return a.ABI > 0 }
