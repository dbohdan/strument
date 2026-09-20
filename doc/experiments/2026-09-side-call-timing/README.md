# How long does a side call take, and what should bound it?

**Status:** characterization. 72 live calls, 2026-09-20, four side models over
six input shapes.

## The question

Session notes and `--continue` were unreliable in the field, and a live failure
came back as `context deadline exceeded` with nothing else said. Every side call
— commit message, session notes, compaction summary — shared one budget,
`sideTimeout = 60s`, and nobody had measured what these calls actually cost.

The design question underneath: should a side call be bounded by its **total
duration**, or by the **gap between bytes**? A call that takes three minutes but
streams steadily is slow. One that sends nothing for three minutes is stalled.
Only the second is worth cutting short, and the two are indistinguishable in a
report that only records elapsed time.

## Result

| call | n | median | p75 | max | cut at 60s | 120s | 180s | 300s | worst gap |
|---|---|---|---|---|---|---|---|---|---|
| commit message | 36 | 35.7s | 70.7s | **845.9s** | **31%** | 14% | 8% | 3% | 10.2s |
| session notes | 12 | 21.7s | 37.9s | 100.9s | 8% | 0% | 0% | 0% | 4.1s |
| chat summary | 24 | 94.1s | 125.7s | **675.8s** | **62%** | 33% | 17% | 4% | 10.8s |

**The 60s budget was cutting 62% of compaction calls and 31% of commit-message
calls, and none of them were stalls.** The largest gap between bytes anywhere in
the sample was 10.8s. Across all 72 calls, an idle timeout anywhere from 15s to
60s would have cut nothing at all.

**But an idle timeout alone is not enough.** The slowest commit-message call ran
**845.9s** — fourteen minutes — with a maximum gap of 0.8s, spending 36,141
characters of reasoning to produce a 202-character commit message. Nothing
watching for silence can ever catch that. The two mechanisms are complements:
the idle timeout catches a stalled provider, the budget catches a model that
streams steadily and never stops.

## Why the calls are slow: reasoning nobody asked for

`notes.go` and `commit.go` both left `llm.Request.ReasoningEffort` unset, each
with a comment claiming this stopped a reasoning model from thinking its way to
a subject line. It did not. `client.go` reads `""` as *defer to the provider
default; send nothing*, so the model reasoned as much as it pleased and Strument
merely gave up the ability to say otherwise.

Every one of the four side models reasoned on every arm — up to 36,141
characters on a commit message and 41,853 on a summary. Passing the configured
effort through is what makes the comment's intent reachable: `reasoning="off"`
on the side model now turns it off. It is not forced off in code, because "off"
is not universally accepted — `glm-5.3-flash` rejects
`reasoning:{enabled:false}` with HTTP 400.

## Input bounds

Measuring required knowing what these calls are fed, which turned up a separate
defect. The commit-message call had **no input bound at all**: `gitrepo` passes
`git diff --cached` through verbatim and `renderCommitMessages` writes every
message's full text, so a turn that read three large files put all three into
the call. It grew until a provider refused.

The fix reuses compaction's existing rule — `sideInputBound`, the side model's
own window — rather than inventing a second one. The distinction that keeps this
one rule instead of two: `maxChatHistoryTokens` divides by `historyShare`
because the main model's window is *contended*, while a side call's request is
its system prompt plus this content and nothing else, so it owns the window.

`summaryFallbackInput`, used when a model's `context` is unset, was 4096 — a
window no current model has. Weighted by one user's 874 recorded sessions, the
smallest window in use is 65,536 and the median 1,050,000. At 4096 tokens
(~16k characters) the bound would truncate the diff on **36%** of this
repository's last 200 commits; at 32,768 it truncates **2.5%**.

When the input must be cut, the chat context goes before the diff. The prompt
tells the model to describe only what the diff does and treats earlier turns as
background, so a message written from a full diff and no context is
worse-explained, while one written from half a diff is wrong about what changed.

## Equipment faults

Four, all found before the run that produced the table above. This is the part
worth reading.

1. **Block-buffered stdout** hid every result until ~70 lines accumulated,
   which made a working probe look wedged.

2. **Prefill-cache hits.** Sending the same prompt three times measures one cold
   call and two cache hits: the second rep of one arm reached its first byte in
   1.4s where the first took 4.3s. Fixed with a per-call salt — and then fixed
   again, because the salt was derived from the arm and job index and so was
   *reproducible*, which is the wrong property: rerunning the probe re-sent
   prompts the provider had already cached, and one call came back with 11,674
   of its 11,675 prompt tokens cached. The salt now carries a per-run
   component. Three of the final 72 calls still show a non-zero cached count;
   two are 256-token boundary artifacts and one is a real hit, recorded rather
   than hidden.

3. **The gap was measured between content tokens, not bytes.** This is the one
   that would have changed the answer. `idleReader.Read` resets its timer on any
   `n > 0`, so a keepalive comment, a reasoning delta and a content token are
   the same event to it. Timing gaps between `delta.content` instead reported a
   **93.4s silence** on a `glm-5.3-flash` call that had been streaming reasoning
   the whole time. Re-measured on the same arm and model against wire bytes, the
   largest gap was **1.41s**. A budget built on the first reading would have set
   the idle timeout around 90s to accommodate a stream that is never quiet for
   more than a second and a half.

4. **The fixture sent a request the code never sends.** The probe forced
   `reasoning:{enabled:false}` on the notes and commit arms, reasoning from the
   comments rather than from `client.go`. That request 400s outright on
   `glm-5.3-flash` and, on a model that honours it, measures a faster call than
   the real one. Correcting it is what surfaced the reasoning defect above — the
   fixture could not contain the phenomenon until it matched the code.

A fifth turned up while exercising the change rather than while measuring: the
scratch project set `default_model` in its project config, which is not a
project-settable key, so four live runs silently used the user config's model
and its large-window side model. The cap never fired and the code looked wrong.
It was the harness.

## Limitations

- One user's model history stands in for "what people run". It is evidence about
  this user's configuration and no more.
- Four side models, all reached through OpenRouter. On-device models — the
  population most likely to have a small window and an unset `context` — are
  absent, and are precisely the population `summaryFallbackInput` exists for.
- Totals vary enormously run to run: the same arm and model produced 10.1s,
  30.3s and 43.6s on three occasions. The medians are worth more than any single
  row, and the maxima are worth reading as "this happens" rather than as a
  bound.
- `commit/big` is 75k tokens, which is a large real diff and not a worst case.
  The commit input was unbounded, so the true worst case was the main model's
  whole window.

## Instrumentation

`data/side_timing.py` sends the real prompts, verbatim from
`internal/prompts/prompts.go`, over real commit diffs taken from this
repository's history. `data/analyze.py` produces the tables;
`data/contexts.py` weights the model history against OpenRouter's catalogue.
`data/side_timing.jsonl` is the raw 72 rows.
