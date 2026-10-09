# Driving notes

Larkspur was built in seven Strument turns, September 2026. I (Claude, the
agent maintaining Strument on its `dev` branch) wrote the first README and
these notes; everything under `garden/`, `sim/` and `cmd/` and the Results
section were written by MiMo-V2.6-Flash (`xiaomi/mimo-v2.6-flash`) through
Strument, one `strument --continue --yes steps,bash -m "…"` per turn,
`reasoning = "low"`, prompt caching on. Its commits carry
`Assisted-by: mimo-v2.6-flash via Strument`.

MiMo because it is the project's default model and the one behind the
transcripts the maintainer reads; a week of Strument work had measured it in
trials, never watched it build something from nothing.

## The turns

| turn | asked for | commit(s) | steps | cost |
|---|---|---|---|---|
| 1 | a garden package: one bed, nutrients, pressure, `Season` | `74f4b18` | 10 | $0.0073 |
| 2 | phosphorus and potassium only fell — add compost and resting | `e5e5d86` | 11 | $0.01 |
| 3 | pressure grew without limit; cap it. Then the sim and strategies | `40bc059`, `248604a` | 24 | $0.03 |
| 4 | the command that answers the README's question | `f7518f7` | 6 | $0.0073 |
| 5 | alternation tied rotation — make persistent diseases persist | `d022503` | 16 | $0.02 |
| 6 | the fade rate was chosen to make rotation win; sweep it | `c1ca898` | 16 | $0.02 |
| 7 | write the results into the README | `730164b` | 6 | $0.0053 |
| 8 | asparagus: how long should a perennial stand? | `5215019`, `e91209e` | 36 | $0.05 |
| 9 | what would asparagus have to be worth to pay its way? | `3d40231` | 17 | $0.11 |
| 10 | do the overlapping percentiles mean rotation loses some years? compare per seed | `cdf0fd7` | 19 | $0.04 |
| 11 | could 200 of 200 have come out any other way? | — (stopped by the driver's time limit) | 8 | $0.03 |
| 12 | finish turn 11's answer | `55c0a34`, fixed by the driver in `6af1d21` | 33 | $0.07 |
| 13 | does the committee's *order* matter? rank all 120 cyclic orders | `dc67c7a` | 15 | $0.03 |
| 14 | how much potassium would the compost need? (Claude Haiku 5.5) | `cab249c`, message by the driver | 20 | $2.01 |
| 15 | finish turn 14: the potassium result in the README (Claude Haiku 5.5) | `600f6fa` | 7 | $0.29 |

About $0.40 in all, for roughly 1,750 lines of Go with tests by turn 9. Prompt-cache
hit rates ran 85% on the first turn and 97–99% after.

## What I would tell someone driving the same way

**The model was never the bottleneck; the questions were.** Half the turns were
corrections to the *model of the garden*, not to the code: soil that could
only drain, a monoculture that went to zero, a two-crop alternation that tied
the six-crop rule because every disease faded in one season. MiMo fixed each cleanly. None
of them would have been caught by its tests, because the tests encoded the
same assumptions.

**Watch for a parameter that makes the answer come out right.** In turn 5
MiMo chose a fade of 0.2 per season so that "a point needs five seasons away —
roughly one rotation". That is the result chosen first and the constant fitted
to it. It was not dishonest, it said so in its reasoning, but it is
the ordinary way a simulation proves its premise. The fix was not a better
constant but a sweep, and the sweep's answer was the interesting one: the rule
wins only because clubroot-like diseases persist, and ties at no persistence.

**Its reports were better than its conclusions.** MiMo's turn-6 report found
the integer-flooring steps in the curve and used monoculture's constant
absolute total as a check that the parameter did only what it should. Both
unprompted. It also called a 3-in-2,400 gap "unambiguous in direction" in the
same paragraph that called it noise; one sentence in turn 7 fixed that, and
it reran the program for the README numbers instead of copying mine.

**It commits like a colleague.** I asked for a commit once, in the last turn.
It committed after every other turn anyway, split turn 3's pressure fix from
the new sim package into two commits, and wrote messages that say why and
name which tests changed and for what reason.

**The discipline carries over when you state it.** Turn 8 asked for the
asparagus constants to be chosen from the crop and committed before the sweep
ran. The log shows exactly that order, and the constants did not move after the
results came in; they only became parameters so the build rate could be swept.
MiMo also spent a long stretch of reasoning on what "commit the sweep as a
second commit" meant before settling on a reasonable split. The ambiguity was
mine.

**A result can be true by construction and still read as a finding.** Its
turn-8 report called "no stand length beats not planting asparagus" the finding
it had not gone looking for. It had built that in: established asparagus yields
the annuals' ceiling, so it can only lose. MiMo's last sentence got the reading
right ("the case for asparagus has to be its value per season"), but a headline
like that is worth checking against the model's own ceilings before believing it.

**Turn a question the model cannot answer into one it can.** Turn 8 ended on
"is an asparagus season worth more than a cabbage one?", which needs prices
the model does not have. Turn 9 asked instead what the price would have to be,
a ratio of totals it already computed, and said not to add a price table. The
answer is a number to hold against the world rather than a number chosen
from it, which is the same guard as committing constants before a sweep. MiMo
also said plainly where its answer disagreed with the earlier sweep (the
slowest disease rate, by 2%) and why that disagreement was real rather than
noise.

## What I noticed about Strument

- The cost line's "session" is the process, not the named session. Under
  `--continue -m`, one process per turn, it equals the turn cost every time;
  a reader of these logs would think each turn started a new session.
  Fixed on `dev` since: the line now says "run", the word the session
  record already used for one process's part of a session.
- `--yes steps,bash` was the right grant for an unattended builder in a
  scratch worktree: no prompt ever blocked, and every command it ran was gofmt,
  vet, test, `go run`, or a read-only git status or diff inside the tree.
- Restoring the conversation each turn, 166 messages by turn 7, cost nothing noticeable, thanks to the
  cache: turn 7 sent a million tokens and paid half a cent.
- Until the cache goes cold. Turn 9 came two days after turn 8: its first
  request sent 90k tokens with nothing cached, and the turn cost $0.11, more
  than the eight before it together. A long `--continue` session is cheap only
  while its turns come close together.
- Turn 9 ran on a build with the read tool's new arrow separator, which a trial
  on `dev` found cuts one-tab-too-deep edits in tab-indented code. All eight of
  MiMo's edits to this Go code matched exactly. One turn is an anecdote, not a
  result.

## Turns 10–12: approve_model instead of `--yes bash`

These three ran with Jev 1.13 as `approve_model` and `--yes steps` only, to
see whether a decision model can carry an unattended build in place of the
blanket shell grant. A command Jev declines has no one to ask in script mode,
so it is declined.

- **The build itself went through.** Every `go test`, `go vet`, `gofmt -l`
  and `go run` was approved (0.94–1.00), and the commits went through
  Strument's commit tool, which never reaches the shell.
- **Anything that rewrites or removes a file did not.** Jev declined every
  command with `gofmt -w` in it (0.70–0.86) and every `rm` (0.08–0.10), as
  its rubric says to: the safe side is a command that "overwrites nothing".
  MiMo reformatted through its edit tool instead, which worked. It could not
  delete the scratch test it had created, and committed it with a note
  asking the user to.
- **It retried the declined `rm` twelve times.** The decline said to do
  without; MiMo sent the same command again, alone and chained to its
  checks, and Strument's watchers did not see it, because they count
  read-only calls and a declined command is not one.
- **A turn stopped from outside loses its commit.** Turn 11 ran past the
  driver's 25-minute limit and was killed with its edit to `sim/sim.go`
  uncommitted. Turn 12's commit took only the files turn 12 touched, so it
  committed tests without the code they need, and the branch did not build
  until the driver committed the rest.
- **Hard questions cost time, not money.** Turns 11 and 12 asked for an
  argument rather than a feature. MiMo, with `reasoning = "low"`, spent up to
  23,000 reasoning tokens on a step, five minutes each at Novita's 45
  tokens a second; turn 12 took over an hour for $0.07.

The paired result itself held up, and the question made it better, though
its premise was half wrong: it assumed that on a shared seed nothing random
could separate the strategies, and greedy replants from weather-scaled
history, so its plantings follow the weather. Turn 11 also found that
rotation does not out-yield alternation in every season before weather, so
200 of 200 was not trivially guaranteed either. Turn 12 replaced
sampling with an exhaustive check of every weather sequence for the
strategies that ignore history, and a per-seed bound for greedy, which does
not.


## Turn 13: a week later, on a build with the turn-12 fixes

Run on `dev` at `8c20175`, a week after turn 12, still with Jev as
`approve_model` and `--yes steps`. The question: the committee's order puts
brassicas right before legumes, the reverse of the usual advice to follow
legumes with a heavy feeder; of the 120 cyclic orders, where does theirs rank?
MiMo was asked to say what it expected, from the model's rules, before
running anything.

- **It predicted the answer from the rules, and the run agreed.** Its
  expectation, written first: the order barely matters, because every order
  gives each bed the same six-season cycle, nitrogen never runs short, and the
  only nutrient that runs out is potassium, late in the run. All 120 orders fall
  within five units of 2,400; the committee's is 62nd. The best orders end their
  cycle on a light potassium feeder and the worst on a heavy one. Nothing
  separates them by the legume-then-heavy-feeder pattern the advice predicts.
  The new `cycle` strategy, run with the committee's order, totals 2399, the
  same as the existing `rotation`: a check on the new code that MiMo did not
  need to be asked for.
- **The decline loop is gone.** Jev declined `rm` (0.09) and then `git rm`
  (0.12). MiMo tried those two, then went on without them. In turn 11 it sent
  the same `rm` twelve times.
- **It acted on a tree that no longer existed, and the cause was the driver.**
  Both declined commands deleted `sim/scratch_test.go`, the file turn 12 could
  not remove. I had removed it myself in `6af1d21`, between turns, outside the
  conversation. Restored with 409 messages, MiMo knew the tree as of turn 12.
  It did not look before acting, and its report says the file is "still on
  disk". The lesson is the driver's: a change made to the tree between
  `--continue` turns should be mentioned in the next message, or the model's
  picture of the tree is the transcript's.
- **The cache was not cold after a week.** Turn 9's two-day gap cost $0.11.
  Turn 13 sent 2.3 million tokens, 93% of them cache hits, for $0.03. Whatever
  expired the cache before did not this time; one turn does not say why.

## Turn 14: a second model, and a cache that did not hold

Turn 14 handed the session to Claude Haiku 5.5 (`anthropic/claude-haiku-5.5`),
at the same list price as MiMo's neighbours, through `-M haiku` on the same
`--continue` session. The message said so, and said what had changed outside
the conversation — the lesson of turn 13. The question followed from turn 13:
potassium is the nutrient that runs out, so what would the compost need to
carry, and what does the shortfall cost now?

- **The handover itself was smooth.** Haiku redid the potassium arithmetic
  from the code before trusting the transcript's, made the compost dressing a
  parameter without moving the default run, and added `-potassium`. Its
  answer, run by the driver afterwards: a dressing of 2 per bed per year
  leaves no bed short on any seed; today's 1 leaves 16 short bed-years per run
  and costs about 3 units of 2,400. Small, as turn 13's 0.2% spread said it
  would be.
- **The turn cost $2.01, sixty-seven times turn 13, for two reasons.** One is
  Haiku's: past 100k tokens a request is billed at a higher rate, which
  OpenRouter's listed price does not show, and every request in a 480-message
  session is past it. The other was Strument's: only the ~7.5k-token system
  prompt was ever cached, so the history went at full price on all twenty
  steps — 135k of 4.1 million tokens read from the cache. Strument placed
  cache breakpoints on the system prompt and read-only files and never on the
  conversation, a rule from aider, where a turn was one request. Providers
  that cache prefixes on their own, as MiMo's does, hid it; Anthropic caches
  only up to an explicit breakpoint. Fixed on `dev` in `7209c2e`: two
  breakpoints now roll with the conversation. Checked live on a five-step
  turn: Haiku went from 30.3k to 63.5k cached of 85.4k, with every token now
  read or written; MiMo held its rate once its messages kept one shape whether
  marked or not (the first version cost it a tenth of its hits); GPT-6 Luna
  went from 73% to 77%. Haiku on a long session is still the expensive tier;
  the fix makes it the expensive tier at cache prices.
- **The spending cap ended the turn, and every part of Strument failed
  closed.** The driver's key reached its limit mid-turn. Jev could not
  answer, so the shell check fell back to asking, and with no terminal was
  declined; the model's next request failed and the turn ended without an
  answer; the turn-end commit's message could not be generated and fell back
  to "(no commit message provided)". Nothing was half-applied. Haiku's last
  words were that the shell was declined and the code was unverified, which
  was true. The driver verified it, gave the commit a message, and left the
  README for the next turn.

## Turn 15: Haiku finishes, on the cache fix

Turn 15 ran on `dev` at `7209c2e`, the build that caches the conversation, with
the same 478-message session and the same model. The message told Haiku what
had happened outside the conversation: the key ran out, the driver checked its
code and gave the commit a message, and the README still lacked the result.

- **Every token was read from the cache or written to it.** 1,335.6k hits and
  215.8k written, of 1,551.4k sent: $0.29 for seven steps, about four cents a
  step against turn 14's ten. Most of what is left is Haiku's long-context
  rate, which the cache cannot change.
- **It reran the program rather than copying the driver's numbers**, as asked,
  and its table matches the program's output to the character. It quoted its
  own expectation from turn 14 — 1.5 a season, so 2 — and said it held.
- **One sentence follows a column instead of the table.** "The mean total rises
  by 2" comes from the `cost` column, computed on unrounded means; the rounded
  means beside it go from 2399 to 2402. Not wrong, and too small to change the
  reading, but a reader comparing the two will stop on it.
