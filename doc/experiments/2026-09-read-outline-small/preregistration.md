# Preregistration: outlines in the read tool, smaller models

Written and committed before the main run. A follow-up to
[`2026-09-read-outline`](../2026-09-read-outline/README.md), which found no
saving from outlines with MiMo-V2.6-Flash and GPT-6 Luna. That write-up left
one question open: whether a weaker model behaves differently. A weaker model
may be more likely to read whole files, which is what arm B acts on, or to
lean on a map rather than on grep.

## What is the same

- **Fixture and tasks:** the parent's fixture (`data/fixture.py` there), with
  the same prompts.
- **Metrics:** the same.
- **Rule:** the same, applied to B and C against A. An arm saves if its median
  input tokens on the past-window tasks are lower than A's at p < 0.05
  (two-sided Mann-Whitney U, pooled over models), and its correctness is not
  lower at p < 0.1 (Fisher). Each model's direction is reported.

## What differs

- **Arms:** A, B and C only. D's required `limit` was answered by the parent:
  reads were already ranged.
- **Arm code:** the parent's arm commit `cae2b9e` plus
  [`data/absolute-path.patch`](data/absolute-path.patch). The patch fixes a
  bug the pilot found. `read {outline: true}` with an absolute path, which
  `read` accepts inside the project, answered "There is no outline", because
  the path was joined to the project root without being resolved. All eight
  of the parent's outline calls used relative paths, so the parent's result
  is unaffected.
- **Tasks:** seven. The `flag` task is dropped, because its planted key was
  wrong.
- **Models:** three, at these reasoning settings.

  | model | reasoning |
  | --- | --- |
  | Ling 3.0 Flash (`inclusionai/ling-3.0-flash`) | provider default: OpenRouter offers no effort levels for it |
  | Qwen3.8 27B (`qwen/qwen3.8-27b`) | `low` |
  | Qwen3.6 35B A3B (`qwen/qwen3.6-35b-a3b`) | provider default: OpenRouter offers no effort levels for it |

  Qwen3.8 runs on the paid route. The free one (`:free`) timed out in all
  three pilot sessions, and a direct request returned 429, "temporarily
  rate-limited upstream", from a shared pool.
- **Size and order:** 3 arms × 3 models × 7 tasks × 3 reps = 189 sessions,
  shuffled with seed 20260928, four at a time.

## Pilot

Twelve sessions, one per model and arm, with Qwen3.8 run again on the paid
route. All answers were correct except one.

- **Ling flails.** The worst session took 109 steps and 3.2M input tokens.
  - It repeated a bash command that had been declined five times over.
  - It passed grep parameters that do not exist (`output_mode`).
  - It emitted a tool name with markup in it (`ls …</arg_value>`).
  - It called `outline` once, hit the absolute-path bug, then paged through
    the file 100 lines at a time.
  - It ended without an answer.

  A fixed outline could matter most for this model, and its sessions are also
  where token counts will be noisiest.
- **Qwen3.6 is quick and grep-first,** like the parent's models.
- **Qwen3.8** answered all three pilot tasks in 31 to 38 seconds, at about
  $0.006 a session. In one session it called the `commit` tool unprompted.

## Prediction

- **Qwen3.6 and Qwen3.8:** the parent's null.
- **Ling:** no prediction. Its variance is too high to guess the sign.
