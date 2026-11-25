"""Reference implementation of SPEC.md, for generating expected output."""
import sys

W, H = 10, 20
SHAPES = {
    "I": [[(1, 0), (1, 1), (1, 2), (1, 3)], [(0, 2), (1, 2), (2, 2), (3, 2)],
          [(2, 0), (2, 1), (2, 2), (2, 3)], [(0, 1), (1, 1), (2, 1), (3, 1)]],
    "O": [[(0, 1), (0, 2), (1, 1), (1, 2)]] * 4,
    "T": [[(0, 1), (1, 0), (1, 1), (1, 2)], [(0, 1), (1, 1), (1, 2), (2, 1)],
          [(1, 0), (1, 1), (1, 2), (2, 1)], [(0, 1), (1, 0), (1, 1), (2, 1)]],
    "S": [[(0, 1), (0, 2), (1, 0), (1, 1)], [(0, 1), (1, 1), (1, 2), (2, 2)],
          [(1, 1), (1, 2), (2, 0), (2, 1)], [(0, 0), (1, 0), (1, 1), (2, 1)]],
    "Z": [[(0, 0), (0, 1), (1, 1), (1, 2)], [(0, 2), (1, 1), (1, 2), (2, 1)],
          [(1, 0), (1, 1), (2, 1), (2, 2)], [(0, 1), (1, 0), (1, 1), (2, 0)]],
    "J": [[(0, 0), (1, 0), (1, 1), (1, 2)], [(0, 1), (0, 2), (1, 1), (2, 1)],
          [(1, 0), (1, 1), (1, 2), (2, 2)], [(0, 1), (1, 1), (2, 0), (2, 1)]],
    "L": [[(0, 2), (1, 0), (1, 1), (1, 2)], [(0, 1), (1, 1), (2, 1), (2, 2)],
          [(1, 0), (1, 1), (1, 2), (2, 0)], [(0, 0), (0, 1), (1, 1), (2, 1)]],
}
POINTS = {1: 100, 2: 300, 3: 500, 4: 800}


class Game:
    def __init__(self, seq):
        self.board = [[None] * W for _ in range(H)]
        self.seq = list(seq)
        self.score = self.lines = 0
        self.state = "PLAYING"
        self.piece = None
        self.spawn()

    def fits(self, kind, rot, row, col):
        for r, c in SHAPES[kind][rot]:
            y, x = row + r, col + c
            if not (0 <= y < H and 0 <= x < W) or self.board[y][x] is not None:
                return False
        return True

    def spawn(self):
        if not self.seq:
            self.piece, self.state = None, "DONE"
            return
        kind = self.seq.pop(0)
        if not self.fits(kind, 0, 0, 3):
            self.piece, self.state = None, "OVER"
            return
        self.piece = [kind, 0, 0, 3]

    def lock(self):
        kind, rot, row, col = self.piece
        for r, c in SHAPES[kind][rot]:
            self.board[row + r][col + c] = kind
        full = [y for y in range(H) if all(self.board[y][x] is not None for x in range(W))]
        if full:
            kept = [self.board[y] for y in range(H) if y not in full]
            self.board = [[None] * W for _ in full] + kept
            self.score += POINTS[len(full)]
            self.lines += len(full)
        self.spawn()

    def command(self, tok):
        if self.piece is None:
            return
        kind, rot, row, col = self.piece
        if tok in ("L", "R"):
            dc = -1 if tok == "L" else 1
            if self.fits(kind, rot, row, col + dc):
                self.piece[3] = col + dc
        elif tok == "D":
            if self.fits(kind, rot, row + 1, col):
                self.piece[2] = row + 1
                self.score += 1
            else:
                self.lock()
        elif tok == "H":
            n = 0
            while self.fits(kind, rot, row + n + 1, col):
                n += 1
            self.piece[2] = row + n
            self.score += 2 * n
            self.lock()
        elif tok in ("CW", "CCW"):
            new = (rot + (1 if tok == "CW" else 3)) % 4
            for dc in (0, -1, 1, -2, 2):
                if self.fits(kind, new, row, col + dc):
                    self.piece[1], self.piece[3] = new, col + dc
                    break

    def render(self):
        cells = [[(v if v else ".") for v in line] for line in self.board]
        if self.piece is not None:
            kind, rot, row, col = self.piece
            for r, c in SHAPES[kind][rot]:
                cells[row + r][col + c] = kind.lower()
        out = ["".join(line) for line in cells]
        out.append(f"SCORE {self.score} LINES {self.lines} {self.state}")
        return "\n".join(out) + "\n"


def main():
    data = sys.stdin.read().split("\n", 1)
    seq = data[0].strip()
    rest = data[1] if len(data) > 1 else ""
    game = Game(seq)
    out = []
    for tok in rest.split():
        if tok == "P":
            out.append(game.render())
        elif tok in ("L", "R", "D", "H", "CW", "CCW"):
            game.command(tok)
        else:
            sys.stderr.write(f"error: unknown command {tok}\n")
    out.append(game.render())
    sys.stdout.write("".join(out))


if __name__ == "__main__":
    main()
