"""Write hidden tests (inputs + expected stdout from the reference) and samples."""
import os, random, subprocess, sys
L = os.path.dirname(os.path.abspath(__file__))
REF = os.path.join(L, "ref", "tetris.py")

def run(inp):
    return subprocess.run([sys.executable, REF], input=inp, capture_output=True, text=True).stdout

T = {}
for k in "IOTSZJL":
    T[f"spawn_{k}"] = f"{k}\nP\n"
    T[f"left_wall_{k}"] = f"{k}\n" + "L " * 6 + "P\n"
    T[f"right_wall_{k}"] = f"{k}\n" + "R " * 6 + "P\n"
    T[f"rotate_cw_{k}"] = f"{k}\nCW P CW P CW P CW P\n"
    T[f"rotate_ccw_{k}"] = f"{k}\nCCW P CCW P\n"
T["kick_I_left_wall"] = "I\nCW L L L L L CW P\n"
T["kick_I_right_wall"] = "I\nCW R R R R CCW P\n"
T["kick_T_right_wall"] = "T\nCW R R R R R CW P\n"
# A vertical I stands in column 5, rows 16-19. A horizontal I lowered to row
# 14 cannot turn upright at column 5; column 4 and column 6 both fit, and the
# kick order takes column 4.
T["kick_order_left_first"] = "II\nCW H\n" + "D " * 14 + "CW P\n"
# Vertical I's in columns 0 and 2 leave a one-column shaft at column 1. A
# vertical I lowered into it cannot turn either way: every shift is blocked.
T["kick_blocked_no_change"] = "III\nCW L L L L L H\nCW L L L H\nCW L L L L " + "D " * 16 + "P CW P CCW P\n"
T["soft_drop_scores"] = "T\nD D D P\n"
T["soft_drop_lock_no_score"] = "TO\n" + "D " * 19 + "P D P\n"
T["hard_drop_scores_rows"] = "LJ\nH P CW H P\n"
T["single_clear"] = "IIO\nL L L H\nR R R H\nH\n"
T["double_clear"] = "IIIIO\nL L L H L L L H\nR R R H R R R H\nH\n"
# Eight O's fill columns 0-7 of rows 16-19, a vertical I column 8, and a
# vertical I in column 9 clears all four rows at once.
T["tetris_clear"] = ("OOOOOOOOII\n" + "L L L L H " * 2 + "L L H " * 2 + "H " * 2 + "R R H " * 2 +
                     "CW R R R H CW R R R R H P\n")
T["clear_with_blocks_above"] = "IITO\nL L L H\nR R R H\nH\nH\n"
T["double_clear_vertical_pieces"] = ("IIIIIIO\nL L L H\nR R R H\nL L L H\nR R R H\n"
                               "CW R H CW R R R R R H H\n")
T["game_over"] = "O" * 12 + "\n" + "H " * 12 + "P L R H\n"
T["game_over_partial_overlap"] = "IIIIIIIIIIIIIIIIIIIIIZ\n" + "CW H " * 21 + "P\n"
T["done_ignores_commands"] = "T\nH L R D H CW P\n"
T["empty_sequence"] = "\nL R H P\n"
T["unknown_commands_ignored"] = "T\nX left d cw H2 L P\n"
T["case_sensitive"] = "T\nl r h P\n"
T["whitespace_any"] = "T\n\n  L\t\tL \n\n D\nCW   P\n\n"
T["no_trailing_newline"] = "TI\nL L H R R H"
T["many_prints"] = "TS\nP L P CW P H P\n"
T["o_rotation_noop"] = "O\nCW CCW CW P\n"
T["s_z_stack"] = "SZSZ\nH L L L H R R R H CW L L L L H P\n"

rng = random.Random(20261123)
cmds = ["L", "R", "D", "D", "CW", "CCW", "H"]
for i in range(5):
    seq = "".join(rng.choice("IOTSZJL") for _ in range(rng.randint(20, 60)))
    body = " ".join(rng.choice(cmds) for _ in range(rng.randint(150, 500)))
    T[f"random_keys_{i}"] = seq + "\n" + body + " P\n"
# Placed games: each piece gets a random rotation and shift, the odd soft
# drop, then a hard drop, which spreads pieces across the board; only games
# that clear lines are kept, so clearing and shifting are tested mid-game.
kept = 0
while kept < 5:
    seq = "".join(rng.choice("IOTSZJL") for _ in range(rng.randint(40, 90)))
    moves = []
    for _ in seq:
        moves += [rng.choice(["CW", "CCW"])] * rng.randint(0, 3)
        moves += [rng.choice(["L", "R"])] * rng.randint(0, 5)
        moves += ["D"] * rng.choice([0, 0, 1, 3])
        if rng.random() < 0.05:
            moves.append("P")
        moves.append("H")
    inp = seq + "\n" + " ".join(moves) + "\n"
    if " LINES 0 " not in run(inp).splitlines()[-1] + " ":
        T[f"placed_game_{kept}"] = inp
        kept += 1

hid = os.path.join(L, "hidden"); os.makedirs(hid, exist_ok=True)
for name, inp in T.items():
    open(os.path.join(hid, name + ".in"), "w").write(inp)
    open(os.path.join(hid, name + ".out"), "w").write(run(inp))

samples = {
    "move_and_drop": "JL\nL L D D P CW H\nR R R CCW H\n",
    "line_clear": "IIIO\nL L L H\nR R R H\nCW R R H\nH\n",
    "stack_to_top": "Z" * 11 + "\n" + "H " * 11 + "P\n",
}
smp = os.path.join(L, "fixture", "samples"); os.makedirs(smp, exist_ok=True)
for name, inp in samples.items():
    assert name not in T
    open(os.path.join(smp, name + ".in"), "w").write(inp)
    open(os.path.join(smp, name + ".out"), "w").write(run(inp))
print(len(T), "hidden tests,", len(samples), "samples")
