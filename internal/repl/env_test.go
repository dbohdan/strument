package repl

import (
	"context"
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/coder"
)

// runEnv drives /env lines through a scripted session and returns the output.
func runEnv(t *testing.T, lines string) (*REPL, *coder.Coder, string) {
	t.Helper()
	r, cdr, out := newTestREPL(t, answerStub("ok\n"), strings.NewReader(lines))
	t.Cleanup(func() { r.Close() })
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return r, cdr, out.String()
}

func TestEnvShowAddDropReset(t *testing.T) {
	t.Setenv("STRUMENT_TEST_A", "1")
	t.Setenv("MY_SERVICE_TOKEN", "2")
	t.Setenv("LC_ALL", "C.UTF-8")

	// The state assertions are made mid-session (before reset undoes them) by
	// ending that script with /exit; each runEnv is one complete session.
	_, afterAdd, addOut := runEnv(t,
		"/env\n"+
			"/env add STRUMENT_TEST_A MY_SERVICE_TOKEN HF_MISSING\n"+
			"/env\n"+
			"/exit\n")

	// The bare display shows the set defaults only, without values. LC_ALL is
	// set above via t.Setenv; MY_SERVICE_TOKEN is not a default, and no "*"
	// remains now that matching is exact everywhere.
	if !strings.Contains(addOut, "default  PATH") || !strings.Contains(addOut, " LC_ALL") || strings.Contains(addOut, "*") {
		t.Errorf("default list not shown:\n%s", addOut)
	}
	// Not every default is set (GOAMD64, LC_PAPER, …), so the truncated list
	// ends with an ellipsis rather than implying completeness.
	if !strings.Contains(addOut, " ...") {
		t.Errorf("ellipsis missing from the default list:\n%s", addOut)
	}
	if !strings.Contains(addOut, "config   (none)") {
		t.Errorf("empty config group not shown:\n%s", addOut)
	}

	// add: set names land on the coder's effective list (the thing every
	// FilterEnv call site reads), a credential-shaped one draws a notice, and
	// an unset one is rejected with an error.
	if !strings.Contains(addOut, "MY_SERVICE_TOKEN looks like a credential") {
		t.Errorf("credential notice missing:\n%s", addOut)
	}
	if !strings.Contains(addOut, "HF_MISSING is not set in the environment and cannot be added") {
		t.Errorf("not-set error missing:\n%s", addOut)
	}
	if !containsName(afterAdd.EnvAllow, "STRUMENT_TEST_A") ||
		!containsName(afterAdd.EnvAllow, "MY_SERVICE_TOKEN") {
		t.Errorf("after add, EnvAllow = %v", afterAdd.EnvAllow)
	}
	if containsName(afterAdd.EnvAllow, "HF_MISSING") {
		t.Errorf("HF_MISSING should not be in EnvAllow: %v", afterAdd.EnvAllow)
	}
	if !strings.Contains(addOut, "session  + STRUMENT_TEST_A") {
		t.Errorf("session add not displayed:\n%s", addOut)
	}

	// TestEnvAddThenShowDoesNotPrintDropLines: /env add used to leave the name
	// keyed (as false) in envDropped, and the display iterated keys, so every
	// add printed both a "+" and a "−" line. Add followed by show pins that.
	_, _, showOut := runEnv(t,
		"/env add STRUMENT_TEST_A\n"+
			"/env\n"+
			"/exit\n")
	if !strings.Contains(showOut, "session  + STRUMENT_TEST_A") {
		t.Errorf("session add not displayed:\n%s", showOut)
	}
	if strings.Contains(showOut, "session  − STRUMENT_TEST_A") {
		t.Errorf("an add must not also print a drop line:\n%s", showOut)
	}

	_, afterDrop, dropOut := runEnv(t, "/env drop STRUMENT_TEST_A PATH\n/exit\n")
	// drop: honored without asking, and dropping PATH warns rather than asks.
	if !strings.Contains(dropOut, "without PATH") {
		t.Errorf("PATH warning missing:\n%s", dropOut)
	}
	if containsName(afterDrop.EnvAllow, "STRUMENT_TEST_A") {
		t.Errorf("after drop, EnvAllow = %v", afterDrop.EnvAllow)
	}

	_, afterReset, _ := runEnv(t,
		"/env add STRUMENT_TEST_A\n"+
			"/env reset\n"+
			"/exit\n")
	if containsName(afterReset.EnvAllow, "STRUMENT_TEST_A") {
		t.Errorf("after reset, EnvAllow = %v", afterReset.EnvAllow)
	}
}

// TestEnvDropDropsConfigEntriesToo: dropping works on the config's entries,
// not only session adds — that is the fix-it-now path for a misconfigured
// env_allow.
func TestEnvDropDropsConfigEntriesToo(t *testing.T) {
	r, cdr, out := newTestREPL(t, answerStub("ok\n"), strings.NewReader(
		"/env drop CFG_ONLY\n/env\n/exit\n"))
	r.opts.Config.EnvAllow = []string{"CFG_ONLY"}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if containsName(cdr.EnvAllow, "CFG_ONLY") {
		t.Errorf("a config entry survived /env drop: %v", cdr.EnvAllow)
	}
	if !strings.Contains(out.String(), "(dropped this session)") {
		t.Errorf("the display should show the drop:\n%s", out.String())
	}
}

func TestEnvValidatesNames(t *testing.T) {
	_, cdr, out := runEnv(t,
		"/env add FOO=bar\n"+
			"/env add \"\"\n"+
			"/env drop FOO=bar\n"+
			"/env nonsense\n"+
			"/exit\n")

	if !strings.Contains(out, `"FOO=bar" is not an environment variable name`) {
		t.Errorf("NAME=VALUE rejection missing:\n%s", out)
	}
	if !strings.Contains(out, "Usage: /env [add NAME... | drop NAME... | reset]") {
		t.Errorf("unknown subcommand message missing:\n%s", out)
	}
	if len(cdr.EnvAllow) != 0 {
		t.Errorf("invalid names must not be added; EnvAllow = %v", cdr.EnvAllow)
	}
}

// TestEnvCompletionPinsWhatTabOffers: add completes set variables the
// allowlist does not pass, drop completes what it does. The interesting
// direction is add's — a secret appearing there is correct (it is what the
// user may choose to expose), while one appearing in drop's set after no
// explicit allow would be a leak of the default list's bounds.
func TestEnvCompletionPinsWhatTabOffers(t *testing.T) {
	t.Setenv("STRUMENT_TEST_SECRET", "x")
	t.Setenv("STRUMENT_TEST_PLAIN", "y")

	r, _, _ := runEnv(t, "/exit\n")
	comps := r.completer()

	addNames := completionsFor(comps, "/env add ")
	if !containsName(addNames, "STRUMENT_TEST_SECRET") {
		t.Errorf("add must offer not-yet-allowed set variables: %v", addNames)
	}
	if containsName(addNames, "PATH") {
		t.Errorf("add must not offer what the defaults already pass: %v", addNames)
	}

	dropNames := completionsFor(comps, "/env drop ")
	if !containsName(dropNames, "PATH") {
		t.Errorf("drop must offer allowed variables: %v", dropNames)
	}
	if containsName(dropNames, "STRUMENT_TEST_SECRET") {
		t.Errorf("drop must not offer withheld secrets: %v", dropNames)
	}
}

// completionsFor runs the prefix completer on a partial line and returns the
// offered words.
func completionsFor(c interface {
	Do(line []rune, pos int) (newLine [][]rune, offset int)
}, line string) []string {
	newLine, _ := c.Do([]rune(line), len(line))
	seen := map[string]bool{}
	out := make([]string, 0, len(newLine))
	for _, w := range newLine {
		s := strings.TrimSpace(string(w))
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// TestEnvAllowTakesEffectAtTheRunnerSeam: what /env add changes is not just
// REPL state — a model-run block through the real interpreter sees the added
// variable and still does not see a withheld one. Observed by running env,
// not by re-deriving the merge.
func TestEnvAllowTakesEffectAtTheRunnerSeam(t *testing.T) {
	t.Setenv("STRUMENT_TEST_ADDED", "visible")
	t.Setenv("STRUMENT_TEST_SECRET", "hidden")

	_, cdr, _ := runEnv(t, "/env add STRUMENT_TEST_ADDED\n/exit\n")
	cdr.Runner = nil

	_, out, err := coder.PipeRunner{Env: coder.FilterEnv(nil, cdr.EnvAllow)}.
		Run(context.Background(), "env", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "STRUMENT_TEST_ADDED=visible") {
		t.Errorf("the /env addition did not reach the model-run block:\n%s", out)
	}
	if strings.Contains(out, "STRUMENT_TEST_SECRET") {
		t.Errorf("a withheld variable reached the model-run block:\n%s", out)
	}
}

func containsName(list []string, name string) bool { return slices.Contains(list, name) }
