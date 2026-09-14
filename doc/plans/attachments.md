# Plan: file attachments that are not `/submit`-compatible text

**Status: in progress.** Parts are listed in dependency order; each is
shippable on its own. Delete this file once Part 5 is done and the live pass in
Verification has run, the way `code-mode.md` says to.

---

## Context

`/submit <file>` sends a file's text as the user's message and refuses four
different things at `internal/repl/commands.go:1079-1108`: a directory, a file
over `submitLimit` (100 KiB, `:1049`), invalid UTF-8 (`:1100`), and an empty
file. Those refusals hide three unrelated needs:

- **Images.** No text substitute exists; the model's own vision is the point.
- **Structured documents** (PDF, docx). Out of scope — see Non-goals.
- **Opaque binaries and oversized text.** Already have a path (`/add`, then
  tools).

Only images need new machinery. The rest is a routing rule and better refusals.

### The distinction the design rests on

Extending `/add` to carry image bytes would reverse a measured decision.
`pinnedFilesNote` (`internal/coder/assemble.go:437`) records that file
*contents* were pulled out of a fabricated user turn in favour of file *names*
plus an instruction to read them: 600 samples, three models, same task success,
one extra step, blind edits from **383 across 230 runs to zero across none**.
The comment states it plainly — "the harness stops asserting in the user's voice
that a block of text is a file's current contents."

An attachment is not that. When the user attaches a screenshot, the user really
did attach it; a user-role image block is the honest encoding, not a synthesis.

> **Pinning is about files the model should go and read. Attaching is part of
> what the user said.** Attachments ride on one user turn, at the moment it is
> sent, and are never re-asserted as current.

A second, separate route is also in scope: the model asking to *look* at an
image it found, via `read`. That is not a synthetic turn either — it is a tool
result, which is where everything else the model learns already arrives.

### Four places that will silently eat an attachment

Every one is a check that cannot fail — the shape this repository keeps
legislating against — so Part 0 closes them before any feature lands.

1. **`ContentBlock` is text-only** (`internal/llm/types.go:130`):
   `{Type, Text, CacheControl}`. `Type` exists but nothing branches on it.
2. **The Anthropic path drops it.** `contentBlocks`
   (`internal/client/anthropic.go:244`) does `if b.Text == "" { continue }`. An
   image block vanishes with no error. The user attaches a screenshot, the model
   says it cannot see one, and the natural conclusion is that the model is bad
   at vision.
3. **The OpenAI-compatible path mis-serializes it.** `wireMessage.Content` is
   `llm.Content` marshalled as-is (`internal/client/client.go:121`), and
   `ContentBlock.Text` has **no `omitempty`** (`types.go:132`), so a new kind
   goes out as `{"type":"image","text":""}` — a shape no provider accepts.
4. **Accounting is blind.** `countMessages`
   (`internal/coder/assemble.go:586`) and `ChatSummary.count`
   (`internal/coder/summary.go:53`) both sum `m.Text()`, and
   `Content.String()` (`types.go:95`) concatenates only `b.Text`. An image
   contributes 0 tokens to `/tokens`, to `checkTokens`, and to the compaction
   trigger — and `renderForSummary` (`summary.go:220`) renders it as the empty
   string, so compaction drops it silently.

### Panel evidence

Read at pinned commits, 2026-09-14 (2026-09-10 for DeepSeek). All are moving
targets; re-pin before trusting any of this.

| harness | repo | commit |
|---|---|---|
| OpenCode | `sst/opencode` | `228e909` |
| Pi | `earendil-works/pi` | `ceea48f` |
| Codex | `openai/codex` | `d77ebc7` |
| Kimi Code | `MoonshotAI/kimi-code` | `4623395` |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `c291e79` |
| Claude Code | closed source | docs only |

What they settled that this plan adopts:

- **Capability is a projection, not a refusal.** OpenCode replaces an
  unsupported part with `ERROR: Cannot read "foo.png" (this model does not
  support image input). Inform the user.`
  (`provider/transform.ts:434`); DeepSeek has `projectImagesForTextModel`,
  deterministic and computable from lengths alone. Refusing at attach time does
  not survive `/model` switching mid-session, which Strument supports.
- **Sniff magic bytes, not extensions.** Pi (`harness/tools/image.ts`) and Kimi
  (`sniffMediaFromMagic`) both do. Pi rejects animated PNG and lossless JPEG
  explicitly.
- **Label the image in the text stream.** Codex wraps each image in
  `<image name="…" path="…">` … `</image>` (`protocol/src/models.rs:1651`) and
  keeps image-plus-labels atomic during truncation
  (`core/src/compact_remote_v2_images.rs`).
- **Declare the token estimator.** Pi: `ESTIMATED_IMAGE_CHARS = 4800` (~1200
  tokens). Codex: `RESIZED_IMAGE_BYTES_ESTIMATE`.
- **Tell the model when media is dropped.** OpenCode injects a synthetic
  message briefing the model to relay the loss (`session/compaction.ts:529`).
  DeepSeek's summarizer *throws* if a summary would contain an image
  (`compaction-basic/src/summarizer.ts:218`).
- **Images in tool results need two wire paths.** Pi's `read` returns image
  blocks; its OpenAI adapter sends the tool message as `"(see attached image)"`
  and re-homes the image into a following user message, gated on
  `model.input.includes("image")` (`packages/ai/src/api/openai-completions.ts:1377`).
- **Nobody has an `/attach` command** — all use paste, drag-drop, or a path in
  the message. A readline TTY cannot receive image bytes from a paste, so a
  command is the available surface; this is a deliberate divergence to record in
  a comment.

---

## Part 0 — the seam

No user-visible change. Everything else depends on it.

- `internal/llm/types.go`: give `ContentBlock` a real kind. Keep `Text` for
  `"text"`; add an image payload (media type + base64 data + source label) and
  put `omitempty` on `Text`. Add constructors (`TextBlock`, `ImageBlock`) so no
  caller builds one by hand.
- `Content.String()` must not return `""` for an image. Return the label
  (`[image: foo.png, 1920×1080]`) so every existing `m.Text()` caller degrades
  to something true rather than to nothing. This one change fixes the silent
  drop in `renderForSummary`, `countMessages` and `ChatSummary.count` at once.
- `internal/client/anthropic.go`: `contentBlocks` switches on kind
  exhaustively; an unknown kind returns an error rather than `continue`. Remove
  the `b.Text == ""` skip for non-text kinds.
- `internal/client/client.go`: marshal image blocks in OpenAI's
  `{"type":"image_url","image_url":{"url":"data:…"}}` shape.
- **Shape guard** in `internal/fixture/`, modelled on `applyconfig_test.go`:
  fail if a `ContentBlock` kind constant exists with no case in both clients,
  with a vacuity counter-arm so a rename cannot make it pass by checking
  nothing.

## Part 1 — capability declaration and projection

- `internal/config/types.go:88`: add `InputModalities []string` to `Model`
  (default `["text"]`). Plumb through `internal/modelconfig/source.go` and
  `emit.go`; `applyconfig.go` carries it like every other setting — that file is
  the single config→Coder path and must stay so (`applyconfig_test.go` enforces
  it).
- A **pure projection** in `internal/coder`:
  `projectForModel(msgs []llm.Message, m *config.Model) []llm.Message` replaces
  every image block the model cannot accept with a text block naming the file
  and saying the model cannot see it and should tell the user. Pure in, pure
  out — unit-testable exhaustively, no I/O. Called once in the send path
  (`internal/coder/send.go`, after `formatMessages()`).
- `/attach` *also* warns at attach time when the current model has no image
  modality — immediate feedback — but the projection is what keeps a session
  sendable after `/model` switches.

## Part 2 — `/attach` (the user route)

- `internal/repl/attach.go`, new. `/attach <file> ...` stages for the next
  message; bare `/attach` reports what is staged; `/attach drop [<file> ...]`
  clears (bare drops all, mirroring `/drop`). Bare-reports is the house pattern
  — `/commits`, `/model`, `/env`, `/notes`, `/context`. Record in a comment why
  not `/detach`: `detach` is a tmux/screen session verb in a TTY-native tool,
  and a top-level verb symmetric with `/drop` would advertise durable state that
  attachments do not have.
- Bytes are read **at attach time** and held on the Coder. Comment the
  tradeoff: reading at send would track a file that changes underneath you, but
  then the staged report is a claim about a file nobody read.
- Sniff the magic bytes; accept PNG/JPEG/GIF/WebP; reject animated PNG. Trusting
  the extension makes a wire error out of a mislabelled file.
- Per-file and per-session size caps, refusing rather than truncating — the
  reason `submitLimit` gives at `commands.go:1089`.
- Run the same ignore/containment gate the file tools use
  (`workspace.refuseIgnored`). An attach path that bypasses it routes around a
  rule the rest of the harness keeps; Kimi runs `isSensitiveFile` here for the
  same reason.
- Consumed by exactly one send: `internal/coder/send.go:234`, where
  `llm.TextMessage("user", inp)` becomes a message carrying blocks. The staged
  set is **not** in `resumeState()` — it is per-message, not session state.
- Echo what was staged and what was sent, the way `/submit` prints the trimmed
  text it is about to send (`commands.go:1109`).

## Part 3 — `read` returns images (the tool route)

- `internal/workspace/read.go`: `Read` detects an image and returns its bytes
  and media type rather than refusing on UTF-8 grounds.
- Tool results become text-plus-optional-media. `results map[string]string`
  (`internal/coder/tools.go:668`) becomes `map[string]toolResult` with a
  `{Text string; Images []llm.ContentBlock}` shape; ~31 mechanical call sites.
  **Do the type change rather than a side-channel map keyed by call id** — a
  parallel map whose keys must agree is precisely the "every caller must
  remember" hazard this codebase just spent commits removing, and the type makes
  the invariant unrepresentable instead of checked.
- `appendToolResults` (`tools.go:1028`) builds the result message with blocks.
- **Anthropic**: `antBlock.Content` (`anthropic.go:114`) must accept a block
  list, not just a string — the API takes either for `tool_result`.
- **OpenAI-compatible**: tool messages are text-only, so follow Pi — send the
  tool result as text naming the image, then re-home the image blocks into a
  following user message. Gate on the model's modalities.
- Update the `read` tool description to say images are supported, the way Pi's
  does.

## Part 4 — accounting and compaction

- `TokensReport` (`internal/coder/session.go:253`) gains an **attachments row**
  of its own. Precedent and reasoning are in that function already: the
  `tool schemas` row exists because schemas "are not part of any message" and
  folding them into a sum hid them.
- A declared per-provider estimator, named and commented, not a magic number.
  `llm.Money`'s rule — "Never fabricate $0 for unknown" — applies to tokens.
- `checkTokens` gets the same count for free once `Content.String()` and the
  estimator are in place.
- **Compaction**: when `ChatSummary` rewrites a message, images become text
  placeholders naming the file and when it was attached. Assert that a summary
  the side model returns contains no image block — DeepSeek throws here, and
  this repo would rather fail loudly than ship a silent drop.
- Keep-all is the v1 policy: no per-turn eviction, a hard session cap, and
  placeholders only at compaction. **Comment why eviction is deferred**: an
  oldest-first policy that removes one image per turn changes the request prefix
  every turn and invalidates the prompt cache Strument explicitly manages
  (`addCacheControl`, `cacheHeadersEnabled`, 1h TTL). DeepSeek's fix is to evict
  in quanta so the removed prefix stays byte-identical across many turns. If
  eviction is ever added, that is the design to port, not a naive one.

## Part 5 — record the harness panel

Add to `CLAUDE.md` beside the model-panel rule, in the same register: the six
harnesses, why these six, and the instruction to shallow-clone into scratch
space, **record the commit**, and never vendor a checkout into the project
directory.

---

## Verification

**Unit (`task test`):** projection is a pure function — exhaustive table test
over every modality/kind pair, including the vacuity counter-arm. Block
marshalling asserted against both wire dialects. `Content.String()` returns a
label, not `""`, for an image. Round-trip through the fixture schema.

**Fixture:** use a 1×1 PNG and put the base64 payload in `Request.Ignore`
(`internal/fixture/fixture.go:76`), asserting on block *shape*, not bytes —
otherwise every fixture carries kilobytes of base64.

**`task check` green in one run** before committing, per `CLAUDE.md`.

**Live pass — not optional here.** A dropped image block produces a plausible
answer, not an error, so nothing but observation distinguishes "working" from
"silently ignored."

- Two providers minimum, one Anthropic-dialect and one OpenAI-dialect through
  OpenRouter, because the point *is* cross-provider disagreement about field
  shape. MiMo-V2.5 is the default under `CLAUDE.md` but cannot answer this
  alone.
- **A discriminating probe image**: a photo of a specific five-digit number or
  an unusual word, so "I see a screenshot of some code" cannot pass for success.
  The metric is a count (did it read the digits), not a judgment.
- Both routes: `/attach` and `read` on a repo image.
- **Counter-metric, reported as prominently as the effect**: attach an image to
  a task that does not need one and check that text-only behaviour, step counts
  and `/tokens` do not move.
- Then switch `/model` to a text-only model mid-session and confirm the
  conversation stays sendable and the model says it cannot see the image.
- Keep raw transcripts; read individual ones before believing an aggregate.

---

## Non-goals

- **Documents (PDF/docx/odt).** Too deep to be a feature on the side, and the
  good extractors are C in a no-cgo project. The model shells out to Pandoc or
  OCRmyPDF with a Skill.
- **Audio and video.** `InputModalities` leaves room; nothing else does.
- **Clipboard paste.** A readline TTY cannot receive image bytes.
- **Per-turn image eviction.** Deferred with its reasoning in Part 4.
