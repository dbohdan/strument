package repl

import (
	"slices"
	"strings"
	"testing"

	"dbohdan.com/strument/internal/fixture"
	"dbohdan.com/strument/internal/history"
)

// TestSubcommandCompletion: commands with subcommands complete the subcommand,
// and then keep completing at the next stage — a session name after
// /session switch, a scope after /consult scope, a grant after /yes add.
func TestSubcommandCompletion(t *testing.T) {
	r, cdr, _ := newTestREPL(t, &fixture.StreamStub{}, strings.NewReader("/exit\n"))
	defer r.Close()
	r.opts.Sessions = &SessionOps{List: func() ([]history.Session, error) {
		return []history.Session{{Name: "main", Current: true}, {Name: "spike"}, {Name: "spike-2"}}, nil
	}}
	cdr.Grants = nil

	for _, tc := range []struct {
		line string
		want []string
	}{
		{"/session ", []string{"new", "switch", "fork", "rename", "delete"}},
		{"/session sw", []string{"itch"}},
		// Switching to or deleting the current session is refused, so it is
		// not offered; renaming it is allowed.
		{"/session switch ", []string{"spike", "spike-2"}},
		{"/session delete sp", []string{"ike", "ike-2"}},
		{"/session rename ", []string{"main", "spike", "spike-2"}},
		{"/session new ", nil},
		{"/notes ", []string{"generate", "drop"}},
		{"/commits o", []string{"n", "ff"}},
		{"/consult scope ", []string{"none", "files", "chat"}},
		{"/yes ", []string{"add", "drop", "reset"}},
		{"/yes add b", []string{"ash"}},
		{"/yes add bash ", []string{"bash", "webfetch", "websearch", "steps", "context", "add-output", "all"}},
	} {
		got := completionsFor(r.completer(), tc.line)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q = %q, want %q", tc.line, got, tc.want)
		}
	}
}
