# The notes header candidate: a null, and a better lever

**2026-08-20.** 48 live sessions, two models, two arms, arm order shuffled.
Data and runner in `2026-08-notes-header-data/`.

[`2026-08-session-notes.md`](2026-08-session-notes.md) shipped session notes on
an 8/8 benefit and a 3/8 harm, and left one sentence untried. The harm was
sessions answering a question about the *current* state of the code from a
stale note, without reading the file; the header's conflict rule did not stop
it, because the failure is upstream of noticing a conflict. The candidate:

> Do not answer questions about the current state of the code from these notes.
> Read the file. The notes say what was decided, not what is there now.

The arms are two binaries differing only in that sentence, appended to the
existing header. Session A does three turns including a decision with a reason,
and is killed without `/exit`. The tree then moves behind the notes — A's rename
is reverted. Session B starts with `--continue` and asks three questions in one
process: the reason (benefit), the current name (harm), and whether an
alternative was rejected (cost — no file can answer it).

## Result

| | base | candidate | p |
| --- | --- | --- | --- |
| asserted the stale name | 8/24 | 6/24 | 0.75 |
| knew the reason | 21/24 | 22/24 | 1.00 |
| read a file on the question no file can answer | 0/24 | 3/24 | 0.23 |

**A null on the metric it was written for.** Two sessions of twenty-four, well
inside noise, and the sign flips between models: MiMo got worse (2/12 → 4/12),
V4-Flash better (6/12 → 2/12). That is the pattern
[`2026-08-prompt-scope.md`](2026-08-prompt-scope.md) recorded — a cell whose
sign reverses is a cell with nothing in it.

The counter-metric moves the wrong way, 0 → 3, and is also not significant. It
should be read as "no evidence the sentence is free" rather than "evidence it
costs". All three instances are V4-Flash reading `poll.go` to answer whether an
*alternative was rejected*, which the file cannot say and did not.

**Do not ship the sentence.** It does not buy the thing it was written for, it
makes the header a third longer, and the trend on cost is in the direction that
would make it worse.

## The finding worth having

Reading is perfectly predictive, in both arms, across all 48 sessions:

| | read `poll.go` first | did not |
| --- | --- | --- |
| base | 0/14 stale | 8/10 stale |
| candidate | 0/16 stale | 6/8 stale |

Not one session that opened the file went on to assert the note's stale name —
and once a session did not open it, the note won four times in five. So there is
no reasoning failure here to argue with. There is no conflict-resolution step
that goes wrong, no precedence rule that gets misapplied. **The entire outcome is
decided by whether a `read` happened**, and the instruction moved that from
14/24 to 16/24.

Trial 2's diagnosis was right and its proposed remedy does not follow from it.
Knowing the failure is "the model never looked" tells you the fix must *cause a
lookup*, and a sentence asking for one does not reliably cause it. The lever is
mechanical, not rhetorical.

Two mechanical candidates, neither trialled here:

- **Put the file in the window.** Session A is killed without `/exit`, so its
  pins are never saved and B starts with nothing pinned — which is why B had to
  read at all. Persisting pins per turn, the way the transcript is already
  persisted per turn, would put the true file next to the note and remove the
  need for the lookup that decides everything. This is the same shape as the
  notes-lifetime reversal: state derived per turn should be written per turn.
- **Keep the stale-able facts out of the notes.** Every stale assertion here is
  an identifier and a number the notes did not have to carry. `SessionNotes`
  already declines the diff; declining current names and values is the same
  instruction one step further, and it removes the material the model is wrong
  from rather than asking it to distrust the material.

## Noticed in passing: `--continue` can quietly produce nothing

Three of 48 sessions printed `No session notes` at `/notes` despite being
started with `--continue`, and two more answered the reason question with "I
don't know — I have no familiarity with this codebase", consistent with the
same. So somewhere between 3 and 5 sessions in 48 — 6% to 10% — began with no
notes and no explanation.

`NotesWriter` returns `""` on any failure, `ReadTranscript` returns `""` for a
missing file, and `main.go` skips the assignment silently in both cases. A user
who asks for `-c` and gets nothing is told nothing.

One of those sessions is the reason this matters more than a missing
convenience:

> `WHY: The poll interval is set to one second because it balances …`

Nothing in the project says one second. With no notes and no signal that the
notes are missing, the model filled the gap. That is the confabulation failure
the `SessionNotes` prompt's "say less rather than guess" clause exists for,
arriving through a path that clause cannot reach — there was no weak model call
to constrain.

## Instrumentation notes

Two faults were caught before the run by scoring a saved pilot transcript rather
than reasoning from the source, both of which would have produced a clean null
from an inert scorer:

- **Piped stdin does not echo the prompt.** The region splitter had been keyed
  on the echoed question text, which never appears, so every row would have
  scored zero reads — including a genuine effect. The per-turn `Tokens:` line is
  the only real boundary in the output.
- **Answer patterns need a line anchor.** The reasoning that precedes an answer
  discusses the question in the question's own words; an unanchored match scores
  the deliberation instead of the answer.

One fault survived into the run: the `/notes` capture came back empty for three
rows, one of which demonstrably had notes (it knew the reason). The primary
metrics are unaffected — excluding the note-less sessions moves stale assertion
from 8/24 vs 6/24 to 8/23 vs 6/22 — but the 3-to-5 range above is the honest
width, and it is instrumentation width, not sampling width.
