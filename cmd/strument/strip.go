package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"dbohdan.com/strument/internal/history"
	"dbohdan.com/strument/internal/render"
)

// defaultStripAge is how far back `strument history strip` reaches when told
// nothing.
//
// A default of "everything" would be the wrong shape for this command. It is
// the one operation in Strument that destroys recorded material, and a bare
// invocation is what someone types to find out what it does — so the bare
// invocation reaches for the part of the history that is plainly old rather
// than for all of it.
//
// Ninety days is a choice, not a measurement. Of the harnesses surveyed in
// doc/sessions.md, Hermes is the only one with time-based expiry at all, which
// is why a number is defensible here and why it is not a consensus.
const defaultStripAge = 90 * 24 * time.Hour

// historyStripCmd removes stored tool payloads that nothing recent points at.
//
// It never removes a record. The record keeps the hash, the size and the
// payload's first line, so the conversation still reads as one and still
// replays — a reader that finds no blob shows the description in its place.
// That is the whole design: retention here is lossy compression rather than
// forgetting.
type historyStripCmd struct {
	OlderThan string `help:"Remove tool output not referenced within this period, e.g. 30d, 6w, or 720h (default: 90d)." placeholder:"<age>"`
	Yes       bool   `help:"Remove without asking for confirmation."                                                     short:"y"`
}

func (c *historyStripCmd) Run() error {
	age := defaultStripAge
	if c.OlderThan != "" {
		parsed, err := parseAge(c.OlderThan)
		if err != nil {
			return err
		}
		age = parsed
	}
	root, err := historyRoot()
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-age)
	plan, err := history.PlanStrip(root, cutoff)
	if err != nil {
		return err
	}
	if plan.Empty() {
		fmt.Printf("Nothing to remove: no stored tool output is older than %s.\n", humanSpan(age))
		if plan.Keep > 0 {
			fmt.Printf("%s in use (%s).\n",
				render.Plural(plan.Keep, "stored output", "stored outputs"), humanBytes(plan.KeepBytes))
		}
		return nil
	}

	fmt.Printf("This removes %s (%s) not referenced in the last %s.\n",
		render.Plural(len(plan.Remove), "stored tool output", "stored tool outputs"),
		humanBytes(plan.Bytes), humanSpan(age))
	if plan.Orphans > 0 {
		// Named separately because they are removed whatever the cutoff, and
		// somebody reading a number that did not match their --older-than
		// deserves to know why.
		fmt.Printf("Of those, %d %s not referenced by any record: left by a deleted session, "+
			"or by a run that stopped partway through writing. These are removed at any age.\n",
			plan.Orphans, map[bool]string{true: "is", false: "are"}[plan.Orphans == 1])
	}
	if plan.Keep > 0 {
		// Phrased as "keeping N" rather than "N stays", because Plural fixes
		// the noun and nothing fixes the verb.
		fmt.Printf("Keeping %s still in use (%s).\n",
			render.Plural(plan.Keep, "stored output", "stored outputs"), humanBytes(plan.KeepBytes))
	}
	fmt.Println("\nEvery record is kept. Each removed output keeps its hash, size, and first line,")
	fmt.Println("so conversations still read and replay; only the stored output is deleted.")

	if !c.Yes && !confirmStrip() {
		return nil
	}
	removed, freed, err := history.ApplyStrip(root, plan)
	if err != nil {
		return err
	}
	fmt.Printf("Removed %s, freeing %s.\n",
		render.Plural(removed, "stored output", "stored outputs"), humanBytes(freed))
	return nil
}

func confirmStrip() bool {
	if !isTerminal(os.Stdin) {
		fmt.Println("\nDeclined: this requires an interactive terminal. Pass --yes to remove without confirmation.")
		return false
	}
	fmt.Print("\nRemove them? (y/N) ")
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// parseAge reads a retention interval.
//
// It takes Go's own duration syntax and adds days and weeks, which are the
// units a retention policy is actually written in — `time.ParseDuration`
// stops at hours, and "2160h" is nobody's idea of three months.
func parseAge(s string) (time.Duration, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, errors.New("give an age such as 30d, 6w, or 720h")
	}
	unit := time.Duration(0)
	switch {
	case strings.HasSuffix(trimmed, "d"):
		unit = 24 * time.Hour
	case strings.HasSuffix(trimmed, "w"):
		unit = 7 * 24 * time.Hour
	}
	if unit > 0 {
		n, err := strconv.ParseFloat(strings.TrimSuffix(trimmed[:len(trimmed)-1], " "), 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not an age; use a value such as 30d, 6w, or 720h", s)
		}
		if n <= 0 {
			return 0, fmt.Errorf("the age must be positive, not %q", s)
		}
		return time.Duration(n * float64(unit)), nil
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("%q is not an age; use a value such as 30d, 6w, or 720h", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("the age must be positive, not %q", s)
	}
	return d, nil
}

// humanSpan renders a retention interval as a length of time.
//
// humanAge next door renders the same kind of value as "3 days ago", which is
// right for "last used" and wrong after "older than" — a sweep reaching back
// "90 days ago" reads as a date rather than a span. Two renderings because
// there are two sentences, not because the arithmetic differs.
func humanSpan(d time.Duration) string {
	switch {
	case d >= 7*24*time.Hour && d%(7*24*time.Hour) == 0:
		return render.Plural(int(d/(7*24*time.Hour)), "week", "weeks")
	case d >= 24*time.Hour:
		return render.Plural(int(d/(24*time.Hour)), "day", "days")
	case d >= time.Hour:
		return render.Plural(int(d/time.Hour), "hour", "hours")
	default:
		return d.String()
	}
}
