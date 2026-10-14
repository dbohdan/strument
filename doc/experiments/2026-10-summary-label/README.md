# Does the summary label keep models from searching the record?

**2026-10-14. Preregistered; results below the line. Not shipped: on Ling 3.0 the label gained +0.67 true facts per session (bar 1) and one fewer wrong answer (bar 2); MiMo unchanged.**

## Why

In `2026-10-recall-models`, every `roll+recall` session searched the session
record in its question turns. Among `summary+recall` sessions, Ling 3.0 never
searched and never even mentioned the tool. The transcripts suggest why. The
summary's label says "It replaces those messages", and Ling 3.0 read it
literally: s23 reasoned that the messages "are not available to me anymore"
and answered "don't know". The roll note says instead that the rest of the
turns is in the record. The summary note (`--recall-note`), a paragraph after
the summary, got Ling 3.0 to search (22/24 against 8/24). Every note session
that searched quoted it first, so the note worked by overruling the label.

This trial changes the label itself. When `recall` is offered, "It replaces
those messages" becomes where they are: it "stands in for those messages
here; the messages themselves, including your own replies and the output of
every tool call, are kept in the session record, and the recall tool
searches it." The new label carries no instruction to search. The question is
whether correcting what the label claims is enough, without the note's
"search the record before deciding you don't know".

## Design

**Binary.** `dev` at 11a5385 plus `data/trial.diff`:

- the 4,000-token history budget hook from `2026-10-recall-models`, so every
  session compacts;
- `prompts.SummaryLabelRecord`, used when `STRUMENT_TRIAL_SUMMARY_LABEL=record`;
- recognition of either label as a summary. The control arm's text and
  behavior are those of `dev`.

At 11a5385, `recall` is offered by default. A hit id passed as a pattern
opens the message, a search names the replies its cutoff hid, and each
compaction is recorded with its text. Both arms have all of these.

**Arms:**

- `summary+recall`: the current label;
- `summary+recall+label`: the new label.

**Script** (`data/runner_label.py`): `2026-10-recall-models`'s runner with the
two arms, including its fix for turns a provider error cut short. The fixture,
the turns and the question wording ("do not read the files again or run
commands") are unchanged. Sessions run `--unattended` with `--yes steps`.

**Models**, each a stratum and never pooled:

| model | where | context | sessions per arm | concurrency | seed |
|---|---|---|---|---|---|
| Ling 3.0 Flash VL | OpenRouter, pinned to DeepInfra | 131,072 | 6 | 2 | 20261031 |
| MiMo-V2.6-Flash | OpenRouter, pinned to Xiaomi | 262,144 | 3 | 4 | 20261032 |

Reasoning is "low" in every config, and `retry_timeout` is 600 seconds. The
first pilot lost a turn to DeepInfra 429s that outlasted the default minute;
about 20% of single small requests are refused, and more under load, hence
also concurrency 2. Ling 3.0 is on DeepInfra because Novita,
which served it in `2026-10-recall-models`, no longer does. This is a new
stratum, not comparable with that trial's Ling 3.0 numbers. Arms are shuffled
within each model. A session that fails on a provider error is rerun in
place and reported.

**Metrics**, as in `2026-10-recall-models` (`data/scorer_recall.py`):
derived facts of 4 (true, "don't know", wrong); user facts of 6; sessions
that called `recall` in the question turns; calls; cost.

## Decision rule

**Ling 3.0 decides.** Its summaries lose facts, so there is room to move.

- **The label ships** if `summary+recall+label`'s mean of true derived facts
  per session is at least 1 above `summary+recall`'s, or its wrong answers
  are at least 2 fewer.
- **Veto:** it does not ship if its wrong answers exceed the control's by
  more than 1.

**MiMo is the counter-check,** reported and not decided on. Its summaries were
already at the ceiling, so the risk there is a new wrong answer or a search
that should not have been needed. Any MiMo wrong answer in the label arm is
read in full and reported.

**Pilot:** one session per arm on Ling 3.0, unscored. It confirms that the
label reaches the model, that `recall` works, and that compaction records
are written.

---

## Results

**Not shipped.** On Ling 3.0, the new label raised the mean of true derived
facts from 2.33 to 3.00 per session (+0.67, against a bar of 1) and cut wrong
answers from 2 to 1 (against a bar of 2). It points the right way and clears
neither bar. MiMo is unchanged.

### Ling 3.0 Flash VL (DeepInfra)

12 sessions at $1.28. Four (s02, s03 and s11, `summary+recall`; s05, the
label arm) failed when a summary request ran out of retries on DeepInfra
429s. Summary requests keep their own shorter retry ladder, which
`retry_timeout` does not extend. All four were rerun in place on the second
pass, with their failed transcripts kept in the scratch archive. No session
was flagged after that.

| arm | n | user facts | derived, true | "don't know" | wrong | sessions searching in the questions | `recall` calls |
|---|---|---|---|---|---|---|---|
| `summary+recall` | 6 | 34/36 | 14/24 | 8 | 2 | 2 of 6 | 12 |
| `summary+recall+label` | 6 | 36/36 | 18/24 | 5 | 1 | 4 of 6 | 28 |

- **Twice as many sessions searched with the new label.** The four label
  sessions that searched answered 15 of 16. The two control sessions that
  searched answered 6 of 8.
- **No wrong answer came from a turn that searched.** Control s07 searched
  in turn 20 and answered both questions right. Its wrong answer came in
  turn 19, where it did not search and gave the longest file of another
  batch. The other two wrong answers (control s10, label s00) were answered
  from the summary without a search.
- **Two control sessions searched at all, against none in
  `2026-10-recall-models`.** This stratum is a different provider, and the
  binary now names hidden replies by id. So the control is not the
  earlier trial's `summary+recall`.

### MiMo-V2.6-Flash (Xiaomi)

6 sessions at $0.70, none failed.

| arm | n | user facts | derived, true | "don't know" | wrong | sessions searching in the questions |
|---|---|---|---|---|---|---|
| `summary+recall` | 3 | 18/18 | 10/12 | 1 | 1 | 0 of 3 |
| `summary+recall+label` | 3 | 18/18 | 10/12 | 1 | 1 | 0 of 3 |

No MiMo session searched under either label. The label did not cost
anything here, and it did not help either.

**The one wrong answer in the label arm (s00), as the preregistration
requires, read in full.** This was the first trial with compaction records,
which keep each summary's text, so the answer could be traced to its source.
The summary kept the right counts. It listed each batch's files and line
counts as one group of a flat list. The turn-4 batch's group read
"zsysnum_linux_mips.go 450, … ztypes_darwin_amd64.go 878". It stood next to
the previous batch's group, "zsyscall_linux_mips.go 653,
zsyscall_linux_ppc.go 658". The model took zsys**call**_linux_mips for
zsys**num**_linux_mips and answered "zsyscall_linux_ppc.go, 658". The
control arm's s03 made the same mix-up and said it did not know. It weighed
`recall` against "do not run commands" and decided not to search.

### Reading

The label moves Ling 3.0 the way the transcripts of `2026-10-recall-models`
predicted, but by less than the preregistered bar. With `compaction = "roll"`
now the default, the label only reaches sessions set to `"summary"`, and the
question it answers matters less than when it was posed. It stays in the
trial build. If summary mode is kept, the next thing to try is a label that
also says when to search: Ling 3.0's note sessions quoted exactly that
instruction, and the label does not carry it.

**Pilot.** One session per arm, unscored, first with the default retry
budget, which lost a turn and was restarted with `retry_timeout = 600`. In
the label session's second question turn, the new "Not shown, your own
replies" header named `t5.12`. The model opened that message by id and
answered correctly. A call by id printed `Recall ""`, which is fixed in
51c64b5.

**Infrastructure.** A Unix socket path is limited to 108 bytes. With a
scratchpad this deep, `nitroctl down label-ling30` could not bind its
notification socket, so the stratum's own "take the service down" failed. It
was restarted every two seconds for 20 minutes, rerunning nothing, since all
sessions were done. `NITRO_SOCK` behind a short symlink fixes it.
