# Anchored edits, phase 2: the indent column, and what the combination breaks

Follows [phase 1](2026-09-anchored-edit-phase1.md), which built anchored edits
(arm D) and found them winning first-try edit success outright while losing the
trial: 30 of 72 outputs came back misindented, because anchoring removes the
line matcher that used to repair whitespace drift.

Arm E adds yoneda's indent column on top — the model names its indentation
rather than typing it, and a name that does not parse is refused. Run to see
whether the combination behaves, and it did not, in a way worth having found.

72 runs an arm, same fixtures and models, shuffled. **$0.2885 + $0.2475.**
Data: [`phase2-armE-results.jsonl`](2026-09-anchored-edit-data/phase2-armE-results.jsonl),
[`phase2-armE-prime-results.jsonl`](2026-09-anchored-edit-data/phase2-armE-prime-results.jsonl).

## Result

| arm | correct | first-try edit | gofmt-clean | parses | steps | input | cost |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **A** today | **72/72** | 83.2% | **72/72** | **72/72** | 4.58 | 36k | $0.188 |
| **D** anchors | 64/72 | **100%** | 42/72 | 70/72 | 5.57 | 79k | $0.301 |
| **E** + indent column | 67/72 | **100%** | 65/72 | 71/72 | 5.17 | 52k | $0.289 |
| **E′** + the fix below | 70/72 | **100%** | 68/72 | **72/72** | 5.50 | 56k | $0.248 |

The column does what phase 1 predicted it would. Formatting recovers from 42/72
to 65/72, the anchor-text-written-into-source failure disappears entirely — a
three-column row echoed back produces an indent that does not parse, so the
strictness catches it — and first-try edit success stays at 100%.

**It still does not beat arm A**, on anything except the metric it was built for.

## What the combination broke

Arm E's residual formatting failures were not the model failing to name
indentation. They were the model naming it *and typing it as well*:

```
sent:   "3 tabs\t\t\treturn nil, fmt.Errorf(...)"
landed: "\t\t\t\t\treturn nil, fmt.Errorf(...)"
```

Correct in the column, correct again in the text, six tabs on disk. My parser
validated the name and then concatenated it with a text field that already
carried the indentation.

That is worse than having no column at all. Without it a model that types
whitespace is repaired by the line matcher; with it, a model that does both is
silently doubled, while believing it has been explicit. The fix is one rule
yoneda's own grammar implies and I did not enforce: **the text column never
begins with whitespace**, because read strips it into the column. Arm E′ refuses
those rows.

E′ recovers 3 of the 5 wrong outcomes and both non-parsing files.

## What is left, and why arm A still wins

Two things the column cannot fix.

**It removes the ability to garble indentation, not the ability to get it
wrong.** "3 tabs" where the file wants 2 is well-formed and accepted. Four of
E′'s remaining defects are that.

**It costs more work, not just more tokens.** 5.50 steps against arm A's 4.58,
56k input against 36k. Naming indentation is a second thing to get right on
every row, and models spend calls restating rows. `xiaomi/mimo-v2.5` accounts
for four of arm E's five wrong runs and in one made *no edit call at all* — the
grammar cost it more than the ambiguity it was avoiding.

## The honest caveat

**E′ is fitted to its own test set.** The whitespace-doubling rule was written
from arm E's failures on these exact fixtures, then evaluated on them. Its 70/72
is optimistic and should not be compared with A's 72/72 as though both were
pre-registered. What E′ legitimately shows is that the failure mode is real and
has a fix; what it cannot show is the rate after that fix on unseen work.

Three fixtures, one language, 72 runs an arm, and Go's `if err != nil` is
unusually repetitive.

## Verdict

Both settings stay off. `anchored_edits` and `indent_column` are built, tested
and documented; this is the second measurement saying not to turn them on.

The trial was still worth running for the reason it was asked for. The
combination surfaced a defect that no unit test would have produced, because it
needed a model that trusts a grammar and hedges anyway — and it closes the
question the phase 1 writeup left open. yoneda's design is coherent: the column
really is the safety net anchoring removes, and implementing it faithfully gets
most of the way back. It just does not get past a baseline that was already at
72/72, on a workload where the ambiguity anchors eliminate costs round trips
rather than outcomes.
