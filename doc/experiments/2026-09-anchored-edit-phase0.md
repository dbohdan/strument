# Anchored edits, phase 0: what the read formats cost

Pre-registered in
[`2026-09-anchored-edit-preregistration.md`](2026-09-anchored-edit-preregistration.md).
No models were called and nothing was spent: rendering a corpus in each arm's
read format and tokenizing answers the input half of the token question on its
own.

Runner: [`phase0_tokens.py`](2026-09-anchored-edit-data/phase0_tokens.py).
Results: [`phase0-go.json`](2026-09-anchored-edit-data/phase0-go.json),
[`phase0-python.json`](2026-09-anchored-edit-data/phase0-python.json).

## Result

Strument's own Go, 278 files, 65,061 lines. Tokens per line, and the overhead
each format adds over what `read` prints today:

| format | | o200k | qwen |
| --- | --- | --- | --- |
| **A** | today: `12⇥func f() {` | 11.51 | 13.15 |
| **C** | + indent column | 13.84 (**+20.2%**) | 15.48 (**+17.7%**) |
| **D_tab** | + anchors, tab-separated | 15.88 (+38.0%) | 16.03 (+21.8%) |
| **D** | + yoneda's `║` separator | 19.41 (**+68.6%**) | 19.55 (**+48.6%**) |

Arm B changes no format, so its read cost is exactly A's.

The Python runners in this directory (space-indented, deeply nested, 5,560
lines) give the same picture and slightly worse: C +18.6%, D +47.6% on qwen.

## The prediction was right, for the wrong reason

The pre-registration guessed +7–9 tokens a line for a yoneda row and got +6.4
to +7.9. It also said the indent column might pay for its own overhead back on
tab-indented Go, and named that as what would change my mind.

**It does not, and the reason kills the idea outright rather than narrowing it.**

| the whitespace itself | tokens | its name in the indent column | tokens |
| --- | --- | --- | --- |
| one tab | 1 | `1 tab` | 2 |
| three tabs | 1 | `3 tabs` | 2 |
| four spaces | 1 | `4 spaces` | 2 |
| eight spaces | 1 | `8 spaces` | 2 |

(Identical in both tokenizers.)

**Any run of whitespace is one token.** BPE merges repeated whitespace
aggressively, so indentation was never expensive — not at one level, not at
eight. Encoding it as words trades a 1-token run for a 2-token phrase plus a
separator, and it does so on *every line of the file*, whether or not that line
is ever edited. There is no depth at which this flips, which is why the
space-indented corpus looks the same as the tab-indented one.

The indent column may still be worth having. It just cannot be argued for on
tokens, and its case is now entirely the whitespace failure class it removes —
the thing `perfectOrWhitespace`, `replacePartWithMissingLeadingWhitespace`,
`matchButForLeadingWhitespace` and `outdent` exist to absorb.

## Two findings for whoever builds this

**The heavy bar is over half of D's overhead.** ` ║ ` is 3 tokens against a
tab's 1, twice per row: D costs +6.39 tokens a line on qwen where D_tab costs
+2.87. If anchors are ever adopted, the separator should be a tab. That is a
free 55% of the format's cost, and it is incidental to the design rather than
part of it.

**Isolated token counts do not compose.** Measured alone, an anchor
(`clever-torrent`, 3.5 tokens mean) is barely more than a right-aligned line
number (3.0 on qwen for a three-digit file) — about +0.6. In context the same
substitution costs +2.87. BPE merges across the boundary between a field and
what follows it, so reasoning about a format by adding up its parts gives the
wrong answer. Only the corpus measurement is load bearing here.

## Rig checks

- **Null control.** Setting the anchors *to* the line numbers makes D_tab render
  byte-identical to C. Passed — the two formats differ in exactly the field they
  are supposed to differ in.
- **Discrimination.** Real anchors change that render. Passed, so the control
  above is not passing because the comparison is inert.
- **Two tokenizers.** o200k (for `gpt-5.6-luna`) and Qwen's BPE (representative
  of the rest of the panel). They disagree on absolute counts — o200k is
  harsher on anchors, qwen on line numbers — and agree on every sign and on the
  ordering, which is what the decision needs.
- **Two indent styles.** Tab-indented Go and space-indented Python agree.
- **Still pending:** calibration against what a provider actually meters (rig
  check 5). This measures relative overhead, which is what the decision turns
  on, but the absolute figures are not yet known to match a bill.

## What this does to the trial

It refutes the token case in *both* directions, and in doing so shows the token
case was the wrong thing to weigh.

Taking oh-my-pi's headline at face value — 61% fewer output tokens, applied to
the whole of a turn's output, which is generous — here is how many lines a turn
may read before the extra input costs more than that saving buys, against this
directory's median turn of 32.9k input and 800 output:

| model | C | D_tab | D |
| --- | --- | --- | --- |
| `xiaomi/mimo-v2.5` | 420 | 340 | 153 |
| `deepseek/deepseek-v4-flash-0731` | 581 | 470 | 211 |
| `z-ai/glm-5.3-flash` | 700 | 566 | 254 |
| `tencent/hy3` | 840 | 679 | 305 |
| `openai/gpt-5.6-luna` | 1259 | 1019 | 458 |
| `qwen/qwen3.8-27b` | 1259 | 1019 | 458 |

A single `read` of one 500-line file exhausts arm D's budget on four of the six
models. So as a token play, anchored editing loses on Strument's traffic.

**But now put a retry beside it.** A failed edit costs a whole extra send —
about 9,400 input and 229 output tokens at this directory's median of 3.5 steps
a turn. How many anchored lines does avoiding *one* retry pay for?

| model | C | D_tab | D |
| --- | --- | --- | --- |
| `xiaomi/mimo-v2.5` | 4,231 | 3,435 | **1,543** |
| `deepseek/deepseek-v4-flash-0731` | 4,306 | 3,496 | **1,570** |
| `z-ai/glm-5.3-flash` | 4,361 | 3,541 | **1,590** |
| `tencent/hy3` | 4,427 | 3,594 | **1,614** |
| `openai/gpt-5.6-luna` | 4,623 | 3,753 | **1,686** |
| `qwen/qwen3.8-27b` | 4,623 | 3,753 | **1,686** |

One avoided retry is worth **ten times** the entire output-token saving — 1,543
lines against 153 on mimo, and the same ratio across the panel. The format
overhead is second order. What decides whether anchored editing is worth
building is **M1, first-try edit success**, and nothing else comes close.

That is the useful thing phase 0 bought, and it cost nothing: not "the idea is
dead" but "the metric everyone is quoting is the wrong one." A trial designed
around token counts would have measured the small number carefully and the large
number not at all.

## Revised plan

- **Arm B is now the cheapest good idea in the ladder** and should be built
  first regardless of what phase 1 says. It costs zero tokens, it delivers
  staleness detection, and Strument has none today.
- **Phase 1 keeps all four arms**, but M1 and M2 are promoted to the only
  primary metrics; M3–M5 become bookkeeping, reported for completeness rather
  than as the question. The pre-registered arms and sampling are unchanged, so
  the $2.54 estimate stands.
- **Arm C's justification changes.** It can no longer be argued for on tokens
  and must earn its place on M9 alone — how often the fuzzy whitespace stack is
  currently rescuing an edit. If M9 is near zero, C is a solution to a problem
  Strument does not have, and the ladder goes A → B → D.
- **If arms are built, D uses a tab separator, not `║`.**
