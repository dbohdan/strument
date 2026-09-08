# Experiments

This is the map of the experiment archive. Each entry gives the question, the
main result, and the resulting decision; open its `README.md` for the design,
evidence, limitations, and instrumentation notes. Each directory contains its
write-up, its data and runner in `data/`, and any preregistration or phase
documents beside them. Directories are named `<date>-<slug>`, so the flat
listing is chronological and this index is where the subject grouping lives.

**Statuses:** **trial** is a comparative live run; **characterization** is an
exploratory live pass; **bug report** records a live finding without a
comparative experiment; **design** is not yet run; **data only** has results but
no write-up.

Two trials are data without a write-up, and are listed as such rather than
tidied away: an unwritten result is still evidence, and knowing it exists is the
point of an index.

Read [`../experimenting.md`](../experimenting.md) before running one. It collects
what has gone wrong with the *equipment* — broken scorers, fixtures that could
not contain the phenomenon, runners that died quietly — which is most of what
goes wrong.

## The `run_code` tool

Whether programs are offered, understood, reached for, and useful once used.

| experiment | question → result → decision |
| --- | --- |
| **trial** — [2026-08-code-mode](2026-08-code-mode/) | Do models use `code` for unnamed repository exploration? No: 0/24 treated runs called it, although probes showed that it worked. The original recommendation against shipping was withdrawn after the follow-up prompt trial found 8/24 uptake; round-trip savings remain unproven. **Follow-up:** [2026-09-code-mode2](2026-09-code-mode2/). |
| **characterization** — [2026-09-code-mode2](2026-09-code-mode2/) | Does naming `code` in the prompt improve uptake? Yes: prompt and description changes moved treated uptake from 0/24 to 8/24 without observed harm. Keep the tool and wording, but do not yet claim round-trip savings. **Follow-up:** [2026-09-code-only](2026-09-code-only/). |
| **trial** — [2026-09-code-only](2026-09-code-only/) | Does forcing observation through `code` beat persuading models to use it? No: round trips rose 3–4× and cost rose about 6×, with no correctness benefit. Keep the mode off by default; test planning interventions instead. |
| **trial** — [2026-09-code-result](2026-09-code-result/) | What should a `run_code` program return? The final value was as effective as the alternatives and cheaper; the suspected lossy-call failure occurred once in 401 programs. Keep final-value return and the warning note; do not require `main()` or return every result. |
| **trial** — [2026-09-code-namespace](2026-09-code-namespace/) | Does a `tools` namespace prevent programs from reaching for Python filesystem APIs? No: models adopted the namespace when offered, but the mistake usually happened in the first program, before the description was consulted. Keep the flat interface; a system-prompt intervention would be a different experiment. |
| **bug report** — [2026-09-error-attribution](2026-09-error-attribution/) | Do tool-call failures inside `run_code` identify their program line? They did not, because the wrapper returned the error before Monty could produce a traceback. Re-enter errors through the interpreter; keep the resulting catchability and test it explicitly. |

## What the model is told about files

How pinned-file content, names, instructions, and synthetic conversation turns
affect model behavior.

| experiment | question → result → decision |
| --- | --- |
| **characterization** — [2026-08-add-authority](2026-08-add-authority/) | Which representation of pinned files carries authority? An instruction naming the files and requiring real reads made all 9/9 models read before editing; a fabricated tool result caused no pre-edit reads but silently mutated as files changed. Take the instruction-based design to a preregistered run, which became [`2026-08-add-instruct`](2026-08-add-instruct/). |
| **trial** — [2026-08-add-instruct](2026-08-add-instruct/) | Does asking models to read pinned files beat injecting their contents? Yes: it cost one step, eliminated blind edits (0/300), and did not materially reduce task success. Adopt A2, while watching its real over-reading and multi-turn token costs. |
| **trial** — [2026-08-agents-md](2026-08-agents-md/) | Does pinning `AGENTS.md` make models follow it, and does naming it help? Compliance rose from 0/8 without the file to 2/8 when it was merely pinned and 6/8 when it was described as standing instructions. Ship the explanatory clause; no session edited the file. |
| **characterization** — [2026-08-readonly-honest](2026-08-readonly-honest/) | Can `/read-only` describe outside-project references without fabricated acknowledgements or misleading refusals? The honest prefix worked without the fabricated assistant reply and exposed two refusal-message bugs. Land R2, but do not spend on a larger trial; moving the block into the system prompt remains untested. |
| **trial** — [2026-08-synthetic-turns](2026-08-synthetic-turns/) | Does the harness's fabricated acknowledgement improve work? Removing it did not change near-ceiling task success, but increased redundant reads from 31% to 39%. Keep the acknowledgement despite the honesty concern, because the counter-metric failed the removal rule. |

## Prompt wording

Whether small changes to scope, welfare, or review language change behavior
without introducing regressions.

| experiment | question → result → decision |
| --- | --- |
| **trial** — [2026-08-prompt-scope](2026-08-prompt-scope/) | Does a positive reach clause make models update affected tests and docs without drive-by edits? Test updates rose from 84% to 97% with no drive-by edits; the cost-framing change was null. Ship the reach clause, and randomize arm order in future runs. |
| **trial** — [2026-08-welfare-wording](2026-08-welfare-wording/) | Can prompt-welfare changes be shipped without breaking scope discipline? The patches caused no measurable safety regression; the larger benefit run was underpowered for a positive claim but excluded a large regression. Keep both wording changes and continue treating the benefit as uncertain. |
| **characterization** — [2026-08-prompt-review](2026-08-prompt-review/) | Can several models find defects in rendered prompts? Five reviewers found nine actionable defects, including two regressions, although no single reviewer found them all. Use an ensemble and rendered artifacts, but treat reviewer judgements as leads that require code confirmation. |

## Tools, and whether they get reached for

Whether a model chooses a tool, and whether the tool description tells it what the
tool is uniquely good for.

| experiment | question → result → decision |
| --- | --- |
| **trial** — [2026-08-symbol-uptake](2026-08-symbol-uptake/) | Does an improved `symbol` description make models choose it for caller questions? Usage roughly doubled, with fewer tool calls and lower cost, though the main uptake result was only suggestive at this sample size. Keep the description change; its mechanism is that `reference` names enclosing functions, which grep cannot. |
| **trial** — [2026-08-skill-uptake](2026-08-skill-uptake/) | Does a relevant skill change chart output, and is it loaded when useful? Compliance rose from 0.79/5 to 4.96/5, with 54/54 loads and no false-positive loads on decoys. Keep the skill mechanism, while treating partial compliance and judgement-heavy skills as open questions. |
| **trial** — [2026-09-tool-arg-order](2026-09-tool-arg-order/) | Do models follow a schema that names `path` before payload fields? Yes for `write` (25/56 to 44/56), but not clearly for `edit`; counter-metrics stayed flat. Ship ordered properties for streaming-diff tools, without claiming that every model will follow them. |
| **trial** — [2026-08-commit-message-tool](2026-08-commit-message-tool/) | Is a `commit_message` tool better than a separate message request? No: the tool was cheaper but produced worse Conventional Commit subjects and missed every breaking-change marker. Keep the separate request and its improved prompt. |
| **trial** — [2026-09-shell-parallel](2026-09-shell-parallel/) | Does prose make models use the advertised parallel-shell pattern? Yes: the prose arm used `a & b & wait` in 9/9 runs, while examples added nothing over baseline. Keep the prose, but test it on tasks where serial execution is also reasonable. |

## Context, notes, and compaction

How much conversation and reasoning to retain, and how historical notes interact
with current project state.

| experiment | question → result → decision |
| --- | --- |
| **trial** — [2026-08-compaction](2026-08-compaction/) | Does a structured compaction prompt preserve a decision's reason better than the old prompt? No: recall of the reason fell from 10/12 to 5/12, so the content rewrite was reverted. Honesty changes and an instruction to preserve user-given reasons were kept. |
| **trial** — [2026-08-session-notes](2026-08-session-notes/) | Do session notes preserve decisions across sessions, and can they become stale? They preserved the reason in 8/8 runs, but stale notes caused 3/8 answers about current code to be wrong. Keep notes as historical context, not current state; the attempted wording fix is in [`2026-08-notes-header`](2026-08-notes-header/). |
| **trial** — [2026-08-notes-header](2026-08-notes-header/) | Does telling models to read before answering about current code prevent stale-note answers? No: stale assertions were 8/24 versus 6/24, with a non-significant cost increase in unnecessary reads. Do not ship the sentence; use a mechanical lookup or keep stale-able facts out of notes instead. |
| **trial** — [2026-08-transcript-depth](2026-08-transcript-depth/) | Does adding reasoning to session transcripts improve later recall? It improved recall of code-recoverable facts, but never preserved a rationale absent from the tree and shortened the notes window. Do not ship reasoning in transcripts without a fixture showing that it preserves the information notes are meant to carry. |
| **trial** — [2026-08-commit-context](2026-08-commit-context/) | Does giving the commit-message model earlier conversation improve its reasons? Yes: the reason appeared in 12/27 wide-context runs versus 2/28 narrow runs, but wider context also misattributed earlier work to later commits. Ship the wider context together with the scoping clause, not widening alone. |

## Editing

| experiment | question → result → decision |
| --- | --- |
| **design** — [2026-09-anchored-edit](2026-09-anchored-edit/) | How should anchored editing be evaluated across its preregistration and phases? This is a multi-phase plan rather than a completed result; start with [`phase0.md`](2026-09-anchored-edit/phase0.md). |

## Sandbox and shell

| experiment | question → result → decision |
| --- | --- |
| **trial** — [2026-08-landlock-live](2026-08-landlock-live/) | Does the Landlock sandbox enforce its policy on a real Landlock kernel without breaking ordinary work? Yes: all checks passed after scorer corrections, denied writes were contained, and ordinary tests and commits worked. Keep the policy and report the actually granted paths, not merely the requested ones. |
| **data only** — [2026-08-containment](2026-08-containment/) | Does the containment probe hold? Results and the runner are in `data/`, but there is no write-up yet. Treat it as recorded evidence, not as a summarized conclusion. |

## Designed, not yet run

| experiment | question → result → decision |
| --- | --- |
| **design** — [2026-09-consult](2026-09-consult/) | Does `/consult`'s source label survive to the next turn, and how much session context should the advisor see? The plan separates attribution from scope and is not yet run. |