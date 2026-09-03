# Anchored edits, M1: what actually makes an edit fail — and a bug it found

Pre-registered in
[`2026-09-anchored-edit-preregistration.md`](2026-09-anchored-edit-preregistration.md);
[phase 0](2026-09-anchored-edit-phase0.md) and
[M9](2026-09-anchored-edit-m9.md) precede this.

M9 measured first-try edit success at 100% and said so with a caveat: its three
fixtures were single, obvious, uniquely-located edits, so a perfect score was a
property of the fixtures rather than a finding about the world. This is the
harder set — repeated code, a 435-line file, an idiom appearing three times —
built so an edit *can* fail.

72 runs, arm A only, shuffled, 4 concurrent. **$0.1905.** Data:
[`m1-results.jsonl`](2026-09-anchored-edit-data/m1-results.jsonl), one
transcript kept as
[`m1-luna-repeats-0.jsonl`](2026-09-anchored-edit-data/m1-luna-repeats-0.jsonl).

## Result

**M1 first-try edit success: 86.0%** — 16 failures in 114 edit calls, against
100% on the easy set.

| cause | count |
| --- | --- |
| **ambiguous** — the text occurs more than once | **13** |
| not found | 3 |

Ambiguity is 81% of all edit failures. That matters for arm D specifically:
an anchor names one line, so ambiguity cannot occur under it. This is the
failure mode anchored editing eliminates by construction, and it is almost the
whole of the failure.

| fixture | edit calls | failed |
| --- | --- | --- |
| `bigfile` (435 lines, windowed read) | 48 | 0 |
| `repeats` (3 near-identical handlers) | 30 | 5 |
| `dense` (one idiom, 3 copies) | 36 | 11 |

Reading a long file is not what makes edits fail. Repetition is.

## The arithmetic, with measured inputs

Phase 0 said one avoided retry is worth ~1,543 anchored lines, which made arm D
look unconditionally worth it. That was the value of *one* retry; what matters
is the rate, and now both sides are measured.

- Ambiguity failures: 13 of 114 calls, over 72 runs = **0.18 retries avoided
  per run** if anchors removed all of them.
- **Lines read per run: median 49**, mean 118, p90 435 — far below the "a turn
  that reads 300 lines" the pre-registration assumed.

Break-even lines a run may read before arm D's extra input outweighs the
retries it saves:

| model | D (tab-separated) | D (`║` separator) |
| --- | --- | --- |
| `xiaomi/mimo-v2.5` | 620 | 279 |
| `deepseek/deepseek-v4-flash-0731` | 631 | 284 |
| `z-ai/glm-5.3-flash` | 639 | 287 |
| `tencent/hy3` | 649 | 291 |
| `openai/gpt-5.6-luna` | 678 | 304 |
| `qwen/qwen3.8-27b` | 678 | 304 |

At the observed median of 49 lines, tab-separated arm D sits an order of
magnitude inside its budget; at p90 it is still inside. With yoneda's `║` it is
inside at the median and marginal at p90 — the separator is the difference
between comfortable and marginal, for a character that carries no information.

**So arm D is worth building, tab-separated.** Phase 0's framing survives, but
the margin is roughly 10× rather than the 30× the one-retry figure implied.

## The bug this found

Two of 72 runs finished **silently wrong**: luna rewrote `GetUser` when asked
to change `GetOrder`, and was told the edit succeeded. One of them made a single
edit call with no failure at all.

The cause is a hole in the ambiguity guard. `coder/tools.go` refuses an edit
whose search text occurs more than once — but it counts occurrences of the
**raw** text, and when the model's indentation is wrong that count is *zero*.
The edit then falls through to the line matcher, which scanned for a match and
**took the first one it found**, across three identical blocks.

```
luna sent      '\t\tif !ok {'      (two tabs)
handlers.go    '\tif !ok {'        (one tab, in three places)
```

Exact-count says "not ambiguous". The fuzzy tier says "found it" — three times
over, and picks one. That is precisely what the guard's own comment says it
exists to prevent: *"a harness returning success on an underconstrained
transformation, leaving the model reasoning from a false local success."* The
guard covered the exact path and not the fuzzy one, which is where the reasoning
applies most.

**Fixed**: `perfectReplace` and `replacePartWithMissingLeadingWhitespace` now
scan every position and refuse when more than one matches, an ambiguous result
stops the matching ladder rather than falling through to a looser rung, and the
tool result names this failure so the model disambiguates rather than hunting
for a typo.

This is a deliberate divergence from aider, which replaces the first match.
Strument had already made that choice for the exact path; the fuzzy path was
left behind.

**Verified against the case that found it** — the same 4 luna runs on
`repeats`, before and after
([`m1-verify-after-fix.jsonl`](2026-09-anchored-edit-data/m1-verify-after-fix.jsonl)):

| | correct | silently wrong |
| --- | --- | --- |
| before | 2/4 | **2/4** |
| after | **4/4** | 0 |

## What this does not settle

Anchors would have prevented the *ambiguity*, but not necessarily this outcome:
if a model picks the anchor of the wrong line, an anchor is just as wrong and
just as confident. Addressing schemes fix "the model could not express the
location uniquely". They do not fix "the model chose the wrong location". Here
the two coincided; they will not always.

Correctness was scored from the files on disk. 70 of 72 runs produced the
correct result before the fix; the 2 exceptions are the bug above. Three
fixtures cannot say how ambiguity rates vary across real codebases — Go's
`if err != nil` idiom is unusually repetitive, and the `dense` fixture leans on
that deliberately.
