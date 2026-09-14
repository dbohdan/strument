package coder

import (
	"slices"
	"testing"

	"dbohdan.com/strument/internal/config"
)

func names(g *Grants) []string {
	var out []string
	for _, s := range g.Sources() {
		out = append(out, s.Name)
	}
	return out
}

// The union the design asks for: --yes is "also this, now", the config is a
// standing preference, and neither is the other's ceiling.
func TestGrantsUnionFlagAndConfig(t *testing.T) {
	g := NewGrants(map[string]bool{GrantSteps: true})
	g.SetConfig(map[string]bool{GrantWebsearch: true})
	if got := names(g); !slices.Equal(got, []string{GrantWebsearch, GrantSteps}) {
		t.Errorf("effective = %v", got)
	}
}

// The /reload requirement, and the reason the sources are kept apart: re-reading
// the config must replace what the config said and leave a decision the user
// made on the command line alone. Coder.ShellWithheld exists for the same
// reason.
func TestReloadReplacesConfigAndKeepsTheFlag(t *testing.T) {
	g := NewGrants(map[string]bool{GrantSteps: true})
	g.SetConfig(map[string]bool{GrantWebsearch: true})

	// An edited auto_approve, as ApplyConfig would hand it over.
	g.SetConfig(map[string]bool{GrantWebfetch: true})

	if g.Granted(GrantWebsearch) {
		t.Error("a config grant survived a reload that removed it")
	}
	if !g.Granted(GrantWebfetch) {
		t.Error("the reloaded config grant did not take")
	}
	if !g.Granted(GrantSteps) {
		t.Error("/reload undid --yes")
	}
}

// A session drop has to beat the flag and the config, or /yes could not revoke
// the thing you most want to revoke mid-turn.
func TestSessionDropOutranksFlagAndConfig(t *testing.T) {
	g := NewGrants(map[string]bool{GrantBash: true})
	g.SetConfig(map[string]bool{GrantWebsearch: true})

	g.Drop(GrantBash)
	g.Drop(GrantWebsearch)
	if g.Granted(GrantBash) || g.Granted(GrantWebsearch) {
		t.Error("a dropped grant is still answered automatically")
	}
	if got := g.Dropped(); !slices.Equal(got, []string{GrantBash, GrantWebsearch}) {
		t.Errorf("Dropped() = %v; the report cannot say what it turned back on", got)
	}
	g.Reset()
	if !g.Granted(GrantBash) || !g.Granted(GrantWebsearch) {
		t.Error("reset did not restore the flag and config grants")
	}
}

// Provenance is what tells the user whether editing the config will change it,
// so a name from several sources reports the most durable one.
func TestSourceReportsTheMostDurable(t *testing.T) {
	g := NewGrants(map[string]bool{GrantBash: true})
	g.SetConfig(map[string]bool{GrantBash: true})
	g.Add(GrantBash)
	if got := g.Sources(); len(got) != 1 || got[0].Source != GrantFromFlag {
		t.Errorf("sources = %+v, want one entry from %q", got, GrantFromFlag)
	}
	h := NewGrants(nil)
	h.SetConfig(map[string]bool{GrantBash: true})
	h.Add(GrantBash)
	if got := h.Sources(); len(got) != 1 || got[0].Source != GrantFromConfig {
		t.Errorf("sources = %+v, want one entry from %q", got, GrantFromConfig)
	}
}

// A nil Grants must read as "ask about everything" rather than panic: script
// mode and the tests both construct coders without one.
func TestNilGrantsAsksAboutEverything(t *testing.T) {
	var g *Grants
	if g.Granted(GrantBash) {
		t.Error("a nil Grants granted something")
	}
	if len(g.Effective()) != 0 || len(g.Sources()) != 0 {
		t.Error("a nil Grants reported grants")
	}
	// And the confirmer built from it must defer rather than answer.
	ac := AutoConfirmer{Granted: g.Effective, Fallback: nil}
	if ac.Confirm(ConfirmRequest{Grant: GrantBash}).Yes {
		t.Error("a nil Grants answered a prompt")
	}
}

// The vocabulary moved to config so auto_approve can be validated at load; the
// re-export must not drift from it.
func TestGrantNamesMatchConfig(t *testing.T) {
	if !slices.Equal(GrantNames, config.GrantNames) {
		t.Errorf("coder.GrantNames = %v, config.GrantNames = %v", GrantNames, config.GrantNames)
	}
	if GrantBash != config.GrantBash || GrantAll != config.GrantAll {
		t.Error("a re-exported grant constant drifted from its definition")
	}
}

// The bug that only running the binary could find: auto_approve was applied to
// a nil Grants and silently dropped, because ApplyConfig ran before the field
// was assigned. The nil check written to be careful is what hid it.
//
// So this asserts the property rather than the ordering: applying a config to a
// coder that has no Grants yet must still leave the grant in place.
func TestApplyConfigCreatesGrantsWhenAbsent(t *testing.T) {
	c := New(t.TempDir(), &config.Model{Slug: "m"})
	c.Grants = nil

	ApplyConfig(c, &config.Config{AutoApprove: []string{config.GrantWebsearch}})

	if c.Grants == nil {
		t.Fatal("ApplyConfig left Grants nil, so auto_approve went nowhere")
	}
	if !c.Grants.Granted(GrantWebsearch) {
		t.Error("auto_approve did not reach the session")
	}
}

// And the other order, which is what main actually does now.
func TestApplyConfigKeepsTheFlagHalf(t *testing.T) {
	c := New(t.TempDir(), &config.Model{Slug: "m"})
	c.Grants = NewGrants(map[string]bool{GrantSteps: true})

	ApplyConfig(c, &config.Config{AutoApprove: []string{config.GrantWebsearch}})

	if !c.Grants.Granted(GrantSteps) {
		t.Error("applying a config dropped a --yes grant")
	}
	if !c.Grants.Granted(GrantWebsearch) {
		t.Error("auto_approve did not reach the session")
	}
}
