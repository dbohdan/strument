# Rolling the history instead of summarizing it

**2026-10-11. Preregistered; results below the line once run.**

## Why

`2026-10-compaction-prompt` made summaries keep what the user said, but a
summary is still a model's account of a conversation, and its failure modes are
a model's:

- continuing the transcript instead of summarizing it, now framed against but
  still guarded;
- an ending in the wrong tense (seen live in Larkspur turn 19);
- paraphrase drift;
- a side call that can fail, or be rate-limited, which Larkspur turn 17 hit.

The Cyborgists' rolling contexts drop the oldest text instead of rewriting it,
and Codex and Kimi keep the user's own messages verbatim beside their summaries.
The combination — drop old turns whole, carry the user's messages forward word
for word, no model call — keeps nothing the summary would have invented and
loses what the summary kept of the model's own work. How much that loss costs is
the question.

This trial also settles `compaction_source`: its September advantage (10/12
against 0/12) was measured on the old summarizer, which stage A of
`2026-10-compaction-prompt` found continuing the transcript in 27 of 64 folds.

## Design

One model, Ling 3.1 Flash (`inclusionai/ling-3.1-flash`), free on OpenRouter,
reasoning low; its own summarizer, as by default. A settled-history budget set through the trial hook (`data/trial.diff`); see the
amendments for its value and the binary.

**Arms** (hidden `--compaction` and `--compaction-source` flags):

| arm | over budget |
|---|---|
| `summary` | clear old tool results, then summarize with P9 (shipped) |
| `roll` | clear old tool results, then drop the oldest turns, carrying the user's messages from them verbatim in one note |
| `roll-bare` | the same, carrying nothing (the floor) |
| `record` | as `summary`, with the summary built from the session record (`compaction_source = record`) |

**Script** (`data/runner_roll.py`): the threshold trial's 21-turn sessions —
sixteen turns reading five files of the Go standard library each — with two
kinds of planted knowledge:

- *user facts*, six, stated in passing in turns 1-3 and 9-11 (as in the
  threshold trial), asked back in turns 17-19;
- *derived facts*, four: in turns 4, 5, 12 and 13 the model is also asked
  which of the five files is longest and how many lines it has, "I will ask
  about it later". The answer is in the model's own reply and nowhere else.
  Asked back in turns 20-21.

Question turns say not to read, search or run anything.

**Metrics** (`data/scorer_roll.py`, self-tested), counts:

- user facts recalled exactly, of 6;
- derived facts recalled, of 4, two ways: against the files (truth), and
  against what the model said at the time (its own result, which is what
  memory can be asked for);
- derived answers that say "don't know", against those that are wrong;
- tool calls in question turns (violations);
- cost, side calls included; compactions; failed folds.

**Size and order.** Six sessions an arm, 24 in all, shuffled (seed 20261017),
two at a time.

**Rule for failed folds.** Ling's free tier rate-limits. A summary call that
fails keeps the history whole, which flatters a summary arm. So a summary or
record session with any failed fold is not scored. It is rerun, in its place
in the order, until it has none, and the reruns are reported.

## Amendments from two pilots, before the main batch

Both pilots ran one session an arm; neither is pooled.

**The first pilot measured nothing.** At a 32k budget no arm ever summarized
or rolled: clearing old tool results, which every arm does first, brought this
script's history under budget every time, because without the file contents
the conversation's own text is small. That is a finding about pruning — on
sessions dominated by tool output, it makes summaries rare — but it left the
arms identical. **The budget is 4,000 tokens**, small enough that the text
itself overflows once tool results are cleared, from about turn 6.

**The second pilot (4k) compacted in every arm** and found three things:

- **The roll note's cap was a quarter of the budget**, 1,000 tokens here, so
  `roll` lost every early user message to it. A real budget is half the
  window and the cap tens of thousands of tokens (Kimi's is 20k), so a cap
  that shrinks with an artificial budget tests nothing a session meets. The
  cap is now a quarter of the budget but never under 16k tokens (a25dee2);
  the binary is a25dee2 plus the hook.
- **Ling's free tier rate-limited summary calls:** 6 failed folds in one
  `summary` session, 3 in `record`. A rule that reruns such sessions until
  they have none might never finish. **Replaced:** every session is scored,
  and failed folds are reported per arm. A failed fold keeps more of the
  history, so failures can only help the summary arms. A `roll` win is
  therefore robust to them; a summary win is to be checked on a paid model.
- **Ling looped once,** mid-turn in `roll-bare`, narrating "Step 1: read."
  instead of calling the tool. Strument's loop detector stopped it and asked
  what to do, and the runner, not answering, waited out its limit. The runner
  now answers "stop", so the turn ends there, and counts loops per arm. The
  turn limit is 3,600 s.

The scorer had two bugs the self-test missed, both fixed and covered:

- it applied a text pattern to bytes;
- it located turns by their echoed prompt, which wraps mid-path at the
  terminal's width; it now uses the usage line that ends every turn.

In the second pilot both summary arms kept all 6 user facts and all 4 derived
facts. `roll` answered "don't know" to all four derived questions rather than
guessing.

**One more, two hours into the main batch.** Session s04 (`record`) stalled
on turn 3: instead of reading the files, Ling chose to count their lines with
`wc -l` first, and Strument asked to approve the shell command. The runner
approves steps only and did not answer, so the turn would have waited out its
limit. The runner now declines shell prompts ("n"): the task is to read the
files, and a declined command leaves the model to do so. Declines are counted
per arm. The runner was restarted. The five sessions already complete (s00–s03,
s05) had no shell prompts and are kept; s04 and s06, which were in progress, ran
again from turn 0.

**And a throttle, seven hours in.** From about 17:20 UTC on 2026-10-11, Novita's free Ling pool answered every request with HTTP 429 ("temporarily rate-limited upstream"). This was the shared pool, not this key: one app alone was using it at about 780M tokens an hour, against this trial's 10M. Two sessions failed mid-run, s12 (`roll`, turn 18) and s14 (`roll`, turn 3), and two more never got past turn 0. The runner did not recognize the error and waited out a full hour per turn; it now ends the turn when Strument gives up retrying. A container restart then ended the batch. It was relaunched at 04:25 UTC on 2026-10-12, once Ling answered again. The 13 complete sessions are kept. The other 11 run from the start, and s12 and s14 count as replacements, since a provider refusal says nothing about compaction. The throttled transcripts and the runs recorded before the relaunch are kept in the scratch archive, and the scorer reads the latest record for each session.

## Decision rule

- **`roll` replaces `summary` as the default** if:
  - it recalls user facts no worse (mean within 0.5 of 6);
  - it recalls derived facts (own result) within 1 of 4 of `summary`;
  - no session gives a wrong derived answer where `summary`'s give "don't
    know" — a confident wrong answer is the worse failure.
- **Otherwise `summary` stays.** `roll` stays available behind the flag if
  it is within 2 on derived facts, as the mode for providers whose side calls
  are unreliable.
- **`compaction_source` is removed** unless `record` beats `summary` by at
  least 1 fact of the 10 a session asks for.
- `roll-bare` is a floor and decides nothing on its own.

Ling only. The verdict on the default needs the validation the maintainer has
planned, at a smaller scale, on MiMo, GLM-5.3-Flash and GPT-6 Luna, once funded.
