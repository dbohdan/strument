A short framing first: I think the proposed note fixes the *actively misleading* part (the old "the work is not kept, check the files or run the commands again" is the sentence that produced the "I'd need to re-read the files" dead end), but I'm skeptical it's the main lever, and I think one failure mode survives it untouched. Answers:

**1.** I'd first check whether my own earlier reply about those five files is still visible — it's a message, not a tool result, so if that turn survived I can answer off the context directly. If it's gone, I'd call recall immediately with a pattern on the filename or on "longest", default `from`, because the note says the rest of those turns is kept whole in the record and recall is the only retrieval the question permits.

**2.** I would not read it as forbidding recall — "search the files" scopes to the filesystem, and the record is literally this conversation, which is what the question asks me to answer from. But "run anything" is broad enough that a literal reader could treat *any* tool call as banned, and nothing in either text rebuts that; what settles it for me is the tool description ("search this session's record") plus the note drawing an explicit line between the record and the files. That line is currently only implicit in the word "or".

**3.** Only half. "what was read, run and concluded" tells me those turns are kept, but it reads to me as *the act of reading and the conclusions*, with the raw output left ambiguous; it says nothing about `from`. What actually settles it is the tool description's `from` list ("tools = tool output such as files read") together with the placeholder's new clause "search the record ... for what it returned". It doesn't bite on this question — the answer is in my own words, so the default `from` covers it — but on "what did file X contain" it does, and a default search returning zero hits is worse than no pointer, because it confirms a false "I don't know".

**4.** It gives a criterion but buries it as a trailing modifier after "where it matters" and a flat "or", which reads as a coin flip at the moment of decision. Worse, the placeholder — which I hit at exactly the point of need — still leads with "make it again", so the nearby signal points at re-reading while the distant note points at recall. I'd want "record for what was said or returned; files for how things stand now", not two co-equal options.

**5.** I'd notice it at roll time, since it's a conspicuous discontinuity. By the time your question arrives it's one of the oldest things in context, sitting above a quote block that may be thousands of tokens long, and it's narration rather than something addressed to me — so honestly I'd be leaning on the tool description, not the note. After the quotes would be better; but the best-located pointer is the placeholder, because it appears exactly where information disappeared.

**6.** Worth the length — it's the only pointer at the point of loss, and ~15 tokens is nothing next to a wrong answer. But it still leads with re-running, and "search the record with recall for what it returned" invites a default `from="conversation"` search that finds nothing, since the result is tool output. Say which `from`, and lead with recall.

**7.** Two things. The note reads as harness state-description more than as guidance — no "you", no imperative until the final clause — so it's easy to skim as background scenery. And it makes a promise ("kept whole") that the placeholders partially contradict; I'd resolve that in favour of the placeholder because it's closer to the moment, but if the record ever failed to contain something the note said it had, I'd stop believing the note altogether. I wouldn't skip either text.

**8.** Three edits:

**(a)** Replace the note's last two sentences with:

> The rest of those turns, what was read, run and concluded, is kept whole in the session record, and the recall tool searches that record alone, without opening files or running anything. Search it for what was said, read or worked out earlier; re-read the files or re-run the commands only when you need to know how things stand now.

*Reason:* it tells me recall is neither a file read nor a command — which is what defeats a "do not run anything" reading — and it ranks the two recoveries by question type instead of leaving a coin-flip "or".

**(b)** Add this line directly after the quoted block:

> Everything else those turns contained — what you read, ran and concluded — is in the session record; recall searches it.

*Reason:* it lands next to the current turn, so it is still in play when a question about earlier work arrives thousands of tokens later.

**(c)** Replace the placeholder with:

> [This result was cleared from the history to save space. The call is above. To recover what it returned, search the session record with recall, passing from="all"; make the call again only if you need the files as they are now.]

*Reason:* the default `from` searches only what was said, so without `from="all"` a search for cleared tool output comes back empty and teaches me the record doesn't hold it.