"""Fixture and expected answers for the run_code ergonomics trial.

Answers are computed from the bytes, never written by hand.

Sized so the mechanism is reached. The first fixture was a twelve-line file,
and five of eight pilot runs answered it by eye without writing a program at
all — the handbook's "a feature that can be declined was offered, not applied".
These files are long enough that counting by hand is not the cheaper option.

No file ends with a blank line: "how many lines are empty" is ambiguous when it
does, and an ambiguous task measures the reader rather than the arm.
"""
import json, pathlib, random, sys

WORDS = ("widget gadget module handler buffer parser writer reader socket "
         "counter adapter printer scanner emitter matcher walker sender").split()

def notes(rng, n=420):
    """A long changelog-ish file with blank lines, three FIXMEs, and one very
    long line. Deterministic for a given seed."""
    out = ["# Change log", ""]
    fixmes = 0
    for i in range(n):
        r = rng.random()
        if r < 0.18:
            out.append("")
        elif r < 0.24 and fixmes < 3:
            fixmes += 1
            out.append(f"- FIXME({fixmes}): {rng.choice(WORDS)} still leaks on the {rng.choice(WORDS)} path")
        else:
            words = [rng.choice(WORDS) for _ in range(rng.randint(3, 12))]
            out.append(f"- {i:03d} {' '.join(words)}")
    while fixmes < 3:
        fixmes += 1
        out.append(f"- FIXME({fixmes}): {rng.choice(WORDS)} leaks on the {rng.choice(WORDS)} path")
    # One line nobody will find by eye.
    out.append("- " + " ".join(rng.choice(WORDS) for _ in range(40)))
    while out[-1] == "":
        out.pop()
    return out

def rows(rng, n, width):
    return [f"{i:04d},{''.join(rng.choice('abcdefghij') for _ in range(width))}" for i in range(n)]

def build(root, seed=7):
    rng = random.Random(seed)
    root = pathlib.Path(root)
    (root / "data").mkdir(parents=True, exist_ok=True)
    nt = notes(rng)
    a = rows(rng, 137, 9)
    b = rows(rng, 91, 13)
    (root / "notes.md").write_text("\n".join(nt) + "\n")
    (root / "data" / "a.txt").write_text("\n".join(a) + "\n")
    (root / "data" / "b.txt").write_text("\n".join(b) + "\n")
    return {
        "blank": sum(1 for l in nt if l == ""),
        "longest": max(len(l) for l in nt),
        "total": sum(len(l) for l in a) + sum(len(l) for l in b),
        "cite": next(i + 1 for i, l in enumerate(nt) if "FIXME(3)" in l),
    }

TASKS = {
    "blank": "How many lines in notes.md are completely empty? Reply with only the number.",
    "longest": "What is the character length of the longest line in notes.md, not counting the newline? Reply with only the number.",
    "total": "What is the total number of characters across all lines of data/a.txt and data/b.txt, not counting newlines? Reply with only the number.",
    "cite": "On which line number of notes.md does FIXME(3) appear? Reply with only the number.",
}

if __name__ == "__main__":
    print(json.dumps(build(sys.argv[1]), indent=2))
