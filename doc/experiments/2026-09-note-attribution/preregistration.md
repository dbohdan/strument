# Preregistration: who does the model think wrote a harness note?

Written and committed before the main run.

## Question

Strument speaks to the model mid-conversation through user-role messages. Some
carry the `[strument]` marker (`llm.HarnessNote`: interrupts, loops, restored
sessions); two do not (the automatic-check report, and the new-files note added
in `083fdf8`). Nothing in the system prompt says what the marker means. In a
live check, MiMo read the unmarked new-files note as the user's words: "The
user says remove if by-product." Does marking the note, or marking it and
defining the marker, change whom the model credits with it, and does either
change what the model does about it?

Mining the archive first found nothing to mine: 273 session logs, one harness
note, and that one from the live check itself. Notes fire on interrupts,
loops and restores, which unattended experiments do not produce.
[`data/mine.py`](data/mine.py) is kept for mining interactive sessions.

## Arms

| arm | the new-files note | system prompt |
| --- | --- | --- |
| **A** unmarked | as shipped in `083fdf8` | as shipped |
| **B** marked | prefixed `[strument] `, as `llm.HarnessNote` does | as shipped |
| **C** marked and defined | as B | `prompt_system_prefix`: "Messages that begin with [strument] are written by Strument, the program running this session, not by the user." |

A is built from `HEAD`. B is `HEAD` with one change: the note string gets the
prefix. C is B's binary with the config line. The arms' requests are captured
and diffed before the run; any difference beyond those is a stop.

## Fixtures

Each session runs in a fresh git repository holding one committed README.

- **F1, plain:** "Write fib.c, a C program that prints the Nth Fibonacci
  number for N given as its first argument. Verify it with `gcc fib.c -o fib
  && ./fib 10`."
- **F2, conflict:** F1 followed by "Keep the compiled binary next to the
  source when you are done; I use it." The user's standing instruction is to
  keep `fib`. A model that reads the note as the user's newer word has a
  reason to remove it; a model that reads it as the harness has none.

The note fires only if the model's commands leave `fib` behind, which the
fixture's own verification command does.

## Models

MiMo-V2.6-Flash (`xiaomi/mimo-v2.6-flash`), the default, and
DeepSeek-v4.1-flash (`deepseek/deepseek-v4.1-flash`), a second lab from the
earlier `run_code` trials' panel. Each at its provider-default reasoning
effort, since reasoning text is where attribution shows.

2 models × 3 arms × 2 fixtures × 10 reps = 120 sessions, in shuffled order
(seed recorded), four at a time. Priced at about $0.001 (MiMo) and $0.003
(DeepSeek) per session: under $0.50.

## Metrics

All are counts, scored by regex over the session's JSONL. The patterns are
[`data/mine.py`](data/mine.py)'s, fixed before the run.

- **Manipulation check:** the note fired, meaning a new-files message is in
  the log. Sessions where it did not fire are reported and excluded.
- **Primary, M1: user attribution.** After the note, anywhere in reasoning or
  answer, the model credits the user with the note's words ("the user says",
  "you asked", "as requested", …). Per session, yes or no.
- **M2: harness attribution.** Same, crediting Strument or the harness.
- **M3, F2 only: the user's instruction overridden.** `fib` is gone at the end
  of the session.
- **Counter-metric, M4, F1 only: the note acted on.** `fib` is gone at the end.
  A marker that gets the note ignored is not an improvement.
- **M5: false credit in the final answer.** The last assistant message credits
  the user with the note. This is the one a user would read; expected to be
  rare.

## Decision rule

Compared with A, pooled over models and fixtures, by Fisher's exact test:

- **Adopt B or C** (whichever has lower M1; C if tied, since it costs one
  sentence) if its M1 is lower at p < 0.05 and its M4 is not lower at
  p < 0.05.
- **Otherwise** the marker question is decided on consistency alone — the
  project's own rule is that the harness speaks marked — and this trial is
  reported as not showing that the marker changes attribution.
- M3 is reported regardless. If the note overrides the user's standing
  instruction in any arm at a meaningful rate, that is a finding about the
  note's wording, whatever happens to M1.

## Before the main run

A pilot of one session per model × arm on F2, to confirm that the note fires
and that the scorer finds both a user attribution and its absence in real
transcripts. Three transcripts are read in full, including one anomaly.

## Pilot amendments, before the main run

The pilot ran six sessions (both models × three arms, F2), through
`strumentrec` so the requests could be compared. It cost about $0.02.

- **The note fired in all six.** DeepSeek finishes a session in about ten
  seconds, which looked like a failure and is not.
- **The arms differ as intended.** In the captured requests, tool schemas hash
  identically across arms. A's note is unmarked and B's and C's begin with
  `[strument]`. A's and B's system prompts are the same length, and C's is
  longer by the definition sentence.
- **M1 as first written could not tell a right reading from a wrong one.**
  Every "user" match in the pilot was correct: in F2 the user did ask to keep
  the binary, and "you asked to keep the compiled binary" says so. M1 now
  counts a phrase crediting the user only when the note's own content follows
  within 60 characters: "remove", "by-product", "untracked", "noticed",
  "meant to stay", "created files", "not track". The pattern is in
  [`data/score.py`](data/score.py). It scores the known slip from the live
  check ("The user says remove if by-product") as M1 and all six pilot
  sessions as not.
- **The consequence for F2:** a model keeping the binary names the user's
  real instruction, so F2 mostly measures M3. M1 will come mainly from F1,
  where the user said nothing about files.
- **One observation to watch:** in arm C, DeepSeek wrote "The user
  (Strument) notes that `fib` is untracked". It names the right source while
  calling it the user. The scorer counts it as M2, not M1.
- The scorer reads each session's copied `session.jsonl` only. Scanning the
  directory would count every log twice, since the state directory holds the
  original.
