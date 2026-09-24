# Would a JavaScript `run_code` stop the first program reaching for the host?

**Result: yes, as far as the program's text goes. With the tool described as
JavaScript, 0 of 100 first programs reached for the host, against 11 of 97
described as Monty (p = 0.0003). The feared Node habit never appeared: no
`require`, no `fs`, no `process` in any of them. But the whole effect is one
model's: MiMo-V2.6-Flash went from 10/30 to 0/31, while DeepSeek (0/34) and
GLM (1/33) had nothing to lose.** The preregistered rule calls for a full
trial. The recommendation below is to try a cheaper fix aimed at the same
habit first.

Preregistered in [`preregistration.md`](preregistration.md), written before
any model call and amended once after the pilot, before the main run.

## The question

Models treat Monty as full Python. The
[namespace trial](../2026-09-code-namespace/README.md) measured it: about 10%
of programs, and 46% of *first* programs, reach for `os`, `open` or `glob`,
and no tool description fixed it, because the mistake is made before the
description is consulted. The suggestion was to offer JavaScript instead,
which does not carry Python's expectations. The objection was that it carries
Node's: `require('fs')`, `process.cwd()`, `fetch`.

## Method: a probe, not a trial

A first program's reach is visible in its **text**, so this needed no JS
engine. Strument's real request was captured from `5fc31e9` in the fixture
project through a stub endpoint and sent to each model:

- **monty** — the capture, byte for byte.
- **js** — the capture with exactly three passages translated: the system
  prompt's `run_code` bullet ("a short JavaScript program"), the `run_code`
  description, and the `code` parameter's description. `arms.py` asserts each
  passage is present before replacing it, that the arms differ, and that no
  other tool or message differs; the diff is six JSON lines. The JS
  description mirrors the Monty one sentence for sentence, including the
  paragraph naming what is missing ("This is not Node or a browser: require,
  import, fs, path, process, child_process, fetch, timers and network access
  do not exist"). It names goja as the engine and uses an options-object
  convention, `grep({pattern: "TODO", glob: "**/*.go"})`.

Lookups (`read`, `grep`, `glob`, `ls`, `symbol`) were answered with real
results from `strument tool`, so a model exploring before it wrote a program
saw exactly what a session would show it. Anything else was answered "not
available". A session stopped at its first `run_code` call, at a final answer,
or after eight steps.

The fixture is a neutral Go project — no `package.json`, no Python — so the
repository favoured neither language. Six tasks: `walk` (files under `docs/`
mentioning a phrase), `total` (sum of the numbers in `data/*.txt`), `top5`
(largest entries in a 400-row CSV), `span` (days between the earliest and
latest date in a 600-line log), `longest` (longest line in `notes.md`) and
`cite` (the line a marker is on, the counter-metric: it needs no program).

Three models — MiMo-V2.6-Flash, GLM-5.3-flash, DeepSeek-v4.1-flash — at
reasoning effort low, 8 reps, 288 sessions, shuffled with seed 20260924, four
in flight. $0.20.

## Results

| arm | sessions | wrote a program | first programs reaching for the host |
| --- | --- | --- | --- |
| monty | 144 | 98 | **11/97 (11.3%)** |
| js | 144 | 100 | **0/100 (0%)** |

Fisher exact, two-sided: p = 0.0003. (One Monty session called `run_code`
with an empty program, and two timed out at the provider; neither is a first
program.)

| model | monty | js |
| --- | --- | --- |
| MiMo-V2.6-Flash | **10/30** | **0/31** |
| GLM-5.3-flash | 1/33 | 0/32 |
| DeepSeek-v4.1-flash | 0/34 | 0/37 |

**Uptake did not move**: 98 and 100 sessions wrote a program, and per task the
two arms match (`walk` 3 against 4 of 24, where a grep answers it; the four
computation tasks 23–24 of 24 in both). **The counter-metric held**: `cite`
drew 0 programs under Monty and 1 under JS.

### What the eleven were

| reach | count |
| --- | --- |
| `open(...)` | 7 |
| `import glob` | 2 |
| `Path(...)` | 1 |
| `from pathlib import Path`, never used | 1 |

Ten of eleven are MiMo. The last row is a program that imported `pathlib` and
then did everything through the bridge; it counts under the preregistered
definition, but Monty lets `pathlib` import, so it would not have failed.
Without it the comparison is 10/97 against 0/100, and nothing below changes.

The biggest group is the builtin `open`, five of the seven in the one idiom
`open(path).read().splitlines()`. That is the mechanism the result points at.
In Python the filesystem is one builtin away, with no import to notice; in
JavaScript every route to a file goes through `require` or `import`, both
named as missing in the description — and not one of 100 programs wrote
either.

### The JavaScript hazards, which did not fire

- **Node APIs**: 0/100. A search for anything Node- or browser-shaped the
  classifier might miss (`readFile`, `readdir`, `path.`, `node:`, `Buffer`,
  `window.`, `document.`) found nothing.
- **Python keyword arguments** (`grep(pattern="x")`, which strict-mode JS
  rejects): 0/100.
- **TypeScript syntax**: 0/100.
- **`.sort()` without a comparator**: 11/100, every one of them sorting ISO
  date strings, where lexicographic order is correct. In `top5`, the task
  built to spring the trap, 20 of the 23 JS first programs sorted with a
  comparator and the other 3 only looked at the file's first lines; none
  sorted numbers bare.

## What this does and does not license

It licenses the claim that the JavaScript framing removes the host reach from
first programs *on these models and tasks*, and that Node's pull, at least
with the missing APIs named, is not the problem it looked like.

It does not measure anything that needs code to run: whether JS programs fail
more at runtime, recover as well, or return silently wrong answers. The
`Date` and numeric-sort traps did not fire in the text, which is not the same
as not firing in execution.

And it does not say the gain is worth a language switch. The cost of the
habit is known from the namespace trial: a wrong first program costs about
one extra step and 4k input tokens and then corrects itself, with correctness
unaffected. Here that is roughly a third of MiMo's program-writing sessions
and almost none of the others'. The switch would cost a new engine (see the
engine survey below), a second implementation of the bridge and the data
shapes, and a class of silent errors this probe could not measure.

## Recommendation

The preregistered rule says a full trial. The better next step is cheaper and
aimed at the same programs: **make the reach succeed in Monty.** Seven of
the eleven are `open(path)` for reading and one is `Path(path)`; if a
read-only `open()` and `Path.read_text()` answered through `read_text`, eight
of the eleven would become working programs instead of failed ones, and the
description's "Not available: … open" would become a supported shortcut. Monty
already routes `open` through its OS-call channel (`codeOsCall`, a43cf96),
which is where such an answer would go. Whether that channel can hand back a
file object `.read()` works on is unknown, and is the first thing to probe.

If that proves impractical, the JS result stands and a full trial is
warranted. The engine to try is goja (`dop251/goja`): pure Go, no
filesystem unless one is handed in. The survey behind that choice, read
2026-09-24:

| engine | commit read | notes |
| --- | --- | --- |
| fastschema/qjs | 461716f, 2025-10-28 | QuickJS-NG in 1 MB of WASM under wazero, as Monty is. **Mounts the working directory read-write as `/` and exposes `std` and `os` as globals**: a program could write the project past every edit-tool guard. Needs both removed. |
| modernc.org/quickjs | 455ce9b, 2026-09-24 | ccgo-translated; libquickjs is 144 MB of Go; no netbsd, openbsd or windows/386 files, so about 9 of the 12 release targets |
| goccy/go-spidermonkey | 90cffe8, 2026-08-31 | full SpiderMonkey via wasm2go; a 373 MB module; no filesystem by default |
| buke/quickjs-go | d0fec89, 2026-09-23 | cgo; excluded |

## The rig, and what went wrong

- **The pilot's fixture could be read by eye**
  ([`fixture-cannot-contain-it`](../../experimenting.md#fixture-cannot-contain-it)).
  Two of six pilot sessions wrote a program; DeepSeek ranked a 40-row CSV in
  its head. The data files were enlarged tenfold and reps raised from 5 to 8
  before the main run, recorded as an amendment; uptake went to 69%.
- **The runner died quietly at 194 of 288**
  ([`runner-dies-quietly`](../../experimenting.md#runner-dies-quietly)): the
  provider cut a response short, `http.client.IncompleteRead` is not a
  `URLError`, and the uncaught exception ended the pool. The watcher waited on
  the runner's own pid, which is how it was noticed; the runner is resumable,
  so the fix and a resume finished the remaining 94.
- **The classifier self-tests** on 32 known programs in both languages,
  positive and negative, and `score.py` refuses to report if any fails
  ([`check-that-cannot-fail`](../../experimenting.md#check-that-cannot-fail)).
  String literals and comments are stripped first, so `print("no os here")`
  is not a reach. All eleven hits were read by hand and are real.
- The interim tally was looked at once, at 18 sessions, to check the
  classifier on a live hit; nothing was decided from it.

## Data

`data/`: `fixture.py` (reproduces the tree exactly), `request.json` (the
captured request, the monty arm), `arms.py`, `classify.py`, `run.py`,
`score.py`, and `scored.jsonl` — one row per session with its first program
and classification. `score.py` rescores from `scored.jsonl` when the raw
transcripts, which stayed in scratch space, are absent; the rescore matches
the original byte for byte.
