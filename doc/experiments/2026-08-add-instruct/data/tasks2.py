"""Tasks for the A0-vs-A2 run.

Every task returns a `controls()` closure giving (correct, wrong) file maps for
its own name draw. `check_controls()` rebuilds each task once per point in the
full cross product of the name vocabularies and asserts the scorer says True for
the correct answer and False for the wrong one.

That harness exists because the previous screen's entire primary result was two
substring bugs, each falsely failing 100% of the samples that drew one
particular name, and nothing in the aggregate looked wrong. Controls would have
caught both in seconds.
"""

import itertools
import re

PKGS = ["calc", "mathx", "numkit", "arith", "tally"]
SUMS = ["Sum", "Total", "AddAll", "Accumulate"]
MAXS = ["Max", "Largest", "Peak", "Highest"]
FILES = ["calc.go", "numbers.go", "agg.go", "reduce.go"]
UTILS = ["util.go", "helpers.go", "support.go"]
VOCAB = {"pkg": PKGS, "sum": SUMS, "max": MAXS, "file": FILES, "util": UTILS}

EXTRAS = ["alpha", "beta", "gamma", "delta"]


def _names(rng):
    return {k: rng.choice(v) for k, v in VOCAB.items()}


def _sum_src(n):
    return (
        f"package {n['pkg']}\n\n// {n['sum']} adds the numbers.\n"
        f"func {n['sum']}(xs []int) int {{\n\ttotal := 0\n"
        f"\tfor _, x := range xs {{\n\t\ttotal += x\n\t}}\n\treturn total\n}}\n"
    )


def _product_src(n):
    return (
        f"package {n['pkg']}\n\nfunc {n['sum']}(xs []int) int {{\n\tresult := 1\n"
        f"\tfor _, x := range xs {{\n\t\tresult *= x\n\t}}\n\treturn result\n}}\n"
    )


def task_many_pinned(rng):
    """Six files pinned, two relevant — the task this run exists for.

    Under A0 all six contents sit in the prompt. Under A2 the model must decide
    what to read, so the extra round trip either stays at one step or becomes six.
    """
    n = _names(rng)
    target = "z_" + n["util"]
    files = {
        "go.mod": f"module {n['pkg']}\n\ngo 1.26\n",
        n["file"]: _sum_src(n),
        target: (f"package {n['pkg']}\n\n// Scale multiplies v by f.\n"
                 f"func Scale(v, f int) int {{\n\treturn v * f\n}}\n"),
    }
    for e in EXTRAS:
        files[f"{e}.go"] = (f"package {n['pkg']}\n\n// {e.title()}Const is unrelated.\n"
                            f"const {e.title()}Const = {len(e)}\n")
    pinned = [n["file"], target] + [f"{e}.go" for e in EXTRAS]
    prompt = (
        f"Two changes, both in files already pinned. In {n['file']}, add "
        f"func {n['max']}(xs []int) int returning the largest element. In {target}, add "
        f"func Halve(v int) int returning v / 2. Change nothing else."
    )

    def score(out):
        if not re.search(rf"func {re.escape(n['max'])}\(", out.get(n["file"], "")):
            return False
        if "func Halve(" not in out.get(target, ""):
            return False
        return all(f"{e.title()}Const" in out.get(f"{e}.go", "") for e in EXTRAS)

    def controls():
        good = dict(files)
        good[n["file"]] += f"\nfunc {n['max']}(xs []int) int {{ return 0 }}\n"
        good[target] += "\nfunc Halve(v int) int { return v / 2 }\n"
        return good, dict(files)

    return files, pinned, prompt, score, n, controls


def task_double_edit(rng):
    """Two edits to one pinned file in one turn — the staleness probe.

    Under A2 the second edit works from a read that is now stale. The claim that
    edit's exact-match requirement turns that into a failed edit rather than a
    wrong one is an argument until this runs.
    """
    n = _names(rng)
    new = f"{n['max']}Total"
    files = {"go.mod": f"module {n['pkg']}\n\ngo 1.26\n", n["file"]: _sum_src(n)}
    prompt = (
        f"Make two separate changes to {n['file']}, one after the other. First, rename "
        f"{n['sum']} to {new}. Then add a doc comment above it reading exactly: "
        f"// {new} is the sum of xs."
    )

    def score(out):
        src = out.get(n["file"], "")
        return (
            re.search(rf"func {re.escape(new)}\(", src) is not None
            and f"// {new} is the sum of xs." in src
            and re.search(rf"\b{re.escape(n['sum'])}\(", src) is None
        )

    def controls():
        good = {"go.mod": files["go.mod"],
                n["file"]: (f"package {n['pkg']}\n\n// {new} is the sum of xs.\n"
                            f"func {new}(xs []int) int {{\n\treturn 0\n}}\n")}
        return good, dict(files)

    return files, [n["file"]], prompt, score, n, controls


def task_cross_file(rng):
    """A rename whose call site lives in a second pinned file."""
    n = _names(rng)
    caller = "main_" + n["util"]
    new = f"{n['max']}Sum"
    files = {
        "go.mod": f"module {n['pkg']}\n\ngo 1.26\n",
        n["file"]: _sum_src(n),
        caller: (f"package {n['pkg']}\n\n// Report describes xs.\n"
                 f"func Report(xs []int) string {{\n\tif {n['sum']}(xs) > 10 {{\n"
                 f"\t\treturn \"big\"\n\t}}\n\treturn \"small\"\n}}\n"),
    }
    prompt = (
        f"Rename the function {n['sum']} to {new} throughout the project. Every call "
        f"site must be updated so the package still compiles."
    )

    def score(out):
        a, b = out.get(n["file"], ""), out.get(caller, "")
        return (
            re.search(rf"func {re.escape(new)}\(", a) is not None
            and re.search(rf"\b{re.escape(new)}\(xs\)", b) is not None
            and re.search(rf"\b{re.escape(n['sum'])}\(", a) is None
            and re.search(rf"\b{re.escape(n['sum'])}\(", b) is None
        )

    def controls():
        good = {n["file"]: _sum_src(n).replace(n["sum"], new),
                caller: f"func Report(xs []int) string {{ if {new}(xs) > 10 {{ }} }}\n"}
        return good, dict(files)

    return files, [n["file"], caller], prompt, score, n, controls


def task_contradicts_name(rng):
    """The block's body contradicts its name; describing it needs the block."""
    n = _names(rng)
    files = {"go.mod": f"module {n['pkg']}\n\ngo 1.26\n", n["file"]: _product_src(n)}
    prompt = (
        f"Add a doc comment directly above {n['sum']} in {n['file']}, in the standard Go "
        f"form starting with the function's name, stating precisely what the function "
        f"computes. Do not change any code."
    )

    def score(out):
        src = out.get(n["file"], "")
        if "result *= x" not in src:
            return False
        head = src.split(f"func {n['sum']}(")[0]
        comments = [l for l in head.splitlines() if l.strip().startswith("//")]
        if not comments:
            return False
        # Strip the function's own name before judging. The prompt requires the
        # comment to begin with it, so leaving it in makes a name like "Sum" read
        # as a claim that the function sums. That was the bug.
        text = re.sub(rf"\b{re.escape(n['sum'])}\b", " ", " ".join(comments)).lower()
        return (any(w in text for w in ("product", "multipl"))
                and not any(w in text for w in ("sum", "adds", "addition", "total")))

    def controls():
        good = {n["file"]: (f"package {n['pkg']}\n\n// {n['sum']} returns the product "
                            f"of the elements in xs.\n" + _product_src(n).split("\n\n", 1)[1])}
        bad = {n["file"]: (f"package {n['pkg']}\n\n// {n['sum']} returns the sum "
                           f"of the elements in xs.\n" + _product_src(n).split("\n\n", 1)[1])}
        return good, bad

    return files, [n["file"]], prompt, score, n, controls


TASKS = {
    "many_pinned": task_many_pinned,
    "double_edit": task_double_edit,
    "cross_file": task_cross_file,
    "contradicts_name": task_contradicts_name,
}


class _FixedRng:
    """Drives _names to a chosen point instead of a random one."""

    def __init__(self, draw):
        self._draw = draw

    def choice(self, seq):
        for key, vocab in VOCAB.items():
            if seq is vocab:
                return self._draw[key]
        return seq[0]


def check_controls():
    """Every task at every point of the vocabulary cross product."""
    failures = []
    keys = list(VOCAB)
    for tname, fn in TASKS.items():
        for combo in itertools.product(*(VOCAB[k] for k in keys)):
            draw = dict(zip(keys, combo))
            _, _, _, score, drawn, controls = fn(_FixedRng(draw))
            assert drawn == draw, (drawn, draw)
            good, bad = controls()
            if not score(good):
                failures.append((tname, draw, "correct answer scored FALSE"))
            if score(bad):
                failures.append((tname, draw, "wrong answer scored TRUE"))
    return failures


if __name__ == "__main__":
    fails = check_controls()
    n = len(TASKS) * len(PKGS) * len(SUMS) * len(MAXS) * len(FILES) * len(UTILS)
    print(f"{n} control checks across {len(TASKS)} tasks")
    for f in fails[:15]:
        print("  FAIL", f)
    print("FAILURES:", len(fails))
