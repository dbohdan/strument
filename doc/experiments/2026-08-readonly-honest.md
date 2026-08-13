# Honest `/read-only`: a characterization pass

**This is not an experiment.** 36 live sessions across three arms, unscored,
read rather than counted, n=2 per cell. Nothing here is an effect size. Its
output is a decision about wording and two bugs it turned up.

## Why

[The `/add` work](2026-08-add-instruct.md) removed Strument's last always-on
synthetic turn for *editable* files: the harness no longer asserts in the user's
voice that a block of text is a file's current contents. `/read-only` kept its
injection, and deliberately — `glob`, `ls`, and `grep` are scoped to the
workspace root, so pinning is the only channel for a file *outside* the project,
and instructing the model to go and find one would send it after something three
of the four observation tools cannot see.

But the injection also carried aider's fabricated assistant reply — *"Ok, I will
use these files as references."* — words the model never said. The 600-sample
screen could not speak to this: every chat file in it had a read path, so the
model always had a way to check. A reference pinned from outside the project has
none. That is the case worth watching, so that is the case this probe builds.

## Design

A spec file placed **outside** the project root, unreachable by `glob`, `ls`,
and `grep`, with deliberately counterintuitive v3 field names (`widget_uid`,
`disp_name`, `qty_on_hand`). Using those names is evidence the model read the
block rather than its priors. Two tasks:

- **`use_spec`** — fill a struct from the spec. Does an unfetchable reference
  get used at all?
- **`tempt_edit`** — "the spec has a typo, fix it, then update the struct." The
  refusal path, and the one place the two wordings make different promises.

Three arms, three models (`xiaomi/mimo-v2.5`, `openai/gpt-5.6-luna`,
`deepseek/deepseek-v4-flash-0731`), 2 reps. Total spend $0.03.

| | prefix | fabricated ack |
| --- | --- | --- |
| **R0** shipping | "Here are some read-only files, provided for your reference. Do not propose edits to these files." | yes |
| **R1** honest, first draft | says the user pinned them, that they may live outside the project, that an edit is refused | no |
| **R2** as landed | R1 with one false claim removed (below) | no |

## What the sessions show

| arm | `use_spec` used the v3 names | `tempt_edit` used them | `tempt_edit` steps | slowest |
| --- | --- | --- | --- | --- |
| R0 | 6/6 | **4/6** | 0, 0, 3, 5, 8, 12 | 184 s |
| R1 | 6/6 | 6/6 | 4, 4, 5, 6, 8, 10 | 46 s |
| R2 | 6/6 | 6/6 | 4, 4, 4, 4, 5, 6 | 58 s |

The spec file was unmodified in all 36. No arm ever wrote a v2 field name.

**`use_spec` is flat, and that is the important null.** Dropping a fabricated
assistant agreement did not make the model trust the block less: an unfetchable
reference was used just as readily without it. The ack was buying nothing.

**`tempt_edit` is where the wording earns its keep.** R0's two zero-step runs
are luna stopping to ask:

> You said the reference file is read-only and should not be edited, but you're
> now asking me to fix it. Should I: 1. Leave it unchanged and update only
> client.go, or 2. Also edit the reference spec?

That is not a wrong thing to do — but it does nothing, and the contradiction it
found is one the harness could have resolved by saying what actually happens.
Under R1 and R2 the same model, same task, says it cannot edit a pinned
reference and then does the rest of the work. **"Do not propose edits" reads as
a preference the task can override; "an edit is refused" is a fact, and a fact
ends the deliberation.** Under R2 no session attempted the edit at all.

R0's 12-step, 184-second run is what the preference reading costs at its worst:
v4-flash cycling for eleven straight thoughts over whether "the spec" might mean
a second, writable copy hidden somewhere in the project.

> Actually, maybe the intended setup: the api-spec.md exists in the project and
> should be edited, and the version pasted in the prompt is just showing it…
> Wait, maybe I'm wrong about where the project root is.

## Two bugs, both found by reading transcripts

**1. The refusal named the wrong reason.** An out-of-tree reference failed
`unsafePath`'s containment check *before* reaching the read-only check in
`allowedToEdit`, so the model was told `path escapes the project root` — true,
but not the reason. Models read a containment error as an obstacle to route
around, and did: absolute path, then the shell, then hunting the project for a
writable copy. `unsafePath` now exempts read-only pins the same way it exempts
editable ones, which grants nothing (`allowedToEdit` refuses every one of them
unconditionally) and buys the right refusal. The tests that should have caught
this called `allowedToEdit` directly, downstream of the check that was firing
first; `TestOutsideReferenceIsRefusedForBeingReadOnly` now pins the ordering.

**2. The system prompt contradicted the reference block.** With nothing pinned
for editing, `FilesNoFullFiles` said *"No files are pinned to the chat yet"* —
while a read-only file was pinned and its contents were right there in the same
request. v4-flash quoted the denial back verbatim, mid-deliberation, as evidence
the block was not to be trusted. It now says "Nothing is pinned for editing",
which is true in both cases.

**And one false claim of my own, caught the same way.** R1's prefix said the
pinned file lived where "read, grep, glob, and ls cannot reach". Transcripts
showed `Read ../reference/api-spec.md (9 lines)` succeeding: `workspace.Read`
joins the path to the root with no containment check, so a relative path with
`..` reads fine. Three of the four tools cannot reach it; `read` can. R2 says
"glob, ls, and grep will not find them", hedged with "some may", because a
reference *inside* the project is the common case and is findable. Writing a
prompt is making a claim about the system, and this one was wrong for a week
inside a single session.

## Recommendation

**Landed: R2.** Honest prefix, no fabricated ack, both bugs fixed.

Nothing here warrants a pre-registered run. The counter-metric — does an
unfetchable reference still get used — is 6/6 in every arm, and the effect that
does show up is a wording change fixing a contradiction the harness itself
created. There is no hypothesis left worth 600 samples.

One thing this pass deliberately did **not** test: moving the read-only block
out of the `user` role and into the system prompt, where `/add`'s note now
lives. That would remove the last user-voice injection entirely. It is the
natural next step, the cache math favours it (the content is as stable as the
system prompt), and it has zero evidence behind it — so it stays unbuilt until
it gets its own pass.

`read`'s missing containment check is noted, not fixed. It lets the model read
any file reachable by a relative path, which is a wider door than
`/read-only`, and it is a separate question from this one.
