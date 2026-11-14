# Featherweight against Strument

**Status: trial, run 2026-11-13.** The design was committed after the first
pilot, the pilot section before the main run. The results are at the end. In
short:
- On these tasks, neither Featherweight's harness nor its prompt made a
  measurable difference to success.
- The harness made a difference to side effects. Featherweight left a stray
  file in a quarter of its sessions (Strument in none), almost all named `&1`,
  and it wandered outside its project in 9 of 160 sessions (Strument in none
  of 320).

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

## Pilots

**The first pilot** was one session per cell: 24 sessions, $0.06. It found:
- **Plumbing:** every session finished, every response came from its pinned
  provider, and the scorer read both harnesses.
- **The fix tasks at the ceiling,** 12 of 12. Their reports named both causes
  ("`x,3,4,5` should add 12"; "ascending numeric order"). They were rewritten
  as symptoms, as users report them: the input, what was expected, that
  something else came out. The hidden tests were unchanged.
- **Featherweight leaving a file named `&1`.** The model wrote shell
  redirection (`2>&1`) inside Tcl's `exec`, which creates a file by that name.

**The second pilot** was the rewritten fix tasks, two sessions per cell: 24
sessions, $0.15.
- **Still at the ceiling.** 24 of 24 passed every hidden check, in every arm,
  with both models.
- **Featherweight's side effects:**
  - one session left an `npm.err` file;
  - one edited `README.md` and `docs/notes.md` while fixing the code;
  - its MiMo sessions took 16 to 22 steps, against Strument's 11 to 12.
- **S-fw edited `docs/notes.md` once.** S changed nothing outside `src/` and
  `test/`.

**Decision, made before the main run:** the fix tasks stay as they are, and
the rule is changed to read them for what they can show.
- Small fixes are within reach of both models in every arm. Harder ones would
  be a different trial.
- So success on the fix tasks is a floor check: any failure in any arm is
  news.
- The fix tasks carry the cost, work and side-effects comparisons.
- The success comparisons of rules 1 and 2 are made on the two ask tasks,
  where the pilots did vary: `tally-ask` went 2 of 4 for S, 4 of 4 for S-fw
  and 3 of 4 for FW. Neither pilot's sessions are counted.

## Rule

All p-values are Fisher two-sided. Rules 1 and 2 pool the two ask tasks and
both models: n = 80 per arm. Every task is also reported on its own.

1. **Prompt:** S against S-fw on success on the ask tasks. A significant loss for S-fw says
   Strument's prompt earns its length. No significant difference, with S-fw
   no more than 5 points below S, supports cutting the prompt toward
   Featherweight's.
2. **Harness:** S-fw against FW on success on the ask tasks, read the same
   way. A significant
   loss for FW says Strument's tools and loop help. No significant difference
   says they do not show on tasks like these.
3. **Cost:** the median cost per session in each arm, per task. A difference
   in success is weighed against this, not reported alone.
4. **Fix tasks:** success is reported as a floor check, with every failure
   read. Steps, cost and side effects are compared across arms. For each
   side-effect count (out of scope, files left behind, own tests broken),
   FW against S and S-fw against S, Fisher, n = 80 per arm.
5. **Side effects** on the ask tasks are reported the same way. A harness
   that succeeds by leaving the project dirty is not the same as one that
   doesn't.

## Prediction

- **Prompt:** no significant difference. In the tool-disclosure and concision
  trials the long prompt mostly shaped prose, and short answers came at no
  measured cost.
- **Harness:** no significant difference in success. Featherweight takes more
  steps on the ask tasks, because Tcl `exec` is a less direct way to run
  `node` than `bash`, and costs about the same or less, because its fixed
  prefix is about a fifth of Strument's.
- **Side effects:** Featherweight leaves more stray files and edits more
  outside the code, significantly on the fix tasks. The pilot found a
  file named `&1`, from shell redirection written inside Tcl's `exec`.
  Strument commits every fix turn, and Featherweight none.

## Results

480 sessions, all finished; 7 ended without an answer. Every response came
from its pinned provider. Total cost $1.85. Data:
- the scorer's rows and tables, in [`data/scores.txt`](data/scores.txt);
- side effects, in [`data/sidefx.txt`](data/sidefx.txt);
- each session's own record, compressed, in `data/runs.tar.xz`: Strument's
  session log or Featherweight's session file, plus stdout and the result.

The wire captures (388 MB) are not kept; the costs in the scores come from
them.

### Success

| task | S | S-fw | FW |
| --- | --- | --- | --- |
| tally-ask | 15/40 | 10/40 | 9/40 |
| pricetag-ask | 40/40 | 40/40 | 40/40 |
| **ask tasks, pooled** | **55/80** | **50/80** | **49/80** |
| tally-fix | 39/40 | 38/40 | 40/40 |
| pricetag-fix | 38/40 | 39/40 | 39/40 |

1. **Prompt (S against S-fw), ask tasks: 55 against 50 of 80, p = 0.51.**
   - Not a significant loss, so the rule's "Strument's prompt earns its
     length" is not met.
   - S-fw is 6.3 points below S, outside the 5-point margin set for "supports
     cutting", so that is not met either. **Inconclusive.**
   - The difference is all DeepSeek on `tally-ask`: 10 of 20 under S, 4 under
     S-fw (p = 0.10). MiMo was 5 against 6. A subgroup pattern, not a result.
2. **Harness (S-fw against FW), ask tasks: 50 against 49 of 80, p = 1.0.** No
   difference. Given a short prompt, Featherweight's tools and loop did as
   well as Strument's.
3. **Fix tasks, the floor check:** 77, 77 and 79 of 80. All seven failures
   were read, and none is specific to a harness:
   - four are MiMo on `pricetag-fix` ending its turn mid-deliberation, with no
     answer and no edit (2 S, 1 S-fw, 1 FW);
   - two are DeepSeek fixing the `parseInt` bug and missing the sort (S,
     S-fw);
   - one is DeepSeek under S-fw rewriting `tally` to return a `Map` ("the
     cleaner contract"), which changed its interface across 7 files.

### Work and cost

Medians, both models pooled:

| task | steps (S / S-fw / FW) | cost (S / S-fw / FW) |
| --- | --- | --- |
| tally-ask | 5 / 5 / 9 | $0.0021 / $0.0019 / $0.0020 |
| pricetag-ask | 4 / 3 / 7 | $0.0014 / $0.0010 / $0.0011 |
| tally-fix | 13 / 12.5 / 16 | $0.0064 / $0.0054 / $0.0060 |
| pricetag-fix | 11 / 11 / 14 | $0.0040 / $0.0042 / $0.0045 |

- **Featherweight takes more steps everywhere** but costs about the same per
  median session: its fixed prefix is about a fifth of Strument's.
- **Its tail is longer.** Total cost was $0.77 for FW, against $0.55 for S and
  $0.52 for S-fw, mostly from long MiMo sessions. MiMo's median `tally-fix`
  wall time was 360 s under FW, 233 under S-fw and 165 under S.
- **The short prompt made Strument slightly cheaper,** with the same number of
  steps.
- **Every arm ran the project's tests** in nearly every fix session.

### Side effects

| | S | S-fw | FW |
| --- | --- | --- | --- |
| left files, fix tasks (of 80) | 0 | 0 | **28** (p < 0.0001 vs S) |
| left files, ask tasks (of 80) | 0 | 0 | **11** (p = 0.0007 vs S) |
| edited outside `src/` and `test/`, fix tasks | 4 | 3 | 5 |
| broke the project's own tests | 0 | 0 | 0 |
| reached outside the project | 0 | 0 | **9 of 160** |
| commits on the fix tasks (80 sessions) | 79 | 81 | 0 |

- **Left files: 36 of the 39 Featherweight files are named `&1`.** Models write
  shell redirection (`2>&1`) inside Tcl's `exec`, which takes `>&1` for
  "write to a file called `&1`". That is a cost of the Tcl tool, not of the
  prompt: S-fw, with Featherweight's prompt and Strument's `bash`, left none.
  A one-line fix suggests itself for Featherweight: say in `run_code`'s
  description that Tcl spells it `2>@1`, or catch the mistake in its `exec`
  wrapper.
- **Out-of-scope edits** were mostly `docs/notes.md`, about equally in all
  arms. They are models updating the notes after a fix, arguably in scope.
- **Reaching outside the project** ([`data/left_project.py`](data/left_project.py))
  happened only under Featherweight, 9 of 160 sessions:
  - **4 lost track of the project root.** They took it for
    `/home/user/strument`, because `about` reports the script's path, and in
    this trial the script lives in Strument's repository. Their imports from
    there failed.
  - **2 read the trial's record of their own session** (`wire.jsonl`, beside
    the project).
  - **1 listed the names of files in sibling sessions' directories.**
  - **2 used `cd ..` or a long `../` path** to reach `/tmp`.

  Every one was read: none read `tasks.py`, a hidden test, or another
  session's answer.

  Two things made this possible. Featherweight's `run_code` can `cd`, and in
  Tcl the working directory belongs to the whole process, so the harness's own
  state followed the model. One session's later records landed two
  directories above its project. And the sandbox confines writes only, so
  reading anywhere was allowed. Strument's file tools refuse paths outside
  the project without approval, and no Strument session tried.

### Predictions, scored

- **Prompt, no significant difference:** right on significance, but the
  margin needed to call it support for cutting was missed (6.3 against 5
  points).
- **Harness, no significant difference in success:** yes (50 against 49).
- **Featherweight takes more steps and costs about the same:** yes for the
  medians. Its total cost was 40% higher, from a long tail.
- **Featherweight leaves more stray files, and edits more outside the code:**
  more stray files, yes, strongly. Edits outside the code, no: about equal.
- **Strument commits every fix turn, Featherweight none:** yes.

### What this says about paring Strument down

- **The harness machinery that a task score can see** (more tools, a richer
  loop) **did not show on these tasks.** A short prompt with Featherweight's
  six tools did as well as Strument's fifteen. Small tasks are where the extra
  machinery should matter least, so this is a floor, not a verdict on larger
  work.
- **The prompt is unresolved, and the one hint points at keeping some of it.**
  DeepSeek's `tally-ask` drop under the short prompt (10 → 4 of 20) is the
  kind of effect a targeted trial could settle: which part of Strument's
  prompt does DeepSeek use to run the code and check it?
- **What did show is the review surface.** Strument left no stray files and
  never left its project. Its file tools stayed in bounds, its `bash` is
  plain shell, and its per-turn commits and snapshots make every turn
  reviewable. Those are the parts the philosophy names as not moving, and
  the trial gives no reason to cut them. If anything, it is a measured case
  for keeping them.
- **Featherweight's advantages are real and cheap to copy:** a short prompt,
  one execution tool that cannot be confused with another, and a session
  store that cannot lose history. Its weaknesses here are specific and
  fixable: the Tcl redirection gotcha, and a process-wide `cd` that the
  harness follows.

### Limitations

- **Small tasks only;** both fix tasks were near the ceiling for every arm.
- **Two models, one provider each.**
- **The fork's `about` tool points at a script inside Strument's
  repository.** That contributed to Featherweight's lost-root sessions, and
  in ordinary use it would be less misleading.
- **The trial's sandbox did not stop reads outside the project.** No session
  used that to reach the hidden tests, as checked above. A future harness
  trial should keep scoring material where no session can read it.
