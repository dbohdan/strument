// Package prompts holds the prompt sets for each edit format.
//
// The prompt strings are Python-str.format-style templates: placeholders
// like {fence[0]}, {final_reminders}, and {platform} are substituted by
// the coder at message-assembly time. Literal braces inside prompt text
// must therefore be doubled ({{ and }}).
package prompts

import "fmt"

// Set is one edit format's prompt set: the CoderPrompts surface that the
// message assembler consumes.
type Set struct {
	MainSystem       string
	SystemReminder   string
	FilesNoFullFiles string
	// FilesNoFullFilesNoCode is the same note with the run_code clause
	// removed, for a session whose schema withholds the tool.
	FilesNoFullFilesNoCode           string
	FilesNoFullFilesWithRepoMap      string
	FilesNoFullFilesWithRepoMapReply string
	ReadOnlyFilesPrefix              string
}

// Shared strings used identically across the editing formats.

// readOnlyFilesPrefix speaks as the harness, which is what it is. It replaces
// aider's "Here are some read-only files, provided for your reference. Do not
// propose edits to these files." — a user turn the user never wrote, followed
// by a fabricated assistant reply agreeing to it.
//
// Two things it must get right, both learned from live sessions
// (doc/experiments/2026-08-readonly-honest/README.md):
//
// Say where the contents come from, because a reference pinned from outside the
// project cannot be found with glob, ls, or grep, and a model that goes looking
// for the "real" file finds nothing and starts theorising. The hedge is
// deliberate: a reference inside the project is the common case and is findable.
//
// Say that an edit is refused, not that edits are unwelcome. "Do not propose
// edits" reads as a preference the task can override, and when a request asked
// for one anyway, models spent whole turns litigating the contradiction. Naming
// the enforcement ends it: they say they cannot, and do the rest of the work.
const readOnlyFilesPrefix = "The user pinned these files as read-only reference, and their contents " +
	"follow.\n" +
	"Some may live outside the project, where glob, ls, and grep will not find them, so treat " +
	"what follows as the copy you have.\n" +
	"Do not edit them: an edit to a pinned reference is refused.\n"

const lazyPrompt = "Implement requested changes completely.\n" +
	"Never leave placeholder comments (like \"... rest of code ...\" or \"implement this later\") " +
	"in place of real code.\n"

// overeagerPrompt is aider's scope block with one clause added, and the reason
// it is worth a comment is that the clause was measured rather than reasoned.
//
// As inherited it was the only block in the tool prompt with no positive
// counterpart: one attend-to, one "do no more", four named bans. That cannot
// distinguish in-scope work the user did not enumerate — the call sites a rename
// breaks, the test that covers the function — from out-of-scope drive-by work,
// so it bans both by implication. Strument's founding regression case was a
// model asked to change a function and its separately-stored test, and Claude
// Haiku would compute the stale assertion, write "2.999 rounds to 300 (not 299)"
// in its own summary, and leave the test file untouched.
//
// Randomised A/B over three models, 90 runs per arm: test-file updates rose from
// 76/90 to 87/90 (CMH p=0.011) with zero drive-by edits in all 180 runs. The
// wording here is the arm that was measured, kept verbatim. "the docs that
// describe it" is part of it: doc edits alone did not move (4/90 vs 6/90), but
// nothing says which third of the clause carries the effect, and trimming a
// measured string to ship an unmeasured one is not an improvement.
// doc/experiments/2026-08-prompt-scope/README.md has the design and the four
// predictions it falsified.
//
// The last sentence is NOT part of that arm and is not measured. It answers a
// welfare finding from the multi-model review: four named bans in a row, with
// no positive framing, read to GLM-5.3 as "you are the kind of agent that does
// these things unless warned". "whoever wrote it" is the load-bearing phrase —
// it makes the rule a fact about diffs rather than a claim about the reader —
// and the second clause gives the impulse somewhere to go, which nothing here
// previously did.
//
// It was added after the measured sentence, not inside it, so the arm survives
// intact. doc/experiments/2026-08-welfare-wording/README.md has both trials. Drive-by
// edits stayed 0/150 in each arm against the original's 0/180 baseline, and
// follow-through came out 86/150 against 78/150 -- a 5.3-point difference with
// a 95% interval of [-5.9, +16.6] and p=0.42. So a regression larger than about
// six points is excluded and a smaller one is not, which is weaker than "the
// effect survived" and stronger than "we did not check". The added sentence is
// still not part of the measured arm; do not cite it as one.
const overeagerPrompt = "Pay careful attention to the scope of the user's request.\n" +
	"Carry the change through everywhere it reaches: the call sites it breaks, the tests that " +
	"cover it, the docs that describe it. That is the same request, not extra work.\n" +
	"Leave everything else untouched: no drive-by refactoring, reformatting, added comments, " +
	"or fixes to things the user didn't ask about.\n" +
	"A diff that does more than was asked is harder to review, whoever wrote it; if you notice " +
	"something worth changing, say so instead.\n"

// Two things here were found by a multi-model review of the rendered prompts
// (doc/experiments/2026-08-prompt-review/README.md), and both were fossils.
//
// "The user will request changes to the supplied code" dated from the harness
// where /add put file contents in the prompt. Nothing is supplied now — the
// model reads what it needs — and the categorical "will request changes" biased
// a question ("why is startup slow?") toward being treated as a prelude to
// editing. FilesNoFullFiles already patched that for the nothing-pinned case;
// the opening now says it for every case.
//
// The tool list said "three groups" and named four. Restoring the count would
// only be right until the next tool: the list is already not exhaustive —
// symbol appears when a grammar is available, check when the project configures
// one, ask_user_question always — and the schema is what actually enumerates
// them. So the sentence no longer counts, and says what it is instead: the ones
// reached for most, distinguished by cost.
//
// "Finish by saying what you did" is the same fossil a third time: it
// presupposes a deed, and the nothing-pinned case this prompt also serves ends
// in an answer. A reviewer predicted the shape it produces — "I ran grep and
// read main.go; startup is slow because …", the narration ahead of the answer.
// "or what you found" is four words against that.

// CodeToolsBullet is the {code_tools} slot's text, exported for the coder's
// assembler, which fills the slot when (and only when) the run_code tool is
// offered. It lives beside the prompt it extends because it is prompt prose;
// the *condition* lives in the coder, derived from the tool set itself.
//
// The tool list is the closed-world hazard the code-mode trial measured: the
// sentence "these are the ones you will reach for most" reads as policy, and a
// flash-class model treats the enumeration as exhaustive —
// doc/experiments/2026-08-code-mode/README.md found 0/36 `run_code` calls with the tool
// offered in the schema but absent from this list. So `run_code` gets a bullet
// through the {code_tools} slot, filled at assembly time only when the tool is
// actually offered (the same mechanism {platform} uses), so the prose can
// never promise a tool the schema withholds — the rule that kept `run_code` out
// of the prompt when it was first added. The bullet is a conditional rule
// ("when one answer needs several lookups combined"), not an announcement:
// per the symbol fix, the description that moves uptake is the one that maps
// a felt need to a tool, and the arithmetic clause is explicit because models
// do not recognize their own mental arithmetic as costly.
//
// The tool list is a %s slot rather than four names written out, because symbol
// is offered only where a repo map is. Naming it unconditionally would promise a
// tool half the sessions do not have — the hazard this file's Ask comment calls
// out — and leaving it out unconditionally hid a tool a program really can call
// wherever grammars exist. Coder.codeCallableTools fills it from the same list
// the schema and the bridge use.
//
// The "inside the harness" clause is the bullet's half of codeCommandsClause's
// fix (see its comment): in a JavaScript project the bullet's first words were
// read as an offer to run the project's code, and the bullet is read first.
const CodeToolsBullet = "- run_code runs a short JavaScript program, which can itself call %s" +
	" — the results come back to the program, not to you, until it returns. " +
	"It changes nothing and needs no permission, and it runs inside the harness, " +
	"not the project: it cannot import or run the project's own code. Reach for it when one answer " +
	"needs several lookups combined — three greps you already know you need are " +
	"one program, and each tool call you make separately costs the user a full " +
	"round trip — and for any arithmetic or counting beyond a single step, " +
	"rather than working figures out in your head. A single lookup, or " +
	"exploration where each search depends on the last result, is a direct call " +
	"to the tool itself.\n"

// ObservationBullet is the tool prompt's read/grep/glob/ls paragraph.
const ObservationBullet = "- read, grep, glob, and ls look at the project. They change nothing and need no permission, " +
	"so use them freely rather than guessing at a file's contents or at how the project works. " +
	"Files the project ignores are " +
	"not listed or searched. Paths are relative to the project root; an absolute path that lies " +
	"inside the project is also accepted.\n"

// {code_tools} renders the code bullet above when the run_code tool is
// offered, and as "" when it is not — the empty case is the feature-reverted
// baseline, where prose naming `run_code` would promise a tool the schema does
// not carry. {observation_tools} renders the observation paragraph for the
// session's mode.
const toolMainSystem = "You are an expert software developer working with a user on their codebase.\n" +
	"Follow the conventions, style, and libraries already present in the codebase.\n" +
	overeagerPrompt +
	lazyPrompt +
	"{final_reminders}\n" +
	"The user will ask for changes to the code, or ask questions about it.\n" +
	"If a request is ambiguous, ask clarifying questions before making changes.\n\n" +
	"Work through the provided tools. These are the ones you will reach for most, and they differ " +
	"in what they cost the user:\n\n" +
	"{code_tools}" +
	"{observation_tools}" +
	"- edit and write change files directly: the change lands the moment you call them, with no " +
	"separate confirmation step, exactly like an ordinary edit. Call them when you are ready to " +
	"make the change.\n" +
	"- bash runs a shell command, and the user is asked before it runs. Reach for it only for work " +
	"the other tools don't cover; reading and searching have their own tools.\n" +
	"- commit closes the edits you have made so far as one commit, with a message you write. " +
	"When a request has several parts, work through them one at a time: make the edits for the " +
	"first part, call commit, and begin the second part after that result comes back. Do not " +
	"make every edit and then commit once at the end — by then there is only one commit to " +
	"make, and the user is reading a diff that does three things at once. A request that is " +
	"genuinely one change is one commit, and needs no call at all.\n\n" +
	"Every call's result comes back to you, so you can keep working within the same turn: read a " +
	"file, make an edit, run the tests, see what failed, and fix it. Finish by saying what you did " +
	"or what you found, without calling a tool — that is what ends the turn and hands back to the " +
	"user.\n\n" +
	HarnessNoteLine +
	"Explain your changes briefly in prose alongside the tool calls.\n\n" +
	"Keep in mind these details about the user's platform and environment:\n" +
	"{platform}\n"

// HarnessNoteLine says what the [strument] marker means. Strument speaks to the
// model mid-conversation in user-role messages (see llm.HarnessNote), and
// without this the model has only the marker to guess from. In
// doc/experiments/2026-09-note-attribution, marking the new-files note cut
// the sessions whose reasoning put the note's words in the user's mouth from
// 4/39 to 1/39, and marking it and defining the marker with this sentence to
// 0/39, with no change in whether the note was acted on. Small numbers, one
// direction; the project's rule that the harness speaks marked decided it.
const HarnessNoteLine = "Messages that begin with [strument] are written by Strument, the program " +
	"running this session, not by the user.\n\n"

// toolSystemReminder is the trailing reminder for the tool format: the
// exact-match rule for search and the one-change-per-call discipline.
const toolSystemReminder = "# Editing rules\n\n" +
	"- edit's old_string must match the file's current contents exactly, character for character, " +
	"including all whitespace, comments, and docstrings. An inexact match is the most common reason " +
	"an edit is rejected, so double-check it.\n" +
	"- Include enough surrounding lines in old_string to identify the location uniquely, and keep " +
	"each call to one small, self-contained change. Use several calls for several changes.\n" +
	"- To move code, use two calls: one to remove it, one to add it in the new place.\n" +
	"- Read a file before editing it unless its contents are already in the conversation. Editing " +
	"from memory is where inexact matches come from.\n\n" +
	"{final_reminders}"

// Tool is the tool-calling edit format: the model edits, suggests commands,
// and requests files through native function calls instead of text blocks.
// It is the default format. The schema does the format-parsing work, so the
// prompt only conveys the tools' natures and the exact-match discipline.
var Tool = Set{
	MainSystem:     toolMainSystem,
	SystemReminder: toolSystemReminder,
	// Nothing pinned is the normal starting state now, not a problem to report.
	// The model finds what it needs with read, grep, glob, and ls, so saying so
	// is the whole message.
	//
	// The second sentence is here because the repo map used to be, in this exact
	// position. The map was not being read as content; it was evidence that a
	// project existed to look at, and removing it left three of ten models
	// answering a question about this codebase without opening a file — one of
	// them inventing a whole subsystem. This says the same thing for thirty
	// tokens instead of a thousand. It is spelled out for questions because
	// everything else the model is told is about making changes, and a question
	// falls outside that contract. It says "the user asks" rather than aider's
	// "I ask" because this text lands in the system prompt now, where a
	// first-person "I" is the harness wearing the user's voice.
	//
	// "Nothing is pinned for editing" rather than "no files are pinned": a
	// read-only reference is pinned and its contents are right there in the
	// request, and a model handed both read the flat denial as evidence that the
	// reference block was not to be trusted. One session spent twelve steps
	// hunting the project for the "real" file it thought the denial implied.
	FilesNoFullFiles:       toolNoPinsHead + runCodeClause + toolNoPinsTail,
	FilesNoFullFilesNoCode: toolNoPinsHead + toolNoPinsTail,
	// The empty-string sentinel disables the repo-map branch in assembly. Its text was written for a harness where the model could not look:
	// it told the model to name the files it needed and stop so the user could
	// add them, which is precisely the behavior the tool set replaces. Left in
	// place it made the model explore and then refuse to edit.
	FilesNoFullFilesWithRepoMap:      "",
	FilesNoFullFilesWithRepoMapReply: "",
	ReadOnlyFilesPrefix:              readOnlyFilesPrefix,
}

// runCodeClause is the half-sentence that offers a program as an alternative
// to a run of direct lookups. It is spliced into the nothing-pinned notes
// rather than written twice, so the offered text stays byte-identical to what
// 2026-09-code-mode2.md measured and the withheld text is provably the same
// sentence without it.
//
// Two hand-written variants would be two places to drift, which is the failure
// this repository has recorded three times for absolute paths (see
// internal/coder/apply.go) and once for the loop detector's strip.
const runCodeClause = " — or one run_code call that calls them, when you " +
	"already know the several things you are looking for"

// The tool set's nothing-pinned note, either side of the clause.
const (
	toolNoPinsHead = "Nothing is pinned for editing. Use read, grep, glob, and ls to find " +
		"what you need"
	toolNoPinsTail = ". You can edit any file in " +
		"the project. If the user asks how something here works, read the code " +
		"that implements it: what you remember about a project is not evidence " +
		"about this one."
)

// RollLabelStem begins every note that stands for turns dropped by rolling
// the history (coder/roll.go), which is how a later roll recognises one.
const RollLabelStem = "Earlier turns of this session were dropped to keep it inside the context window."

// RollLabel introduces the user's messages from the dropped turns. They are
// quoted, not summarized: nothing in the note was written by a model. What
// the turns did is gone, and the label says so, so that the reader looks
// rather than assumes — the calibration line in SummaryLabel, for the same
// reason.
const RollLabel = RollLabelStem + " What the user said in them is kept below, word for word, " +
	"oldest first. The work those turns did is not kept: where it matters, check the files " +
	"or run the commands again."

// RollRecallLabel and RollRecallTail replace RollLabel when the recall tool
// is offered. RollLabel's "The work those turns did is not kept" is false
// then — the record keeps it — and its "check the files or run the commands
// again" points at the one recovery a question may rule out. In
// doc/experiments/2026-10-recall a model reasoned "I would need to re-read the
// files" and stopped at "I don't know" with the answer one search away.
//
// The pointer is in two places: a clause in the label, read first, and the
// instruction after the quotes, which can run to thousands of tokens, nearest
// the turn that needs it. Six models reviewed the wording
// (doc/experiments/2026-10-recall-pointers); all six put it after the quotes.
const RollRecallLabel = RollLabelStem + " What the user said in them is quoted below, word for word, " +
	"oldest first; the rest of those turns is in the session record."

// RollRecallTail ends a roll note when recall is offered; see RollRecallLabel.
const RollRecallTail = "The rest of those turns, including your own replies and the output of every " +
	"tool call, is kept in the session record, and the recall tool searches it. When a question " +
	"concerns something said, read or worked out in those turns, search the record before deciding " +
	"you don't know; file contents and command output need from=\"all\". Re-read a file only when " +
	"its current contents are what you need."

// RollOmitted says how many of the oldest user messages did not fit the note.
func RollOmitted(n int) string {
	if n == 1 {
		return " The oldest message did not fit and is left out."
	}
	return fmt.Sprintf(" The oldest %d messages did not fit and are left out.", n)
}
