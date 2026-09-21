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
// Ninety days is a choice, not a measurement. Of the harnesses surveyed for
// doc/plans/sessions.md, Hermes is the only one with time-based expiry at all,
// which is why a number is defensible here and why it is not a consensus.
const defaultStripAge = 90 * 24 * time.Hour

// historyStripCmd removes stored tool payloads that nothing recent points at.
//
// It never removes a record. The record keeps the hash, the size and the
// payload's first line, so the conversation still reads as one and still
// replays — a reader that finds no blob shows the description in its place.
// That is the whole design: retention here is lossy compression rather than
// forgetting.
type historyStripCmd struct {
	OlderThan string `help:"Strip payloads nothing has referenced in this long, e.g. 30d, 6w, 720h (default: 90d)." placeholder:"<age>"`
	Yes       bool   `help:"Strip without asking."                                                                  short:"y"`
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
		fmt.Printf("Nothing to strip: no stored payload is older than %s.\n", humanSpan(age))
		if plan.Keep > 0 {
			fmt.Printf("%s in use, %s.\n",
				render.Plural(plan.Keep, "payload", "payloads"), humanBytes(plan.KeepBytes))
		}
		return nil
	}

	fmt.Printf("This removes %s (%s) that nothing has referenced in %s.\n",
		render.Plural(len(plan.Remove), "stored payload", "stored payloads"),
		humanBytes(plan.Bytes), humanSpan(age))
	if plan.Orphans > 0 {
		// Named separately because they are removed whatever the cutoff, and
		// somebody reading a number that did not match their --older-than
		// deserves to know why.
		fmt.Printf("%s of those %s referenced by no record at all, left by a deleted session "+
			"or a run that died mid-write.\n",
			render.Plural(plan.Orphans, "payload", "payloads"),
			map[bool]string{true: "is", false: "are"}[plan.Orphans == 1])
	}
	if plan.Keep > 0 {
		// Phrased as "keeping N" rather than "N stays", because Plural fixes
		// the noun and nothing fixes the verb.
		fmt.Printf("Keeping %s still in use, %s.\n",
			render.Plural(plan.Keep, "payload", "payloads"), humanBytes(plan.KeepBytes))
	}
	fmt.Println("\nEvery record is kept. Each stripped result keeps its hash, its size and its first line,")
	fmt.Println("so the conversation still reads and still replays — the payload itself is what goes.")

	if !c.Yes && !confirmStrip() {
		return nil
	}
	removed, freed, err := history.ApplyStrip(root, plan)
	if err != nil {
		return err
	}
	fmt.Printf("Stripped %s, freeing %s.\n",
		render.Plural(removed, "payload", "payloads"), humanBytes(freed))
	return nil
}

func confirmStrip() bool {
	if !isCharDevice(os.Stdin) {
		fmt.Println("\nDeclined: there is no terminal to ask on. Pass --yes to strip without one.")
		return false
	}
	fmt.Print("\nStrip? (y/N) ")
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
		return 0, errors.New("an age like 30d, 6w or 720h")
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
			return 0, fmt.Errorf("%q is not an age; try 30d, 6w or 720h", s)
		}
		if n <= 0 {
			return 0, fmt.Errorf("an age has to be positive, not %q", s)
		}
		return time.Duration(n * float64(unit)), nil
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("%q is not an age; try 30d, 6w or 720h", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("an age has to be positive, not %q", s)
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
