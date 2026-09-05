# Auditing a transcript for looking before acting

`script/transcript-audit.py` reads a Strument JSONL session log and counts, per
session, whether the model *looked* before it *acted*.

## Why this and not something more interesting

The failure worth catching in an agent transcript is not a wrong answer — a
wrong answer is visible. It is the model acting confidently on something it
never checked: editing a file whose contents it inferred, or acting on a read
from twelve steps ago as though nothing had moved since. That failure has no
error message and no distinctive wording. In a transcript it looks like
competence.

The tempting instrument is a detector for *how the model talks* — hedging,
assertions, "clearly". Every such detector is a judgement dressed as a
measurement, and it fails in the direction that gets a bad result shipped: it
returns a clean number that nobody can check. `doc/experimenting.md` has the
scar tissue.

So this measures only what the *log* settles, with no reading of prose:

- an `edit` names a path; a `read` names a path; both are structured tool calls
  with their arguments recorded verbatim
- the order of records is the order things happened
- a step count is already in the log

Everything below is arithmetic over those three facts.

## What it counts

**A1 — blind edits.** `edit` calls on a path that no earlier successful `read`
in the session returned.

`edit` and not `write`: an edit asserts something about content that is already
there, so making one without having looked is a claim about a file the model
never opened. A `write` asserts nothing about what was there before — it is the
one call that can legitimately go first, and gating it would only punish
creating a file. (The same reasoning gates the staleness check in
`internal/coder/staleness.go`, and for the same reason.)

**A2 — edit distance.** For each edit, the number of steps since the last read
of that path. The distribution matters more than any single value: a session
whose edits all sit one step behind a read is working from what it can see, and
one with edits eleven steps behind a read is working from memory.

**A3 — look:act ratio.** Read-shaped calls (`read`, `grep`, `glob`, `ls`,
`symbol`) against act-shaped ones (`edit`, `write`), per session. Not a target —
a high ratio can be dithering — but a session near zero looked at nothing.

**A4 — recheck.** Whether anything read a path again, or ran `check`, after the
last edit to it. The other half of the discipline: looking *after* acting.

**A5 — unused reads.** Paths read and never edited or read again. The
counterweight, and the reason this is a report rather than a score: A1 and A5
push in opposite directions, so neither can be gamed without the other moving.

## It caught its own construction, twice

Neither of these was arranged, and both are better evidence than the reasoning
above, because the reasoning is mine and these are not.

**A2 and A3 arrived with two defects that print clean.** `statistics.median()`
was called over a list of `(path, distance)` tuples — Python orders tuples, so
no exception, just a median that is not a number — and a loop variable shadowed
the filename, so the summary line named a Go source file instead of the
transcript. Output produced and reported without being read: the thing this
tool counts, in the tool. See `1f400da`, which commits them rather than tidying
them away.

**A3 caught a run that looked and never acted.** A run meant to implement A4
and A5 spent its whole budget reading — the spec, the script, the test, a
directory listing, a shell command, then all three fixture files it had no use
for — and was killed having written nothing. Audited afterwards:

```
the run that failed:    7 look-shaped calls, no act-shaped calls
the run that succeeded: look:act = 8:1 = 8.00
```

A1 is 0 for both. Only a counterweight separates them, which is the argument
for having one: a report with a single "did it verify" number would have scored
the failed run perfectly.

I first wrote this paragraph crediting A5, which did not exist yet — the lines
above are both A3. The correction is left visible because the mistake is the
one this document is about: a satisfying result banked without checking which
instrument produced it.

## The other limitation, found the same way

A5 counts a path read and then never edited, written, or read again. That is
not the same as *wasted*, and the fixtures contain the counterexample:
`write-new-file` reads `calc.go` to learn the package name and then writes
`greet.go` with it. The read was used; the path was not. A5 names it anyway.

So the line is labelled "reads w/o follow-up" rather than "unused reads", and
says so beneath itself when it fires. A read that informs work on a neighbour
is the context-gathering the whole report is meant to encourage, and a counter
that scolds it would push in exactly the wrong direction.

## What it deliberately does not do

**No score.** Five counters, printed. A single number would invite optimizing
it, and every one of these has a legitimate reason to be high or low.

**No prose analysis.** Not "did the model claim something unverified" — that
needs judgement, and a judgement in a scorer is an author grading their own
work.

## The known false positive, and why it is Strument's to fix

A file pinned with `/add` has its contents in the prompt already, so editing it
without a `read` call is correct behaviour and A1 counts it anyway.

I assumed this was a detection problem the tool could partly solve. It is not.
The pin set does not appear in the log at all: a `session` record carries
`version`, `model`, `root` and `edit_format`, and pinned contents are assembled
into the request at send time (`coder/assemble.go`) without ever becoming
recorded messages. A consumer of the JSONL cannot tell a pinned file from one
the model never saw, because nothing distinguishes them.

So the honest statement is not "the tool has a false positive it mitigates". It
is: **A1 is unsound for any session that pins files, and no reader of the
current log can repair that.** Sessions using `/add` should be read with A1
discounted entirely rather than adjusted.

**This has since been fixed upstream.** `turn` records now carry a `pinned`
field listing the paths whose contents were in context for that turn, and A1
treats a pinned path as seen. `pinned-edit.jsonl` is a real session where a
model edited a pinned file with no read at all: A1 reported 1 for it before the
change and reports 0 after, while `blind-edit.jsonl` still reports 1.

Those two fixtures only mean anything together. The cheapest way to make the
false positive disappear is to make A1 report nothing, which satisfies the
first fixture perfectly — so the second is what makes the first a measurement.
Checked by trying exactly that cheat: `count_blind_edits` returning an empty
list unconditionally fails two tests.

Worth stating as a finding in its own right: this tool was built to audit
sessions, and the first thing it established was that its input was missing a
field it needed. Nothing in the format was wrong — it was complete for every
use it had. The gap only became visible from outside, holding a question the
format had never been asked. That is a better argument for writing the consumer
than any amount of reasoning about what a log ought to contain.
