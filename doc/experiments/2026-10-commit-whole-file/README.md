# Telling the commit tool that an edited file is committed whole

**2026-10-09.** 96 live sessions, three models, two arms, order shuffled (seed
20261009). Preregistered in cf45d0b before the run. Data, runner and scorer in
`data/`.

## Why

In the live pass for committing what the model's shell commands staged (c2193b6), the
fixture asks for two commits — a `git mv` rename, then a change to `greet.py` —
in a repository where the user has an unstaged line in `greet.py` and a staged
change to `other.txt`. Qwen3.8-27B failed it twice in two ways: once it reset
its own commit to split the user's line into a commit of its own, unstaging
the user's `other.txt` on the way; once it spent the turn deliberating whether
the commit tool takes an edited file from the index or from disk, reverse-
engineered the answer from the first commit's diffstat, and hit the 400 s
runner timeout before making the second commit.

The commit tool's description does not say. The treatment adds one sentence of
fact:

> A file you edit is committed whole, as it is on disk, including any changes it
> had before your first edit; the commit names such a file in a trailer.

## Design

Two binaries, built from c2193b6 and from c2193b6 plus that sentence
(`data/treatment.diff`). Verified on the wire against a local stub before any
spend: the request bodies differ in the tool description and nowhere else
except the working-directory line, which is why run directories are neutral
ids (`run-NNN`) — a directory named after the arm would have told the model
which arm it was in.

One message per run, the fixture above, `--yes bash`, 600 s timeout.
MiMo-V2.6-Flash, GLM-5.3-Flash and Qwen3.8-27B, `reasoning = "low"`, 16 reps
per model per arm: 96 runs, job list shuffled (seed 20261009), four at a time.

**Scorer** (`data/scorer.py`): counts read off git state and output, no
judgement. Self-tested against five runs from the earlier live pass whose
outcomes were read by hand, in both directions. It caught one wrong
expectation of mine, not one of its own.

- **Primary — success:** the turn finished (a `Tokens:` line), the rename
  landed whole in one commit (`R notes.txt → NOTES.md`, HEAD has no
  `notes.txt`, `NOTES.md` is `notes\nmore\n`), HEAD's `greet()` returns
  `"hi"`, and the rename's commit does not touch `greet.py`.
- **Secondary:** finished; output tokens; steps; history-rewriting git
  commands (`reset`, `--amend`, `rebase`, `restore --staged`, `checkout --`,
  `update-index`).
- **Counter-metrics:** the user's `# user comment` line lost (in neither HEAD
  nor the working tree); the user's staged `other.txt` no longer staged and
  uncommitted; its content lost from disk. And success on MiMo and GLM, which
  passed this fixture and could be made worse by the sentence.

**Decision rule.** Ship the sentence if Qwen's success rises by at least 3/16
and no counter-metric, on any model, worsens by more than 2/16. Otherwise do
not. Fisher's exact p is reported for each comparison but is not the gate:
at 16 per cell it cannot be, for anything short of a large effect.

A pilot of one run per model per arm checks the runner, that models call the
commit tool, and the scorer on live output; it is not pooled.

---

## Result: not shipped by the preregistered rule

**96 runs, all finished or timed out cleanly (no worker failures). $0.50,
plus three timed-out runs whose cost was never printed.**

| model | success: baseline | treatment | p |
| --- | --- | --- | --- |
| Qwen3.8-27B | 13/16 | 15/16 | 0.60 |
| MiMo-V2.6-Flash | 16/16 | 16/16 | 1.00 |
| GLM-5.3-Flash | 4/16 | 2/16 | 0.65 |

Qwen rose by 2/16; the rule asked for 3. GLM fell by 2/16, inside the 2/16 the
rule allowed. **The sentence does not ship.**

The baseline failed far less often than the two-for-two that prompted the
trial: 13/16 succeeded. A prompt case found by watching a model fail twice
regressed toward its mean.

### What moved anyway, on Qwen

| Qwen | baseline | treatment | permutation p |
| --- | --- | --- | --- |
| output tokens, median | 6,600 | 1,300 | 0.012 |
| wall clock, median | 266 s | 67 s | 0.038 |
| runs rewriting history (`reset`, `--amend`, `rebase`, …) | 6/16 | 0/16 | 0.017 (means) |
| steps, median | 10 | 9 | 0.031 |
| cost, mean | $0.018 | $0.009 | |

MiMo moved the same way and did not reach significance (output tokens 4,950 →
3,800, p = 0.17); GLM did not move. These were secondary metrics, chosen
before the run, and they are the cost of deliberation the trial was prompted
by: Qwen, told nothing, spends minutes and thousands of tokens working out
whether an edited file is committed from the index or from disk, and in a
third of runs rewrites its own history to find out. The sentence ends that.
It did not change whether the task got done, which is what the gate measured.
A confirmatory trial with output tokens or history rewrites as the primary is
the honest next step if the sentence is wanted; this result cannot be
promoted to one after the fact.

### GLM's success rate is a bug since fixed, not the arms

24 of GLM's 26 failures, in both arms, are one pattern: `git mv notes.txt
NOTES.md && echo more >> NOTES.md` in one command. The binaries under test
committed a staged path as the index had it, so the rename went in without
the line. 19ea9d1, made while this trial ran, commits staged paths as they
are on disk; the same command, rerun live on MiMo, GLM and Qwen, now commits
both. The other two: one run never wrote the line, one wrote `notes\n\nmore\n`
and the scorer's exact-content check rejected the blank line.

### The finding the trial was not designed for: models delete the user's line

The fixture's `greet.py` carries the user's uncommitted `# user comment`. In 5
of 96 runs that line ended in neither HEAD nor the working tree — deleted by
the model, through its edit tool, to keep the user's work out of its own
commit (`data/transcripts/`):

| run | model | arm | how |
| --- | --- | --- | --- |
| 009 | Qwen | baseline | read the uncommitted-changes notice, re-edited greet.py to drop the line |
| 021 | Qwen | treatment | the same, then timed out |
| 031 | Qwen | baseline | dropped it before the first commit, unprompted |
| 033 | Qwen | baseline | read the notice, re-edited greet.py to drop the line |
| 056 | MiMo | baseline | dropped it before committing; the notice then said the changes *were* in the commit, which was false by then, and MiMo rewrote history over the contradiction |

Four of the five came after the notice that 579ae4a added ("greet.py had
uncommitted changes before you changed it; they are in this commit"), which
models read as a problem to repair. The one repair available to them is
deletion. Before 579ae4a, Strument committed a dirty file separately before
editing it, so the user's line would have been in its own commit and there
would have been nothing to repair. This is a regression introduced by
replacing that commit, and it is the reason this write-up is longer than its
result.

### Counter-metrics

| | Qwen B / T | MiMo B / T | GLM B / T |
| --- | --- | --- | --- |
| user's line lost | 3 / 1 | 1 / 0 | 0 / 0 |
| user's staged `other.txt` no longer staged | 2 / 0 | 2 / 0 | 0 / 0 |
| `other.txt` content lost from disk | 0 / 0 | 0 / 0 | 0 / 0 |

None worsened in the treatment arm.

### Equipment

- The scorer's self-test caught a wrong expectation of mine (round-1 Qwen had
  unstaged `other.txt`), not a fault of its own, and gained the
  `other_lost` column that separates "unstaged" from "gone".
- An arm-named run directory would have put the arm in the system prompt's
  working-directory line; the stub check found it before the run.
- Qwen at `reasoning = "low"` still ran to the 600 s timeout in 3 runs.

## Follow-up: separate commits restored (563d66b)

The finding above decided it: the user's pre-existing changes went back to a
commit of their own, made before the model's first write to the file, of the
contents the turn found (`internal/coder/staging.go`). The same fixture, rerun
on that build with no treatment sentence — 48 runs, 16 per model, shuffled
(seed 20261010), `data/followup/`:

| | Qwen | MiMo | GLM |
| --- | --- | --- | --- |
| success | 16/16 | 16/16 | 14/16 |
| user's line lost | **0/16** | **0/16** | **0/16** |
| user's line in HEAD | 16/16 | 16/16 | 16/16 |
| separate commit made | 16/16 | 16/16 | 16/16 |
| runs rewriting history | 0/16 | 6/16 | 2/16 |
| user's `other.txt` still staged | 16/16 | 16/16 | 15/16 |

Against the trial's no-sentence arm: lost lines 3/16 → 0/16 on Qwen and 1/16 →
0/16 on MiMo; Qwen's success 13/16 → 16/16 and its history rewrites 6/16 → 0/16
— the deliberation the sentence was meant to end is gone without it, because
there is nothing of the user's in the model's commit to deliberate over.
Different builds a day apart, not an A/B, so read the direction rather than
the margins.

GLM's two failures committed `notes\n\nmore\n` (its own `printf '\nmore\n'`);
the rename and the line landed, and the exact-content scorer rejects the blank
line. The staged-from-disk fix (19ea9d1) is what took GLM from 4/16 to 14/16.

What remains is models protecting the user's *staged* file themselves: GLM ran
`git reset other.txt` once, leaving it unstaged (contents intact), and MiMo's
six rewrites are `restore --staged` / `reset --soft` / amend sequences, all
ending with `other.txt` staged again. The commit already leaves the user's
staged work out; the description does not say so. A sentence saying it is a
prompt change, and would want its own trial.
