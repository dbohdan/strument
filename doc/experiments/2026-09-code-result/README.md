# What a `run_code` program should hand back

**Result: keep the final value. The alternatives cost 21% and 55% more input
for no measurable gain — and the failure they were built to fix did not
reproduce, once in 401 programs.**

## The question

`run_code` returns the program's last evaluated value, the way a Jupyter cell
echoes its last expression. Two field transcripts suggested models do not read
it that way. A loop of `read(path=…)` that keeps nothing handed back the bare
word `None` after four successful reads, and MiMo took that for "the files do
not exist" and went looking for them again. Two `grep(…)` calls on consecutive
lines handed back the second and dropped the first, so a model verifying two
files had verified one — and nothing on screen said so, because the session
printed both searches.

The diagnosis offered was that last-value return is a Lisp/Ruby idiom that
misleads in Python. Two corrections shaped the design. A Jupyter cell echoes
the *last* expression (`ast_node_interactivity` defaults to `'last_expr'`), so
we already are Jupyter; the thing that echoes every expression is the
interactive REPL, and Monty exposes no per-statement hook to imitate it.
And the failing programs are not Python mistakes: nobody expects a discarded
expression statement to be reported. What models seem to expect is that
`read()` is a **tool call**, and everywhere else in this harness a tool call's
result comes back.

## Arms

One binary, one `--code-result` flag, so "the arms were the same program"
([`clean-null-has-many-causes`](../../experimenting.md#clean-null-has-many-causes)) cannot happen. Each arm carries its own contract paragraph *and* its own
worked example, since an example contradicting the paragraph above it is the
loudest thing in a tool description.

| arm | what comes back |
| --- | --- |
| `last` | the program's final value — the shipped behaviour |
| `all` | every bridged call's result, in order, then the final value |
| `main` | the return value of a `main()` the program defines |
| `bare` | `last`, from a worktree build with the warning sentence removed |

`bare` exists because the other three all carry a sentence added days earlier
("two calls on two lines return the second one's result and drop the first"),
so none of them can say whether that prose is what prevents the failure. It was
built with `git worktree add --detach HEAD`, and both binaries were grepped to
confirm they differ in exactly that sentence and nothing else.

3 models (MiMo-V2.5, GLM-5.3-flash, DeepSeek-v4-flash), 3 tasks, 2 reps, job
list shuffled with a fixed seed. 72 runs in the main trial, 36 in the probe.
About $0.15.

## Results

Correctness is at ceiling everywhere, so the comparison is cost.

| arm | correct | median steps | median input tokens |
| --- | --- | --- | --- |
| `last` | 18/18 | 7.0 | 27.5k |
| `all` | 18/18 | 8.0 | 33.4k |
| `main` | 18/18 | 8.5 | 42.6k |

Per model, `all` goes both ways — cheaper than `last` for MiMo (5.0 steps /
22.2k against 8.5 / 38.3k), dearer for DeepSeek (6.0 / 38.5k against 5.0 /
20.8k). Providers disagree, as usual; the pooled number hides a real split and
should not be read as a stable 21%.

`main` is the clearest result: it costs the most and buys nothing. It also does
not do what it is for. **30 of 89 programs wrote `main()` themselves** even
after being told the harness calls it, which is the same instinct the arm was
supposed to redirect.

## The finding that matters most: the hazard did not fire

| | programs | multi-call | lossy |
| --- | --- | --- | --- |
| `bare` | 133 | 28 | 0 |
| `last` | 97 | 23 | 0 |
| `all` | 98 | 22 | 0 |
| `main` | 135 | 27 | 0 |
| probe (`last`) | 29 | 19 | 0 |
| probe (`bare`) | 38 | 22 | 0 |

**One lossy program in 401**, and that one was in an arm where it costs nothing.
A program is lossy when it makes two or more bridged calls, prints nothing, and
never assigns, returns or comprehends a result — at most its final expression
can reach the model, so with two calls at least one is gone.

Two things had to be got right before that table meant anything.

**The first classifier counted call *sites*.** A loop with one `read(` scored as
one call, which is exactly the field's own failure shape, and it reported a
clean zero — [`renderer-has-two-forms`](../../experimenting.md#renderer-has-two-forms)'s warning that a clean zero deserves the same suspicion as a
clean p=1.0. It now asserts, before reporting, that it detects three
known-lossy programs and clears five sound ones. That self-check caught a second
miss: keying on the last line missed a `try`/`except` loop ending in `pass`.
The rule is about *binding*, not about the tail.

**The fixture had to be able to contain the phenomenon** ([`clean-null-has-many-causes`](../../experimenting.md#clean-null-has-many-causes)). The trial's
tasks are analysis — "which function ignores its argument" — where a model wants
the text in front of it and reaches for `print()` unprompted. The field cases
were *verification*: did my edit land, do these paths exist, where the model
wants a yes/no per file and might glance rather than keep. So a probe added two
verification-shaped tasks, in **normal mode** with the direct tools offered (the
setting the reports came from, not the forced one). Still zero. Reading the
programs rather than the counter confirms it is behaviour and not blindness:

```python
for f in ["pkg/alpha.py", "pkg/gamma.py", "pkg/zeta.py"]:
    print(f, "RETRIES" in read(path=f))
```

All 36 probe runs reached for `run_code` voluntarily even with direct tools
available, and every multi-call program printed or bound its results.

## What this does and does not license

It says: on three models and five task shapes, across two modes and four
descriptions, the losing shape is rare enough that paying 21% more input for
insurance against it is not worth it, and `main()` is worse on every axis.

It does not say the losing shape never happens — it happened twice in the field,
which is what started this. It says the trial could not make it happen again,
and a fix aimed at a hazard we cannot reproduce is a fix we cannot evaluate.
Nor does it say the warning sentence works: `bare` had zero too, so that prose
is not what is holding the line, and it should not be credited with doing so.

The honest summary is that models had already learned to print.

## Two harness bugs the trial found

Neither is about the arms, and both would have contaminated the numbers.

**`run_code`'s result was never truncated.** It was the one tool result in the
codebase that did not pass through `truncateResult`; five reads of a
long-lined file aggregated to 480 KB against a 60 KB per-call cap. Uncapped
output would have made `all`'s counter-metric a measurement of a pre-existing
hole. Fixed before the run.

**The `main` arm ran every program twice.** Told "the program is run and then
`main()` is called", 38 of 89 programs called it themselves, and the harness
appended another — every read repeated, every call double-counted against the
bridge cap. In the first batch that showed up as `main` costing more and timing
out twice, which is exactly the shape that gets written up as a fact about the
design. Fixed, and the arm re-run: it still costs the most, but now it is the
design paying for it rather than a bug.

## A limitation of this trial's own instrument

Reasoning effort was set per model in the config rather than as a rule, so GLM
ran at `"low"` and MiMo and DeepSeek ran at their defaults — and defaults are
drifting toward maximum. The input-token and step columns above survive that,
since reasoning lands in output; anything read off output tokens or latency does
not. `experimenting.md` [`mechanism-must-fire`](../../experimenting.md#mechanism-must-fire) now carries the rule this should have followed.

## Instrument faults worth carrying forward

- The scorer matched any line *containing* `ANSWER:`, so a model quoting the
  instruction back scored the answer `"`. Tightened to lines that begin with the
  marker, checked in both directions on crafted inputs.
- `setsid nohup … & echo $!` captures **setsid's** pid, not the program's.
  setsid exits immediately, so a `kill -0` watcher declared a 54-run batch
  finished at 12/54 while it was still running. [`runner-dies-quietly`](../../experimenting.md#runner-dies-quietly) says capture the specific
  pid; a wrapper defeats that just as thoroughly as a `pgrep` pattern.
- Asking a model to review the scorer while the batch was running produced 429s
  and slowed the batch. Four-way parallelism is the ceiling for everything
  together, not per script.
- Two models returned reasoning and no content at a 20-token cap, which reads as
  an API failure and is [`mechanism-must-fire`](../../experimenting.md#mechanism-must-fire)'s broken instrument.

## Recommendation

Keep `last` as the default. Keep the lost-calls note, which is cheap and correct
when it fires. Keep the flag: this measures a property of today's models, and
the cheapest way to notice that changing is to be able to re-run this.

Do not ship `all` on this evidence, and do not require `main()`.
