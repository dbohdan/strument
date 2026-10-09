# Prompt caching: conversation breakpoints, message shape, and sticky routing

**2026-10-09.** Live checks, not a preregistered trial: a five-step read-only
turn (read three files, run `go test`, answer in one paragraph) in a fresh
clone of Larkspur per run, `reasoning = "low"`, `cache = True`, through
OpenRouter. 64 runs, about $0.17. Prompted by a Larkspur turn on Claude Haiku
5.5 that cost $2.01, and then by Command Code's claim of "99%+ cache hit
rates".

## 1. The conversation was never cached on Anthropic (fixed, 7209c2e)

Strument put breakpoints on the system prompt and read-only files and never on
the conversation, a rule from aider, where a turn was one request. Anthropic
caches only up to an explicit breakpoint, so in a tool loop every step resent
the history at full price; providers that cache prefixes on their own hid it.
Turn 14 of Larkspur sent 4.1 million tokens and read 135k from the cache.

Two breakpoints now roll with the conversation: on the request's last message
and on the message before the last answer, where the previous request's last
breakpoint was. Cache hits / tokens sent, per run:

| model | before | after |
|---|---|---|
| Claude Haiku 5.5 | 30.3k / 85.3k (system prompt only) | 63.5k / 85.4k, plus 21.8k written — every token read or written |
| GPT-6 Luna | 73–74% | 77% |

On the same 478-message Larkspur session, turn 15 then read 1,335.6k and wrote
215.8k of 1,551.4k sent.

## 2. A message's shape is part of an automatic cache's key

The first version made MiMo-V2.6-Flash worse: 48.3k mean hits of 65.4k over
ten runs, against 53.2k before. A message went out as a plain string one step,
as a marked block list the next, and as a string again after, and MiMo's
provider caches on the bytes. With caching on, user and tool messages now go
out as blocks whether marked or not: 52.4–52.5k in six runs of six, against
48.4–52.5k for the old build in the same batch.

## 3. A session key for sticky routing made it worse (not shipped)

OpenRouter routes a conversation to the provider that served it when the
request carries `session_id` or `x-session-id`, and otherwise guesses the
conversation from its first messages and sticks only after a cache hit. A
session-scoped key (a hash of project root and session name) was tried,
six runs against six of the build above, alternating, four at a time:

| model (OpenRouter endpoints) | without key: hits / 60–65k, cost | with key |
|---|---|---|
| MiMo-V2.6-Flash (8) | 48.4k ×2, 52.4–52.5k ×4; $0.0021–0.0026 | identical |
| GLM-5.3-Flash (33) | 44.8–50.1k; $0.0018–0.0022 | 43.5–44.5k ×4, 29.2k, **0**; $0.0037–0.0093 |

With the key, GLM ran faster (31–52 tokens/s against 16–27) and cost about
twice as much at the same hit count: the run stays with whichever provider
served its first request, often a faster and dearer one, where without a key
OpenRouter keeps choosing on price and still finds the cache most of the time.
One run read nothing from the cache at all. The key is not sent.

## What Command Code does, and what "99%" measures

Read from `command-code` 1.79.2 on npm (published 2026-10-08, `UNLICENSED`,
read as data, not run) and its documentation, 2026-10-09:

- Its client marks the system prompt in two cached sections — the base
  prompt, then taste, skills, scratchpad and an environment snapshot — and
  leaves the volatile IDE context after both, unmarked.
- The environment snapshot (working directory, branch, git status, recent
  commits) is computed once per session and memoized, so the system prompt is
  byte-stable for the session at the price of a stale status line. Its
  changelog records "stabilize prefix cache" against exactly that snapshot,
  and a "dynamic prefix cache thrashing" overhaul with a "2.9x median cost
  reduction". Strument already does the equivalent: the prompt names the date
  Strument started and leaves the current time to the `about` tool, and
  carries no git status.
- One-shot calls, such as its goal verifier, set `promptCache: "off"`, since
  a cache write costs more than plain input and is never read. Strument's side
  calls already carry no breakpoints.
- Requests for hosted models go to its own server with a `threadId` and the
  system sections; message breakpoints and provider routing happen there, out
  of sight. Its cache documentation is listed as in progress.

A hit rate measured as tokens read over tokens sent approaches 1 − Δ/C on a
long session, where C is the context and Δ what each step adds: at 200k
context and 2k a step it is 99% with no technique beyond caching the
conversation at all. Five-step runs like these cannot exceed about 80% because
the first request and each step's additions are writes. "99%+" says more about
session length than about method, and is not a number to compare short runs
against.
