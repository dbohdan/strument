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

About $0.10 in all, for roughly 1,250 lines of Go with tests. Prompt-cache
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

## What I noticed about Strument

- The cost line's "session" is the process, not the named session. Under
  `--continue -m`, one process per turn, it equals the turn cost every time;
  a reader of these logs would think each turn started a new session.
  Worth a look on `dev`.
- `--yes steps,bash` was the right grant for an unattended builder in a
  scratch worktree: no prompt ever blocked, and every command it ran was gofmt,
  vet, test, `go run`, or a read-only git status or diff inside the tree.
- Restoring the conversation each turn, 166 messages by turn 7, cost nothing noticeable, thanks to the
  cache: turn 7 sent a million tokens and paid half a cent.
