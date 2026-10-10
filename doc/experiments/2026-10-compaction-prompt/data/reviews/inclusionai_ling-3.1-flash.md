**1. Unclear or contradictory (quotes as the summarizer would read them)**

- *"everything the user said that cannot be recovered from the files"* — applying this requires knowing file contents, but tool results are cut short; I'd guess, and guess toward omission. *"requests"* also sweeps in every completed request — unbounded bloat.
- *"Carry its 'Stated by the user' paragraph into yours word for word"* vs. *"Start a new paragraph whenever the topic changes"* — facts can end up split across paragraphs, and later folds look for one paragraph and silently miss the rest. Undefined if no such heading exists (first fold, or a summary from the old prompt).
- *"Where a later message changes a value... drop only the one it replaces"* — forces binary replace/keep on facts that may be additive or scoped ("limit is 40" vs. "in staging it's 100"). Dropping the old one is then wrong.

**2. Over ten–twenty folds**

The section grows monotonically; nothing bounds it. The oldest items sit at the top of the context, farthest from recency bias, so when length pressure truncates, they go first — the exact failure being fixed. Meanwhile *"Its account of the work may be shortened"* compounds per fold, so the summary inverts its priorities: ever more user trivia, ever less work detail.

**3. Where false content enters**

A value misquoted at fold 1 is carried *"word for word"* forever and looks authoritative — confident wrong answers. A misjudged supersession silently deletes a still-true value with no trace. The ban *"no 'I', no 'you', no 'the assistant'"* can strip speaker identity, letting a user assertion blur into tool output — yet the heading itself attributes, so the rule is inconsistent.

**4. What I simply would not do**

*"holding everything"* — no summarizer includes literally everything; the instruction gets silently, arbitrarily partially obeyed. Past a few folds, *"word for word"* gets quietly compressed under length pressure.

**5. Three edits (exact replacement text)**

1. Replace the second paragraph with:
> A message that begins "Summary of the earlier part of this conversation" is an earlier summary — the only record of what came before. Merge its "Stated by the user:" items into yours, unchanged. Where a later message explicitly corrects one ("no", "actually", "change X to Y"), keep the new value and mark the old one "superseded:" instead of deleting it. Where a fact may hold in several contexts (environments, files, dates), keep each with its context. If the earlier summary lacks the heading, start the paragraph. Keep this section one paragraph.

2. Replace *"holding everything the user said that cannot be recovered from the files: requests, facts, names, codes, numbers, preferences and reasons"* with:
> holding what a person re-reading the repository could not recover: the user's requests still pending, plus facts, names, codes, numbers, preferences and reasons stated only in prose

3. Replace *"Do not attribute actions to anyone — no "I", no "you", no "the assistant". Say what happened."* with:
> Never use "I" or "you", and never write "the assistant". Do write "the user said" or "the user asked" where the speaker matters, and "the command returned" for tool output.

The instincts — a verbatim user-fact channel, exact-copy carry-forward — are right; the flaws are unboundedness and silent deletion. Marking superseded facts instead of dropping them turns both failure modes (loss and staleness) into visible state.