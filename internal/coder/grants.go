package coder

import (
	"maps"
	"slices"
)

// GrantSource says where a standing approval came from, so the session can
// report it and /reload can replace the right half.
type GrantSource string

const (
	// GrantFromFlag is --yes on the command line. Fixed for the session: a
	// /reload must not undo a decision the user made when they started, the
	// same reason Coder.ShellWithheld remembers --no-shell.
	GrantFromFlag GrantSource = "--yes"
	// GrantFromConfig is auto_approve in the config. Replaced wholesale on
	// /reload, because that is what re-reading the config means.
	GrantFromConfig GrantSource = "config"
	// GrantFromSession is /yes add. It outranks nothing; it is simply another
	// source, and /yes reset clears it.
	GrantFromSession GrantSource = "session"
)

// Grants is the set of prompts answered without asking, and where each came
// from.
//
// Three sources rather than one map, because they have different lifetimes and
// a single merged set cannot say which. /reload replaces the config half and
// must leave --yes alone; /yes reset clears the session half and must leave
// both others alone; and the report has to be able to say "websearch (config)"
// so the user knows whether editing the config will change it.
type Grants struct {
	flag    map[string]bool
	config  map[string]bool
	added   map[string]bool
	dropped map[string]bool
}

// NewGrants builds the session's standing approvals from the flag.
func NewGrants(flag map[string]bool) *Grants {
	return &Grants{
		flag:    maps.Clone(flag),
		config:  map[string]bool{},
		added:   map[string]bool{},
		dropped: map[string]bool{},
	}
}

// SetConfig replaces the config half. Called by ApplyConfig, so /reload picks
// up an edited auto_approve without disturbing anything else.
func (g *Grants) SetConfig(names map[string]bool) {
	if g == nil {
		return
	}
	g.config = maps.Clone(names)
}

// Add and Drop are /yes add and /yes drop.
//
// Dropping records the name rather than deleting it, because the name may come
// from the flag or the config, which this cannot edit. A drop is the session
// saying "ask me about this one after all", and Reset takes it back.
func (g *Grants) Add(name string) {
	delete(g.dropped, name)
	g.added[name] = true
}

func (g *Grants) Drop(name string) {
	delete(g.added, name)
	g.dropped[name] = true
}

// Reset forgets this session's changes, leaving the flag and the config.
func (g *Grants) Reset() {
	g.added = map[string]bool{}
	g.dropped = map[string]bool{}
}

// Granted reports whether a prompt with this name is answered automatically.
func (g *Grants) Granted(name string) bool {
	if g == nil || name == "" || g.dropped[name] {
		return false
	}
	return g.flag[name] || g.config[name] || g.added[name]
}

// Effective is the set AutoConfirmer answers from.
func (g *Grants) Effective() map[string]bool {
	out := map[string]bool{}
	if g == nil {
		return out
	}
	for _, m := range []map[string]bool{g.flag, g.config, g.added} {
		for name := range m {
			if !g.dropped[name] {
				out[name] = true
			}
		}
	}
	return out
}

// Sources lists what is granted and where each came from, in GrantNames order
// so the report is stable rather than map-ordered. A name from more than one
// source reports the most durable, which is what the user would have to edit
// to change it.
func (g *Grants) Sources() []struct {
	Name   string
	Source GrantSource
} {
	var out []struct {
		Name   string
		Source GrantSource
	}
	if g == nil {
		return out
	}
	for _, name := range GrantNames {
		if !g.Granted(name) {
			continue
		}
		src := GrantFromSession
		switch {
		case g.flag[name]:
			src = GrantFromFlag
		case g.config[name]:
			src = GrantFromConfig
		}
		out = append(out, struct {
			Name   string
			Source GrantSource
		}{name, src})
	}
	return out
}

// Dropped lists names this session turned back on asking for, so /yes can say
// so rather than leaving a silent subtraction.
func (g *Grants) Dropped() []string {
	if g == nil {
		return nil
	}
	var out []string
	for name := range g.dropped {
		if g.flag[name] || g.config[name] {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// StaticGrants adapts a fixed set for callers whose grants never change --
// `strument tool`, and the tests that predate the session being able to change
// them.
func StaticGrants(m map[string]bool) func() map[string]bool {
	return func() map[string]bool { return m }
}
