package history

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Sequential session names, for /clear: clearing "foo" moves to "foo-2", and
// clearing "foo-2" to "foo-3". The earlier conversation stays whole under its
// own name, because the session log is append-only and a clear that only
// emptied memory came back on the next restore.
//
// The names are the whole data model. No index records which sessions form a
// sequence, so nothing can fall out of step with the directory.

// seqSuffix is a trailing "-N". Only N from 2 to 99 counts, and only when the
// base is itself a session (nextSessionName): "release-2026" is a name, not
// the 2026th clear of "release", and a hand-named "spike-2" with no "spike"
// beside it is a name too.
var seqSuffix = regexp.MustCompile(`^(.+)-([1-9][0-9]?)$`)

// nextSessionName proposes the session /clear moves to, given the current one
// and every existing name. It is pure, so the rules are a table test.
//
// The number is the highest in the sequence plus one, not the lowest free
// one: after "foo-2" is deleted, the next clear makes "foo-4", so a name never
// comes to mean a second, later conversation. Names are compared without case,
// because on macOS and Windows "Foo-2" and "foo-2" are one directory.
func nextSessionName(current string, existing []string) string {
	have := map[string]bool{}
	for _, e := range existing {
		have[strings.ToLower(e)] = true
	}
	base := current
	if m := seqSuffix.FindStringSubmatch(current); m != nil {
		if n, _ := strconv.Atoi(m[2]); n >= 2 && have[strings.ToLower(m[1])] {
			base = m[1]
		}
	}
	highest := 1
	prefix := strings.ToLower(base) + "-"
	for name := range have {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			if n, err := strconv.Atoi(rest); err == nil && n > highest && strconv.Itoa(n) == rest {
				highest = n
			}
		}
	}
	for n := highest + 1; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		b := base
		// A name has a length limit, so the base gives way to the number. A
		// cut that leaves a trailing dot or dash would be invalid or ugly, so
		// those go too.
		if len(b)+len(suffix) > maxSessionName {
			b = strings.TrimRight(b[:maxSessionName-len(suffix)], ".-_")
		}
		name := b + suffix
		if !have[strings.ToLower(name)] && ValidSessionName(name) == nil {
			return name
		}
	}
}

// NextSessionName returns the name /clear should move to from current. The
// project lock allows one Strument per project, so the name cannot be taken
// between this and the switch that creates it.
func NextSessionName(projectRoot, current string) (string, error) {
	dir, err := artifactPath(projectRoot, artSessions)
	if err != nil {
		return "", err
	}
	var names []string
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return nextSessionName(current, names), nil
}

// SessionTurns counts the turns a session has recorded. /clear on a session
// with none has nothing worth keeping, and empties memory instead of making a
// new session each time.
func SessionTurns(projectRoot, session string) int {
	_, turns := countTurns(projectRoot, session)
	return turns
}

// compareNatural orders names with digit runs compared as numbers, so
// "foo-9" sorts before "foo-10". Runs of digits compare by value, then by
// length (so "foo-02" follows "foo-2"), and everything else byte by byte.
func compareNatural(a, b string) int {
	for a != "" && b != "" {
		da, db := digitRun(a), digitRun(b)
		if da > 0 && db > 0 {
			na := strings.TrimLeft(a[:da], "0")
			nb := strings.TrimLeft(b[:db], "0")
			if len(na) != len(nb) {
				return cmpInt(len(na), len(nb))
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			if da != db {
				return cmpInt(da, db)
			}
			a, b = a[da:], b[db:]
			continue
		}
		if a[0] != b[0] {
			return cmpInt(int(a[0]), int(b[0]))
		}
		a, b = a[1:], b[1:]
	}
	return cmpInt(len(a), len(b))
}

func digitRun(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
