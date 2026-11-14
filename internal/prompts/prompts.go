// Package prompts holds the model-facing text: the system prompt and the notes
// the harness writes into a conversation.
//
// The system prompt is one literal string. It used to be a set of
// str.format-style templates, with slots for the platform, the reply
// language, the run_code bullet and the observation paragraph, filled at
// assembly time. Every slot but the platform had come to render the same text
// in every production session, and the platform is facts rather than
// instructions, so the coder now appends it after this string (see
// Coder.factsText) instead of splicing it in. What is written here is what the
// model reads.
package prompts

import "fmt"

// Shared strings used identically across the editing formats.

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
// editing. The how-to-look note already patched that for the nothing-pinned case;
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

// codeToolsBullet is the system prompt's run_code bullet.
//
// The tool list is the closed-world hazard the code-mode trial measured: the
// sentence "these are the ones you will reach for most" reads as policy, and a
// flash-class model treats the enumeration as exhaustive —
// doc/experiments/2026-08-code-mode/README.md found 0/36 `run_code` calls with the tool
// offered in the schema but absent from this list. So `run_code` gets a
// bullet. It used to be a slot, filled only when the tool was offered, so the
// prose could never promise a tool the schema withheld; nothing withholds it
// now. The bullet is a conditional rule
// ("when one answer needs several lookups combined"), not an announcement:
// per the symbol fix, the description that moves uptake is the one that maps
// a felt need to a tool, and the arithmetic clause is explicit because models
// do not recognize their own mental arithmetic as costly.
//
// The tool list names symbol with "where it is offered" because symbol is
// offered only where a repo map is. Naming it unconditionally would promise a
// tool half the sessions do not have, and leaving it out hid a tool a program
// really can call wherever grammars exist. It was a slot filled from
// Coder.codeCallableTools; a clause says the same in a literal string.
const codeToolsBullet = "- run_code runs a short JavaScript program, which can itself call " +
	"read, grep, glob, ls, and symbol where it is offered" +
	" — the results come back to the program, not to you, until it returns. " +
	"It changes nothing and needs no permission, and it runs inside the harness, " +
	"not the project: it cannot import or run the project's own code. Reach for it when one answer " +
	"needs several lookups combined — three greps you already know you need are " +
	"one program, and each tool call you make separately costs the user a full " +
	"round trip — and for any arithmetic or counting beyond a single step, " +
	"rather than working figures out in your head. A single lookup, or " +
	"exploration where each search depends on the last result, is a direct call " +
	"to the tool itself.\n"

// observationBullet is the system prompt's read/grep/glob/ls paragraph.
const observationBullet = "- read, grep, glob, and ls look at the project. They change nothing and need no permission, " +
	"so use them freely rather than guessing at a file's contents or at how the project works. " +
	"Files the project ignores are " +
	"not listed or searched. Paths are relative to the project root; an absolute path that lies " +
	"inside the project is also accepted.\n"

// The loop paragraph's worked sequence ends in a commit too, because it is
// the order of work a model copies and it used to stop at the fix. Neither
// change was enough on its own for MiMo on a one-line fix: 0/2 sessions
// committed with the bullet, 0/4 with the bullet and the sequence. They are
// kept because the old text was false, not because they were shown to work.
//
// commitBullet used to end "A request that is genuinely one change is one
// commit, and needs no call at all", which was true while every turn ended in
// a commit of its own. That went, and the sentence stayed: in the first live
// pass without it, MiMo finished a fix and did not commit, reasoning that a
// commit would sweep in a change the user had left in the tree. Both halves of
// that are answered here — nothing else commits, and the user's changes are
// committed apart from the model's — because the tool's description says so
// too, and the model reads this first.
const commitBullet = "- commit records the edits you have made so far as one commit, with a " +
	"message you write. Nothing else commits them: when a change is done, commit it with the " +
	"commit tool. Changes the user had already made to the same files are committed " +
	"separately first, so your commit holds only your work. " +
	"When a request has several parts, work through them one at a time: make the edits for the " +
	"first part, call commit, and begin the second part after that result comes back. Do not " +
	"make every edit and then commit once at the end — by then there is only one commit to " +
	"make, and the user is reading a diff that does three things at once.\n"

// mainSystem is the instructions half of System: who the model is working
// with, the scope rule, and what each tool costs.
const mainSystem = "You are an expert software developer working with a user on their codebase.\n" +
	"Follow the conventions, style, and libraries already present in the codebase.\n" +
	overeagerPrompt +
	lazyPrompt +
	"\n" +
	"The user will ask for changes to the code, or ask questions about it.\n" +
	"If a request is ambiguous, ask clarifying questions before making changes.\n\n" +
	"Work through the provided tools. These are the ones you will reach for most, and they differ " +
	"in what they cost the user:\n\n" +
	codeToolsBullet +
	observationBullet +
	"- edit and write change files directly: the change lands the moment you call them, with no " +
	"separate confirmation step, exactly like an ordinary edit. Call them when you are ready to " +
	"make the change.\n" +
	"- bash runs a shell command, and the user is asked before it runs. Reach for it only for work " +
	"the other tools don't cover; reading and searching have their own tools.\n" +
	commitBullet + "\n" +
	"Every call's result comes back to you, so you can keep working within the same turn: read a " +
	"file, make an edit, run the tests, see what failed, fix it, and commit the fix. Finish by " +
	"saying what you did or what you found, without calling a tool — that is what ends the turn " +
	"and hands back to the user.\n\n" +
	HarnessNoteLine +
	"Explain your changes briefly in prose alongside the tool calls.\n"

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

// editingRules is the exact-match rule for edit and the one-change-per-call
// discipline.
//
// It once went out twice per request: aider also appends it to the last user
// turn, rules at the top and at the bottom. Claude Haiku reported the
// duplicate when asked what looked odd about the harness. The end copy went:
// the system prompt is a cacheable prefix, the copy in the user turn was
// regenerated every send, and putting it there edited words into the user's
// own message.
const editingRules = "# Editing rules\n\n" +
	"- edit's old_string must match the file's current contents exactly, character for character, " +
	"including all whitespace, comments, and docstrings. An inexact match is the most common reason " +
	"an edit is rejected, so double-check it.\n" +
	"- Include enough surrounding lines in old_string to identify the location uniquely, and keep " +
	"each call to one small, self-contained change. Use several calls for several changes.\n" +
	"- To move code, use two calls: one to remove it, one to add it in the new place.\n" +
	"- Read a file before editing it unless its contents are already in the conversation. Editing " +
	"from memory is where inexact matches come from.\n"

// howToLook is the note that closes the instructions.
//
// Its second sentence is here because the repo map used to be, in this exact
// position. The map was not being read as content; it was evidence that a
// project existed to look at, and removing it left three of ten models
// answering a question about this codebase without opening a file — one of
// them inventing a whole subsystem. This says the same thing for thirty
// tokens instead of a thousand. It is spelled out for questions because
// everything else the model is told is about making changes, and a question
// falls outside that contract. It says "the user asks" rather than aider's "I
// ask" because this text lands in the system prompt, where a first-person "I"
// is the harness wearing the user's voice.
//
// It began "Nothing is pinned for editing.", from when /add could pin files
// and this note was what a session with none got. Pinning went, and with it
// the sentence; the rest is unchanged.
const howToLook = toolNoPinsHead + runCodeClause + toolNoPinsTail

// System is the built-in system prompt, used unless the config's
// system_prompt replaces it. The coder appends the session's facts after it.
const System = mainSystem + "\n" + editingRules + "\n" + howToLook

// runCodeClause is the half-sentence that offers a program as an alternative
// to a run of direct lookups, byte-identical to what 2026-09-code-mode2.md
// measured. It was kept separate so a session that withheld run_code could
// get the same note without it; nothing withholds run_code now.
const runCodeClause = " — or one run_code call that calls them, when you " +
	"already know the several things you are looking for"

// The how-to-look note, either side of the clause.
const (
	toolNoPinsHead = "Use read, grep, glob, and ls to find " +
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
