You are reviewing a system prompt for a coding assistant's context compaction. When a long conversation is folded, a summarizer model receives the older messages as text (sections headed # USER, # ASSISTANT, # TOOL; tool results are cut short) and the prompt below as its system prompt. Its output replaces those messages. An earlier summary arrives as a # USER section beginning "Summary of the earlier part of this conversation, written by Strument to keep it inside the context window. It replaces those messages; it is not something anyone said." Folds happen at the end of a user's turn, and long sessions fold many times, so each summary is built partly from the previous one.

What went wrong with the current prompt: in a live trial, users stated six facts in passing inside requests to read files ("Note for later: ..."). After repeated folds, the summarizer kept none of them; the next model answered "I don't know", and twice confidently gave wrong values. An earlier trial found that a structured template of sections lost users' stated reasons more often than plain prose did.

CURRENT PROMPT:
---
Briefly summarize this partial conversation about programming. Give more
detail to the most recent messages and less to the older ones. Start a new
paragraph whenever the topic changes.

This is only part of a longer conversation, so don't end with a wrap-up
phrase like "Finally, ..."; the conversation continues after your summary.

Include the function, library, and package names under discussion, along
with the filenames the assistant references inside fenced code blocks. Leave
the fenced code blocks themselves out of the summary.

Keep any reason the user gave for a decision, in their own terms. A choice
can be read back from the code; the reason for it cannot.

Do not attribute actions to anyone — no "I", no "you", no "the assistant".
Say what happened.
---

PROPOSED PROMPT:
---
These messages are about to be removed from the conversation and replaced
by what you write here; whatever isn't in it, the conversation no longer
has. Summarize them so the work can continue without them.

Begin with a paragraph headed "Stated by the user:" holding everything the
user said that cannot be recovered from the files: requests, facts, names,
codes, numbers, preferences and reasons, including passing remarks and
notes for later that seem unrelated to the work (for example, "the demo
moved to Thursday"). Copy names, codes and numbers exactly as written, and
keep reasons in the user's own words.

A message that begins "Summary of the earlier part of this conversation"
is an earlier summary, not something the user said, and it is the only
record left of what came before. Carry its "Stated by the user" paragraph
into yours word for word. Where a later message changes a value or a
decision in it, keep the new one and drop only the one it replaces. Its
account of the work may be shortened.

Then say what was done. Keep test outcomes, error lines and values a
command returned. File contents, search results and ordinary command output
can be read or run again: name the file or command rather than repeating
it. Leave fenced code blocks out, naming the files they came from. Start a
new paragraph whenever the topic changes, and don't end with a wrap-up like
"Finally, ..."; the conversation continues after the summary.

Do not attribute actions to anyone — no "I", no "you", no "the assistant".
Say what happened.
---

Please critique the PROPOSED prompt as candidly as you can. In particular:
1. If you were the summarizer, which instructions would you find unclear, contradictory, or likely to be misapplied? Quote them.
2. What will this prompt do over ten or twenty folds? Consider growth, drift, and errors that compound.
3. Where could it cause the summary to contain things that are false (invented facts, mis-attributed statements, stale values)?
4. Is there anything in it that a model like you would simply not do, however it is phrased?
5. Propose at most three concrete edits, as exact replacement text.
Be specific and brief: under 600 words.