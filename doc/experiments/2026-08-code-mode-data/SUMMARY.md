# 2026-08 code-mode trial — data

Part 4 of doc/plans/code-mode.md. 36 runs = 6 models x 2 reps x 3 arms, seed
20260830, shuffled. Lives in /tmp (moved out of the repo: the worlds/ tree is
7.5 GB of repo copies and was straining project backup).

- run.py — the shuffled, resumable runner (REPO path hardcoded after the move)
- runs/  — per-run .txt (rendered transcript) and .jsonl (JSONL transcript)
- worlds/ — disposable per-run copies of the repo + config (deletable)
- results.json — consolidated per-run measures (recomputed from runs/, with two
  scorer false negatives corrected by hand and annotated)
- strument-arm{A,B,C} — the three arm binaries

**Headline: code uptake was 0/36.** Not one `code` call in any arm. Arms B and
C are byte-identical in symbol table (both were built from the bridge commit),
so the trial is really A vs code+bridge — and the answer is: models don't
reach for it on an exploration task. Live probes confirm the tool works and
that mimo calls `code` when the task names it; the uptake failure is model
choice, not a broken arm.

Wire check: arm A has no code tool (live probe: "Do you have a tool named
code?" -> "No"); arm B and C both expose `code` with the bridge (probes).

Scorer notes: run.py's pre-registered scorer misses `60_000` and `ANSWER:**`;
the numbers in results.json use a lenient re-score, noted per row.
