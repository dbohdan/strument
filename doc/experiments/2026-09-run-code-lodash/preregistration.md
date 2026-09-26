# Preregistration: a helper library for `run_code`

Written and committed before the main run. The design is in
[`README.md`](README.md).

## Arms

Three binaries from one commit. They differ only in the link-time string
`coder.codeArm` (`internal/coder/codearm.go`), and a test pins what each arm
installs.

| arm | runtime | `run_code` description |
| --- | --- | --- |
| A | unchanged | unchanged |
| B | `Object.groupBy` and `Map.groupBy` polyfilled, in their ES2024 shape | unchanged |
| C | B, plus Lodash 4.17.21 as `_` | one added line: "Inside a program, Lodash 4 is loaded as _: for example _.countBy(rows, "kind"), _.groupBy, _.sortBy, _.uniq, _.keyBy and _.sumBy." |

- **Lodash** is the npm tarball, sha256 `6a087ac9…caf804`. It is compiled
  once per process and costs about 5 ms per program.
- **C's line is worded as an addition inside a program,** not as a list of
  what can be called. That follows the pilot of
  [`2026-09-tool-disclosure`](../2026-09-tool-disclosure/). There, rewording
  the "callable functions are exactly" sentence made no difference, and no
  model read it as the tool list.

## Fixture and tasks

- **Fixture:** Larkspur's year of harvest logs (`data/fixture.py`, seed
  20260926). It has twelve monthly JSON files with 240 records, and a list
  of 20 plot holders.
- **The key** is computed from the data, and asserted to have no ties.

| task | kind | answer |
| --- | --- | --- |
| `count` | count by key | records per crop family, 6 lines |
| `groupmax` | group and pick | each bed's heaviest harvest, 16 lines |
| `join` | join two sources | the plot holders with no harvest, 4 names |
| `distinct` | distinct across files | the number of varieties (27) |
| `control` | one lookup | the weight of H-0042 (3,311 g) |

## Models and size

- **Models:** MiMo-V2.6-Flash and Qwen3.8-27B, both at `reasoning = "low"`.
- **Size:** 3 arms × 2 models × 5 tasks × 6 reps = 180 sessions.
- **Order:** shuffled with seed 20260926, four at a time.
- **Per session:** a fresh fixture, `--no-git`, and `--yes steps`.
- **The pilot's six sessions are discarded.**

## Metrics

- **Primary: program bytes.** The summed length of a session's `run_code`
  programs, on the four helper tasks.
- **Counter-metric: correctness,** on all five tasks.
- **Reported:**
  - `run_code` calls per session;
  - steps;
  - uptake: `_.` calls in C, and `groupBy` calls in B and C;
  - Lodash 3 names that Lodash 4 removed;
  - cost.

## Rule

- **What counts as a win.** An arm wins if its median program bytes on the
  helper tasks are lower than A's at p < 0.05 (two-sided Mann-Whitney U,
  pooled over models), **and** its correctness is not lower at p < 0.1
  (Fisher).
- **Which ships.** If B and C both win, B ships, because it is smaller,
  unless C's bytes are also lower than B's at p < 0.05.
- **If neither wins,** nothing ships, and the arm code is reverted.

Per-model directions are reported beside the pooled test.

## Pilot

Six sessions, one per arm and model:
- **All six answers were correct.**
- **Five wrote a program.** The sixth was the control task, which was
  answered without one.
- **Both programs read looped with `for`** and accumulated into an object.
  Neither used the `reduce` idiom that motivated the design.
- **Qwen used `_.sortBy` once, in arm C,** to order the output lines.

## Prediction

A null. The pilot's models loop rather than reduce, so there is little
hand-rolled code for a library to replace. Uptake in C will be real but
shallow: sorting and formatting, not the aggregation. B's polyfill will go
almost unused, because no transcript has ever reached for `groupBy`.
