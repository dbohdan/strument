# How much history to keep before compacting

**2026-10-10. Preregistered; results below the line once run.**

## Why

Strument folds the settled history when it passes context/8 — 131k tokens on
MiMo-V2.6-Flash's 1.05M window — keeping a tail under half of that and a
summary of the rest. `2026-10-cache-routing` found folding cut a six-turn
session's cost by 29–34% on automatic caches, at one budget. Whether context/8
is the right place, and what folding costs in what the model still knows, was
not measured.

## Design

One model, MiMo-V2.6-Flash, `reasoning = "low"`, `cache = True`, 1.05M
window. Six arms: a settled-history budget of 16k, 32k, 64k, 128k, 256k
tokens, and no compaction (10⁹). The budget is set by
`STRUMENT_TRIAL_HISTORY_BUDGET`, read by `maxChatHistoryTokens` in a trial
binary built from `f8ca71a` plus that one change (`data/trial.diff`); the arms
are one binary and differ only in the variable. Three sessions per arm, 18 in
all, job order shuffled (seed 20261010), four at a time.

Each session is one REPL process driven through a pty — not `--continue`,
whose restart re-fold would be a confound — through 19 turns:

- Turns 1–16 each read five files of the Go standard library, one per step,
  and answer in two or three sentences (80 files, about 380k tokens,
  `data/files.txt`), so the history passes 256k and every arm but the last
  folds at least once.
- Six facts are given by the user, in the messages of turns 1–3 ("old") and
  9–11 ("middle"): invented names and codes, in no file, so they cannot be
  recovered by reading — only the conversation or its summary holds them.
- Turns 17–19 each ask for one old and one middle fact, with no file, and ask
  for `ANSWER1:` and `ANSWER2:` lines.

Each session has its own config (`--config`) pointing at its own recording
proxy, which logs OpenRouter's per-request usage and cost.

## Metrics (`data/scorer.py`, self-tested both ways)

- **Primary — cost:** OpenRouter's cost for the session, the summary side
  calls included.
- **Counter-metric — recall:** facts answered by exact token, out of 3 old
  and 3 middle; "don't know", wrong, and no answer kept apart.
- **Also:** folds, hit rate (cached over prompt tokens), tool calls in the
  question turns (each one a violation), wall time.

## Decision rule

The default budget becomes the cheapest arm whose mean recall is within 1 of
the no-compaction arm's on both the old and the middle facts. If that is the
128k arm, context/8 stands for MiMo. The trial is one model; a result moves
the default for other models only as far as it argues for a token budget
rather than a share of the window, which is a separate decision.

A pilot of one session at 16k and one without compaction checks that folds
happen where intended and that the scorer reads live transcripts; it is not
pooled.

### Amendments from the pilot, before the main batch

The pilot (16k and no compaction, one session each) ran 2026-10-10 and is not
pooled. Folds happened where intended: once a turn at 16k, never without
compaction. Three changes followed, all applying to every arm alike:

- **The end-of-turn pattern.** A turn with no tool call prints its usage
  line without "N steps"; the runner waited for the word and timed out on
  the first question turn, which had in fact answered.
- **Provider order.** Past about 320k tokens OpenRouter began sending
  requests to Novita instead of Xiaomi, each switch reading almost nothing
  from the cache (4k of 329k on the first) and taking minutes: the
  no-compaction session's turn 8 took 589 s and turn 9 passed the 900 s
  limit. Xiaomi serves the full 1M window, so the config now puts it first
  (`provider.order = ["xiaomi"]`, fallbacks allowed). One provider also
  makes the cost metric one price list.
- **Turn timeout** 900 s to 2,400 s, as margin rather than expectation.

The 16k session answered both questions of its first question turn with
"I don't know", saying the summary did not mention them: at that budget the
summarizer had dropped what the user asked it to note. That is the
counter-metric working, not a fault in the rig.

---
