# Deduplicating unchanged re-reads: the base rate

**Status: characterization, run 2026-11-19.** A count over recorded sessions,
made to size a trial before building one. In short:
- Across 1,051 recorded sessions and 5,951 reads, a read that repeats an
  earlier read's exact window, of a file nothing could have changed in
  between, happened once. A deduplicating `read` would have fired once or
  twice in all of it.
- So no trial was run: an arm whose treatment never fires measures nothing
  (`doc/experimenting.md`, §27). Nothing ships.
- What the records lack is long interactive sessions. The scripts here run
  on a live state directory, for a count where re-reads are likelier.

## Why

Command Code's write-up of its read tool
(<https://commandcode.ai/docs/harness-engineering/read-tool>, read
2026-11-19) prices "unchanged-read dedup" as its third-largest saving: an
identical re-read of a file whose mtime and size have not changed returns a
short stub pointing at the earlier result, used once, so the worst case is
one wasted turn. Their figure is modeled, not measured, and comes from
sessions of "a few hundred reads". Before building the stub and a trial, the
question was how often Strument's sessions contain the thing it would catch.

## Method

`data/rereads.py` walks each session's tool calls in order and keeps, per
read window (path, offset, limit), what has happened since it was read:
nothing; a `bash` call or a `run_code` program that could write; or an
`edit` or `write` to that file. A read whose window was read before is
counted under that state. Only the first is a re-read the stub could answer
without checking the disk; the second needs the check; the third is a read
of a changed file. `data/contained.py` adds a read whose window falls inside
or overlaps an earlier one, and reads from inside `run_code` programs.

The records are the raw sessions of four trials in
`dbohdan/experimental-data` (at `2f55612`): 2026-11-featherweight,
2026-11-pared-down and its follow-up, and the 2026-11-consult trial runs.
The first three are single-turn coding tasks, MiMo and DeepSeek; the consult
sessions are longer documentation tasks, median 29 requests and up to 69.
The concision-block archive holds no session logs and is left out.

## Results

| records | sessions | reads | exact re-read, unchanged | after a command only | after an edit | inside an earlier window |
| --- | --- | --- | --- | --- | --- | --- |
| featherweight | 320 | 1,962 | 1 | 3 | 18 | 1 |
| pared-down | 560 | 2,904 | 0 | 8 | 32 | 0 |
| pared-down follow-up | 80 | 193 | 0 | 6 | 2 | 0 |
| consult | 91 | 892 | 0 | 0 | 16 | 16 overlapping, none contained |
| **all** | **1,051** | **5,951** | **1** | **17** | **68** | |

The one clean re-read cost 701 bytes, of 4.5 MB of tool results in the
coding sessions. Reads from programs were 260; 11 of them read a file the
session had read directly before, and a program needs the text to compute
with, so a stub could not answer them anyway.

## What this leaves open

The records are trials: one turn each, small fixtures, two models. Real
interactive sessions run longer and span turns, which is where a model loses
track of what it has read, and where Command Code's numbers come from. They
are also where compaction drops earlier results, and a re-read after that is
not a duplicate of anything the model can see. To count real sessions:

```sh
python3 doc/experiments/2026-11-read-dedup/data/rereads.py ~/.local/state/strument
python3 doc/experiments/2026-11-read-dedup/data/contained.py ~/.local/state/strument
```

(`$XDG_STATE_HOME/strument` where that is set.) They print counts only. A
clean re-read rate of a few percent of reads would be the point to build the
stub and run the trial.

## Instrument notes

- The first version of the log finder counted every consult session twice:
  the archives hold a runner's copy of each log and the original in the
  state directory. Fixed, and the counts above are from the fixed version.
- "Clean" is conservative in one direction only: a command between two
  reads is counted as possibly changing the file even when it did not, so
  the 17 "after a command only" re-reads are an upper bound on what a stub
  that checks the disk would add.

## Files

- `data/rereads.py`, `data/contained.py`: the two counts.
- `data/counts.txt`: their output per archive.
