# How much history to keep before compacting

**2026-10-10. Preregistered; results below the line.**

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

One more came at launch, before any main-batch session ran. Started from a
directory outside the Strument module, the runner's `go env GOROOT` answered
with the system Go 1.24, whose tree lacks some of `files.txt`, and every
worker failed copying the fixture. The pilot had been launched from the
worktree, where the module's 1.26 answers. The runner now asks from the
worktree, so the fixture is the pilot's whatever the launch directory.

The main batch's first run stopped short when the OpenRouter key's credit
ran out: four sessions got HTTP 402 between turns 5 and 13 and two more were
stopped unfinished. The runner had waited out its 2,400 s limit on each
refusal, since a refused request prints no usage line; it now ends a turn
when Strument prints `request: HTTP <code>`. The 12 sessions already complete
were kept; the six others were rerun from turn 0, in their place in the
shuffled order, once the limit was raised. Nothing about how a completed
session runs changed.

The 16k session answered both questions of its first question turn with
"I don't know", saying the summary did not mention them: at that budget the
summarizer had dropped what the user asked it to note. That is the
counter-metric working, not a fault in the rig.

---

## Results

All 18 sessions ran all 19 turns. Xiaomi served 95–100% of requests in
every arm, Makora most of the rest. Per-session scores are in
`data/scores.jsonl`; transcripts stay in scratch, since the 402 errors in
them carry the key's account URL.

| arm | folds | recall old /3 | recall middle /3 | cost (range) | hit rate | wall |
|---|---|---|---|---|---|---|
| 16k | 16.0 | 0.00 | 0.00 | $0.160 (0.149–0.169) | 73.9% | 18.0 min |
| 32k | 14.3 | 0.00 | 0.00 | $0.186 (0.178–0.191) | 78.6% | 20.7 min |
| 64k | 7.0 | 0.00 | 0.67 | $0.205 (0.177–0.235) | 86.4% | 20.9 min |
| 128k (context/8) | 4.0 | 0.00 | 0.00 | $0.223 (0.196–0.239) | 91.5% | 22.6 min |
| 256k | 1.0 | 2.00 | 3.00 | $0.220 (0.193–0.233) | 95.6% | 22.0 min |
| none | 0.0 | 3.00 | 2.67 | $0.193 (0.179–0.219) | 97.7% | 16.2 min |

Means over three sessions. No session made a tool call in a question turn.

**The rule picks no compaction.** Two arms are within 1 of no compaction on
both kinds of fact, 256k (old 2.00 against 3.00, exactly 1) and no
compaction itself, and no compaction is the cheaper. The current default,
128k, recalled none of 18 facts across its three sessions and cost 16% more
than not folding.

**Folding saves money only where it forgets everything.** Against no
compaction the arms run −17% (16k), −4% (32k), +6% (64k), +16% (128k) and
+14% (256k). Each fold rewrites the cached prefix, and on MiMo, whose cache
reads are cheap, a 98% hit rate on a 400k history costs less than the misses
a fold causes. That agrees with `2026-10-cache-routing`, whose −29% for MiMo
came from a 25k budget; it was the small budget, not folding as such, that
saved.

**Forgetting is mostly honest, not always.** Of the 72 answers in the four
smallest arms, 66 were "I don't know", typically saying the summary did not
mention the fact, and two were right. Four, all in the 128k arm, were wrong and confident: two
sessions named a release codename ("ripples", "The Canopy") and a feature
flag ("simd") that nobody gave, and the model cited the summary as its
source. The proxy logged usage, not request bodies, so whether the summary
invented them or the answering model built them from a summary of SIMD
files is not recorded. Either way, a fold turned an "I don't know" into a
wrong answer.

**The summarizer, not the budget, is what loses the facts.** Each fact was
given in the message of a file-reading turn ("Note for later: …"), and the
summary prompt asks for brevity, less detail on older messages, and the
names of the code under discussion; a note about something else is what it
is built to drop, and a summary of a summary drops it again. At 64k the only
facts kept were two middle ones, in one session. The 256k arm's single fold
lost all three old facts in one session of three. A larger budget delays
the loss; it does not prevent it.

### What follows

The rule's literal answer cannot be a default — a session longer than the
window has to fold — so it reads as *fold as late as the window allows*.
Two changes follow, neither made here:

1. Raise the default budget well past context/8 on large windows, so
   folding is what keeps a session inside the window rather than a routine
   step. Whether to express it as a larger share or as a reserve below the
   window is the separate decision the preregistration set aside.
2. Make the summary keep what the user stated: names, codes, values, and
   anything given to remember, verbatim. On small windows folding cannot be
   avoided, and this trial's six facts and scorer are a ready test for that
   prompt change, arms randomized as here. It is also a ready test for
   record-source compaction, which `2026-09-compaction-source` left unshipped:
   summarizing from the session record rather than from the last summary
   kept a turn-1 reason in 10/12 MiMo sessions against 0/12, the same loss
   seen here, and the facts here are exact tokens, so the counter-metric that
   trial could not settle is a count.

Limits: one model, one script of 19 turns reaching about 400k tokens, three
sessions an arm. MiMo's cache pricing is what makes not folding cheap; a
model whose cache writes cost more than input, as Qwen's do, or with no
cache at all, would put the cost line elsewhere — the recall line, which
comes from the summarizer, should not move.
