package render

import "strings"

// Sanitize makes untrusted text safe to put on a terminal: newlines and tabs
// survive, every other control character and escape sequence does not.
//
// The untrusted text is everything a model influences — its own words, the
// paths and patterns it passes as tool arguments, the diff bodies it writes,
// and the output of commands it asked to run. A model's instructions can come
// from a README or a scraped page, so all of it is attacker-reachable, and a
// terminal reading an escape sequence does what the sequence says: retitle the
// window, clear the screen, move the cursor back over text the user has already
// read. That last one matters most here, because what the user reads off the
// screen is the review surface the whole design rests on.
//
// It is applied where untrusted text *enters* the output layer rather than at
// the writer, because the renderer's own colors are escape sequences too and
// share that writer. Stripping there would take them with it; whitelisting
// there would couple this to the renderer's alphabet. The guarantee is the
// test, which feeds an escape through every public entry point of the output
// and asserts none survives.
//
// The gap this closes was not the one a review predicted. Reads of the code
// suggested the streamed answer was raw and tool arguments were quoted; on the
// wire it was the other way round. quoteToolArg adds quotes and nothing else,
// so a path carrying an OSC reached the terminal intact, while the answer
// stream stripped "\r" and mangled "\x1b[2J" because the markdown parser claims
// the "[". Inconsistent filtering is harder to reason about than none.
func Sanitize(s string) string {
	if !needsSanitizing(s) {
		return s // the overwhelmingly common case: no copy, no allocation
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\n' || c == '\t':
			b.WriteByte(c)
		case c == 0x1b:
			// Skip the whole sequence, not just the ESC: leaving its payload
			// behind would print "[0;PWNED" as text, which is noise rather than
			// safety.
			i += escapeLen(s[i:]) - 1
		case c < 0x20 || c == 0x7f:
			// Other C0 controls and DEL. Dropped rather than escaped: this text
			// is being shown to a person, and "^H" in the middle of a sentence
			// helps nobody.
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func needsSanitizing(s string) bool {
	for i := range len(s) {
		if c := s[i]; (c < 0x20 && c != '\n' && c != '\t') || c == 0x7f {
			return true
		}
	}
	return false
}

// escapeLen is the length of the escape sequence starting at s[0], which is
// ESC. An incomplete or unrecognized one counts as just the ESC, so nothing is
// swallowed that was not part of a sequence.
func escapeLen(s string) int {
	if len(s) < 2 {
		return 1
	}
	switch s[1] {
	case '[': // CSI: parameters, then a final byte in @-~
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']': // OSC: runs to BEL or ST (ESC \)
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	case 'P', 'X', '^', '_': // DCS, SOS, PM, APC: run to ST
		for i := 2; i < len(s); i++ {
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	default:
		return 2 // a two-byte escape
	}
}
