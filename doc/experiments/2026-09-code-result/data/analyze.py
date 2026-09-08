"""Did the phenomenon the arms are about ever occur?

A program loses results when it makes two or more bridged calls, prints
nothing, and does not end with a value that carries them. Counting call *sites*
in the source gets this wrong — a loop has one site and many calls, which is
precisely the field failure — so the calls are counted from the per-call
outcome lines the session prints between the program block and its summary.
"""

import json
import pathlib
import re
import sys

sys.path.insert(0, ".")
import run as R  # noqa: E402

NAMES = ("read(", "grep(", "glob(", "ls(", "symbol(")
OUTCOME = re.compile(
    r"^(Read |Searched for |Matched |Listed |Found |No matches|No files)", re.M
)


def blocks(text: str) -> list[tuple[str, int]]:
    """Each program with the number of bridged calls it actually made."""
    t = R.strip_ansi(text)
    out = []
    for m in re.finditer(r"‹run_code›\n(.*?)\n‹/›\n(.*?)^Ran ", t, re.S | re.M):
        src, between = m.group(1), m.group(2)
        out.append((src, len(OUTCOME.findall(between))))
    return out


BOUND = None


def lossy(src: str, calls: int) -> bool:
    """Two or more calls whose results can reach nobody.

    The rule is about *binding*, not about the last line. A program that never
    assigns, returns, or comprehends a call's result, and never prints one, has
    at most its final expression statement to hand back — so with two or more
    calls, at least one is gone. Keying on the tail missed the field's own
    shape, a loop whose body ends in `pass`.
    """
    if calls < 2 or "print(" in src:
        return False
    names = "|".join(n.rstrip("(") for n in NAMES)
    bound = (
        re.search(rf"=\s*[^=\n]*\b({names})\(", src)
        or re.search(rf"\breturn\b[^\n]*\b({names})\(", src)
        or re.search(rf"[\[{{][^\]}}]*\b({names})\([^\]}}]*\bfor\b", src, re.S)
    )
    return not bound


def selfcheck() -> None:
    """The check that could not fail is the one that finds nothing (§15)."""
    must = [
        ('for p in ["a.py","b.py"]:\n    read(path=p)', 2),
        ('for path in ["a","b"]:\n    try:\n        read(path=path)\n    except:\n        pass', 2),
        ('grep(pattern="x", glob="a")\ngrep(pattern="x", glob="b")', 2),
    ]
    must_not = [
        ('hits = {}\nfor p in ["a","b"]:\n    hits[p] = read(path=p)\nhits', 2),
        ('for p in ["a","b"]:\n    print(read(path=p))', 2),
        ('read(path="a.py")', 1),
        ('[read(path=p) for p in ["a","b"]]', 2),
        ('def main():\n    return [grep(pattern="x", glob=g) for g in ["a","b"]]', 2),
    ]
    for src, n in must:
        assert lossy(src, n), f"classifier misses a lossy program:\n{src}"
    for src, n in must_not:
        assert not lossy(src, n), f"classifier flags a sound program:\n{src}"
    print("classifier self-check: 3 lossy detected, 5 sound cleared")


def main() -> int:
    selfcheck()
    rows = [json.loads(line) for line in open("results.jsonl") if line.strip()]
    per_arm: dict[str, dict[str, int]] = {}
    for r in rows:
        p = pathlib.Path("runs") / f"{R.job_id(r)}.txt"
        if not p.exists():
            continue
        bs = blocks(p.read_text())
        n_lossy = sum(1 for src, calls in bs if lossy(src, calls))
        multi = sum(1 for _, calls in bs if calls >= 2)
        d = per_arm.setdefault(r["arm"], {k: 0 for k in
                                          ("runs", "progs", "multi", "lossy", "runs_lossy")})
        d["runs"] += 1
        d["progs"] += len(bs)
        d["multi"] += multi
        d["lossy"] += n_lossy
        d["runs_lossy"] += 1 if n_lossy else 0
    print(f"\n{'arm':5s} {'runs':>5s} {'programs':>9s} {'multi-call':>11s} "
          f"{'lossy':>6s} {'runs with one':>14s}")
    for arm in ("bare", "last", "all", "main"):
        d = per_arm.get(arm)
        if not d:
            continue
        print(f"{arm:5s} {d['runs']:5d} {d['progs']:9d} {d['multi']:11d} "
              f"{d['lossy']:6d} {d['runs_lossy']:14d}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
