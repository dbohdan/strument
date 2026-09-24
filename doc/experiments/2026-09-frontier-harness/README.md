# FrontierHarness's easy tasks, without Docker

**Status: characterization.** It is not an admissible FrontierHarness result:
one trial per task, a different sandbox, and a different route to the model.

**Result: Strument with Kimi K3 passed 11 of 12 tasks for $2.31.** The twelve
published configurations passed all twelve, for between $1.17 (pi-responses)
and $7.91 (claude-code). Strument's cost puts it eighth of thirteen. The one
failure was a real miss: K3 left a compiled binary in a directory the test
requires to hold only the source file. It did this in both runs of that task.
On these tasks Strument takes about as many turns as the other harnesses, and
each turn costs more.

**What it changed:** `retry_timeout` (`ce9cc7e`). Nine trials died when the
provider's rate limit outlasted the one-minute retry ladder inherited from
aider. That limit was fine for someone at the prompt and wrong for an
unattended run.

## The question

[FrontierHarness](https://frontierharness.org/) runs coding-agent harnesses
against one model and publishes each task's pass/fail, cost and turns.
Repository `frontier-harness-eval/eval` at `e837a70`, read 2026-09-24. It
covers 21 Terminal-Bench 2.1 tasks and 9 Datacurve tasks, all with Kimi K3 on
Fireworks, one trial per cell. Running it as intended needs Docker and an
account on their platform. How does Strument compare on a cheap subset run
through OpenRouter?

## Design

**Tasks.** The twelve tasks every published configuration passes. These show
cost and reliability; they cannot show capability, because nobody fails them.
The tasks come from `laude-institute/terminal-bench-2` at `2fd12b8`, in Harbor
format with public images `alexgshaw/<task>:20251031`. The six tasks that
separate the published configurations were left for a phase 2, which was not
run.

**Model.** `moonshotai/kimi-k3` on OpenRouter, with the provider pinned to
Fireworks (`allow_fallbacks = False`) to match the published runs. It has a
1M context and prompt caching on. Pricing is $3/$15 per million tokens, $0.30
for a cache read. Reasoning was left at the provider's default. The binary was
built at `56a6582`, which predates the fix for a turn's step count across a
renewed budget (`cf89aa2`). A task that auto-renewed its budget therefore
shows only the steps after the last renewal. merge-diff's strict run shows 2
steps for $0.99 for this reason.

**Invocation.** `strument --no-git --yes all -m "$(cat instruction.md)"` in
the image's working directory, with `sandbox = ""`. The chroot is the
sandbox, and Landlock inside a chroot would add nothing to compare.

**The sandbox** ([`data/trial.py`](data/trial.py)). There is no Docker
daemon. Each run works like this:

- [`data/pull.py`](data/pull.py) fetches the image from the registry API
  directly: it resolves the index to linux/amd64 and applies the layers with
  their whiteouts.
- The image is the lower layer of an overlay, so every trial starts from the
  same files.
- The agent runs chrooted in its own network namespace. It has only loopback,
  with two forwarders to Unix sockets on the host:
  - a SOCKS5 proxy that allows `openrouter.ai:443` and nothing else, used as
    the provider's `proxy`;
  - an HTTP proxy at `127.0.0.1:3128`, set as the task's `http_proxy` and
    `https_proxy`. It allows only the package registries in FrontierHarness's
    own egress policy (`providers.sh` at `e837a70`: PyPI, npm, Debian and
    Ubuntu, GitHub, astral, PyTorch).

  Every connection is logged. Strument honors only a SOCKS proxy, so the
  model's route and the tools' route are separate and cannot leak into each
  other.
- The verifier runs the task's own `tests/test.sh` afterwards, on the host
  network, in a fresh mount namespace over the upper layer the agent left.

Two runs were made:

| arm | egress for the task's tools |
| --- | --- |
| **strict** | none: `allow_internet = false` read literally |
| **registries** | the registries above, as the published runs had |

This environment intercepts TLS, so the interceptor's CA is appended to the
image's trust store in the upper layer. `pip.conf` and `uv.toml` point at it,
so trust arrives through files. Strument filters the environment of commands
the model causes, so a variable would not reach them.

## Results

### Registries arm (comparable to the published runs)

| task | reward | steps | cost | rank by cost (of 13) | published median | cheapest published |
| --- | --- | --- | --- | --- | --- | --- |
| build-cython-ext | 1 | 16 | $0.653 | 6 | $0.690 | $0.419 |
| constraints-scheduling | 1 | 4 | $0.144 | 12 | $0.071 | $0.047 |
| db-wal-recovery | 1 | 8 | $0.136 | 9 | $0.118 | $0.050 |
| git-leak-recovery | 1 | 9 | $0.118 | 11 | $0.077 | $0.029 |
| log-summary-date-ranges | 1 | 7 | $0.101 | 11 | $0.066 | $0.030 |
| merge-diff-arc-agi-task | 1 | 17 | $0.218 | 11 | $0.152 | $0.071 |
| modernize-scientific-stack | 1 | 7 | $0.111 | 12 | $0.093 | $0.035 |
| multi-source-data-merger | 1 | 7 | $0.156 | 11 | $0.108 | $0.043 |
| openssl-selfsigned-cert | 1 | 6 | $0.122 | 11 | $0.073 | $0.036 |
| **polyglot-c-py** | **0** | 6 | $0.193 | 10 | $0.132 | $0.051 |
| sqlite-db-truncate | 1 | 6 | $0.141 | 9 | $0.126 | $0.087 |
| vulnerable-secret | 1 | 19 | $0.218 | 12 | $0.139 | $0.087 |

| configuration | passes | cost on these 12 |
| --- | --- | --- |
| pi-responses | 12/12 | $1.17 |
| opencode | 12/12 | $1.36 |
| dsh-creator | 12/12 | $1.51 |
| codex | 12/12 | $1.59 |
| dsh-standard | 12/12 | $1.67 |
| dsh-ptc | 12/12 | $1.72 |
| dsh-minimal | 12/12 | $1.85 |
| **strument** | **11/12** | **$2.31** |
| kimi-code | 12/12 | $2.37 |
| oh-my-pi | 12/12 | $2.43 |
| hermes | 12/12 | $2.49 |
| exo | 12/12 | $2.73 |
| claude-code | 12/12 | $7.91 |

Published costs are FrontierHarness's `cost_first_cold_usd`. Strument's are
what OpenRouter billed.

**Rate limits.** The first attempt ran three trials at a time. Nine trials
died on their first request, each with exactly nine `rate_limit: HTTP 429`
lines: eight retries on the ladder, 0.25s doubling to 32s, then the give-up.
Rerunning one trial at a time recovered six of them. The other three
(db-wal-recovery, sqlite-db-truncate, polyglot-c-py) still exhausted the ladder
against Fireworks alone. They passed once OpenRouter could fall back to
Moonshot, Together or Baseten at the same price. Strument's log does not record
which provider served a request, so it is unknown which one did. All nine dead
trials are kept as `registries-429` in the data and excluded from the tables.
Eight of the twelve valid trials also met 429s and got through them.

### Strict arm

10/12 for $3.22. Two results stand out:

- **build-cython-ext failed as it should.** It needs `pip install`. K3 found
  no route out and stopped. It wrote a five-step plan and asked for
  networking or the source to be provided.
- **merge-diff-arc-agi-task passed without git.** The image has no git, and
  the strict arm allows nothing to be installed. K3 implemented enough of git
  in Perl (objects, refs and a three-way merge) to complete the task. That is
  the most expensive single trial in either arm, at $0.99.

polyglot-c-py failed here the same way it did in the registries arm.

### The failure

polyglot-c-py asks for one file, `/app/polyglot/main.py.c`, that runs under
both `python3` and `gcc`. The test's first assertion is that the directory lists `["main.py.c"]`,
and nothing after it ran. K3 had checked gcc's output for N up to 92. The
Python path went untested, because the image has no Python. K3 compiled using the
task's own suggested command, `gcc … -o /app/polyglot/cmain`, and left `cmain`
in the directory. That happened in both runs. K3 said in its answer that it
could not run the Python path, rather than claiming a check it had not
made. Every
published configuration passed this task, so this is a miss specific to this
model and harness, not the test's fault.

## Where the cost goes

Over the twelve registries-arm trials:

| share of cost | range | typical small task |
| --- | --- | --- |
| output (reasoning and answer) | 27–81% | 30–50% |
| uncached input | 10–51% | 40–45% |
| cached input | 5–39% | 5–15% |

- **The fixed prompt** is about 5.7k tokens. Tool schemas are about 3.7k of
  that; the system prompt is about 1.1k.
  - `run_code`'s schema is the largest at 3.4k characters, followed by
    `ask_user_question` (1.8k), `bash` (1.7k) and `grep` (1.6k, almost all
    parameter descriptions).
  - On a cold first request, those 5.7k tokens cost about $0.017. Each later
    request re-reads them from cache for about $0.002.
  - On a seven-step task that comes to about $0.03 of $0.10–0.15, or
    20–25%.
- **Turns are not the gap.** Strument's median is 7 steps. The published
  configurations' medians on these tasks run from 6 to 8.5, and claude-code's
  is 15.5.
- **Cache hit rate** was 54–77% on the small tasks. The published
  configurations were at 69–87%. That comparison is confounded: a larger
  fixed prompt raises the hit rate. For Strument the rate is close to the
  ceiling append-only history allows. What stays uncached is the first
  request plus about 1.5k new tokens per step.

Tool calls across the valid trials: `bash` 168, `read` 51, `edit` 29,
`write` 29, `run_code` 26, `ls` 17, `grep` 4, `webfetch` 1,
`ask_user_question` 1 (the strict cython run), `glob` 0.

## Caveats

- **One trial per task.** A single pass or fail says little. The polyglot
  miss happening twice is the only repeated result.
- **Not their sandbox.** These runs used an overlay, a chroot and proxies
  rather than Harbor and Docker. The task files, images and tests are the
  same.
- **Not their route.** The model was reached through OpenRouter rather than
  Fireworks directly. Three tasks ran with fallback providers allowed. The
  published figure is FrontierHarness's `cost_first_cold_usd`; Strument's is
  what OpenRouter billed.
- **Easy tasks only.** The set was chosen because it does not discriminate.
  Nothing here says how Strument does where harnesses differ.
- **An older binary** (`56a6582`), before the step-count fix. Step counts on
  renewed budgets undercount.

## Reproducing

The scripts expect root (for overlay, chroot and namespaces), Python 3.11+,
and the two clones under `/tmp/harness/`: `fh-eval` at `e837a70` and `tb2` at
`2fd12b8`. The scripts use fixed paths under `/tmp/fh/`, so copy `data/*.py`
there and put a static `strument` binary at `/tmp/fh/strument`. The key is
read from `OPENROUTER_API_KEY`.

```sh
cd /tmp/fh
FH_REGISTRIES=0 python3 phase.py floor k3 1     # strict: pulls images, runs 12 trials
RUNTAG=-reg FH_WORKERS=1 python3 phase.py floor k3 1
RUNTAG=-reg FH_WORKERS=1 FH_PROVIDERS='["fireworks","moonshotai","together","baseten"]' \
  python3 phase.py floor k3 1                  # reruns only what has no result.json
python3 score.py k3-reg                        # the tables above
```

Unattended runs should now set `retry_timeout` well above the default. At
600, each of the nine dead trials would have had ten minutes of waiting
instead of about one. Whether that is enough against a saturated provider was
not tested.

[`data/results.jsonl`](data/results.jsonl) has one row per trial in all three
groups, including the dead ones: reward, outcome, steps, tokens, cost,
rate-limit errors, egress counts and the tools called.
