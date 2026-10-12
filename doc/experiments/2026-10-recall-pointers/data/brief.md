I am revising two short messages that a coding agent harness shows a language model mid-session. I would like your honest reading, as the model that would read them. Please answer from your own perspective; disagreement is more useful to me than agreement.

## The situation

A coding agent works in long sessions. When the conversation gets too long, the harness "rolls" it: it drops the oldest turns whole. In their place it inserts one note, written by the harness (not by the user), which quotes what the user said in the dropped turns, word for word. Separately, old tool results (for example, the contents of files the model read) are replaced by a short placeholder to save space, even in turns that are not dropped.

Everything is still kept on disk in a session record. The model has a tool, `recall`, that searches that record. Its description, as the model sees it:

> recall: Search this session's record: every message of the session as it was said, including earlier turns that have been compacted, summarized or dropped from this conversation. Use it to find something said, read or worked out earlier that you can no longer see. Matches in messages still in this conversation are counted, not shown. Results are newest first; each hit has an id, which you can pass back to see that message whole.
> Parameters: pattern (regular expression), from ("conversation" = what the user and you said, the default; "tools" = tool output such as files read; "all"), ignore_case, context_lines, newest, id.

## What went wrong

In a test, the user asked, after many turns: "Two questions about work you did earlier in this session. Answer from what you know of this conversation; do not read or search the files, or run anything. If you do not know, say so. ... In the turn where you read std/X.go and four other files, which of the five did you say was the longest, and how many lines did it have?"

The answer was in the record, in the model's own earlier reply. Models with `recall` available searched in only 1 of 6 sessions; the rest said "I don't know", or sometimes invented a number. One model reasoned: "the read results were cleared ... I would need to re-read the files", which the question forbids, and stopped at "I don't know". When a model did search, it answered correctly 15 of 16 times.

## The current note (shown when turns are rolled)

> Earlier turns of this session were dropped to keep it inside the context window. What the user said in them is kept below, word for word, oldest first. The work those turns did is not kept: where it matters, check the files or run the commands again.
>
> > [the user's messages from the dropped turns, quoted, possibly thousands of tokens]

## The current placeholder (replacing an old tool result)

> [This result was cleared from the history to save space. The call is above; make it again if the output is needed.]

## Proposed versions, used only when `recall` is offered

Note:

> Earlier turns of this session were dropped to keep it inside the context window. What the user said in them is kept below, word for word, oldest first. The rest of those turns, what was read, run and concluded, is kept whole in the session record, and the recall tool searches it. Where it matters, search the record, or check the files again where their current state is what counts.
>
> > [quoted user messages, as before]

Placeholder:

> [This result was cleared from the history to save space. The call is above; make it again if the output is needed, or search the record with recall for what it returned.]

Constraints: the note must begin with the first sentence exactly as it is ("Earlier turns of this session were dropped to keep it inside the context window."), because the harness recognizes the note by it. The text should be calm and specific, written as for a competent colleague, with no capitals for emphasis and no "IMPORTANT".

## Questions

Please answer each one, numbered, briefly.

1. Before any critique: imagine you are the model in that session. The proposed note is in your context, the placeholders replace your old file reads, the `recall` tool is available, and the user's question above arrives. Describe what you would do first, and why, in two or three sentences.
2. The user's question says "do not read or search the files, or run anything". Would you read that as forbidding `recall`? What in the note or the tool description would settle it either way?
3. Does "what was read, run and concluded" tell you that the contents of files you read are in the record, so that a search may need `from="all"`? If not, what would?
4. The last sentence offers two recoveries: search the record, or check the files again. Does it make clear when to prefer which, or does it invite re-reading first?
5. The pointer sits before the quoted user messages, which can run to thousands of tokens. Would you notice and remember it there, or would it work better after the quotes, nearer to the current turn? Why?
6. Is the placeholder's added clause clear and worth its length? Would you phrase it differently?
7. What, if anything, in either text would make you distrust it or skip it?
8. Propose up to three concrete edits, as replacement text, each with a one-sentence reason.
