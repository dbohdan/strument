**1. Unclear / misappliable instructions**

- "Begin with a paragraph headed 'Stated by the user:' holding everything the user said that cannot be recovered from the files" — "cannot be recovered from the files" invites judgment calls. A summarizer may decide a stated value *could* be recovered from some file it never saw, and drop it. Also, a user's request ("read foo.py") is arguably recoverable, so requests may get silently omitted despite being listed.
- "Carry its 'Stated by the user' paragraph into yours word for word" vs. the prior instruction "Include the function... names" — nothing in the new prompt says whether carried content should also be *merged* with new items of the same kind, or appended. Expect duplicate or interleaved entries.
- "Where a later message changes a value or a decision in it, keep the new one and drop only the one it replaces" — the summarizer must detect that a *new* message contradicts an *old carried* item, across possibly hundreds of messages. This is the instruction most likely to fail silently: the old value survives and the new one also gets added, leaving both.
- "everything the user said that cannot be recovered" but later "keep test outcomes, error lines and values a command returned" — tool output and user statements have different retention rules, but the first paragraph's scope ("everything the user said") may swallow assistant findings the user merely echoed.

**2. Behavior over 10–20 folds**

- **Monotonic growth.** "Word for word" carry-over plus new items each fold means the paragraph only grows. After many folds it dominates the context — the opposite of compaction. There is no instruction to prune anything except superseded values, and supersession detection is unreliable (see above), so stale entries accumulate.
- **Copy-error drift.** Repeated verbatim re-copying of names/numbers is a transcription task run N times; each fold is a chance to garble a token, and garbles then become canonical ("word for word" propagates the corruption).
- **Stale-value persistence.** Because dropping requires recognizing a contradiction, wrong-but-not-obviously-contradictory entries (e.g., a date changed implicitly) never get dropped — this may be exactly how the trial produced confident wrong values.
- The earlier trial's finding (structured templates lose user reasons) isn't obviously addressed: this is still a rigid template, just a one-section one.

**3. False-content risks**

- Invention under pressure: "holding **everything** the user said" plus "including passing remarks" encourages the summarizer to manufacture plausible "user statements" when memory is thin — misattributing assistant/tool statements into the user paragraph, especially since tool results are truncated.
- Carried items whose context was lost get re-asserted as current fact ("the demo moved to Thursday" may have been true two weeks ago).
- "Say what happened" + no-attribution rule pushes passive, agentless prose that obscures who wanted what, inviting blend-through.

**4. Things a model won't reliably do**

- Perfect verbatim carry-over across many folds: models paraphrase; "word for word" will degrade to near-verbatim. Also, "everything the user said" is a completeness demand models satisfy by generating, not by retrieving.
- Detecting supersession across a long folded history without errors.

**5. Proposed edits**

1. Replace the opening of paragraph one:
> "Begin with a paragraph headed 'Stated by the user:' listing each thing the user said as a separate line, copying it as close to verbatim as possible. Include every user statement — requests, facts, numbers, preferences, reasons, and remarks unrelated to the work. Do not judge whether it could be recovered elsewhere; if the user said it, list it. Do not add anything the user did not say."

(The last sentence is the anti-invention guard; per-line format makes carried items auditable and dedupable.)

2. Replace "Carry its 'Stated by the user' paragraph into yours word for word. Where a later message changes a value...":
> "Copy every line of that paragraph into yours unchanged. When a later message replaces one of those items, move the superseded line to a final paragraph headed 'Superseded:' rather than deleting it, and note what replaced it."

(Keeps a verifiable audit trail; no silent drops, no silent staleness; bounds error-compounding because old values stay visible.)

3. Add after the "Then say what was done" paragraph:
> "Distinguish clearly between what the user stated and what tools or files showed; never move tool or file content into the 'Stated by the user' paragraph."

(Directly targets the mis-attribution risk that likely produced the confident wrong answers.)