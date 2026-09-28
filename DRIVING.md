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

About $0.26 in all, for roughly 1,750 lines of Go with tests. Prompt-cache
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
