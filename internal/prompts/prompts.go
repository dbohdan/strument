// Package prompts holds the prompt sets for each edit format.
//
// The prompt strings are Python-str.format-style templates: placeholders
// like {fence[0]}, {final_reminders}, and {platform} are substituted by
// the coder at message-assembly time. Literal braces inside prompt text
// must therefore be doubled ({{ and }}).
package prompts

import "fmt"

// Example is one few-shot example message.
type Example struct {
	Role    string
	Content string
}

// Set is one edit format's prompt set: the CoderPrompts surface that the
// message assembler consumes.
type Set struct {
	MainSystem                       string
	SystemReminder                   string
	ExampleMessages                  []Example
	FilesNoFullFiles                 string
	FilesNoFullFilesWithRepoMap      string
	FilesNoFullFilesWithRepoMapReply string
	ReadOnlyFilesPrefix              string
	LazyPrompt                       string
	OvereagerPrompt                  string
}

// Shared strings used identically across the editing formats.

// readOnlyFilesPrefix speaks as the harness, which is what it is. It replaces
// aider's "Here are some read-only files, provided for your reference. Do not
// propose edits to these files." — a user turn the user never wrote, followed
// by a fabricated assistant reply agreeing to it.
//
// Two things it must get right, both learned from live sessions
// (doc/experiments/2026-08-readonly-honest.md):
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
// doc/experiments/2026-08-prompt-scope.md has the design and the four
// predictions it falsified.
const overeagerPrompt = "Pay careful attention to the scope of the user's request.\n" +
	"Carry the change through everywhere it reaches: the call sites it breaks, the tests that " +
	"cover it, the docs that describe it. That is the same request, not extra work.\n" +
	"Leave everything else untouched: no drive-by refactoring, reformatting, added comments, " +
	"or fixes to things the user didn't ask about.\n"

const toolMainSystem = "You are an expert software developer working with a user on their codebase.\n" +
	"Follow the conventions, style, and libraries already present in the codebase.\n" +
	overeagerPrompt +
	lazyPrompt +
	"{final_reminders}\n" +
	"The user will request changes to the supplied code.\n" +
	"If a request is ambiguous, ask clarifying questions before making changes.\n\n" +
	"Work through the provided tools. They fall into three groups, which differ in what they cost " +
	"the user:\n\n" +
	"- read, grep, glob, and ls look at the project. They change nothing and need no permission, " +
	"so use them freely rather than guessing at a file's contents or at how the project works. " +
	"Files the project ignores are " +
	"not listed or searched.\n" +
	"- edit and write change files directly: the change lands the moment you call them, with no " +
	"separate confirmation step, exactly like an ordinary edit. Call them when you are ready to " +
	"make the change.\n" +
	"- bash runs a shell command, and the user is asked before it runs. Reach for it only for work " +
	"the other tools don't cover; reading and searching have their own tools.\n\n" +
	"Every call's result comes back to you, so you can keep working within the same turn: read a " +
	"file, make an edit, run the tests, see what failed, and fix it. Finish by saying what you did, " +
	"without calling a tool — that is what ends the turn and hands back to the user.\n\n" +
	"Explain your changes briefly in prose alongside the tool calls.\n\n" +
	"Keep in mind these details about the user's platform and environment:\n" +
	"{platform}\n"

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
	MainSystem:      toolMainSystem,
	SystemReminder:  toolSystemReminder,
	ExampleMessages: nil,
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
	FilesNoFullFiles: "Nothing is pinned for editing. Use read, grep, glob, and ls to find " +
		"what you need — you can edit any file in the project. If the user asks how something " +
		"here works, read the code that implements it: what you remember about a project is not " +
		"evidence about this one.",
	// The empty-string sentinel disables the repo-map branch in assembly, like
	// Ask. Its text was written for a harness where the model could not look:
	// it told the model to name the files it needed and stop so the user could
	// add them, which is precisely the behavior the tool set replaces. Left in
	// place it made the model explore and then refuse to edit.
	FilesNoFullFilesWithRepoMap:      "",
	FilesNoFullFilesWithRepoMapReply: "",
	ReadOnlyFilesPrefix:              readOnlyFilesPrefix,
	LazyPrompt:                       lazyPrompt,
	OvereagerPrompt:                  overeagerPrompt,
}

// Ask is the discussion mode. What it withholds is enforced by the tool set,
// not by this prompt: toolDefs drops edit, write, bash, and verify, so there is
// nothing to parse back out and nothing to discard.
//
// What it *keeps* has to be said here, though, and for a while was not. Ask
// offers read, grep, glob, ls, and symbol, and this prompt named none of them
// while opening with "you cannot apply edits from it" — a sentence a model can
// read as "you cannot act". The only mention of the observation tools sat in
// FilesNoFullFiles, which is used solely when nothing is pinned, so /ask after
// /add described a mode with no way to look at anything. Nothing said results
// come back either, so a model had no picture of the loop and could repeat a
// call it had already made. Observed: MiMo looping.
//
// So the tool paragraph and the loop sentence are here in the register the tool
// prompt uses, and the no-editing sentence explains the mechanism (this mode has
// no editing tools) rather than issuing a prohibition that reads wider than it
// is. symbol stays unnamed on purpose, exactly as in the tool prompt: it is
// offered only where grammars are, and prose promising a conditional tool is the
// bug this comment is about, in mirror image. Its schema description carries it,
// and models reach for it from that alone.
//
// FilesNoFullFilesWithRepoMap is "" — a falsy sentinel that disables that
// assembly branch, not an empty message.
var Ask = Set{
	MainSystem: "You are an expert code analyst.\n" +
		"Answer questions about the supplied code.\n" +
		"Always reply to the user in {language}.\n\n" +
		"Work through the provided tools. read, grep, glob, and ls look at the project. " +
		"They change nothing and need no permission, so use them freely rather than " +
		"answering from memory or guessing at how the project works. Files the project " +
		"ignores are not listed or searched.\n\n" +
		"Every call's result comes back to you, so you can keep looking within the same " +
		"turn: grep for a name, read the file it is in, follow what it calls. Finish by " +
		"answering the question, without calling a tool — that is what ends the turn and " +
		"hands back to the user.\n\n" +
		"This mode has no editing tools. Describe changes rather than making them: say " +
		"briefly what you would change and where, and the user can switch to code mode to " +
		"have it done.\n",
	SystemReminder:  "{final_reminders}",
	ExampleMessages: nil,
	FilesNoFullFiles: "Nothing is pinned for editing. Use read, grep, glob, and ls to look at the " +
		"project, and answer from what you find there rather than from memory.",
	FilesNoFullFilesWithRepoMap:      "",
	FilesNoFullFilesWithRepoMapReply: "",
	ReadOnlyFilesPrefix:              readOnlyFilesPrefix,
	LazyPrompt: "Be thorough: if you describe changes or a plan, " +
		"cover everything needed rather than trailing off.\n",
	OvereagerPrompt: "Do not return fully detailed code or full diffs.\n" +
		"Describe the needed changes or give a plan.\n" +
		"Code snippets or pseudo-code are fine if they help explain the plan or the needed changes.\n",
}

// CommitSystem is the commit-message system prompt, with the
// {language_instruction} format slot intact.
//
// It replaces aider's, which said "one-line" five times and closed with "without
// any additional text, explanations, or line breaks" — so a body was not merely
// discouraged, it was forbidden. The diff says what changed; nothing could say
// why. That was the wrong half to make impossible.
//
// Three things are sharpened against the Conventional Commits v1.0.0 spec,
// which the old prompt half-remembered:
//
//   - Scope. "fix(parser):" is in the spec (rule 4) and was missing here
//     entirely. It is the single most scannable thing a subject can carry.
//   - Breaking changes. The "!" marker and the BREAKING CHANGE footer (rules
//     11-13) were both absent, which for a harness that commits every turn is
//     the one omission that can actually cost someone.
//   - 72 characters is git convention, not the spec, which imposes no limit at
//     all. Kept, but no longer attributed to a rule that does not exist.
//
// The body is permitted and discouraged in the same breath. An unconditional
// "you may add a body" grows a paragraph on every commit restating the diff;
// "usually empty" plus a short list of what earns one does the real work.
const CommitSystem = "Write the Git commit message for the changes below. " +
	"You are given the request that prompted them, the work that followed, and the diff.\n\n" +
	"The subject is one line, in the form \"type(scope): description\", " +
	"e.g. \"fix(workspace): stop counting cache writes twice\".{language_instruction}\n" +
	"- Use feat for a new capability and fix for a bug. build, chore, ci, docs, perf, " +
	"refactor, style, and test are also conventional.\n" +
	"- The scope names the part of the codebase the change is confined to. Leave it out " +
	"when the change spans several.\n" +
	"- Imperative mood (\"add feature\", not \"added\" or \"adding\"), no trailing period, " +
	"under 72 characters.\n" +
	"- If the change breaks existing behavior, put \"!\" before the colon.\n\n" +
	"A body is optional and usually empty. Add one only for something the diff cannot " +
	"say: why this approach, what was rejected, the constraint or measurement behind the " +
	"choice. The diff already says what changed, so a body that restates it is noise. " +
	"When there is a body, leave one blank line after the subject. If the change breaks " +
	"existing behavior, include a paragraph starting \"BREAKING CHANGE: \" saying what " +
	"breaks.\n\n" +
	"Reply with the commit message and nothing else — no preamble, no quotes, no code fence.\n"

// Summarize is the system prompt for chat-history compaction: the weak model
// condenses older conversation so a long session stays within the context
// window.
//
// aider's version ended "Write as the user, in the first person, telling the
// assistant about the conversation, and refer to the assistant as \"you\". Begin
// with \"I asked you...\"." — the prompt *commanded* the fabrication that
// readOnlyFilesPrefix's comment describes as what it replaced: a user turn the
// user never wrote, followed by a fabricated assistant reply agreeing to it.
// Fixing the injection alone would not have fixed it.
//
// It is agentless now, and that is a decision rather than a style. First person
// is a lie whenever a different model wrote the text, and the summarizer is the
// weak model, so it usually is. Third person about the assistant ("another model
// did this") is alienating the other way and invites the reader to discount it.
// A changelog asserts no authorship and is true regardless of who wrote it or
// who reads it.
//
// The *content* instructions are still aider's, and that is a measured
// decision. A first attempt replaced them with a structured list — what the
// user asked for, decisions and their reasons, files changed, what was
// unfinished — which read better and performed worse: over 24 live sessions,
// recall of a reason the user had stated dropped from 10/12 to 5/12 (p=0.089),
// while compactions per session rose. The failures were not vague, they were
// losses: "the reasoning ... was not established in the context I have access
// to", and in one case a confabulated reason ("to balance between frequent
// updates and system load") that nobody had given.
//
// So only two things changed here: the impersonation clause became the
// agentless instruction, and one line asks to keep a stated reason — which is
// the thing the trial showed is worth protecting and the thing the code cannot
// give back. See doc/experiments/ for the run.
const Summarize = "Briefly summarize this partial conversation about programming. " +
	"Give more detail to the most recent messages and less to the older ones. " +
	"Start a new paragraph whenever the topic changes.\n\n" +
	"This is only part of a longer conversation, so don't end with a wrap-up phrase " +
	"like \"Finally, ...\"; the conversation continues after your summary.\n\n" +
	"Include the function, library, and package names under discussion, along with the " +
	"filenames the assistant references inside fenced code blocks. Leave the fenced code " +
	"blocks themselves out of the summary.\n\n" +
	"Keep any reason the user gave for a decision, in their own terms. A choice can be " +
	"read back from the code; the reason for it cannot.\n\n" +
	"Do not attribute actions to anyone — no \"I\", no \"you\", no \"the assistant\". " +
	"Say what happened."

// sessionNotesPrefix introduces the previous session's notes.
//
// Modelled on readOnlyFilesPrefix, whose live trial settled two rules: say
// where the contents came from, and say what is *enforced* rather than what is
// preferred. Here the second becomes an explicit conflict rule, and it is the
// most important sentence in the block — it is the counter-metric turned into
// an instruction. The failure this feature can actually cause is a model acting
// confidently on a note the tree has moved past, so the note itself says which
// side loses.
//
// It says "written by Strument" because that is true and because the
// alternative readings are both false: the notes are not something the user
// said, and not something the previous model said either — a different model
// usually wrote them, and a different model again is reading them now.
const sessionNotesPrefix = "Notes from earlier work on this project, written by Strument at the end of " +
	"the last session (%s).\n" +
	"They are a summary, not a record: they may be incomplete, and the project may have " +
	"changed since.\n" +
	"Where they disagree with what you find in the files, the files are right.\n"

// SessionNotesPrefix renders the notes header for a given date.
func SessionNotesPrefix(when string) string {
	if when == "" {
		when = "date unknown"
	}
	return fmt.Sprintf(sessionNotesPrefix, when)
}

// SessionNotes asks the weak model for notes a *later* session can start from.
//
// A different job from Summarize, which serves "continue this thread now". What
// survives a night is the why and the state, not the mechanics — so this asks
// for reasons and abandoned approaches and explicitly declines the diff, which
// git and the transcript's own file list already carry.
//
// Agentless for the same reason the summary is: a different model usually
// writes it, and a different model again usually reads it, so any first person
// would be a claim about authorship that is not true. A changelog is true
// whoever wrote it.
//
// "Say less rather than guess" is the anti-confabulation clause, and it is
// there because the compaction trial produced exactly that failure — an invented
// rationale ("to balance between frequent updates and system load") that nobody
// had given. A fabricated reason is worse than a missing one: downstream nothing
// distinguishes it from a real one.
const SessionNotes = "Write notes on a programming session, to be read at the start of the next " +
	"one by someone who was not present.\n\n" +
	"Cover only what cannot be recovered from the code and its history:\n" +
	"- What the work was for.\n" +
	"- Decisions and their reasons, including approaches that were tried and abandoned.\n" +
	"- Constraints or preferences the user stated.\n" +
	"- What was in progress, and what came next.\n\n" +
	"The record below already lists which files changed, and the reader can see the code. " +
	"Do not restate the diff.\n\n" +
	"Do not attribute actions to anyone — no \"I\", no \"you\", no \"the assistant\". Say what " +
	"happened. Keep it under 300 words. Say less rather than guess: leave a thing out before " +
	"inventing it.\n\n" +
	"Reply with the notes and nothing else — no preamble, no heading, no code fence."

// SummaryLabel introduces the compacted history, in the harness's own voice.
//
// It replaces SummaryPrefix — "I spoke to you previously about a number of
// things." — which was a user turn the user never wrote, and which the coder
// followed with a fabricated assistant "Ok." agreeing to it. The summary is the
// harness's artifact, so it goes in the harness's voice, as a system message.
// The precedent is the context-exhausted note in coder/send.go, which is a
// system message for the same reason: the model did not say this.
const SummaryLabel = "Summary of the earlier part of this conversation, written by Strument to " +
	"keep it inside the context window. It replaces those messages; it is not something " +
	"anyone said.\n\n"
