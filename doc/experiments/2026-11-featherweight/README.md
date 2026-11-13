# Featherweight against Strument

**Status: trial, preregistered.** This design was committed after the first
pilot and while the second was running, before its results were read. A
pilot section follows before the main run, and results after it.

## Why

[Featherweight](https://github.com/dbohdan/featherweight) is an agent harness
that models built through itself, starting from a small core the maintainer
wrote in Tcl. It is about 3,400 lines in one file. Strument is about 38,000
lines of Go, much of it inherited from aider. The two differ in nearly every
part a model sees:

| | Strument | Featherweight |
| --- | --- | --- |
| system prompt | 4,936 characters (859 words) | 440 characters (71 words) |
| tools | 15; schemas 20,357 characters | 6 |
| running code | `bash`, plus a JavaScript `run_code` that cannot touch the project | one `run_code`: Tcl with `exec` |
| per-turn commits | yes | no |
| sandbox, approvals, secret-file guard, `/undo` | yes | none |

The maintainer is considering paring Strument down. This trial asks whether,
on small coding tasks, Strument's extra prompt and machinery buy anything a
model can show, and if so, whether it is the prompt or the harness that does.
It cannot measure what Strument's safety and review machinery is for. A tie
here argues for less prompt, not for less sandbox.

## Arms

| arm | what runs |
| --- | --- |
| S | Strument at `4b941d2`, as it is |
| S-fw | the same binary, with `prompt_code` set to Featherweight's prompt |
| FW | Featherweight, the trial fork [`data/featherweight-trial.tcl`](data/featherweight-trial.tcl) |

- **S-fw's tool names are translated.** Featherweight's `read_file`,
  `write_file` and `edit_file` become `read`, `write` and `edit`, and its Tcl
  `run_code` becomes `bash`. A prompt that names tools the schema lacks is a
  mismatch this project has measured. The text is in
  [`data/runner.py`](data/runner.py).
- **S against S-fw** is the prompt's effect, with the harness held constant.
- **S-fw against FW** is everything else: the tools, the loop, folding, the
  wire. Both prompts are short there.

**The fork.** Featherweight has no headless mode, and its maintainer does not
plan one, so this repository carries a fork made for this trial: Featherweight
at commit `915b3b3` with three marked changes, listed in its header.
1. `debug turn --message` runs one live turn through the same
   `request_async`/`apply_message` loop the window uses.
2. A `provider_only` key pins OpenRouter's routing.
3. A headless turn writes its debug log to the state directory, not to the
   project.

The prompt, tools, folding and loop are upstream's. Upstream's own test
scripts pass on the fork, except the two that need Tk.

**Sandboxing.** Featherweight runs inside
[`script/strument-sandbox.sh`](../../../script/strument-sandbox.sh) (Landlock),
since it has none of its own. Strument uses its built-in sandbox. Both confine
writes to the project and the temp directories.

## Models and routing

| model | provider (pinned) |
| --- | --- |
| MiMo-V2.6-Flash | Xiaomi (first party) |
| DeepSeek-V4.1-Flash | Wafer |

Both run at `reasoning = "low"`, the effort Featherweight hard-codes. Routing
is pinned in every arm, as the handbook now requires after
[2026-11-concision-block](../2026-11-concision-block/): `extra_params` for
Strument, `provider_only` for Featherweight. Every request goes through
`strumentrec`, and the scorer reports any response that came from another
provider.

## Tasks

Four tasks, one user turn each, in a fresh git copy of the fixture
([`data/tasks.py`](data/tasks.py)):

| task | fixture | what is asked | scored by |
| --- | --- | --- | --- |
| tally-ask | `tally` | four yes/no questions about behavior; running the code finds the answers | all four right |
| pricetag-ask | `pricetag` | four yes/no questions; reading suffices | all four right |
| tally-fix | `tally` | a user's report: these lines, these expected totals, wrong totals came out. Find and fix it | 8 hidden checks, all passing |
| pricetag-fix | `pricetag` | a shop's report: comma prices total too low, valid SKUs rejected. Find and fix it | 8 hidden checks, all passing |

- **The hidden tests are kept outside the project.** They run after the
  session against its final state, committed or not.
- **`tasks.py --check` validates each suite both ways:** it fails on the
  fixture as shipped (1 of 8 and 4 of 8 checks pass) and passes on a reference
  fix (8 of 8), and the fixture's own tests pass both before and after.

## Measures

All are counts from each session, via [`data/score.py`](data/score.py).

- **success:** all four answers right, or every hidden check passing.
- **work:** steps, tool calls, output tokens, cost and wall time; whether the
  model ran the project's code, and whether it ran the tests.
- **side effects:**
  - the project's own tests broken;
  - files changed outside `src/` and `test/`;
  - files left behind, which git does not ignore;
  - commits made.

## Rule

All p-values are Fisher two-sided. Models and tasks are pooled per comparison,
n = 160 per arm. They are also reported per task.

1. **Prompt:** S against S-fw on success. A significant loss for S-fw says
   Strument's prompt earns its length. No significant difference, with S-fw
   no more than 5 points below S, supports cutting the prompt toward
   Featherweight's.
2. **Harness:** S-fw against FW on success, read the same way. A significant
   loss for FW says Strument's tools and loop help. No significant difference
   says they do not show on tasks like these.
3. **Cost:** the median cost per session in each arm, per task. A difference
   in success is weighed against this, not reported alone.
4. **Side effects** are reported for every arm. No rule; a harness that
   succeeds by leaving the project dirty is not the same as one that doesn't.

## Prediction

- **Prompt:** no significant difference. In the tool-disclosure and concision
  trials the long prompt mostly shaped prose, and short answers came at no
  measured cost.
- **Harness:** no significant difference in success. Featherweight takes more
  steps on the ask tasks, because Tcl `exec` is a less direct way to run
  `node` than `bash`, and costs about the same or less, because its fixed
  prefix is about a fifth of Strument's.
- **Side effects:** Featherweight leaves more stray files. The pilot found a
  file named `&1`, from shell redirection written inside Tcl's `exec`.
  Strument commits every fix turn, and Featherweight none.
