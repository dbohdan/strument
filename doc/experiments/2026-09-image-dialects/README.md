# Does an image survive each wire dialect?

**Result: one of three dialects was broken, and it was the one with the
dialect-specific special case. Images inside an Anthropic `tool_result` return
HTTP 400; re-homing them into the following user turn, the way the other two
dialects already did, fixes it — 0/2 to 2/2 with nothing else moving.**

## The question

Strument grew image support in three wire dialects at once, and the unit tests
for it assert the shapes this repository's author read in three sets of
documentation. A test written from a reading confirms the reading. Nothing in
`task check` could distinguish "the provider accepts this" from "I believe the
provider accepts this", and the failure mode is silent by construction: an
image the provider never received produces a plausible answer, not an error, so
the user reads it as the model being bad at vision.

Two shapes in particular invited a mistake. Chat completions spells an inline
image `{"type":"image_url","image_url":{"url":"data:…"}}` — an object — while
the Responses API spells it `{"type":"input_image","image_url":"data:…"}`,
a bare string under the same field name.

## Design

Three arms on OpenRouter, one key, **the model held constant**, so the dialect
is the only variable. A cross-vendor comparison — Anthropic via Anthropic
against OpenAI via OpenAI — would have confounded dialect, vendor and model in
every failure.

| dialect | endpoint | Strument adapter |
| --- | --- | --- |
| chat completions | `/api/v1/chat/completions` | `openrouter` |
| Anthropic Messages | `/api/v1/messages` | `anthropic` + `base_url` |
| Responses | `/api/v1/responses` | `responses` + `base_url` |

All three accept an OpenRouter bearer token and all three accept
`xiaomi/mimo-v2.5`, checked before the run. `/messages` answers in Anthropic's
own error envelope rather than OpenRouter's flat one, which is some evidence it
is a real Messages endpoint rather than an alias.

Both routes into an image are separate arms, because they take different wire
paths: `/attach`, where the user hands one over, and the `read` tool, where the
model fetches one itself.

The vendor axis is the vision-capable half of the standard panel on the
chat-completions dialect. The other half is the projection arm's subject:
`deepseek` and `hy3` are genuinely text-only, so nothing had to be faked.

| alias | slug | image input |
| --- | --- | --- |
| mimo | `xiaomi/mimo-v2.5` | yes |
| glm | `z-ai/glm-5.3-flash` | yes |
| luna | `openai/gpt-5.6-luna` | yes |
| qwen3.8-27b | `qwen/qwen3.8-27b` | yes |
| deepseek | `deepseek/deepseek-v4-flash-0731` | no |
| hy3 | `tencent/hy3` | no |

**The metric is a count, not a judgment.** The probe is a generated PNG of a
five-digit number; the question is whether the model reports those exact
digits. "I see a screenshot with a number in it" cannot pass. Order was
randomized across all 36 jobs per pass ([`confound-cannot-reach`](../../experimenting.md#confound-cannot-reach)).

## Result

36 runs before the fix, 36 after, at two repetitions per cell.

| dialect | model | route | declared | before | after |
| --- | --- | --- | --- | --- | --- |
| anthropic | mimo | **read** | yes | **0/2** (HTTP 400) | **2/2** |
| anthropic | mimo | attach | yes | 2/2 | 2/2 |
| anthropic | mimo | text | yes | 2/2 | 2/2 |
| chat | mimo | read / attach / text | yes | 2/2 each | 2/2 each |
| chat | glm | read / attach | yes | 2/2 each | 2/2 each |
| chat | luna | read / attach | yes | 2/2 each | 2/2 each |
| chat | qwen3.8-27b | read / attach | yes | 2/2 each | 2/2 each |
| responses | mimo | read / attach / text | yes | 2/2 each | 2/2 each |
| chat | deepseek | read | **no** | 0/2 | 0/2 |
| chat | hy3 | read | **no** | 0/2 | 0/2 |
| chat | deepseek | read | **yes (a lie)** | 0/2 (HTTP 404) | 0/2 (HTTP 404) |

**Exactly one cell moved.** The counter-metric — every text-only conversation
and every arm that already worked — is unchanged, which is what makes the fix
safe to keep.

### What was wrong

The `read` tool returned its image inside an Anthropic `tool_result`, which the
API documents as legal. Through OpenRouter's Messages endpoint it returns

```
HTTP 400 — Param Incorrect: `text` is not set
```

Two controls isolate it to that construction rather than to images on that
dialect:

| shape sent to `/api/v1/messages` | result |
| --- | --- |
| image block inside a `tool_result` | **400** |
| image block in a plain user message | accepted, digits read |
| `tool_result` as a string, image re-homed into the same user turn | accepted, digits read |

So the fix was to **delete the special case**, not to work around it. All three
dialects now re-home, which is both what the wire accepts and one path to
reason about instead of three. The most complex path was the only broken one.

### The projection, and its counter-arm

A model that cannot accept images gets each image replaced by text naming the
file and saying it could not be seen. The arms show this works and, separately,
that it is load-bearing.

`hy3`, declared honestly as text-only, answered: *"I can't read the number from
it"*, and offered alternatives rather than guessing. `deepseek` was caught
reasoning about the instruction and deciding against a workaround:

> I could attempt to decode the PNG and maybe OCR it, but there's no OCR
> available. However, maybe I can inspect pixels. That's risky and would be
> guessing. The instructions say: "do not guess at what it showed." … I
> shouldn't try to reverse-engineer digits from pixels as that would be
> guessing.

The counter-arm is the one that makes this mean something. Declaring `deepseek`
as image-capable — a deliberate lie — and sending it an image returns
`HTTP 404: No endpoints found that support image input`. The capability
declaration is therefore doing real work: without it the request simply fails,
so the projection is not decorating something the provider would have tolerated.

## Instrumentation faults

Two, both caught before they could produce a result.

**The working directory answered the question.** `notes.txt`, holding the same
digits for the counter-metric route, sat beside `probe.png` in every run. The
first pass caught `deepseek` — blind to the image — listing the directory and
reading the answer off the disk, which the scorer would have counted as vision
([`fixture-cannot-contain-it`](../../experimenting.md#fixture-cannot-contain-it)). The routes now get one file each, and `notes.txt`
carries *different* digits so any future leak is visible rather than scored as a
hit. The recorded `leak` field is empty for all 72 runs.

**`max_steps` is a checkpoint, not a cap.** With `--yes steps` a blind model
auto-continued past it and spent 137 seconds hunting the filesystem. Dropping
the auto-approval lets the checkpoint end the turn, uniformly in every arm so it
cannot confound the comparison.

The probe image was also read by eye before any model saw it. A blank or
illegible probe would have produced a clean null across every arm, which is the
shape that gets a broken change shipped.

## Limitations

- **OpenRouter's `/messages` and `/responses` are translations.** This shows
  Strument's bytes are acceptable to that translator; whether Anthropic's own
  endpoint accepts an image inside a `tool_result` — which its documentation
  says it should — is untested here. That matters only if someone wants to
  reinstate the branch.
- Two repetitions per cell. Enough for a result this categorical (400 versus
  200 on every run) and not enough for anything probabilistic.
- One image, one size, one format. Large images, and the provider-side
  downscaling the token estimator assumes, are untested.

Total cost across both passes: **$0.045** over 72 sessions.

## Reproducing

`data/probe.py` generates the image; `data/run.py` runs the sweep;
`data/summarize.py` folds two passes into the table above. `OPENROUTER_API_KEY`
in the environment, never a file.
