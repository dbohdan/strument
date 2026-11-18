package coder

import (
	"context"
	"slices"
	"time"

	"dbohdan.com/strument/internal/llm"
	"dbohdan.com/strument/internal/skill"
)

// toolSpec is what Strument knows about one tool, in one place. A tool used to
// be described across six switches on its name — the definitions offered, the
// dispatch of a direct call, the dispatch of a program's call, the loop
// detector's exemptions, the calls that count as reads, and the tools a
// program may not call — and adding one meant finding all six, with no
// compiler to say which was missed.
type toolSpec struct {
	name string
	// def is the definition the model is offered. offered says whether this
	// session offers the tool at all; nil offers it always.
	def     func(c *Coder) llm.ToolDef
	offered func(c *Coder) bool

	// fromProgram is whether a run_code program may call the tool.
	fromProgram bool
	// observes marks the tools that look and change nothing. In a program,
	// their failure sentences become exceptions (bridgeToolFailure).
	observes bool
	// edits marks edit and write: the calls that change files, which do not
	// count toward the loop detector's read cap.
	edits bool
	// loopExempt tools may repeat a call verbatim legitimately: a command or
	// a check run again after an edit is a new question.
	loopExempt bool
	// endsTurn ends the turn once the step's results are in.
	endsTurn bool

	// answer runs a call whose result is its answer, the same way for a
	// direct call and a program's. nil for the tools a step handles in phases
	// (edit and write as one batch, then bash, then commit), which
	// applyToolCalls and runBridgedAction answer themselves.
	answer func(c *Coder, ctx context.Context, tc llm.ToolCall) toolAnswer
}

// toolAnswer is a tool's result.
type toolAnswer struct {
	text   string
	images []llm.ImageSource
	// badArgs means the arguments could not be used and nothing ran: a
	// reflection for a direct call, an exception in a program.
	badArgs bool
}

// withArgs answers a tool that parses its arguments and then runs on them.
func withArgs[A any](parse func(llm.ToolCall) (A, string),
	run func(c *Coder, ctx context.Context, a A) string,
) func(*Coder, context.Context, llm.ToolCall) toolAnswer {
	return func(c *Coder, ctx context.Context, tc llm.ToolCall) toolAnswer {
		a, msg := parse(tc)
		if msg != "" {
			return toolAnswer{text: msg, badArgs: true}
		}
		return toolAnswer{text: run(c, ctx, a)}
	}
}

// observe answers an observation tool through the Inspector, as the REPL and
// `strument tool` do, so what the model is shown is what they show. read is
// the one whose answer can carry images, so it does not go through the
// text-shaped Run.
func (c *Coder) observe(tc llm.ToolCall) toolAnswer {
	if tc.Name == toolRead {
		text, images := c.runRead(tc)
		return toolAnswer{text: text, images: images}
	}
	return toolAnswer{text: c.inspector().Run(tc.Name, tc.Arguments)}
}

// observing is observe in the table's shape.
var observing = func(c *Coder, _ context.Context, tc llm.ToolCall) toolAnswer { return c.observe(tc) }

// namedDef is the definition called name from a list that defines several.
func namedDef(defs []llm.ToolDef, name string) llm.ToolDef {
	return defs[slices.IndexFunc(defs, func(d llm.ToolDef) bool { return d.Name == name })]
}

// tools is every tool, in the order they are offered. The order is the cached
// prefix of every request, so moving a tool moves every tool after it, and
// testdata/model-facing.golden shows the move.
//
// Set in init rather than as an initializer, because run_code's definition
// lists the tools a program may call, which reads this table.
var (
	tools      []toolSpec
	toolByName map[string]*toolSpec
)

func init() {
	readOnly := func(name string) func(*Coder) llm.ToolDef {
		return func(*Coder) llm.ToolDef { return namedDef(readOnlyTools(), name) }
	}
	editing := func(name string) func(*Coder) llm.ToolDef {
		return func(*Coder) llm.ToolDef { return namedDef(editTools(), name) }
	}
	tools = []toolSpec{
		{name: toolRead, def: readOnly(toolRead), fromProgram: true, observes: true, answer: observing},
		{name: toolGrep, def: readOnly(toolGrep), fromProgram: true, observes: true, answer: observing},
		{name: toolGlob, def: readOnly(toolGlob), fromProgram: true, observes: true, answer: observing},
		{name: toolLS, def: readOnly(toolLS), fromProgram: true, observes: true, answer: observing},
		{
			// A question's answer is for the model to think about, not for a
			// program to compute over.
			name: toolAskUser, def: func(*Coder) llm.ToolDef { return askTool() },
			answer: func(c *Coder, _ context.Context, tc llm.ToolCall) toolAnswer {
				// Not routed through ConfirmGrouped: a question is not a
				// permission prompt, and --yes must not answer it.
				bad := false
				text := c.runAskUser(tc, &bad)
				return toolAnswer{text: text, badArgs: bad}
			},
		},
		{
			// A program returns rather than ending the turn.
			name: toolInterrupt, def: func(*Coder) llm.ToolDef { return interruptTool() }, endsTurn: true,
			answer: func(*Coder, context.Context, llm.ToolCall) toolAnswer {
				// Every call is answered so the wire stays well-formed.
				return toolAnswer{text: "The turn has ended."}
			},
		},
		{
			// about answers what the model cannot find out by looking.
			name: toolAbout, def: func(*Coder) llm.ToolDef { return aboutTool() }, fromProgram: true,
			answer: func(c *Coder, _ context.Context, _ llm.ToolCall) toolAnswer {
				c.Out.Toolf("About Strument")
				return toolAnswer{text: c.About(time.Now())}
			},
		},
		{
			// recall reads the session record, which mutates nothing; offered
			// from the first turn rather than from the first compaction,
			// because a tool list that changes mid-session confuses models and
			// breaks the cached prefix.
			name: toolRecall, def: func(*Coder) llm.ToolDef { return recallTool() },
			offered: (*Coder).offersRecall, fromProgram: true,
			answer: withArgs(parseRecallArgs, func(c *Coder, _ context.Context, a recallArgs) string {
				c.Out.Toolf("%s", recallAnnouncement(a))
				return c.runRecall(a)
			}),
		},
		{
			// symbol reads the same tree-sitter layer the repo map is built
			// from, so it is offered exactly when that layer is available.
			name: toolSymbol, def: func(*Coder) llm.ToolDef { return symbolTool() },
			offered: func(c *Coder) bool { return c.RepoMap != nil }, fromProgram: true, observes: true,
			answer: observing,
		},
		{
			// The port is always set in the binary, so this is a guard for a
			// directly built Coder, not a setting a user can be on the wrong
			// side of.
			name: toolWebfetch, def: func(*Coder) llm.ToolDef { return webfetchTool() },
			offered: func(c *Coder) bool { return c.Scrape != nil }, fromProgram: true,
			answer: withArgs(parseFetchArgs, (*Coder).runWebfetch),
		},
		{
			// Genuinely conditional, unlike webfetch: with no configured
			// instance there is no search to offer.
			name: toolWebsearch, def: func(*Coder) llm.ToolDef { return websearchTool() },
			offered: func(c *Coder) bool { return c.Search != nil }, fromProgram: true,
			answer: withArgs(parseSearchArgs, (*Coder).runWebsearch),
		},
		{
			// Not from a program: each call spends money at another model's
			// price, and a loop would make one approval cover fifty.
			name: toolConsult, def: func(c *Coder) llm.ToolDef { return consultTool(c.Consult) },
			offered: func(c *Coder) bool { return len(c.Consult) > 0 },
			answer:  withArgs(parseConsultArgs, (*Coder).runConsult),
		},
		{
			// Offered only when there is something to name, because the enum
			// would otherwise be empty and the description would list nothing.
			name: toolSkill, def: func(c *Coder) llm.ToolDef { return skillTool(c.Skills) },
			offered: func(c *Coder) bool { return len(skill.Usable(c.Skills)) > 0 }, fromProgram: true,
			answer: withArgs(parseSkillArgs, (*Coder).runSkill),
		},
		{
			// Ahead of edit, where it has always been. A program starting a
			// program is not a thing.
			name: toolRunCode,
			def: func(c *Coder) llm.ToolDef {
				return codeTool(c.codeCallableTools(), c.offersShell(), c.dataFuncs())
			},
			answer: withArgs(parseCodeArgs, (*Coder).runCode),
		},
		{name: toolEdit, def: editing(toolEdit), fromProgram: true, edits: true},
		{name: toolWrite, def: editing(toolWrite), fromProgram: true, edits: true},
		{
			name: toolBash, def: func(*Coder) llm.ToolDef { return bashTool() },
			offered: (*Coder).offersShell, fromProgram: true, loopExempt: true,
		},
		{
			name: toolCheck, def: func(c *Coder) llm.ToolDef { return checkTool(c.Check) },
			offered: func(c *Coder) bool { return len(c.Check) > 0 }, fromProgram: true, loopExempt: true,
			answer: func(c *Coder, ctx context.Context, tc llm.ToolCall) toolAnswer {
				return toolAnswer{text: c.runCheckTool(ctx, tc)}
			},
		},
		{
			// Offered whenever editing is, including where it will decline: a
			// session without git, or with commits off, answers the call with
			// the reason rather than hiding the tool. Withholding it would
			// leave a model that wanted a commit boundary with no way to find
			// out why it could not have one, which is how the plan it wrote
			// silently became one commit.
			name: toolCommit, def: func(*Coder) llm.ToolDef { return commitTool() },
			fromProgram: true, loopExempt: true,
		},
	}
	toolByName = make(map[string]*toolSpec, len(tools))
	for i := range tools {
		toolByName[tools[i].name] = &tools[i]
	}
}

// offeredTools is the tools this session offers, in order.
func (c *Coder) offeredTools() []*toolSpec {
	var out []*toolSpec
	for i := range tools {
		if s := &tools[i]; s.offered == nil || s.offered(c) {
			out = append(out, s)
		}
	}
	return out
}

// toolDefs is the tool set offered to the model.
//
// One tool set for every turn. There used to be an ask mode that withheld the
// mutating tools; it went because a tool set that changes under the model
// confuses it, and a user who wants discussion can say so in the message.
// What is still conditional is what depends on configuration: bash, check,
// the parser, a scraper, search, skills, models to consult.
func (c *Coder) toolDefs() []llm.ToolDef {
	specs := c.offeredTools()
	defs := make([]llm.ToolDef, 0, len(specs))
	for _, s := range specs {
		defs = append(defs, s.def(c))
	}
	return defs
}

// codeCallableTools is the one list behind the run_code description, the
// registration, and the bridge's allow check: the offered tools a program may
// call, in their order. It follows what is offered by construction, so symbol
// is callable exactly where the parser is, bash exactly where the shell is,
// and so on; a program can never call a tool the session does not offer, and
// never miss one it does.
func (c *Coder) codeCallableTools() []string {
	var out []string
	for _, s := range c.offeredTools() {
		if s.fromProgram {
			out = append(out, s.name)
		}
	}
	return out
}
