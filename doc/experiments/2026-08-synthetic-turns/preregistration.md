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

### 2026-08-13 — arm B redefined, before any sample was collected

**What changed.** Arm B was "file context merged into the real user turn". It is
now "the fabricated *assistant* replies are removed; the file-context user
messages are untouched".

**Why.** The original B was caught by `TestTheUserMessageIsLeftAlone`
(`assembly_test.go:58`), which exists because the system reminder used to be
edited into the last user message and that was removed as *"one of the places
the harness pretended the user had said something."* Merging file context into
the user's turn is the same act in a different place. The project had already
decided this question, and the treatment arm walked into it.

**What this buys, beyond not contradicting ourselves.** The two halves of a
synthetic pair are not equally indefensible, and separating them is a better
experiment than bundling them:

- The **user** half has a real referent. The user did pin those files, with
  `/add`. Calling it fabricated is a stretch.
- The **assistant** half has none. "Ok, I will use these files as references" is
  the harness writing the model's lines and answering a question nobody asked.

Three further consequences, all improvements:

- The arms now differ by *exactly* the messages under test. Everything else is
  byte-identical and in the same order.
- The cache prefix does not move, so **tokens-sent is no longer confounded** and
  can be read as a result rather than as description. The caveat in *Metrics* is
  withdrawn.
- The whole existing test suite passes under B, so the change is invisible to
  every behavioral assertion the project already makes; it differs only on the
  wire.

**Wire risk, checked rather than assumed.** B produces consecutive user messages
at two slot boundaries. All three models were probed directly and answer them
correctly. A first probe appeared to show `mimo` and `v4-flash` returning null
content; that was `max_tokens: 20` being consumed by reasoning before any
content, not a provider limitation — an artifact of the probe, and worth
recording because the aggregate looked exactly like a provider rejection.

**Scope note.** Consequently this experiment now tests only "the harness speaks
as the model". The other direction — "the harness speaks as the user" — is
untested and stays for a later decision, on the stronger footing that its
remaining sites each have some real referent to point at.

### 2026-08-13 — what arm B actually removes, checked against the prompt text

Prompted by a smoke run: at n=1, arm B produced several hundred words of
reasoning agonizing over whether the file block could be trusted
(*"to avoid any mistake, I'll read the file again to ensure I have the exact
string"*) where arm A read the file once and edited it. That raised the
possibility that B removes *information*, not just a fabricated turn, which
would make the whole comparison measure the wrong thing.

Checked rather than assumed. The removed message is not `"Ok."` — it is
*"Understood. Any changes I propose will be to those files, and I'll treat this
message as their current contents."* But `filesContentPrefix`, in the user
message that stays, already says *"Trust this message as the true contents of
these files"* and *"I have added these files to the chat so you can go ahead and
edit them."*

So the assistant reply is a **restatement of the user message's instruction, in
the model's own voice**. No unique instruction is lost, and the comparison is
sound.

This sharpens the hypothesis rather than changing the design. The question is
not whether the instruction matters — both arms carry it — but whether the model
*hearing it in its own voice* adds anything: whether a fabricated
acknowledgement works as a commitment device. That is the interesting form of
the question, and there is no way to test it without fabricating the turn, so
arm B is the only possible treatment.

The n=1 observation is recorded here as the reason for the check, and is
explicitly **not** evidence of an effect.

### 2026-08-13 — task set changed after a pilot showed a ceiling

**Pilot (n=24, all four original tasks).** 23/24 passed, and the one failure was
a 300-second timeout — my own sentinel, not a model failure. So the behavioral
pass rate was 24/24.

That is a ceiling, and it matters for a reason worth stating precisely: it is
**not** a precision problem. Binomial variance shrinks near the ceiling, so the
confidence intervals would have been *tighter*. It is an external-validity
problem. A null at 96% licenses only "no difference on tasks this easy", and the
first fair question would be whether harder work differs.

**Three harder tasks were added**, aimed at the hypothesis rather than at
difficulty for its own sake. The removed message says *"I'll treat this message
as their current contents"*, so the discriminating task is one where the block
contradicts what a model would otherwise assume:

- `contradicts_name` — the block shows a function whose body contradicts its
  name (`Sum` computes a product). Describing it correctly requires reading the
  block rather than the name. Pilot: **67%**.
- `many_call_sites` — a rename across four files and six call sites. More steps,
  more room to drift. Pilot: **67%**.
- `unusual_signature` — a helper with reversed parameter order. Pilot: 12/12,
  still at ceiling, so **dropped**.

**Final task set**, chosen so the pooled baseline lands near the 85% the power
arithmetic assumed rather than being tuned after the fact:
`contradicts_name`, `many_call_sites`, `cross_file`, `search_required` —
expected pooled baseline ≈ 83%, giving ~8.5pp detection and a ~5pp bound at
300/arm, as originally registered.

**Pilot data is discarded, not pooled.** It informed this design; reusing it
would be selection on the outcome. The final run starts from an empty results
file.

**Two runner defects the pilot exposed**, both fixed before the real run:
`ThreadPoolExecutor.map` yields in submission order, so a slow early job stalled
all logging behind it (now `as_completed`); and the 300-second timeout was tight
enough to manufacture a failure (now 600, with timeouts recorded by return code
so they are never silently counted as behavioral failures).

### 2026-08-13 — DeepSeek model changed

`deepseek/deepseek-v4-flash-0731` replaces `deepseek/deepseek-v4-flash` at the
user's request (substantially stronger, and cheaper at $0.08/$0.18 per M).
