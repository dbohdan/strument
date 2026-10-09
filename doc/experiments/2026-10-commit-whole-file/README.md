# Telling the commit tool that an edited file is committed whole

**2026-10-09. Preregistered; results below the line once run.**

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
