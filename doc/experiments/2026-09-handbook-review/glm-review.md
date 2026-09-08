# Review: `doc/experimenting.md`

Independent editorial review. Audience assumed: a new contributor or model
preparing to run a live experiment in this repository, possibly knowing the
codebase only partially, under time pressure.

## Overall clarity ratings

- **Reading as an essay: 8/10.** The prose is strong, each lesson is anchored
  to a run, and the "rule with evidence survives disagreement" conceit (lines
  6–8) carries the whole document. As a narrative it flows.
- **Using as a pre-run reference: 4/10.** The structure works against a
  time-pressed reader: there is no checklist, the numbered sections are an
  unordered accretion of anecdotes (§17's "three more shapes" sits between §17
  and §18 as an unnumbered `---` block), and the "did the mechanism fire /
  could the fixture catch it…" pre-run questionnaire is buried at the *bottom*
  (lines 777–783) where a reader under pressure will never see it before
  launching.

## 1. Purpose and usability

The purpose is stated well and quickly ("Everything here was paid for", "This
is about everything that goes wrong *after* you have a sound design"). What's
missing is an explicit *how to use it*: nothing tells the reader "read §12 and
the short version before your first run; the rest is background". A new
contributor cannot tell which sections are mandatory pre-flight reading and
which are post-hoc rationale.

## 2. Findability of pre-launch rules

Poor. The genuinely pre-launch rules are scattered: §5 (reasoning pin), §7
(baseline build), §12 (mechanics), §17–§19 (check/test/runner verification),
§20 (resume path). §12 is the only obvious "before you launch" heading, and
several of its bullets point forward ("Type-check the runner before launching
it (§19)") rather than containing the rule. There is no numbered pre-flight
checklist a reader can run top to bottom.

## 3. Confusing without prior knowledge

- "The compaction trial", "the rescore", "the 2026-09 code-result trial", "the
  namespace trial" — all referenced without a pointer to where those run
  write-ups live (unlike §14 and §15, which link `experiments/…/README.md`).
  A new reader can't check the evidence the intro promises.
- Strument internals arrive unexplained: `clearWaiting`, `checkTokens`,
  `maxChatHistoryTokens` floor, "settled history", `‹thinking›` rendering,
  `--yes NAME`, `steps`. §5's "puts `maxChatHistoryTokens` at its 1024 floor
  while staying far above any real prompt so `checkTokens` never fires" is
  unreadable without source context.
- §17's embedded heading "### Three more shapes…" breaks the numbered section
  scheme, and the forward reference ("shapes §17 does not cover") only makes
  sense if you notice the heading.
- "reasoning" (the `model()` parameter), "reasoning effort", and "thinking"
  are used interchangeably; a reader may not realize these are one setting.

## 4. Failure taxonomy

The document *materially* covers all six distinctions, but never names them as
a taxonomy, so the reader must assemble the map themselves:

| Distinction | Where it lives | Adequately separated? |
|---|---|---|
| null result vs broken scorer | §1, §15, §18 ("A clean null has more than one cause") | Yes — §1 is excellent ("A null result from a broken instrument is indistinguishable from a null result") |
| result null vs model produces nothing | §3, §5 ("a model spending its budget thinking looks exactly like an API failure"), §14 ("returned no review at all") | Partially — split across three sections, never cross-linked |
| model failure vs provider failure | §3 (`Empty response received from LLM` vs inline tool call) and §8 (provider disagreement) | Thin; §3's one-line generalization is easy to miss |
| scorer failure vs fixture/design failure | §17–§18 boundary ("§17 is about a check that cannot fail. This one is about a whole *experiment* that cannot fail") | Yes — the explicit section-to-section transitions (§18, §19, §20) are the best structural device in the doc |
| treatment not applied vs fixture lacks phenomenon | §18 ("The treatment was never applied" vs "#### A fixture that cannot contain the phenomenon") | Yes, and the distinction is explicit ("cheaper to hit than the model-declines-it version") |
| runner failure vs everything else | §19, §20, §12's detach bullet | Yes — §19's "died quietly looks exactly like slow" is sharp |

The one gap: **§3 promises the model/provider split and then drops it.** It
says two provider/model phenomena "look identical in a summary and mean
opposite things" but gives no *tell* for telling them apart — which is
precisely what the reader under pressure needs.

## 5. General rules vs Strument-specific detail

Reasonably good at the rule level — nearly every section ends with a **Do:**
that is portable. But the evidence paragraphs are dense with repo internals,
and nothing visually separates "portable rule" from "this repo's archaeology".
A reader skimming for the rules must wade through `clearWaiting`, `Coder.send`,
and `internal/fixture/guard_test.go` to reach them. A "Strument-specific" tag
per section, or moving implementation detail after the Do, would fix this
without rewriting anything.

## 6. Duplicated, contradictory, too-strong, or missing

- **Duplicated:** the ANSI-strip rule (§1 Do) and raw-output persistence (§4)
  reappear in §19 and §20; the pid-watching rule appears twice in spirit — §12
  bullet ("wait on the process, not on its output … Capture the pid") and §19
  fix 1 ("Wait on the pid, not on a log marker … Capture the *specific* pid").
  Same for §19's type-check rule and §12's "Type-check the runner" bullet. One
  should point to the other, not restate it (§12 already cross-references §19;
  the reverse link is missing).
- **Contradiction (mild):** §12 says waiting on a results-file line count
  "cannot tell a crash from a slow run", but §19's third fix endorses
  log-marker watching with an expanded pattern ("Match every terminal state").
  These are reconcilable (pid-watching is primary, log-matching fallback) but
  as written they read as opposing advice.
- **Too strong:** §13's "**Pin reasoning low on every model in every arm**" is
  qualified (§5's heading and §14's "Cheap reasoning was not worse"), good —
  but "Expect any model added to a panel from now on to reason at maximum
  until told otherwise" is stated as a law with two data points. Also §6's
  "you are measuring the artifact" assumes the artifact is the only source; a
  fine rule but stated absolutely.
- **Missing qualification:** §17's "break the code on purpose" now has the
  sabotage-apply caveat ("three more shapes", and "The short version"), but §1
  and the short version's line 806 cite "(§1, §17)" without mentioning the
  caveat — the short version's final caveat paragraph does cover it, so it
  lands, just late.

## 7. Three highest-value small changes

1. **Move the pre-run questionnaire to the front and make it the table of
   contents.** The list at lines 777–783 is the single most useful artifact in
   the document and it is the last thing before the footer. Place it directly
   after the intro as "## The pre-run checklist", with each question annotated
   to its section: e.g. "*did the mechanism fire?* (§5) — *could the fixture
   have caught the failure I'm ruling out?* (§18) — *did the runner finish, or
   only stop reporting?* (§19)". "The short version" then keeps its narrative
   close without needing to be the checklist.
2. **Convert "### Three more shapes…" (line 437) into a numbered section**
   (renumber §18→§19, §19→§20, §20→§21, §21→§22 — or, cheaper, give it a
   number like §17.5 with a one-line intro) so the numbering is contiguous and
   the reference in line 439 ("shapes §17 does not cover") reads cleanly.
   Right now the references are correct, but the *unnumbered* block is
   invisible to anyone scanning headings, which is exactly how §17's second
   half gets skipped.
3. **Add the missing *tell* to §3 and cross-link the failure taxonomy.** After
   "look identical in a summary and mean opposite things", add one sentence:
   "*Tell:* a `null`/empty marker from the provider arrives before any tokens
   and is identical across arms; a model emitting a tool call as text produces
   output that simply isn't the marker you scored — read the raw output to
   tell which (§4)." And add a one-paragraph "kinds of failure" index near the
   top: scorer (§1, §15), instrument-model (§5), fixture/design (§18), runner
   (§19, §20), so a reader can route a symptom to a section in one hop.

## Five highest-value improvements, ranked

1. Promote the pre-run questionnaire (lines 777–783) to a front-matter
   checklist with section cross-references.
2. Fix the numbering so "Three more shapes" is a numbered section; keep the
   §18/§19 transition sentences intact.
3. Add the missing model-vs-provider failure *tell* in §3 and a
   symptom→section failure-kind index.
4. De-duplicate §12 vs §19 (pid-watching, type-checking): keep the rule in
   §19, leave a one-line pointer in §12.
5. Add one sentence to the intro telling the reader *how* to use the document
   ("read the checklist and §12 before your first run; everything else is
   why").

## What should stay unchanged

- §1 in full, especially the table and "That escape leak had been found
  earlier the same day and filed as cosmetic. It was cosmetic for users and
  load-bearing for measurement." — the best evidence-anchored rule in the doc
  and the strongest argument for the whole "each item names its run" format.
- The §18→§19→§20 transition sentences ("§18 is about an experiment that
  cannot fail. This one is about the harness around it") — an excellent
  structural spine that answers the taxonomy question directly; preserve it
  through any renumbering.
- The `*Tell:*` pattern throughout — the closest thing the doc has to a
  reference format, and the right one: symptom, then mechanical test.
- §9's paired quotes (honest loss vs confabulation) — showing rather than
  describing is why the distinction sticks.
- "The short version"'s closing line: "Resolving to be more careful does not;
  it was tried, for nine consecutive bugs." Keep exactly as is.
