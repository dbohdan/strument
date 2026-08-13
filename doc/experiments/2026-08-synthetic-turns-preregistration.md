# Pre-registration: does removing the synthetic file-context turns cost anything?

Written before any sample was collected. Amendments are appended at the end,
dated, rather than edited into the body.

## Question

Strument assembles every request with a fabricated user/assistant pair carrying
the files in the chat:

```
user:      <FilesContentPrefix><file contents>
assistant: <FilesContentAssistantReply>
```

and the same shape for read-only files (`assemble.go:306-311, 318-327`). The
harness is speaking as the user, and then answering as the model. Neither turn
happened.

The motive for removing them is architectural, not performance: a harness that
fabricates turns has no clean account of what the transcript *is*. So the
hypothesis under test is **not** that removing them helps. It is that removing
them does not measurably hurt.

**This is a non-inferiority screen.** It is designed to detect a regression, and
to bound the plausible harm when it finds none.

## Arms

Two binaries built from two commits, not a runtime flag, so the arms provably
differ only by the diff and no experimental scaffolding enters production code.

- **A (baseline).** Current `formatChatChunks`: the user/assistant pairs above.
- **B (treatment).** No fabricated turns. File context is merged into the real
  user turn, ahead of what the user typed.

B merges rather than emitting a bare second user message (some providers reject
consecutive user turns) or a mid-conversation system message (support varies,
and that variance would confound the arm with the provider).

## Sampling

4 tasks × 3 models × 2 arms × 25 reps = **600**.

Models, all within one price band so no single stratum sets the sample size
(the 2026-08 prompt-scope experiment let Haiku take 95% of the budget and the
sample size was then decided by cost rather than by the question):

| model | $/M in | $/M out |
| --- | --- | --- |
| `xiaomi/mimo-v2.5` | 0.14 | 0.28 |
| `openai/gpt-5.6-luna` | 0.10 | 0.60 |
| `deepseek/deepseek-v4-flash` | 0.14 | 0.28 |

Estimated total: $3–8.

**Arm order is randomized across the entire job list**, with the shuffle seed
recorded. In the previous experiment this mattered more than tripling the sample:
shuffling alone moved a baseline from 65% to 84% and took a p=0.0009 result to
p=0.15, because the unrandomized design confounded the arm with the wall-clock
window it ran in and providers drift across such a window.

Each sample permutes surface details of its task (file names, identifier names,
paths) so the 600 runs are not 600 repetitions of one literal prompt.

## Tasks

Chosen to span the mechanism rather than to be four of the same thing, so a
task-dependent effect is visible as such:

1. **Chat-only.** Everything needed is in the chat files; no search required.
   The change should matter most here.
2. **Search-required.** The target is in a file not in the chat, so the model
   must `grep`/`read` regardless. Should matter least.
3. **Mixed.** One edit in a chat file, one in a file it must find.
4. **Cross-file consistency.** A change in a chat file that breaks a caller in
   another chat file, which must also be fixed.

## Metrics

Both mechanical. No model judges another model's output, and no metric depends
on my reading of an answer.

- **Primary:** the edited file's content matches the expected result exactly.
  Binary, per sample.
- **Counter-metric, reported as prominently as the primary:** the number of
  `read`/`grep` calls targeting files *already in the chat*. This is what B is
  most likely to break — if the file block stops reading as a turn addressed to
  the model, it may re-fetch what it already has. Counted from the harness's own
  outcome lines.
- **Descriptive only, not tested:** steps per turn, tokens sent. B changes the
  cache prefix, so a tokens-sent difference is confounded and must not be
  reported as a cost result.

## Analysis

- Cochran–Mantel–Haenszel on the primary, stratified by model.
- Per-model and per-task Fisher exact as disaggregation, understood to be
  underpowered (~75/arm/task) and read for sign and coherence, not significance.
- Counter-metric compared by Mann–Whitney U on the per-sample count.

**Power.** At 300/arm pooled, with a baseline near 85%:

- 80% power to detect a regression of **≥8pp**;
- if the observed difference is zero, the one-sided 95% upper bound on harm is
  **~5pp**.

Stated in advance so the result cannot be reinterpreted afterwards: an observed
null means "not worse by more than about 5pp", not "identical".

**Decision rule.** Adopt B if the CMH point estimate is not a regression beyond
the 5pp bound and the counter-metric shows no increase in redundant reads. If B
regresses, do not bisect the three sites within this experiment — run a second
one, because the sites were bundled deliberately and the bundle is what failed.

**Transcripts.** Every failure is read, plus ten successes per arm. In the
previous experiment a provider returning `Empty response received from LLM` and
a model emitting a tool call as inline text were indistinguishable in the
aggregate and meant opposite things; only opening individual runs separated
infrastructure failure from model behavior.

## Out of scope

- The two dead sites (`assemble.go:264-269`, `296-301`) — unreachable, deleted
  without measurement.
- The rare sites (`AppendExchange`, `NoteUndo`'s assistant half, the interrupt
  pair, the summary padding). They fire too seldom to sample at any budget and
  are design decisions, not empirical ones.
- `send.go`'s context-limit note, already changed from an assistant message to a
  system one on the same reasoning, without measurement.

## Amendments

*(none yet)*
