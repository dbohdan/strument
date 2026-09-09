package repl

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dbohdan.com/strument/internal/coder"
)

// cmdConsult asks another model a question and offers to put its answer in the
// chat, labelled as that model's.
//
// The label is the whole point, and it is why this is not /model. Switching the
// active model mid-conversation puts the new model's reply into the history as
// an *assistant* turn, so on the next turn the original model reads it as
// something it said itself. An opinion the session cannot tell from its own
// memory is not a second opinion.
//
// The shape follows /run and /check rather than /btw: the answer streams, and
// then the user decides whether it becomes context. Asking is what keeps the
// turn boundary the human's — an advisor the model could summon would spend the
// user's money on its own judgment about whether it is stuck.
func cmdConsult(ctx context.Context, r *REPL, args string) string {
	if r.opts.Config == nil {
		r.out.Errorf("No configuration loaded; /consult is unavailable.")
		return ""
	}

	alias, question, _ := strings.Cut(args, " ")
	alias = strings.TrimSpace(alias)
	question = strings.TrimSpace(question)
	if alias == "" || question == "" {
		r.out.Errorf("%s", usage("consult"))
		return ""
	}

	m, ok := r.opts.Config.Models[alias]
	if !ok {
		// The same sentence /model answers a bad alias with, because it is the
		// same mistake and the aliases are the same closed set. It is also what
		// a question typed without an alias — `/consult how do I …` — lands on,
		// which is the case for naming the advisor positionally: the error can
		// say what was expected.
		aliases := slices.Sorted(maps.Keys(r.opts.Config.Models))
		r.out.Errorf("Unknown model alias %q (aliases: %s).", alias, strings.Join(aliases, ", "))
		return ""
	}

	// A client for the advisor's provider. Falling back to the session's client
	// keeps the tests' stubs working and is right whenever the two models share
	// a provider; MakeClient is always set in the binary.
	client := r.coder.Client
	if r.opts.MakeClient != nil {
		client = r.opts.MakeClient(m)
	}

	answer := r.withinTurn(ctx, m.QualifiedSlug(), func(tctx context.Context) string {
		return r.coder.RunConsult(tctx, client, m, question, r.opts.ConsultScope)
	})
	if strings.TrimSpace(answer) == "" {
		return "" // nothing came back; there is nothing to offer
	}

	// Through the coder rather than r.Confirmer() so --yes add-output reaches
	// this prompt; calling the REPL's confirmer directly, which is what /run and
	// /check used to do, never consults the grant.
	//
	// No Group, and so no "a = all turn" on offer. There is no turn here to scope
	// an answer to: /consult, /run and /check are typed at the prompt, between
	// turns. The scope was borrowed from the model-caused prompts, where "this
	// turn" bounds something real, and measured it reached until the user's next
	// *message* and across all three commands — an "a" here silently added the
	// next /run's output too. See ConfirmRequest.Group.
	if r.coder.ConfirmGrouped(coder.ConfirmRequest{
		Prompt: "Add the answer to the chat?",
		Grant:  coder.GrantAddOutput,
	}) {
		r.coder.AppendContext(consultLabel(alias, m.QualifiedSlug(), question, answer))
		r.printf("Added %s's answer to the chat.", alias)
	}
	return ""
}

// consultLabel wraps the exchange in its provenance.
//
// Not llm.HarnessNote: the [strument] marker means the harness is speaking, and
// this is material the user brought in — the distinction AppendContext exists to
// keep. It follows /skill's shape, a sentence saying where the text came from
// and then the text, with the model named twice so a reader arriving at the
// answer alone still has the attribution.
func consultLabel(alias, slug, question, answer string) string {
	return fmt.Sprintf(
		"The user asked %s (%s) for a second opinion. The answer below was written by that model, not by you.\n\n"+
			"The question:\n%s\n\n%s's answer:\n%s",
		alias, slug, question, alias, strings.TrimRight(answer, "\n"))
}
