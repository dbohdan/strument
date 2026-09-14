# Is `replace_all` reached, now?

**Result: reached and correct — 20/20 in the pilot, though a later five-model
run on the unreduced file puts real uptake nearer 2/5 (see the update below).
The ambiguity it exists to avoid
fired once in 20 runs instead of three times in one session. The 2026-08
finding that models do not reach for `replace_all` (1/18) does not hold for
GLM-5.3-Flash on this task shape. Safety is still unmeasured: the fixture had
no decoys, so the hazard never had a chance to fire.**

## Why this was run again

A field report had GLM-5.3-Flash bumping FreeBSD versions in a CI matrix where
`version: '14.3'` appears twice, told apart only by the `architecture:` line
above it. Three of about twelve edit calls failed on ambiguity, one of them a
byte-identical resend of a call it had just been told was ambiguous.

Showing *where* the matches are helped
(`fix(coder): show where an ambiguous edit matched`), but the model still hit
the same ambiguity a second time for a different version — it did not
generalize the lesson within the turn. What it wanted to express was "both of
these, the same way", and `edit` had no way to say it.

That is a feature this project has already trialled and declined.
[`clean-null`](../../experimenting.md#clean-null) records three arms, six models,
54 runs, and the conclusion that "models rarely reach for `replace_all` (1/18),
which is an argument against adding it". The code-mode trials extended the
precedent to "1/18 and 0/36: two features, both correct, both unused when
**unnamed**", and the follow-up found that *naming* a feature where it is wanted
moved uptake from 0/24 to 8/24.

So the question was not "should this exist" in the abstract but the one the
handbook tells you to answer before spending: **is it reached?**

## The panel

Read at pinned commits, 2026-09-14 (2026-09-10 for DeepSeek).

| harness | commit | parameter | default | named in the ambiguity error |
| --- | --- | --- | --- | --- |
| OpenCode | `228e909` | `replaceAll` | false | no — named in the tool description instead |
| Kimi Code | `4f45efd` | `replace_all` | false | yes |
| DeepSeek Harness | `c291e79` | `replaceAll` | false | yes |
| Pi | `53816d7` | none — an `edits[]` array, each entry unique | — | n/a |
| Codex | `4d8eca1` | none — `apply_patch` diff format | — | n/a |
| Claude Code | closed source | `replace_all` per docs | false | unknown |

Three of five open-source harnesses have it, all defaulting off. Two of those
three name it in the failure message, in wording close to what shipped here.
Two solve the problem a different way entirely — a batch of unique edits, or a
patch format that carries its own context.

## Design

Two arms differing only in whether the ambiguity message names the parameter.
The binaries were compared before spending
([`baseline-from-head`](../../experimenting.md#baseline-from-head)) and differ.
Five reps each, order randomized, GLM-5.3-Flash at `reasoning = "low"`.

Two prompts, and the second exists because the first was wrong:

| prompt | wording |
| --- | --- |
| leading | "change **every** FreeBSD 14.3 entry to 14.5 and **every** 15.0 entry to 15.1" |
| neutral | "Upgrade the FreeBSD point releases: 14.3 becomes 14.5 and 15.0 becomes 15.1" |

The metric is uptake — did any edit call carry `replace_all: true` — which is a
count. The counter-metric is whether the file ends correct.

## Result

| prompt | arm | uptake | ambiguity messages | correct |
| --- | --- | --- | --- | --- |
| leading | named | 5/5 | 0 | 5/5 |
| leading | unnamed | 5/5 | 0 | 5/5 |
| neutral | named | 5/5 | 0 | 5/5 |
| neutral | unnamed | 5/5 | 1 | 5/5 |

**The feature is reached: 20/20.** That is the question worth the money, and it
answers cleanly in the direction opposite to the 2026-08 result.

**The naming comparison is vacuous, and that is a finding rather than a
disappointment.** The ambiguity message fired once in twenty runs, because a
model that has `replace_all` reaches for it *before* getting into trouble
rather than recovering afterwards. Its wording cannot matter when it is not
read. The prediction going in was that naming would be the load-bearing half;
it is not. The parameter is.

The leading prompt was itself the mistake worth recording: saying "every"
names the feature in the user's own words, so both arms saturated and the
message could never fire. That is
[`clean-null`](../../experimenting.md#clean-null) with the sign flipped — a
treatment applied in *both* arms rather than neither. The neutral prompt was
run to fix it and produced the same saturation, which is the real answer.

## What this does not show

**Nothing about safety.** The fixture's every occurrence was meant to change.
The 2026-08 trial built decoys — a `maxRetriesExceeded` beside the `maxRetries`
being renamed, occurrences inside user-facing strings — and recorded that they
never fired, so its 18/18 was not a safety result. This fixture has no decoys
at all, so 20/20 correct is a weaker statement still: it says the feature works
when every match should change, and says nothing about what happens when one
should not. A trial that wants to claim safety needs a fixture where a careless
replacement is visible, and needs to check the decoys actually fire.

**One model, one task shape.** GLM-5.3-Flash on a YAML matrix. The 2026-08
result covered six models, and the divergence may be the model, the year, or
the shape.

### Update: 20/20 overstates it, and the fixture is why

Five later runs on the *unreduced* file — the field report's own `ci.yml`, with
five models: GLM-5.3-Flash twice, MiMo-V2.5, Poolside Laguna S 2.1, and a
locally served Qwen3.8 — used `replace_all` in **2 of 5**, not 20/20. The local
run reported no cost at all, which is the right answer rather than a gap: a
local endpoint returns none, and the turn record omits the field instead of
writing a zero, per `llm.Money`'s rule about never fabricating an unknown.

The pilot's fixture is the explanation. It was cut down to the matrix alone, so
editing the `version:` line was the only obvious route and every model took it.
The real file offers a second: replace the whole matrix entry as a block, which
is unique by construction and needs no `replace_all`. Three of the five did
exactly that. Both routes are correct and both avoid the failure.

So the pilot's fixture distorted *both* numbers, in opposite directions — it
could not measure safety, having no decoys, and it inflated uptake by removing
the alternative. The honest reading is that `replace_all` is reached often
enough to earn its schema entry, not that it is what models reach for.

What the five runs do show cleanly is the thing worth having: **zero ambiguity
failures, zero failed edits and zero verbatim retries across all five**, against
three failures and one verbatim retry in the session that prompted the work. The
feature and the message together removed the failure mode; which of the two did
it, per model, this cannot say.

## What shipped

- `replace_all`, default off, exact matches only. Fuzzy matching is a good bet
  at one site and a bad one at five: applying a tolerant match everywhere
  multiplies the risk rather than dividing it.
- Refused beside an `anchor`, which already names one range by identity, so
  "all of them" cannot mean anything.
- The result says how many places changed, since a model that asked for "all of
  them" has no other way to learn whether that was two or twenty.
- The ambiguity message names it, kept on the panel's precedent and because it
  costs one sentence — but **not** on this evidence, which cannot support it.
