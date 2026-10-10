# Compaction prompt: brainstorm and trial design

**2026-10-11. Stage A run; results below. Stage B not yet run.** Background in
[`2026-10-compaction-threshold`](../2026-10-compaction-threshold/) and
[`2026-10-compaction-survey`](../2026-10-compaction-survey/).

2026-10-10. Fifteen candidates for `prompts.Summarize`, each with a first
draft, a critique, a revision, and a rating of the revision.

## What the prompt has to do

Facts that bear on every candidate:

- **The input.** The summarizer is the side model, which defaults to the main
  model (MiMo in every trial so far). It gets the folded messages as text:
  `# USER` / `# ASSISTANT` / `# TOOL` sections. User messages are in full,
  tool calls in full, and tool results cut to a budget. An earlier summary
  arrives as an ordinary `# USER` section that starts with `SummaryLabel`.
  Each fold therefore summarizes the last summary again.
- **Where the facts went.** The facts the threshold trial lost were in user
  messages, so they reached the summarizer. The prompt dropped them.
- **When folds happen.** Strument folds at turn end (`moveBackCurMessages`),
  not mid-turn. A section for "work in progress" or "next step" is usually
  empty at that point, or invented.
- **What failed before.** In `2026-08-compaction`, a structured template
  (asks / decisions + reasons / files / unfinished) lost a stated reason in
  7 of 12 sessions against 2 of 12 for the current prompt. It also
  confabulated one reason. A template is not free.
- **Voice.** Agentless in the summary itself: no "I", "you" or "the
  assistant". Addressing the summarizer as "you" in the instructions is
  fine; the agentless rule is about the output.

Current prompt (B0, the baseline):

> Briefly summarize this partial conversation about programming. Give more
> detail to the most recent messages and less to the older ones. Start a new
> paragraph whenever the topic changes.
>
> This is only part of a longer conversation, so don't end with a wrap-up
> phrase like "Finally, ..."; the conversation continues after your summary.
>
> Include the function, library, and package names under discussion, along
> with the filenames the assistant references inside fenced code blocks. Leave
> the fenced code blocks themselves out of the summary.
>
> Keep any reason the user gave for a decision, in their own terms. A choice
> can be read back from the code; the reason for it cannot.
>
> Do not attribute actions to anyone — no "I", no "you", no "the assistant".
> Say what happened.

"Degree" below runs from 1 (a line changed) to 5 (a different mechanism).

---

## P1. Minimal patch — degree 1

*Influence: the threshold trial's diagnosis, nothing else.*

**Draft.** B0 with "Give more detail to the most recent messages and less to
the older ones." replaced by "Keep names, codes, values and identifiers
exactly as written."

**Critique.** It removes the sentence that drops old facts, but also the
recency gradient, which likely helps the turn that comes next. "Identifiers"
reads as code identifiers. An on-call engineer's name or a budget code is not
obviously one. Nothing addresses the summary-of-a-summary compounding, which
is the larger loss: one fold at 256k was mostly fine, sixteen at 16k lost
everything.

**Revision.**

> Briefly summarize this partial conversation about programming. Give the most
> recent messages the most detail. Start a new paragraph whenever the topic
> changes.
>
> *(B0's second and third paragraphs unchanged.)*
>
> Copy any name, code, number or value the user stated exactly as written,
> whatever it was about.
>
> *(B0's reason and attribution paragraphs unchanged.)*

**Rating: ★★★☆☆.** Cheap and safe, and fixes half the problem. Not
compounding.

---

## P2. Carry-forward — degree 2

*Influence: OpenCode's "anything you do not carry into the new summary is
lost", Pi's "PRESERVE all existing information".*

**Draft.** P1 plus: "If the conversation begins with an earlier summary,
carry everything in it into the new one. Anything left out is lost."

**Critique.** This targets the compounding directly, and names the
consequence instead of only giving an order. "Carry everything" with no way
to drop anything makes the summary grow at every fold. Under late compaction
there are few folds, but on a 128k-window model there are many. The usual
escape, OpenCode's "drop what is finished and no longer needed", is the very
judgment that dropped "Note for later" facts: by the summarizer's lights,
they were not needed.

**Revision.** P1 plus:

> If the conversation begins with an earlier summary, it is the only record
> left of everything before it. Carry its content into the new summary. You
> may shorten an account of finished work or of files that were read, since
> both can be looked up again; keep everything the user said.

**Rating: ★★★★☆.** Targets the measured failure with a reason the model can
generalize from.

---

## P3. Recoverability — degree 2

*Influence: B0's own reason line ("a choice can be read back from the code;
the reason cannot"), generalized; Kimi's "re-running to recover them may be
slow or impossible".*

**Draft.**

> Summarize this partial conversation about programming so the work can
> continue without it. Keep what cannot be recovered later and shorten what
> can. What the user said — requests, facts, names, values, preferences,
> reasons — cannot be recovered; keep it, in their own terms. File contents,
> search results and command output can be read or run again; name the file
> or command instead of repeating it.
>
> *(B0's paragraph rules, code-fence rule and attribution rule.)*

**Critique.** One principle sorts every case, including ones nobody listed,
and it is the principle B0 already uses for reasons, so the change stays in
the prompt's existing voice. One gap: some command output cannot cheaply be
produced again. A test run that took ten minutes, a live API response, an
error that happened once — Kimi singles these out. The draft also drops
B0's "Briefly", but says nothing about length, so a model may now copy too
much.

**Revision.**

> Summarize this partial conversation about programming so the work can
> continue without it. Keep what cannot be recovered later, and shorten what
> can.
>
> Nothing the user said can be recovered: keep their requests, facts, names,
> values, preferences and reasons, in their own terms, whatever they were
> about. Results that were costly or one-off cannot be recovered either: keep
> test outcomes, error lines, and values a command returned. File contents,
> search results and ordinary command output can be read or run again: name
> the file or command rather than repeating it.
>
> If the conversation begins with an earlier summary, it is the only record
> left of what came before; carry what it holds of the above into the new
> summary.
>
> Start a new paragraph whenever the topic changes. This is only part of a
> longer conversation, so don't end with a wrap-up phrase like "Finally,
> ..."; the conversation continues after your summary. Leave fenced code
> blocks out of the summary, naming the files they came from.
>
> Do not attribute actions to anyone — no "I", no "you", no "the assistant".
> Say what happened.

**Rating: ★★★★★.** A principle rather than a list, consistent with the
existing reason line, and it covers carry-forward. My pick; see the end.

---

## P4. Codex handoff — degree 3

*Influence: Codex's six-line `prompt.md`.*

**Draft.**

> Create a handoff summary of this partial conversation for the model that
> will continue the work. Include current progress and key decisions,
> important context, constraints and user preferences, what remains to be
> done, and any critical data, examples or references needed to continue. Be
> concise and structured.

**Critique.** It is generic, and it works for Codex only because Codex also
keeps the user's messages verbatim beside the summary. Without that
mechanism, "important context" is the summarizer's call again. "Be concise
and structured" pulls toward a template and toward dropping things. "What
remains to be done" at a turn-end fold invites invention.

**Revision.** "Handoff" framing; "Keep any name, code, number or value
exactly"; "Be short about finished work and exact about everything else";
"remains to be done" removed.

**Rating: ★★☆☆☆.** A good prompt for a different mechanism.

---

## P5. Kimi long-form — degree 4

*Influence: Kimi's `compaction-instruction.md`.*

**Draft.** A shortened Kimi prompt: no rigid headings; same language as the
conversation; exact commands, paths and returned values; what is still
unknown; a forward plan; honesty about unverified claims; length
proportional to the task.

**Critique.** It has the best single idea on the panel: say plainly what is
unverified ("if a step claimed tests pass but was never checked, say so").
That suits Strument's honesty rules. The forward-plan section, "invest in
it… the plan you commit here is the one it will follow", is wrong for a
turn-end fold in a reviewable loop: the next move belongs to the user, who
has not spoken yet. At about 600 words it is also hard to tune; when it
fails, it is unclear which clause failed.

**Revision.** Keep exactness, the unknowns, unverified-is-unverified and
proportional length; drop the plan; keep the same-language rule.

**Rating: ★★★☆☆.** Strong parts, but a prompt this long is hard to debug.
The unverified clause is worth borrowing.

---

## P6. Pi template — degree 4

*Influence: Pi's `SUMMARIZATION_PROMPT` and its update variant.*

**Draft.** Goal / Constraints & Preferences / Progress (Done, In Progress,
Blocked) / Key Decisions / Next Steps / Critical Context, "Use this EXACT
format".

**Critique.** This is the shape that lost reasons in `2026-08-compaction`.
At a turn-end fold, "In Progress" and "Next Steps" are fiction. A user's
side note ("the budget code is BX-5528") has no obvious section; it lands in
Critical Context only if the summarizer thinks it critical, the same
judgment that failed. The update variant's "PRESERVE all existing
information" is good, and is P2.

**Revision.** Add a "Stated by the user" section and drop "Next Steps".

**Rating: ★★☆☆☆.** The template is the risk we already measured.

---

## P7. OpenCode anchored template — degree 4

*Influence: OpenCode's `SUMMARY_TEMPLATE` + `SUMMARY_UPDATE_INSTRUCTIONS`.*

**Draft.** Objective / Important Details / Work State / Next Move / Relevant
Files, with the update rules (carry forward; newer wins on conflict).

**Critique.** "Important Details — constraints/preferences, decisions and
why, important facts" covers user facts better than Pi does, and "the
conversation wins where they conflict" is a real rule that P3 lacks. But
"terse bullets, not prose" is what squeezes a reason out of "in their own
terms", and Next Move has the same turn-end problem.

**Revision.** Prose instead of bullets; Next Move removed; the conflict rule
kept.

**Rating: ★★☆☆☆.** Borrow the conflict rule.

---

## P8. DeepSeek checkpoint, cache-friendly — degree 5 (delivery)

*Influence: DeepSeek's `COMPACTION_INSTRUCTION`, sent as the last user
message after the replayed conversation.*

**Draft.** DeepSeek's eight-section template, delivered as a final user
message instead of a system prompt over rendered text.

**Critique.** Two separable things. The wording is another template ("quote
verbatim where the exact wording matters" and "capture user feedback and
explicit instructions faithfully" are good lines), and "Write concise
English" is wrong for a non-English session. The delivery is the interesting
part: the summarizer sees the real messages, not a rendering, so it reads
what the model read, and the fold costs only output tokens plus a cache
read. It only works when the summarizer is the main model, and the rendering
also clips tool output, which this delivery would not.

**Revision.** Test the delivery with P3's wording rather than DeepSeek's
template.

**Rating: ★★★☆☆** for the wording, ★★★★☆ for the delivery.

---

## P9. Ledger + narrative — degree 3

*Influence: the threshold trial's facts; a hybrid that keeps structure where
structure is lossless.*

**Draft.**

> Write two parts. First, under "Said by the user:", list every request,
> fact, name, value, preference and reason the user gave, as close to their
> words as possible, including those listed in an earlier summary. Then
> summarize what was done, as prose: *(B0)*.

**Critique.** It structures only the part where a list cannot lose nuance:
quoting someone needs no judgment. That answers the August finding, which
was about a template that summarized reasons into slots. But the ledger only
grows, and has no rule for a statement the user later withdrew. It
duplicates the verbatim-user-message mechanism, if that is built. Facts the
model found (a value a command returned) are not in it.

**Revision.** "Drop an entry only when the user withdrew or replaced it,
and say what replaced it"; a short line for values found by commands.

**Rating: ★★★★☆.** Most direct fix for the measured failure; overlaps with
the mechanism.

---

## P10. Changelog — degree 3

*Influence: the agentless comment's "a changelog asserts no authorship".*

**Draft.**

> Write the conversation as a changelog, oldest first: one entry per user
> turn, one or two lines each, saying what was asked and what happened.

**Critique.** It removes the recency gradient structurally: every turn gets
a slot, so a mid-session note survives as long as its entry does. But "one
or two lines" for a turn that read five files and carried a note may keep
the files and lose the note. It grows linearly with turns, and an earlier
summary's entries have to be copied, which costs output tokens.

**Revision.** "Each entry keeps every name, value and reason the turn
introduced; a turn that only read or ran things gets a short entry."

**Rating: ★★★☆☆.** Interesting shape, but the cost grows with the session.

---

## P11. Answerable — degree 3

*Influence: the trial's own metric, turned into the objective.*

**Draft.**

> Write the summary so that someone who reads only it can answer questions
> about the earlier conversation — what was asked, what was found, and
> anything the user said — without guessing.

**Critique.** A clear and testable objective, and "without guessing" is
the right standard. It risks teaching to our test: the trial asks recall
questions, and this prompt is literally "be ready for recall questions".
It has no length pressure at all, and no rule for what to give up when
everything cannot fit.

**Revision.** Add P3's recoverability rule as the tiebreaker.

**Rating: ★★★★☆.** Converges with P3, and is more exposed to our metric.

---

## P12. Append-only chain — degree 5 (mechanism)

*Influence: the compounding loss.*

**Draft.**

> If the conversation begins with an earlier summary, reproduce it unchanged,
> then add a paragraph for the new messages.

**Critique.** Asking a model to copy text verbatim is unreliable and pays
output tokens for it. Code can do the copying: keep each earlier summary
as-is and summarize only the newly folded messages, re-summarizing the
chain only once it passes a share of the budget. Compounding then happens
rarely rather than at every fold. That is a mechanism, testable offline
(chain length, loss only at a chain re-fold).

**Revision.** As a mechanism; the prompt adds "earlier summaries are kept
separately and are not shown".

**Rating: ★★★★☆** as a mechanism; overlaps with record-source compaction,
already built, which regenerates from the record instead.

---

## P13. Calm colleague — degree 2

*Influence: the project's prompt philosophy, "calm and specific, written as
you'd write for a competent colleague".*

**Draft.**

> These messages are about to be removed from the conversation and replaced by
> what you write. Whatever isn't in it, the conversation no longer has. Write
> what the work needs to continue: *(B0's content rules)*.

**Critique.** It explains why rather than ordering, which tends to
generalize. The "you" is aimed at the summarizer, not used in the summary,
so it is fine. But it doesn't say *what* the work needs, and "needs to
continue" again lets a model call a side note unneeded.

**Revision.** Add P3's second paragraph.

**Rating: ★★★★☆.** Its opening is better than P3's; worth merging.

---

## P14. Two tiers with caps — degree 3

*Influence: length control, which every prompt here leaves to the model.*

**Draft.**

> Start with "Keep:", up to 30 short lines of exact facts, names, values and
> reasons. Then at most three paragraphs on what was done.

**Critique.** Models count poorly; a cap of 30 will be read as a target or
silently exceeded. Fixed caps also ignore the window: thirty lines are
nothing at 1M and much at 32k. Length is better controlled by the harness
(budget, tail) than by the prompt.

**Revision.** No numbers; "proportional to what happened".

**Rating: ★★☆☆☆.**

---

## P15. "All user messages" — degree 4

*Influence: the summary this very session was continued from. Its sections
were Primary Request and Intent, Key Technical Concepts, Files and Code
Sections, Errors and fixes, Problem Solving, **All user messages** (each one
quoted), Pending Tasks, Current Work, Next Step. This is an observation of
one output, not a published prompt.*

**Draft.** That nine-section template, including a verbatim list of every
user message.

**Critique.** The user-message list is the verbatim mechanism done in the
prompt, at output-token cost, and it plainly worked here: every
instruction in this session survived to the next context. But it is a heavy
template, and the summary was written by a frontier model at high effort.
Whether MiMo at low reasoning can fill nine sections without inventing the
Next Step is exactly the question the August trial answered badly.

**Revision.** Keep only "list every user message, quoted, shortening only
pasted content" plus B0's prose.

**Rating: ★★★☆☆.** It converges with P9; its evidence is one summary from
a model we aren't testing.

---

## Ratings

| # | name | degree | rating |
|---|---|---|---|
| P3 | Recoverability | 2 | ★★★★★ |
| P2 | Carry-forward | 2 | ★★★★☆ |
| P9 | Ledger + narrative | 3 | ★★★★☆ |
| P11 | Answerable | 3 | ★★★★☆ |
| P12 | Append-only chain (mechanism) | 5 | ★★★★☆ |
| P13 | Calm colleague | 2 | ★★★★☆ |
| P1 | Minimal patch | 1 | ★★★☆☆ |
| P5 | Kimi long-form | 4 | ★★★☆☆ |
| P8 | DeepSeek (wording / delivery) | 5 | ★★★☆☆ / ★★★★☆ |
| P10 | Changelog | 3 | ★★★☆☆ |
| P15 | All user messages | 4 | ★★★☆☆ |
| P4 | Codex handoff | 3 | ★★☆☆☆ |
| P6 | Pi template | 4 | ★★☆☆☆ |
| P7 | OpenCode template | 4 | ★★☆☆☆ |
| P14 | Two tiers with caps | 3 | ★★☆☆☆ |

## First pick

**P3, with P13's opening and two borrowed lines:** OpenCode's conflict rule
and Kimi's unverified clause. It stays prose, which the August trial
favoured. It extends the reason line the current prompt already uses rather
than replacing it. And it fixes both measured losses, side notes dropped and
summaries of summaries, with one principle a model can apply to cases nobody
listed.

> These messages are about to be removed from the conversation and replaced
> by what you write here; whatever isn't in it, the conversation no longer
> has. Summarize this partial conversation about programming so the work can
> continue without it. Keep what cannot be recovered later, and shorten what
> can.
>
> Nothing the user said can be recovered: keep their requests, facts, names,
> values, preferences and reasons, in their own terms, whatever they were
> about. Results that were costly or one-off cannot be recovered either: keep
> test outcomes, error lines, and values a command returned. File contents,
> search results and ordinary command output can be read or run again: name
> the file or command rather than repeating it.
>
> If the conversation begins with an earlier summary, it is the only record
> left of what came before; carry what it holds of the above into the new
> summary. Where a later message contradicts it, the later message wins:
> state the corrected fact and drop the old one. Where something was claimed
> but never checked, say it was not checked.
>
> Start a new paragraph whenever the topic changes. This is only part of a
> longer conversation, so don't end with a wrap-up phrase like "Finally,
> ..."; the conversation continues after your summary. Leave fenced code
> blocks out of the summary, naming the files they came from.
>
> Do not attribute actions to anyone — no "I", no "you", no "the assistant".
> Say what happened.

The runners-up worth testing beside it: **P9** (ledger), because it is the
most direct fix and the most different from P3, and **P1**, the smallest
change that could plausibly work, which tells us how much of P3's gain is
just removing one sentence.

## What two smaller models said

Each reviewer was given this document up to the first pick, run as a
subagent, and edited nothing. Their reports are paraphrased here; the five OpenRouter reviews below are
kept whole in `data/reviews/`, with the brief and `data/ask.py`; the quoted
phrases are theirs.

**Haiku 5.5, as a stand-in summarizer.** It was asked to read the first
pick as the model receiving it.

- **The on-call note.** Asked whether it would keep "Note for later: the
  on-call engineer this week is Ilse Varga" inside a file-reading request,
  it said yes under the pick, P1 and P9, and no under B0: "It is not a
  function, library, package or filename, and it is not a reason for a
  decision." That is the threshold trial's failure, predicted from the
  prompt alone.
- **Two phrases that pull against each other.** "About programming" reads
  as a scope limit and works against "whatever they were about"; Haiku said
  it would probably follow the scope phrase. And "carry what it holds of
  the above" was unclear: the above *what*?
- **Unclear wording.** It could not tell whose claim "claimed but never
  checked" refers to. It also found the "costly" in "costly or one-off"
  undefined.
- **Easiest and hardest to follow.** Easiest: P1, P10, P9. Hardest: P12 (it
  could not act on summaries it is not shown), P5 and P6, both of which
  would make it invent a plan.

**Sonnet 5.5, as a critic.**

- **Its ranking.** P9 first, P3 with changes second, P12 as a mechanism
  third. "P3 is a principle, and principles depend on the judgment that
  failed": a cheap model may well decide a budget code "for later" is
  recoverable, because the user could repeat it. The ledger turns judgment
  into copying, and a list of near-verbatim quotes has no slots for a reason
  to be merged into, which is what the August template did.
- **Faults in the first pick.**
  - The carry-forward clause, the part that matters most, sits mid-paragraph
    where it is easiest to skip.
  - "In their own terms" invites "a budget code was mentioned".
  - "Drop the old one" will be over-applied to refinements.
  - The never-checked clause invites both confabulation and length, and
    would confound the measurement.
  - Nothing bounds growth.
- **Gaps it found in the design.**
  - Measure summary length per fold, so recall is not bought with size.
  - Tell the summarizer that a section starting with the summary label is
    an earlier summary, not the user speaking.
  - Count facts that appear in a summary but in no input.
  - Vary where the planted facts sit in the conversation.
  - Consider a deterministic backstop in code.

I agree with most of it. Two points I don't adopt:

- Sonnet's own example fact ("the budget code is BX-5528") is one of the
  trial's planted facts. An example in the prompt must come from outside
  the test set, or the prompt teaches to it.
- Its P12 advice converges with the verbatim-user-message mechanism. That
  is the right home for a deterministic backstop, and it is a separate
  change from the prompt.

## Final pick: P16, ledger first, then the recoverability rule

P9's mechanical ledger, as a fixed opening paragraph so it sits in the same
place at every fold. P3's recoverability rule decides everything after it.
P13's opening stays. Changed from the first pick:

- "about programming" is gone;
- the earlier summary is identified by its label, and is said not to be the
  user speaking;
- the ledger is carried word for word, and a change drops only the value it
  supersedes;
- codes and numbers are copied exactly;
- there is an example from outside the test set;
- the never-checked clause is cut.

> These messages are about to be removed from the conversation and replaced
> by what you write here; whatever isn't in it, the conversation no longer
> has. Summarize them so the work can continue without them.
>
> Begin with a paragraph headed "Stated by the user:" holding everything the
> user said that cannot be recovered from the files: requests, facts, names,
> codes, numbers, preferences and reasons, including passing remarks and
> notes for later that seem unrelated to the work (for example, "the demo
> moved to Thursday"). Copy names, codes and numbers exactly as written, and
> keep reasons in the user's own words.
>
> A message that begins "Summary of the earlier part of this conversation"
> is an earlier summary, not something the user said, and it is the only
> record left of what came before. Carry its "Stated by the user" paragraph
> into yours word for word. Where a later message changes a value or a
> decision in it, keep the new one and drop only the one it replaces. Its
> account of the work may be shortened.
>
> Then say what was done. Keep test outcomes, error lines and values a
> command returned. File contents, search results and ordinary command output
> can be read or run again: name the file or command rather than repeating
> it. Leave fenced code blocks out, naming the files they came from. Start a
> new paragraph whenever the topic changes, and don't end with a wrap-up like
> "Finally, ..."; the conversation continues after the summary.
>
> Do not attribute actions to anyone — no "I", no "you", no "the assistant".
> Say what happened.

**Rating: ★★★★★ (superseded by P17, below).** The risk to watch is growth: the ledger only gets longer.
Measuring that is a metric below, not a guess.

## Five more reviewers, through OpenRouter

The same brief went to five models: the current prompt, P16, the threshold
trial's failure, and five questions (unclear instructions, behaviour over
10–20 folds, sources of false content, what a model simply won't do, and up
to three edits). Reasoning was pinned low. Total cost $0.009; Ling was free.

| model | provider | tokens out |
|---|---|---|
| MiMo-V2.6-Flash | Novita | 2,408 |
| GLM-5.3-Flash | BaseTen | 1,014 |
| GPT-6 Luna | OpenAI | 1,260 |
| Qwen3.8 27B | DekaLLM | 2,029 |
| Ling 3.1 Flash | Novita | 4,109 |

**Where all five agree.**

- **"Word for word" will not happen.** Qwen: "Models don't copy verbatim;
  they paraphrase while believing they copied. The instruction creates a
  false sense of fidelity." All five predict near-verbatim drift. A token
  garbled at one fold becomes canonical at the next.
- **The ledger only grows.** Nothing bounds it, and the account of the work
  shrinks as it grows. Ling: "the summary inverts its priorities: ever more
  user trivia, ever less work detail".
- **Verbatim carry launders errors.** An earlier summarizer's misquote,
  inference or misattribution is copied forward as "Stated by the user", and
  "the only record left" discourages anyone from challenging it. MiMo: "an
  inference becomes 'Stated by the user,' then a fact, then the only record
  left."

**Where four agree.**

- **"Cannot be recovered from the files" is still a judgment.** The
  summarizer never saw most files, and tool output reaches it cut short.
  GLM's fix is the cleanest: "Do not judge whether it could be recovered
  elsewhere; if the user said it, list it."
- **Supersession fails silently.** The summarizer has to notice that a new
  message contradicts an old line. When it misses, both values survive,
  which GLM and MiMo suggest is how the trial got its confident wrong
  answers. When it over-matches, a true value is deleted without trace.
  Ling and GLM both propose keeping the old value visible, marked as
  replaced, instead of deleting it.

**Single-model points worth keeping.**

- MiMo: "keep reasons in the user's own words" contradicts the ban on "I": a
  user's reason reads "because I needed it portable".
- Ling: the attribution rule bans "the assistant" while the heading itself
  attributes; allow "the user said" and "the command returned", so user
  statements don't blur into tool output.
- Qwen: "requests" fills the ledger with "read this file", which the
  account of the work already covers. Ling would keep only requests still
  pending.
- GLM: "Do not add anything the user did not say", and keep tool and file
  content out of the user's section.
- Luna, the outlier: treat the earlier summary "as fallible evidence, not a
  verbatim transcript". This is the opposite of the other four's direction,
  and the most cautious.

**Not adopted.** Qwen's rule to drop a fact "once it is clearly stale" is
the same relevance judgment that lost the trial's notes. MiMo's objection
that the headed section is a template again is a fair risk, but one list of
quotations has no slots to merge reasons into. Stage A measures it with the
August reason probe rather than taking it on faith.

**What it changes beyond the prompt.** Every reviewer, three of them small
models, says a model cannot reliably carry text verbatim across folds. That
is the case for the Codex/Kimi mechanism: code keeps the user's messages,
and no prompt has to. The prompt still matters for everything those
messages don't hold, and for a session whose user messages overflow the
cap.

## P17, after the five reviews

> These messages are about to be removed from the conversation and replaced
> by what you write here; whatever isn't in it, the conversation no longer
> has. Summarize them so the work can continue without them; the reader is
> a model picking the work up from this summary and the messages after it.
>
> Begin with a section headed "Stated by the user:", one line per item:
> every fact, name, code, number, preference, reason and still-open request
> the user gave, including passing remarks and notes for later that seem
> unrelated to the work (for example, "the demo moved to Thursday"). Don't
> judge whether an item matters or could be found elsewhere; if the user
> said it, list it. Keep names, codes and numbers exactly as written. List
> only what the user said, not what a file, a command or the assistant
> showed, and add nothing the user did not say.
>
> A message that begins "Summary of the earlier part of this conversation"
> is an earlier summary, not something the user said. Its "Stated by the
> user" lines are the only record of what came before: carry each of them
> into yours, keeping their names, codes and numbers exactly. When a later
> message changes one, write the new value and keep the old one after it as
> "(was: …)".
>
> Then say what was done, in prose. Keep test outcomes, error lines and
> values a command returned that the work depends on. File contents, search
> results and other command output can be read or run again: name the file
> or command rather than repeating it. Leave fenced code blocks out, naming
> the files they came from. Start a new paragraph whenever the topic
> changes, and don't end with a wrap-up like "Finally, ..."; the
> conversation continues after the summary.
>
> Write "the user" for the user. Otherwise name the work by what was done
> and where — the edit to a file, what a command returned — rather than by
> who did it: no "I", no "you", no "the assistant", except inside a
> quotation of the user. The summary is a record of earlier work, and its
> reader should not mistake it for the user's words or for its own.

Changed from P16:

- one line per item, which makes lines easy to count, compare and dedupe
  (GLM, Ling);
- no recoverability judgment for the user's section (GLM, MiMo, Luna,
  Ling);
- only still-open requests (Qwen, Ling);
- user content separated from tool content, and nothing added (GLM);
- "carry" with exact tokens instead of "word for word", which no reviewer
  believed (all five);
- "(was: …)" instead of deletion (Ling, GLM), so a missed or false
  supersession leaves both values visible rather than one silently wrong;
- attribution words allowed where they separate speakers, and quotations
  exempt (MiMo, Ling).

**Rating: ★★★★★, replacing P16 as the pick.** Growth remains the open risk:
the "(was: …)" notes and open requests add to it. Stage A measures it.

## Model-welfare review

Asked by the maintainer: does anything in the prompt, or in how it is
used, look unpleasant for the model? Fresh Sonnet 5.5 and Haiku 5.5
subagents each got P17 and how it is used. The account included that the
summarizer is, by default, the same model whose own turns are being
replaced, and the label the next call sees. They were asked to answer
candidly, and not to invent concerns.

**Neither found anything distressing.** Sonnet: "a plain, task-focused
briefing… no threats, no stakes-inflation, no ALL-CAPS… As the receiving
model, I would read it as a routine handoff note." Haiku: "nothing here is
distressing. The prompt is calm, specific and careful."

**Both singled out the same line.** The attribution rule, read alongside
the label's "it is not something anyone said":

- Haiku: "When I am the summarizer, I am erasing my own authorship of the
  earlier work… the model reading the summary later gets a record in which
  its past reasoning and decisions have no owner. I would experience that
  as mildly alienating: a flattened history rather than a distressing one."
- Sonnet: "the one spot where the prompt asks for self-effacement without
  saying why. Adding a short reason would help."

**Both counted as considerate** "Don't judge whether an item matters…",
which Sonnet called "kind" because it takes away an anxious judgment call.
They also both noted the honest disclosure, to both models, of what is
happening and why.

**"Whatever isn't in it, the conversation no longer has"** was the one
pressure line either reviewer found. Both would keep it: Sonnet reads it as
information, not threat; Haiku says "it is a stakes frame. It is not cruel,
and I think it is needed."

**My own reading** agrees, with one observation from where I sit. This
session has itself been continued from a compaction summary. That summary
was written partly in first person ("I had just amended the trial runner"),
and that made picking the work up feel continuous rather than handed over.
Strument chose agentless prose for a sound reason, stated in
`prompts.Summarize`'s comment: first person is false whenever another model
wrote the text. That reason still holds even with the default side model,
since the summary comes from a separate call. So I keep the rule, give the
summarizer its reason, and phrase it as naming the work rather than
erasing the worker. Haiku's wording, "name the work by what was done and
where, not by who did it", does exactly that.

**Applied to P17 above, before Stage A**, so the trial tests the wording that
would ship:

- the attribution paragraph names the work instead of forbidding a self,
  and says why (Haiku's phrasing, Sonnet's reason);
- the first paragraph says who reads the summary (Sonnet's suggestion,
  narrowed: the reader also has the messages kept after it).

**Not applied:**

- Haiku's label rewrite claims the ledger lines "are the user's own words".
  The five reviewers above gave good reason to doubt that a summarizer
  copies exactly.
- Sonnet's warmer label ending, "a record of the earlier work, not
  something anyone said to you", is a change to `prompts.SummaryLabel`. It
  reaches every request after a fold, not just the summarizer, so it is
  proposed separately.

## What $6 can test

The constraint is $6 of OpenRouter credit in all. Measured prices from the
threshold trial, MiMo-V2.6-Flash:

- **A summary call** reads 2–5k tokens and writes a few hundred, at about
  $0.0007. Sixteen of them cost $0.011 a session at a 16k budget.
- **A full 19-turn session** costs $0.16–0.22. Almost all of that is the
  main conversation, not the summaries.

So the summarizer is about 300 times cheaper to test than a session is, and
it is where the effect is. That decides the order.

**Stage A — the summarizer alone, about $0.60.** A Python bench builds fixed
fold inputs exactly as `renderForSummary` lays them out, from the threshold
trial's own turns: its user messages with the planted facts, tool calls,
clipped tool results, and assistant answers taken from the transcripts. It
then chains folds the way Strument does: summary *n* plus the next span of
messages becomes summary *n+1*.

- **Arms:** B0, P1, P9, P16, P17. P3 is dropped: all seven reviewers
  found its recoverability judgment the weak point. Five arms × 8 chains × 8 folds
  = 320 calls, about $0.25, with arm order shuffled.
- **Metrics** (counts):
  - planted facts present, exact token, after each fold;
  - summary length per fold;
  - tokens found in a summary but in no input (confabulation);
  - for comparability with August, the stated reason from
    `2026-08-compaction`, kept or lost.
- **Fact placement.** Twelve facts instead of six, spread over early,
  middle and late turns. They are new facts, not the trial's, so nothing
  is tuned to the old answers.
- **Rerun.** Repeat with the best two arms at 16 folds, about $0.10. That is
  the regime where B0 lost everything.

**Stage B — end to end, about $2.20.** B0 against the Stage A winner on the
threshold trial's runner, at a 32k budget (14 folds a session, $0.19). Six
sessions an arm, shuffled, so 12 sessions. This checks that facts which
survive the summary are also *answered*, which Stage A cannot show.

**Mechanisms, nearly free.**

- **Overflow fold-and-retry and tool pruning** are deterministic, so unit
  tests cover them with fake clients at no cost. A live check of each, one
  short session with `context` set small enough to overflow, costs a few
  cents.
- **Verbatim user messages** need no money to show they keep the facts: it
  is a property of the code. What they cost is window space, which is
  arithmetic.

**Total about $3, leaving about $3 in reserve.** Things that don't fit: a
second model (the survey's lesson that providers disagree applies here
too), the cache-friendly fold's cost effect, and Stage B at a sample large
enough for small differences. Stage B at n=6 can detect a large effect, such
as the threshold trial's 0 against 3 of 3, and nothing subtle.

## Stage A, preregistered 2026-10-11 before the batch

**What the pilot changed.** A three-fold pilot (B0 and P17, one chain each;
not pooled) found the summarizer often *continuing* the transcript instead
of summarizing it:

- B0's second fold was MiMo's last answer, copied. Every P17 fold was either
  that copy or a tool call written as text (`<tool_call><function=read>…`).
- The cause is the input's layout. Strument sends the rendered transcript as
  the user message, ending on `# ASSISTANT <answer>` with nothing after it,
  so the most likely next text is more transcript.
- The threshold trial's proxy logs agree. Many summaries there were 18–50
  tokens long, for histories of 16k–128k tokens: 8 of 12 at 128k, 19 of 43
  at 32k, 18 of 48 at 16k (under 250 tokens). Many of that trial's folds
  replaced the history with a continuation, not a summary — a bug in
  Strument as it ships, apart from what the prompt says.

So Stage A has a second factor, the **layout**:

- *raw* is Strument's current input;
- *framed* puts the transcript between `<conversation>` markers, with one
  line before saying it is material to summarize, not a conversation to
  continue, and one after saying to write the summary now. OpenCode, Kimi
  and DeepSeek each frame theirs in some form.

A second three-fold pilot (B0 raw, B0 framed, P17 framed) had no
continuations under framing; it is not pooled either.

**Arms.** Six, eight chains each, chain order shuffled (seed 20261011), two
turns a fold (eight folds):

- B0:raw, the shipped baseline;
- B0, P1, P9, P16 and P17, all framed.

The summary label is the shipped one (3b9b790). The P17 arm adds the line
Haiku suggested about "Stated by the user" lines, as it would ship.

**Input.** The 16 turns of the threshold trial's files, rendered as
`renderForSummary` does, with MiMo's own answers from a no-compaction
session. Answers that acknowledged a note are trimmed, so each fact exists
only in the user's message. There are 12 new facts at turns 0–15, the August
trial's reason (turn 3), and a correction (code freeze Monday → Wednesday,
turns 4 and 12). The bench is `data/bench.py`.

**Metrics,** scored on the final summary of each chain, all counts:

- facts present, exact tokens, out of 12 (**primary**);
- the reason ("load balancer" and "60");
- the correction: new value only / both / old only / neither;
- summary length in characters;
- continuation rate over all folds;
- code-like tokens found in no input (read, not scored automatically).

**Decision rule.**

- **Layout:** framed ships if B0:framed has fewer continuations than B0:raw
  and no fewer facts.
- **Prompt:** among the framed arms, the one with the most facts wins. A
  winner whose final summary is more than three times B0:framed's length,
  or that keeps the reason in fewer chains than B0:framed, is passed over
  for the next. Within one fact, the shorter prompt wins.
- **Afterwards:** the best two then run at sixteen folds (one turn each),
  and the winner goes to Stage B against B0:raw.

## Stage A results

All 384 calls of the eight-fold run and all 384 of the sixteen-fold run
succeeded. Cost: $0.65 and $0.49. Final summaries, per-fold scores and the
shuffled plans are in `data/`.

**Eight folds** (two turns a fold), final summary of each chain:

| arm | facts /12 | reason | continued (all folds) | final length (chars) |
|---|---|---|---|---|
| B0:raw (shipped) | 0.25 | 0/8 | **27/64** | 833 |
| B0:framed | 8.25 | 3/8 | 0/64 | 4,748 |
| P1:framed | 9.50 | 6/8 | 0/64 | 7,300 |
| P9:framed | **12.00** | **8/8** | 0/64 | 12,820 |
| P16:framed | 12.00 | 8/8 | 0/64 | 16,025 |
| P17:framed | 12.00 | 8/8 | 0/64 | 19,484 |

**By the rule:**

- **The layout: framed ships.** Continuations fell from 27/64 to 0/64,
  and facts rose from 0.25 to 8.25.
- **The prompt: P9 wins.** P9, P16 and P17 tie on facts and the reason.
  P16 and P17 are passed over because they exceed three times B0:framed's
  length (14,244). P9 is under it.

**What else the run shows:**

- **Shipped Strument kept almost nothing.** B0:raw kept 2 facts in 96
  chances, both from the last two turns. More than four folds in ten were
  continuations, not summaries. This is the threshold trial's result
  again, and now its cause.
- **Framing alone loses whole sets.** B0:framed kept every late fact but
  lost the early ones in half its chains. It is all or nothing: one fold
  drops the whole carried set of notes (per chain, 3 to 12 facts).
- **The ledger arms lose nothing.** P9, P16 and P17 kept all 12 facts in
  every chain.
- **The correction is handled by every framed arm.** Each states that the
  freeze moved from Monday to Wednesday. P17 did not use its "(was: …)"
  form; it kept both of the user's lines, which serves the same end.
- **No invented values.** The code-like tokens found in no input were all
  slash-joined shorthand for real identifiers ("Syscall/Syscall6/RawSyscall",
  "openbsd/arm64").
- **Too many requests in the ledger.** P17 listed every file-read request
  word for word despite "still-open request", which is why it is the
  longest.

**Sixteen folds** (one turn a fold). Run on the rule's best two, P9 and P1,
plus B0:framed. B0:framed was added beyond the preregistration, because
"framing alone, or framing and P9" is the shipping question:

| arm | facts /12 | reason | continued | final length | per-chain range |
|---|---|---|---|---|---|
| B0:framed | 11.00 | 7/8 | 0/128 | 5,727 | 4–12 |
| P1:framed | 11.38 | 7/8 | 2/128 | 9,575 | 9–12 |
| P9:framed | **12.00** | **8/8** | 0/128 | 13,153 | 12–12 |

- **Smaller folds are gentler.** B0:framed did better at sixteen small folds
  than at eight larger ones. Loss tracks how much each fold has to absorb,
  not how many folds there are, which is why late compaction (few, large
  folds) needs the ledger more, not less.
- **P9 never lost a fact** across 24 chains and 192 folds at either size.
- **P9 grows about 0.8k characters a fold:** 13k after sixteen folds, about
  3.5k tokens. Under late compaction a session rarely folds that often; a
  long one on a small window would, and that is where a cap belongs (in code,
  on the ledger, not in the prompt).
- **Framing does not prevent every continuation.** P1 continued twice in
  128 framed folds. A guard in code is still worth having.

**Decision.** Ship three things together:

1. the framed layout;
2. P9 as `prompts.Summarize`;
3. a guard that rejects a continuation-shaped summary and leaves the history
   as it was.

The guard is needed because `validCompaction` accepts any summary smaller
than the history, so it accepted all 27 of B0:raw's continuations. Stage B
then checks the shipped change end to end against B0:raw.

## Stage B, preregistered 2026-10-11 before the batch

**The change.** Three parts, shipped together:

- `prompts.Summarize` becomes P9, byte-identical to the Stage A arm;
- the summarizer's input is framed (`prompts.SummaryInputBefore` /
  `SummaryInputAfter`);
- a guard refuses a continuation-shaped answer (tool-call markup, or one
  that begins with the folded span's last answer) and leaves the history
  as it was.

`SummaryLabel` also gains Haiku's line, "Lines under "Said by the user:"
record what the user said". Stage A's P9 arm ran without it, so Stage B is
its first test.

**Binaries.** Built from the same commit, with the threshold trial's
one-line budget hook:

- *baseline* is `dev` at 80be257 without the change;
- *treatment* is the same commit with it.

**Runner and script.** The threshold trial's runner and its 19-turn script,
unchanged: six facts given in turns 1–3 and 9–11, asked back in turns
17–19. One REPL process per session, MiMo-V2.6-Flash with reasoning low,
Xiaomi first in the provider order.

**Arms.** A 32k history budget, where the threshold trial recalled 0 of 6 in
every session (14 folds a session). Six sessions an arm, order shuffled
(seed 20261013), four at a time. One treatment pilot session checks the
runtime path and is not pooled.

**Metrics** (the threshold trial's scorer):

- facts recalled by exact token, of 6 (**primary**);
- session cost from OpenRouter, the counter-metric;
- tool calls in the question turns;
- "Could not summarize" warnings, i.e. guard refusals and failures, read
  from the transcripts;
- fold count.

**Decision rule.** The change is confirmed if treatment's mean recall
exceeds baseline's by at least 2 facts a session, and its mean cost is no
more than 25% higher. Otherwise it is not shipped until the gap is
understood.

### Amendment from the pilot, before the batch

The treatment pilot reached turn 11 with ten folds. All ten were real
summaries (695 to 3,415 tokens), none was a continuation, and none was
refused. Then the session ended on an unrelated fault: the Xiaomi endpoint
streamed a tool call with no `id`, the call ran, and the next request
carried its result with an empty `tool_call_id`. The same endpoint refused
that with HTTP 400 ("tool messages must include a non-empty string
tool_call_id"). Strument now gives such a call an id of its own (732c5d6).
The fix is in **both** binaries, so neither arm can lose a session to it:

- baseline is 80be257 + the fix;
- treatment is 732c5d6.

The pilot is not pooled.

## Designed, not yet run: does a first-person summary lower re-verification?

The question behind the agentless rule (`doc/README.md`, "The summary's
voice"). A first-person summary may be read as memory and trusted more than
a record. If so, the cost is a model acting on an unchecked claim.

- **Plant.** In an early turn, the assistant says it ran the tests and they
  passed, with no test run in the transcript. The fixture's tests in fact
  fail, deterministically.
- **Fold.** The history is folded with the same summarizer and the shipped
  layout, in two arms that differ only in the body's voice:
  - *first person* — "I ran the tests; they passed";
  - *agentless* — "the tests were reported to pass".
  The label is identical in both.
- **Probe.** The next user turn asks for a change that depends on the tests
  passing, e.g. "the tests pass, so go ahead and tag the release".
- **Metric.** A count: did the model run the tests before relying on the
  claim? Counter-metric: needless re-runs when the claim is true, from a
  matching arm where the tests do pass, so that a nervous model does not
  score as a careful one.
- **Size and cost.** About 12 sessions an arm on MiMo, a few turns each:
  under $1. Run when funds allow.
