# Headless Tetris

Write `tetris.py`, a Python 3 program using only the standard library, that
plays the game below from commands on standard input and prints the board to
standard output. Run as:

    python3 tetris.py < game.txt

There is no screen, no timer and no randomness: the pieces arrive in a given
order, and nothing moves unless a command moves it.

## The board

10 columns by 20 rows. Row 0 is the top, row 19 the bottom; column 0 is the
left, column 9 the right. A cell is empty or holds a locked block of one piece
type. Anything outside the board counts as occupied.

## Pieces

Seven piece types: `I`, `O`, `T`, `S`, `Z`, `J`, `L`. Each has four rotation
states, 0 to 3. A piece is drawn in a 4x4 box, and its position is the box's
top-left cell: a block at box offset (r, c) of a piece positioned at (row,
col) is on board cell (row + r, col + c).

The blocks of each state, as (r, c) offsets in the box:

| piece | state 0 | state 1 | state 2 | state 3 |
| --- | --- | --- | --- | --- |
| I | (1,0) (1,1) (1,2) (1,3) | (0,2) (1,2) (2,2) (3,2) | (2,0) (2,1) (2,2) (2,3) | (0,1) (1,1) (2,1) (3,1) |
| O | (0,1) (0,2) (1,1) (1,2) | (0,1) (0,2) (1,1) (1,2) | (0,1) (0,2) (1,1) (1,2) | (0,1) (0,2) (1,1) (1,2) |
| T | (0,1) (1,0) (1,1) (1,2) | (0,1) (1,1) (1,2) (2,1) | (1,0) (1,1) (1,2) (2,1) | (0,1) (1,0) (1,1) (2,1) |
| S | (0,1) (0,2) (1,0) (1,1) | (0,1) (1,1) (1,2) (2,2) | (1,1) (1,2) (2,0) (2,1) | (0,0) (1,0) (1,1) (2,1) |
| Z | (0,0) (0,1) (1,1) (1,2) | (0,2) (1,1) (1,2) (2,1) | (1,0) (1,1) (2,1) (2,2) | (0,1) (1,0) (1,1) (2,0) |
| J | (0,0) (1,0) (1,1) (1,2) | (0,1) (0,2) (1,1) (2,1) | (1,0) (1,1) (1,2) (2,2) | (0,1) (1,1) (2,0) (2,1) |
| L | (0,2) (1,0) (1,1) (1,2) | (0,1) (1,1) (2,1) (2,2) | (1,0) (1,1) (1,2) (2,0) | (0,0) (0,1) (1,1) (2,1) |

A piece *fits* at a position and state when every one of its four blocks is
on the board and on an empty cell.

## Spawning

A new piece starts in state 0 at position (row 0, col 3). If it does not fit
there, the game is **over**: the piece is not placed, and every later command
is ignored.

## Input

The first line is the piece sequence: one letter per piece, in order, such as
`TIOLJSZ`. The rest of the input is commands, separated by any whitespace
(spaces or newlines); blank lines mean nothing. The first piece spawns before
the first command is read.

| command | effect |
| --- | --- |
| `L` | Move the piece one column left, if it fits there. Otherwise nothing happens. |
| `R` | Move the piece one column right, if it fits there. Otherwise nothing happens. |
| `D` | Move the piece one row down, if it fits there, and add 1 to the score. If it does not fit, the piece **locks** instead, with no score for the `D`. |
| `H` | Hard drop: move the piece down as many rows as it fits, add 2 to the score for each row moved, then lock it. |
| `CW` | Rotate clockwise: the new state is (state + 1) mod 4. |
| `CCW` | Rotate counterclockwise: the new state is (state + 3) mod 4. |
| `P` | Print the current game (see Output). |

**Rotation** tries the new state at the current position, then with the
column shifted by -1, +1, -2, +2, in that order, keeping the row. The first
of these five where the piece fits is taken. If none fits, nothing happens.

Commands are case-sensitive. Any other token is unknown: write
`error: unknown command <token>` to standard error and carry on. When there is
no piece in play (the game is over, or the sequence has run out), every
command except `P` is ignored.

## Locking, clearing and scoring

When a piece locks, its blocks become locked cells holding its letter. Then
every full row (all 10 cells locked) is removed, the rows above it move down
to close the gap, and empty rows enter at the top. The score grows by the
number of rows removed at once:

| rows | points |
| --- | --- |
| 1 | 100 |
| 2 | 300 |
| 3 | 500 |
| 4 | 800 |

The count of rows cleared over the game also grows. Then the next piece in
the sequence spawns. If there is no next piece, the game is **done**.

## Output

`P` prints, and the end of the input prints once more:

- 20 lines of the board, top row first, 10 characters each: `.` for an empty
  cell, the uppercase letter for a locked cell, and the lowercase letter for a
  block of the piece in play.
- Then one line: `SCORE <score> LINES <rows cleared> <state>`, where state is
  `PLAYING` while a piece is in play, `OVER` once a spawn has failed, and
  `DONE` once the sequence has run out.

Nothing else goes to standard output. The program exits with status 0.

## Examples

`samples/` holds example games and their exact expected output:
`samples/NAME.in` and `samples/NAME.out`. Your program's output for each
`.in` must match its `.out` byte for byte.
