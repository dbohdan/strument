The proposal addresses the main failure: it explicitly tells the summarizer to retain passing user-stated facts. But it risks replacing omission with an ever-growing, error-prone ledger.

1. **Unclear or likely to be misapplied**
   - “**everything the user said that cannot be recovered from the files**” is an extremely broad and subjective test. Does it include quoted text, hypothetical examples, questions, or a user’s mistaken claim about a file? “Everything” also conflicts with producing a useful compact summary.
   - “**Carry its ‘Stated by the user’ paragraph into yours word for word**” conflicts with “**Where a later message changes a value or a decision in it, keep the new one and drop only the one it replaces.**” The latter requires editing that paragraph; “drop only the one it replaces” leaves unclear what to do with related facts or superseded reasons.
   - “**File contents, search results and ordinary command output can be read or run again**” is too categorical: files may change or become unavailable, and output can contain information that cannot be reproduced. It also sits awkwardly beside “**Keep test outcomes, error lines and values a command returned**.”

2. **Across ten or twenty folds**
   
   The user-fact paragraph will accumulate, potentially without bound. Word-for-word inheritance makes even redundant or obsolete wording persistent; repeated summaries can also duplicate facts or make the “work” section grow. That may eventually crowd out recent work. Conversely, each fold still paraphrases the work, so details can drift even while the fact paragraph is copied. If an early summary omitted or misstated a fact, later folds have no way to recover the original.

3. **How it can introduce falsehoods**
   
   The instruction to preserve an earlier summary treats that summary as the only record, not as a fallible account. A summarizer may therefore perpetuate an earlier misattribution or wrong value verbatim. “Drop only the one it replaces” can preserve stale facts when a correction is broader than a single value. And labeling material “Stated by the user” can falsely imply the user asserted something that was actually an assistant paraphrase, a quoted example, or an earlier summarizer’s error.

4. **What the model may not do**
   
   No wording can guarantee “everything” is retained, exact copying is error-free, or that the summary stays compact over repeated folds. The prompt should set a priority, not promise perfect preservation.

5. **Concrete edits**
   
   Replace the first paragraph with:
   > Preserve user-stated facts that cannot reliably be recovered later, including passing notes for later. Keep names, codes, numbers, preferences, and reasons exact where possible. Distinguish assertions from questions, hypotheticals, and quoted material.

   Replace the earlier-summary instruction with:
   > Treat an earlier summary as fallible evidence, not a verbatim transcript. Carry forward its user-stated facts without needless duplication; apply explicit later corrections, and do not present uncertain attribution as certain.

   Replace the command-output sentence with:
   > Omit reproducible file contents and routine output when naming the file or command is enough; retain outputs that are not reliably reproducible or that matter to the ongoing work.