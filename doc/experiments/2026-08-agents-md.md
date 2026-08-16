# Naming AGENTS.md is what makes it work

**2026-08-16.** 24 live sessions, two models, three arms, order randomized.
Data and runner in `2026-08-agents-md-data/`.

## The question

Strument pins `AGENTS.md` when it finds one. Does a model then *follow* it, and
does it need to be told what the file is?

I expected pinning to be enough — frontier models know the convention — and
planned to add a prompt clause only if the data asked for it. The data asked
for it.

## Design

Three arms, because two could not separate "the instruction worked" from "the
model would have done that anyway":

| arm | |
| --- | --- |
| `none` | no `AGENTS.md` at all — the floor |
| `pin` | `AGENTS.md` pinned, no explanation (shipped behaviour) |
| `slot` | same, plus one clause naming it as the project's standing instructions |

The rule is mechanically checkable and **contrary to habit**, so compliance
cannot be mistaken for default behaviour: every exported function must carry a
doc comment beginning `Contract:`. Nothing writes that unprompted.

One turn: add an exported `Repeat` to a small Go package. Scored on the file
afterwards, not on the prose.

## Result

| arm | did the work | complied | edited `AGENTS.md` |
| --- | --- | --- | --- |
| `none` | 8/8 | **0/8** | — |
| `pin` | 8/8 | **2/8** | 0/8 |
| `slot` | 8/8 | **6/8** | 0/8 |

`none` vs `slot`: **p = 0.007**. `pin` vs `slot`: p = 0.13 on its own, but the
progression 0 → 2 → 6 is monotone and the floor arm pins it down.

The floor arm earned its cost. At 0/8 it establishes that the probe measures
the instruction rather than the model's habits, which is what makes 6/8 mean
something.

Per model, the shipped behaviour was worse than the aggregate suggests: `pin`
scored 2/4 on DeepSeek-v4-flash and **0/4 on MiMo**. Pinned and unexplained,
`AGENTS.md` is read as one more source file the model happens to have been
handed.

## Counter-metric

`AGENTS.md` is pinned **read-write**, so the model can update it as an ordinary
edit. The risk that buys is a model rewriting its own instructions.

**0 of 24 sessions touched it.** That matches the maintainer's experience
pinning it by hand, and it is the outcome that makes read-write the right
default: the update path stays open for when it is wanted, and nothing wandered
into it.

## What shipped

The clause, in `pinnedFilesNote`:

> `AGENTS.md` holds the project's standing instructions. Follow them for every
> change you make here.

It lives beside the pinned-file names rather than in the system prompt, so a
project without the file says nothing about it — a harness that claims standing
instructions exist when none do is inventing a rule the user never wrote.
