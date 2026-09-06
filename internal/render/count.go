package render

import "fmt"

// Counting words for messages a person reads. See doc/messages.md.
//
// These live here rather than in either caller because there used to be one in
// each: `plural` in cmd/strument and `pluralize` in internal/repl, identical but
// for whether they printed the number. Two spellings of one rule is how a rule
// drifts, and four messages used "(s)" anyway rather than reach for either.

// Plural renders a count with the word that matches it: Plural(1, "turn",
// "turns") is "1 turn", Plural(12, …) is "12 turns".
//
// Never write "turn(s)". It saves a helper call and costs every reader a small
// stumble, in a line they did not choose to read.
func Plural(n int, one, many string) string {
	return fmt.Sprintf("%d %s", n, PluralWord(n, one, many))
}

// PluralWord picks the word without the count, for a sentence that puts the
// number somewhere else — or leaves it out, as "no turns" does.
func PluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
