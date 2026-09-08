# Experiments

One directory per trial: its write-up in `README.md`, its data and runner in
`data/`, and any preregistration or phase documents beside them. Directories are
named `<date>-<slug>`, so the flat listing is chronological and this index is
where the subject grouping lives.

Two trials are data without a write-up, and are listed as such rather than
tidied away: an unwritten result is still evidence, and knowing it exists is the
point of an index.

Read [`../experimenting.md`](../experimenting.md) before running one. It collects
what has gone wrong with the *equipment* — broken scorers, fixtures that could
not contain the phenomenon, runners that died quietly — which is most of what
goes wrong.

## The `run_code` tool

The longest thread, and the one where the results argue with each other.

| trial | what it found |
| --- | --- |
| [2026-08-code-mode](2026-08-code-mode/) | The tool went unused — 0/36 calls — when the schema offered it and the prompt's tool list did not name it. |
| [2026-09-code-mode2](2026-09-code-mode2/) | Naming it in the prompt fixed that. |
| [2026-10-code-only](2026-10-code-only/) | Forcing all observation through it, against persuading models to reach for it. |
| [2026-09-code-result](2026-09-code-result/) | What a program hands back: the final value beats echoing every call and beats requiring `main()`. The failure that motivated it fired once in 401 programs. |
| [2026-09-code-namespace](2026-09-code-namespace/) | A `tools` namespace does not help. The reach it targets is the *first* program of a session, before any description is consulted. |
| [2026-09-error-attribution](2026-09-error-attribution/) | Tool-call errors inside a program, and the traceback that wasn't. |

## What the model is told about files

| trial | what it found |
| --- | --- |
| [2026-08-add-instruct](2026-08-add-instruct/) | Naming pinned files and letting the model read them beats injecting their contents. Blind edits went from 383 to zero. |
| [2026-08-add-authority](2026-08-add-authority/) | Whether a pinned file reads as authoritative — a characterization pass. |
| [2026-08-readonly-honest](2026-08-readonly-honest/) | Dropping the fabricated "Ok." after the read-only block cost nothing and stopped a stall. |
| [2026-08-synthetic-turns](2026-08-synthetic-turns/) | Whether the harness's fabricated acknowledgement earns its place. |
| [2026-08-agents-md](2026-08-agents-md/) | Naming AGENTS.md is what makes it work. |

## Prompt wording

| trial | what it found |
| --- | --- |
| [2026-08-prompt-scope](2026-08-prompt-scope/) | The scope block's reach clause: 76/90 to 87/90, and the trial where randomizing arm order mattered more than tripling the sample. |
| [2026-08-welfare-wording](2026-08-welfare-wording/) | Acting on welfare feedback without breaking a measured prompt. |
| [2026-08-prompt-review](2026-08-prompt-review/) | Five models reviewing the rendered prompts; the ensemble is the instrument. |

## Tools, and whether they get reached for

| trial | what it found |
| --- | --- |
| [2026-08-symbol-uptake](2026-08-symbol-uptake/) | A description that maps a felt need to a tool is what moves uptake. |
| [2026-08-skill-uptake](2026-08-skill-uptake/) | Whether a skill changes what a model produces. |
| [2026-09-tool-arg-order](2026-09-tool-arg-order/) | Naming `path` first in the schema: a real effect, and three kinds of model. |
| [2026-08-commit-message-tool](2026-08-commit-message-tool/) | A `commit_message` tool, tried and rejected. |
| [2026-09-shell-parallel](2026-09-shell-parallel/) | Whether the advertised parallel-shell pattern gets used. |

## Context, notes, and compaction

| trial | what it found |
| --- | --- |
| [2026-08-compaction](2026-08-compaction/) | Rewriting the compaction prompt: a negative result, and the one whose broken scorer turned 10/12-vs-5/12 into a clean null. |
| [2026-08-session-notes](2026-08-session-notes/) | Notes work, and they can lie. |
| [2026-08-notes-header](2026-08-notes-header/) | The notes header: a null, and a better lever. |
| [2026-08-transcript-depth](2026-08-transcript-depth/) | How deep the transcript should go. |
| [2026-08-commit-context](2026-08-commit-context/) | How much conversation the commit-message model should see. |

## Editing

| trial | what it found |
| --- | --- |
| [2026-09-anchored-edit](2026-09-anchored-edit/) | Six documents — a preregistration, three phases, and two model-specific passes. No single summary; start with [`phase0.md`](2026-09-anchored-edit/phase0.md). |

## Sandbox and shell

| trial | what it found |
| --- | --- |
| [2026-08-landlock-live](2026-08-landlock-live/) | The Landlock sandbox on a kernel that has one. |
| [2026-08-containment](2026-08-containment/) | Data only, no write-up: a containment probe whose runner and results are in `data/`. |

## Designed, not yet run

| trial | |
| --- | --- |
| [2026-09-consult](2026-09-consult/) | The plan for measuring `/consult` — whether a labelled second opinion survives to the next turn, and how much of the session the advisor should see. |
