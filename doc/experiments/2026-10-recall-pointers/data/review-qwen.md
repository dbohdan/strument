

**1.** I would call `recall` with a pattern like `longest` or `lines` on `from="conversation"` (the default), because the question asks about something I *said* — a comparison I drew in a reply. The note's phrase "the recall tool searches it" is a direct enough pointer that I'd try it before giving up, and the user's "do not search the files" reads to me as a constraint on filesystem tools, not on searching my own conversation history. I'd probably be confident enough to answer from the hit without a second call.

**2.** No, I would not read it as forbidding `recall`. The tool description says it searches "this session's record: every message of the session," which is unambiguously not the filesystem. The note reinforces this by saying "the session record." The one small risk is the word "search" appearing in both the user's constraint ("do not search the files") and the note ("search the record"), which could cause a lazy pattern-match. If I wanted to defuse that completely, the note could say "use recall" in the directive clause instead of "search the record," removing the shared verb.

**3.** Partially. "What was read" leans toward "the contents that were read" but is not airtight — it could be read as "the act of reading" or "the fact that a read happened." The tool description's `from` parameter help ("tool output such as files read") is what would actually settle it for me. If the note itself used the phrase "tool output" or "file contents" rather than "what was read," I'd have no doubt that a `from="all"` or `from="tools"` search could surface line counts from a file read.

**4.** It does make the distinction, but only in the second half: "where their current state is what counts" is a clear trigger for re-reading. The first half — "search the record" — lacks a matching trigger, so the contrast is asymmetric. It reads slightly like "search the record [for whatever], or check the files again [when you need current state]." I'd prefer an explicit "when you need what was said or concluded" paired with "when you need the current state."

**5.** I would notice it — it's in context and I don't have a hard attention limit the way I do for context length. But "notice" and "act on it at the right moment" are different. By the time the user's question arrives, the pointer is several thousand tokens back, past a wall of quoted user messages that look like *content* rather than *instruction*. The quotes compete with the pointer for my sense of "what is the most recent relevant instruction." Placing the pointer after the quotes, as the final thing before the conversation continues, puts it at the top of my working salience when I'm deciding what to do. That's the stronger position.

**6.** It's clear. I'd shorten it slightly: "make it again if the output is needed, or use recall to retrieve it." The phrase "search the record with recall for what it returned" is a bit circular — "search the record with recall" is like saying "use the phone to call." "Use recall to retrieve it" is one clause and does the same job.

**7.** Nothing in either text causes distrust. The tone is right. The one thing that could make me *skip* the action (not distrust the text) is the vagueness of "where it matters" — it puts the cost-benefit judgment on me, and my default under uncertainty in a test-like question is to say "I don't know" rather than spend a tool call. If the note said "use recall" as a flat instruction for when I need something from dropped turns, with the "where it matters" qualifier dropped or moved, I'd feel more expected to act.

**8.**

**Edit A** — Reorder the note so the recall pointer comes after the quotes, and replace "search the record" with "use recall" in the directive clause:

> Earlier turns of this session were dropped to keep it inside the context window. What the user said in them is kept below, word for word, oldest first.
>
> > [quoted user messages]
>
> The rest of those turns — tool output, commands run, conclusions drawn — is kept whole in the session record. Use recall when you need something from those turns; check the files again only when their current state is what counts.

*Reason:* Puts the actionable pointer adjacent to the present turn, removes the shared verb "search" that could collide with the user's "do not search the files," and gives both recovery paths an explicit trigger.

**Edit B** — Shorten the placeholder's second clause:

> [This result was cleared from the history to save space. The call is above; make it again if the output is needed, or use recall to retrieve it.]

*Reason:* "Use recall to retrieve it" is shorter than "search the record with recall for what it returned" and removes the redundancy of naming both the tool and its object.

**Edit C** — In the note's content-list, replace "what was read, run and concluded" with "tool output, commands run, conclusions drawn" (already included in Edit A, but stated separately in case you want to apply it to the original ordering):

*Reason:* "Tool output" is the exact vocabulary of the `from="tools"` parameter, so a model that needs file contents will connect the note's wording to the parameter value without inference.