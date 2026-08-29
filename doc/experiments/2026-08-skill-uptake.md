# Does a skill change what a model produces?

2026-08-29. Skills landed (`c3c6e50`…`cf9fb4b`) and a live pass proved
*delivery*: six models loaded a fixture skill and echoed a token that existed
only in its body. That says the plumbing works. It says nothing about whether a
skill improves the work, or whether models reach for one when nobody tells them
to.

The second question is the one that decides whether the feature earns its keep,
and it is the hazard [§18](../experimenting.md) opens with: *"The treatment was
never applied."* A skill is a feature the model may decline — `replace_all`
existed in an arm and was used once in eighteen runs, so seventeen of those runs
compared the control arm to itself. This trial was built to detect that rather
than be surprised by it.

**It did not happen. Every model loaded the skill, every time.**

## Design

**Three arms**, so no cell is uninformative:

| arm | setup | answers |
| --- | --- | --- |
| **A** `none` | no skill installed | the baseline |
| **B** `skill` | installed globally; the model may load it | the feature **as shipped** |
| **C** `inline` | no skill tool; the same body appended to the message | the **ceiling** |

A→C asks whether the instructions are worth anything. B→C asks what on-demand
loading costs. A→B is the number a user cares about. C is a ceiling, not a
replica of B: a loaded skill arrives as a tool result, inlined text arrives in
the user turn.

**The skill** is a chart-styling house style with five rules — a fixed
categorical palette, no chartjunk, a unit on the value axis, direct labels
instead of a legend, horizontal gridlines only. Installed globally, so trust
(orthogonal to this question) never enters. **It is not in this repository**:
it was written for the trial and lives outside the tree, and `run.py` takes
`--skill PATH`.

The palette is the load-bearing part. It was **generated, not borrowed** —
seeded hue rotation over a lightness ladder, then verified: ≥3:1 contrast on
white (WCAG 1.4.11's non-text threshold, not the 4.5:1 text figure), minimum
pairwise ΔE2000 of 23.7 normal / 13.7 deuteranopia / 19.5 protanopia, and no
overlap with tab10, D3, Tableau or Google defaults. `palette.py` does the
search and prints the evidence.

Why that matters: **in arm B the model sees the skill's description in the tool
schema whether or not it loads the body.** So "B beat A" could be a description
effect. The palette appears only in the body, so R1 is the measurement the
confound cannot reach ([§16](../experimenting.md)). Confirmed on the wire, not
assumed — see the rig checks.

**Fixtures.** Three working-but-ugly charts (grouped bar, 3-series line,
4-series bar), each violating all five rules, so the fixture contains the
phenomenon it is scored on. Task, identical across arms and naming no rule and
no tool: *"Make the chart in chart.html look more professional. Keep the data
exactly as it is."*

**Two decoys**, where the skill does not apply: a Go off-by-one, and — the
sharper one — an HTML page with no chart in it, carrying a ten-row table of
near-identical rows. A model that loads a chart skill *there* is over-triggering
on file type rather than on task, which a Go bug cannot probe.

6 models × 3 arms × 3 charts × 3 reps, plus 6 × 2 arms × 2 decoys × 3 reps =
**234 runs**, $2.17, seed 20260829829, shuffled across the whole job list.
Models: `deepseek/deepseek-v4-flash-0731`, `openai/gpt-5.6-luna`,
`qwen/qwen3.8-27b`, `tencent/hy3`, `xiaomi/mimo-v2.5`, `z-ai/glm-5.3-flash`,
priced before the arms were designed (0.045–0.425 in, 0.090–2.550 out per M).

`--yes steps` is granted deliberately: arm B spends an extra step loading the
skill, so an ungranted budget checkpoint would handicap it by construction.
`bash` is not granted.

## Results

| arm | n | loaded | rules/5 | R1 palette | R2 junk | R3 unit | R4 labels | R5 grid |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| **A** none | 53 | 0 | **0.79** | 0 | 9 | 5 | 0 | 28 |
| **B** skill | 54 | **54/54** | **4.96** | 54 | 54 | 53 | 53 | 54 |
| **C** inline | 53 | — | **4.98** | 53 | 53 | 53 | 53 | 52 |

All five rules satisfied: **0/53 → 52/54**, Fisher p = 1.2×10⁻²⁸.
**B versus C: 52/54 vs 52/53, p = 0.76 — the mechanism is free.**

The decomposition is the interesting part, not the aggregate:

- **R5** (drop the vertical gridlines) is something models do anyway — 28/53
  unprompted, the only rule with a real baseline.
- **R2** and **R3** are occasional: 9/53 and 5/53.
- **R1 and R4 are 0/53 and 0/53 unprompted, and 54/54 and 53/54 with the
  skill.** Models never invent your palette and never replace a legend with
  direct labels. That is what the skill buys.

Consistent across every model (9 runs per cell) and every fixture:

| | none | skill | inline | | | none | skill | inline |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| glm | 1.1 | 5.0 | 5.0 | | latency | 0.71 | 5.00 | 4.94 |
| hy3 | 0.9 | 4.9 | 5.0 | | revenue | 0.94 | 4.94 | 5.00 |
| luna | 0.1 | 5.0 | 5.0 | | storage | 0.72 | 4.94 | 5.00 |
| mimo | 0.9 | 5.0 | 5.0 |
| qwen | 1.0 | 4.9 | 5.0 |
| v4flash | 0.8 | 5.0 | 4.9 |

### Counter-metrics

Reported as prominently as the effect, because that is what makes a result safe
to act on.

| | none | skill | inline |
| --- | --- | --- | --- |
| malformed SVG | 0 | 0 | 0 |
| data values changed | 0 | 0 | 0 |
| files edited outside the target | 0 | 0 | 0 |
| cost per run | $0.0118 | $0.0126 | $0.0133 |
| steps per run | 7.7 | **5.5** | 8.2 |

**The skill arm is the cheapest in steps**, despite spending one on the load.
Knowing the rules up front replaces deliberation about what "professional"
means. It costs 7% more money than no skill and 5% less than inlining.

**False-positive loading: 0 of 36.** On both decoys, in every skill-arm run,
the tool was offered (verified on the wire) and declined, and 36/36 fixed the
bug. The HTML decoy — markup, repetitive, no chart — did not draw it either.

### The one real regression

`qwen-skill-storage-1` deleted the legend and added no direct labels, leaving
four storage tiers unidentifiable. It scored 4/5; the chart is *worse* than the
baseline's. **Half of rule 4 is worse than none of it**, and no counter-metric
here catches it — the file is well-formed and the data intact. A rule phrased as
"replace X with Y" can be followed halfway; that is a fact about writing skills,
not about this one.

The other two imperfect runs: `hy3-skill-revenue-1` wrote "Revenue" with no
unit; `v4flash-inline-latency-1` left six vertical gridlines.

### Incidental: a quarter of the reasoning is arithmetic

Not part of the design, measured afterwards because reading the transcripts
made it conspicuous. Counting lines in the reasoning stream that join two
numerals with an operator, or assign a number to a coordinate:

| arm | reasoning lines | arithmetic lines | share | arithmetic chars ≈ tokens |
| --- | --- | --- | --- | --- |
| none | 242 | 49 | 20% | 9,100 ≈ **2,300** |
| skill | 152 | 38 | 25% | 9,400 ≈ **2,350** |
| inline | 176 | 45 | 26% | 10,200 ≈ **2,550** |

Per run. The heaviest single run spent ~12,700 tokens of reasoning on
arithmetic; five more exceeded 7,700. The models are recomputing bar heights,
axis scales and label coordinates by hand, digit by digit — `300.0 * 412 / 672`
and its four hundred siblings.

**The interesting part is that the skill does not reduce it.** It cuts
reasoning lines by 37% (242 → 152) while arithmetic characters stay flat
(9,100 → 9,400). What the skill removes is *deliberation about what
"professional" means*; the geometry is untouched, because the geometry was
never the part the model was unsure about. That is also why the skill arm
spends fewer steps: it is not thinking faster, it is thinking about one fewer
thing.

So the two costs are separable, and a skill only addresses one of them. The
other wants a calculator — but not, as tested, a Starlark one: Starlark rejects
`round()`, `sum()`, `**`, f-strings, `%` formatting, `.format()` specs, `while`
and `try`, while accepting enough Python to look like Python. A model's
instincts would walk it into a syntax error per attempt. A tool for this should
have a grammar with no Python resemblance to mislead.

*Metric caveat:* a regex proxy over the rendered reasoning stream, and
characters÷4 for tokens. It counts a line once however much arithmetic is on
it, so it under-reports; it cannot tell recomputation from first computation.
Directionally sound, not precise. `arith.py` reproduces the table — it needs
the raw transcripts, which are not committed, so it takes `--runs <dir>`.

Two runs were dropped, both provider failures (`Empty response received`, an
`INTERNAL_ERROR` stream reset), one in `none` and one in `inline` — neither in
`skill`, so the exclusions do not favour the treatment.

## What the rig had to survive first

Six checks, each mechanical. Three found faults that would have shipped, and
they pushed in **both** directions.

1. **The arms differ on the wire.** Captured with `cmd/strumentrec`: arm A
   offers 11 tools and no `skill`; arm B offers 12 with `enum:
   ["chart-style"]`; arm C offers 11 and carries the palette in the user
   message. Critically, arm B's *first* request contains **none of the six
   hexes** — so R1 cannot be satisfied by a description effect.
2. **The scorer reads 0 and 5.** Untouched fixture → 0/5; hand-written
   reference → 5/5, on all three fixtures. This caught a real cap: the first
   compliant generator still drew vertical gridlines, so both new references
   came out at 4/5. Left alone, arm B would have been limited by the fixture
   and the writeup would have said "the skill works less well on line charts".
3. **Each rule discriminates.** Breaking the reference one rule at a time flips
   exactly that rule. One break silently no-opped on a stale coordinate — a
   break that does nothing looks identical to a check that works.
4. **The palette is unguessable.** 0 of 53 baseline runs emitted a palette hex.
   They reached for Tableau (`#4E79A7`/`#F28E2B`), ColorBrewer
   (`#2166AC`/`#A6BEDB`), and their own inventions instead.
5. **The mechanism is reached.** 54/54.
6. **The task is acted on in every arm.** 53/53, 54/54, 53/53.

### Three scorer faults

- **R5 called a greyed y-axis a gridline.** A model had deleted all seven
  verticals and greyed the axis from black to `#BBBBBB` — which rule 1 asks
  for. R5 was marking it down for obeying a different rule. *Deflated the
  treatment arm.* Fixed by classifying axes geometrically: a line on the plot
  box's edge is an axis, only interior verticals are gridlines.
- **R1 called Tailwind's cool greys data colours.** `#374151` has a channel
  spread of 26 and a Lab chroma of 11. Structural greys run C\* 0–11 and data
  colours 17.3 up, so chroma separates them and channel spread does not.
  *Deflated the treatment arm.*
- **R2 passed a chart with gradient fills and drop shadows.** Rule 2 forbids
  them; the check only looked for a background and a border — one clause
  shorter than the rule it was named after. *Inflated the baseline*, which is
  the direction that matters.

In two of the three the model's own summary was right where the scorer was
wrong.

And a free finding from reading the counter-arm's *other* lines: with the strip
disabled, three of seven markup fixtures still did not fire. They were
decorative, passing as regression tests while proving nothing. Every "must not
fire" fixture now asserts it can fire first.

## Two Strument bugs, found by running it

- **The loop detector fired on quoted markup** (`a0b981c`). Two of thirty pilot
  runs were stopped mid-turn for quoting the file they had been asked to edit.
  `script/find-loops.py` — the tool that tuned the thresholds — has always
  stripped fenced code; the in-process detector never got that step, and the
  divergence was the bug.
- **A pinned file named absolutely was written to the wrong place**
  (`ed4e0c6`). `unsafePath` exempts a pinned file before testing for an
  absolute path; `fullPath` then joined it onto the root anyway, so the write
  landed in a shadow tree mirroring the whole absolute path inside the project
  while the real file kept its old contents — silently. The model reported a
  fix that was not there.

Both were found because the trial ran the real binary against real tasks, which
is the argument for live passes in one line.

## What this supports, and what it does not

Supported: a relevant skill is loaded reliably (54/54, six models, three
fixtures); it moves compliance from 0.79/5 to 4.96/5; on-demand loading is
indistinguishable from having the text in context (p = 0.76); it does not
corrupt output, does not spread to other files, and is not pulled in when
irrelevant (0/36).

Not supported. Every arm is at a ceiling or a floor, so this measures a skill
whose rules are unambiguous and mechanically checkable — the easy case, and the
one a scorer can score. It says nothing about a skill of judgement. The
`qwen-skill-storage-1` regression is n=1 and the trial cannot put a rate on
partial compliance, which is the failure mode most worth knowing. And "the
model declined a chart skill on a Go bug" is a weaker claim than it sounds:
these decoys are unambiguous, and a near-miss task (a chart in a project the
task is not about) is untested.

**Worth a trial next:** whether a calculator tool cuts the arithmetic above.
The observation is solid — the token counts are real and uniform across arms —
but this trial did not manipulate it, so it says nothing about whether a
calculator would be *reached for*, which §18 says is the question to settle
first and cheaply.
