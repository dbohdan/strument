**1. Unclear / contradictory / likely misapplied**

- *"Copy names, codes and numbers exactly as written, and keep reasons in the user's own words."* Direct contradiction: a reason usually *contains* names/numbers. So is a reason quoted or paraphrased? "In their own words" also silently authorizes copying a whole sentence verbatim — which collides with the ban on *"I"* and *"you"*, since a user's reason reads "because I needed it portable."
- *"File contents, search results and ordinary command output can be read or run again"* vs. *"Keep test outcomes, error lines and values a command returned."* A test outcome *is* command output, and a returned value is recoverable by rerunning. The model must guess which rule wins — that guess is where the six facts died.
- *"everything the user said that cannot be recovered from the files"* — recoverability is an open judgment the model makes poorly under compression; "the demo moved to Thursday" isn't about files at all, yet needs keeping. Say "unrecoverable anywhere", not "from the files".
- The mandated headed paragraph re-imposes a structured template — the exact thing the earlier trial found drops reasons. Structure hides items when a box is deemed full.

**2. Over 10–20 folds**

"Carry ... word for word" makes the paragraph append-only: it can only grow or lose whole items, never tighten. At fold 15 the section dominates the summary, then gets truncated by the *next* fold's text budget — so the losses arrive at the end, silently. Worse, an error early on (a misattributed remark, a wrong number) is copied verbatim forever and acquires authority from repetition: nothing lets the model correct an earlier summary, only preserve it.

**3. Where it creates falsehoods**

- Verbatim carry launders the previous summarizer's paraphrase into the user's voice: an inference becomes "Stated by the user," then a fact, then the *only record left*. That framing ("the only record") discourages challenge.
- *"keep the new one and drop only the one it replaces"* requires exact matching of what a new message supersedes; when the match fails, both old and new value survive — the source of the two confidently wrong answers.
- "Copy exactly" applied to numbers the model just summarized correctly-then-incorrectly produces plausible, wrong digits.

**4. What no model will actually do**

Strict verbatim preservation of a paragraph across folds — models rewrite when told to carry. Also impossible in practice: determining recoverability-by-rerun for every claim within a summarization budget.

**5. Three edits**

Replace *"Copy names, codes and numbers exactly as written, and keep reasons in the user's own words."* with:
> Copy every name, code, number and short quoted phrase exactly as written. For reasons and requests, keep the user's phrasing but you may trim surrounding words; never invent a value to fill a gap.

Replace *"Carry its 'Stated by the user' paragraph into yours word for word. Where a later message changes a value or a decision in it, keep the new one and drop only the one it replaces. Its account of the work may be shortened."* with:
> Take its 'Stated by the user' paragraph as your starting list: keep every item unless a later message clearly supersedes that specific item, then keep only the newer one. Prefer the newer wording when they disagree. You may condense wording but may not delete items for being old or unrelated; if it must be shortened, drop repeated wording first, items last.

Replace *"File contents, search results and ordinary command output can be read or run again: name the file or command rather than repeating it."* with:
> File contents, search results and general command output can be read or run again: name the file or command rather than repeating it. Keep any status, number or error line that a later message reacts to.