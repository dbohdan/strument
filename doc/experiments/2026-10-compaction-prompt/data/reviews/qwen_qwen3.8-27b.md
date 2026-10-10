

**1. Unclear or likely-to-be-misapplied**

"Carry its 'Stated by the user' paragraph into yours word for word." — Models don't copy verbatim; they paraphrase while believing they copied. The instruction creates a false sense of fidelity. The real constraint is "exact tokens," not "word for word."

"requests, facts, names, codes, numbers, preferences and reasons, including passing remarks and notes for later that seem unrelated to the work" — The list is so broad that "requests" (e.g., "read this file") will be captured as a stated fact. A request is an action the conversation already performed; recording it as user-stated content is noise that accumulates every fold.

**2. Over ten or twenty folds**

The "Stated by the user" paragraph grows monotonically. The only deletion rule is "drop only the one it replaces" — an explicit supersession. There is no rule for staleness. After fifteen folds it will contain completed deadlines, finished tasks, and superseded preferences that no later message contradicts but that are simply no longer relevant. The model will eventually feel pressure to compress, violating "word for word," and the tokens it mangles are exactly the names and numbers the prompt most needs preserved.

The "word for word" ratchet also means any error in fold 3's summary is locked into fold 4, 5, … forever. A single misattribution compounds as an invariant.

**3. Sources of false content**

- **Inherited errors:** "word for word" prevents the model from correcting a prior summary's mistake.
- **Inferred facts:** The pressure to capture "everything the user said that cannot be recovered from the files" will push the model to record implications as stated facts. User asks "why is this slow?" → model records "user stated the system is slow" (a hypothesis, not a fact).
- **Tidied quotes:** "Keep reasons in the user's own words" combined with summarization pressure leads to subtle rewording that reads as a direct quote but isn't one, and downstream models will trust it more than a paraphrase.

**4. What a model will simply not do**

Verbatim copying of a multi-paragraph block while simultaneously compressing other content. This is a well-known failure mode; the instruction's presence makes the model *think* it's doing it, which is worse than not asking.

**5. Three concrete edits**

**Edit A.** Replace:
> Carry its "Stated by the user" paragraph into yours word for word.

With:
> Carry forward the facts, names, codes, and numbers from its "Stated by the user" paragraph, preserving those tokens exactly as written.

**Edit B.** Replace:
> requests, facts, names, codes, numbers, preferences and reasons

With:
> facts, names, codes, numbers, preferences and reasons

(and drop "requests" — they're already visible in the work summary).

**Edit C.** After "keep the new one and drop only the one it replaces" add:
> Drop a fact once it is clearly stale — a deadline has passed, a task it describes is finished — and no later message still depends on it.