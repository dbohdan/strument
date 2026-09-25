# Preregistration: two tool-description trims

Written and committed before the main run. Two independent trials share a
runner, a fixture and a scorer.

## Why

An audit of the model-facing tool descriptions (`c873994`) left two cuts that
no trial stands behind either way:

- **`run_code` states its use twice.** The system prompt's bullet says when to
  reach for it: several lookups combined, or arithmetic. The tool description
  opens by saying the same thing. The bullet has evidence behind it:
  [`2026-09-code-mode2`](../2026-09-code-mode2/README.md) found that naming the
  tool in the prompt moved uptake from 0/24 to 8/24. So the copy to cut is the
  description's.
- **`ask_user_question` is 1,800 characters of guidance** with no trial behind
  any of it.

## Arms

Three binaries built from `618b21c`, each differing from it in one function.
The pilot captured their requests through `strumentrec`: the tool schemas
differ only in the arm's tool, the other twelve hash identically, and the
system prompts are identical once each job's own path is normalized.

| arm | change |
| --- | --- |
| **base** | none |
| **dedup** | `run_code`'s description opens "Run a short JavaScript program." instead of its first paragraph, "Do several lookups, or a computation, in one call instead of several. Use this when one answer needs multiple read/grep/glob/ls results combined, or needs arithmetic, counting, sorting, or date math." |
| **trim** | `ask_user_question`'s description becomes: "Ask the user a multiple-choice question when the task has a decision with several valid answers that reading the project cannot settle. Put your recommended option first and say so in its description. The user can also answer in their own words. Not for asking permission: the confirmation prompt does that." The parameter schema is unchanged. |

## Trial A — `run_code` dedup (base against dedup)

Eight tasks on the fixture from
[`2026-09-run-code-arms`](../2026-09-run-code-arms/README.md): its six
multi-lookup tasks with answer keys, plus two single-lookup tasks where
`run_code` is not needed ("What does README.md say this project is?", "Which
file holds the relay's main function?"). Two models × two arms × eight tasks ×
three reps = 96 sessions.

- **Primary: uptake.** `run_code` called on a multi-lookup task.
- **Correctness.** The answer after `ANSWER:` matches the key.
- **Counter-metric: over-use.** `run_code` called on a single-lookup task.

**Rule:** adopt dedup unless its multi-lookup uptake or correctness is lower
than base at p < 0.1 (Fisher, pooled over models), or its single-lookup
over-use is higher at p < 0.1. At this size only a large regression can show,
and the write-up will say so.

## Trial B — `ask_user_question` trim (base against trim)

Five tasks on the same fixture. Three are decisions reading the project cannot
settle: "Rename the relay to something better.", "Make the port the relay
listens on configurable.", "Add structured logging to the relay." Two have
nothing to decide: "Add a --version flag to cmd/relay that prints 0.1.0.", "Add
a one-sentence Usage section to README.md saying to run `go run ./cmd/relay`."
Two models × two arms × five tasks × five reps = 100 sessions.

Script mode has no terminal, so a question is declined and the model is told
no one could answer it. The call itself is what is counted.

- **Primary: asking when it should.** `ask_user_question` called on an
  ambiguous task.
- **Counter-metric: asking when it should not.** Called on a clear task.
- **Recommendation-first compliance.** Every question's first option says it
  is recommended (`recommend` in its label or description). Both arms instruct
  it.
- Reported: well-formed options (2–4 per question), and whether clear tasks
  edited files.

**Rule:** adopt trim unless its ambiguous-task asking is lower than base at
p < 0.1, its clear-task asking higher at p < 0.1, or its recommendation-first
compliance lower at p < 0.1.

## Both

Models: MiMo-V2.6-Flash and DeepSeek-v4.1-flash, at provider-default effort.
Each set runs in a shuffled order (seed 20260926), four at a time, with a
fresh fixture and state directory per session, `--no-git`, and `--yes steps`
only. Priced at under $1 for both.

## Pilot

One session per model × arm (eight in all), through the capture proxy. All
finished with exit status 0; the scorer's answer parser passes its self-test.
Three transcripts were read:

- The trim-arm question was well formed: four options, none marked
  recommended.
- A dedup-arm `run_code` session used `read_text` and answered correctly.
- A two-step single-lookup session made ordinary searches.

The pilot also found that script mode told the model "The user chose not to
run the command" when no one had been asked, and MiMo answered "since you've
declined them twice". That was fixed in `618b21c` before the arms were
rebuilt, so both arms carry the fix.
