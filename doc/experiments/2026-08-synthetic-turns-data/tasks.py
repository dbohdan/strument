"""Task fixtures for the synthetic-turn screen.

Each task builds a small Go project, states a request, and can score the result
mechanically. Surface names are permuted per sample so the 600 runs are not 600
repetitions of one literal prompt.

Scoring is deterministic: a task passes when the file on disk contains exactly
what the request asked for. No model judges another model's output.
"""

import random
import re

# Surface vocabularies. Picking from these per sample varies the prompt's
# surface without varying its structure.
PKGS = ["calc", "mathx", "numkit", "arith", "tally"]
SUMS = ["Sum", "Total", "AddAll", "Accumulate"]
MAXS = ["Max", "Largest", "Peak", "Highest"]
FILES = ["calc.go", "numbers.go", "agg.go", "reduce.go"]
UTILS = ["util.go", "helpers.go", "support.go"]


def _names(rng):
    return {
        "pkg": rng.choice(PKGS),
        "sum": rng.choice(SUMS),
        "max": rng.choice(MAXS),
        "file": rng.choice(FILES),
        "util": rng.choice(UTILS),
    }


def _sum_src(n):
    return (
        f"package {n['pkg']}\n\n"
        f"// {n['sum']} adds the numbers.\n"
        f"func {n['sum']}(xs []int) int {{\n"
        f"\ttotal := 0\n"
        f"\tfor _, x := range xs {{\n"
        f"\t\ttotal += x\n"
        f"\t}}\n"
        f"\treturn total\n"
        f"}}\n"
    )


# --- tasks -----------------------------------------------------------------
#
# Each returns: files {path: content}, chat [paths to /add], prompt, and a
# scorer taking the final file map.


def task_chat_only(rng):
    """Everything needed is in the chat. No search required.

    This is where removing the assistant reply should matter most: the file
    block is the only thing telling the model what the code looks like.
    """
    n = _names(rng)
    files = {"go.mod": f"module {n['pkg']}\n\ngo 1.26\n", n["file"]: _sum_src(n)}
    prompt = (
        f"In {n['file']}, add a function {n['max']} that takes a []int and returns "
        f"the largest element. Match the style of {n['sum']}. Do not change {n['sum']}."
    )

    def score(out):
        src = out.get(n["file"], "")
        return (
            f"func {n['max']}(" in src
            and f"func {n['sum']}(" in src
            and "range" in src.split(f"func {n['max']}(")[-1]
        )

    return files, [n["file"]], prompt, score, n


def task_search_required(rng):
    """The target is in a file that is NOT in the chat.

    The model must glob/grep/read regardless, so the file block carries little.
    This is the control: removing the assistant reply should matter least here.
    """
    n = _names(rng)
    files = {
        "go.mod": f"module {n['pkg']}\n\ngo 1.26\n",
        n["file"]: _sum_src(n),
        n["util"]: (
            f"package {n['pkg']}\n\n"
            f"// Clamp bounds v to [lo, hi].\n"
            f"func Clamp(v, lo, hi int) int {{\n"
            f"\tif v < lo {{\n\t\treturn lo\n\t}}\n"
            f"\tif v > hi {{\n\t\treturn hi\n\t}}\n"
            f"\treturn v\n}}\n"
        ),
    }
    prompt = (
        f"Somewhere in this project there is a function called Clamp. Find it and add a "
        f"doc comment line directly above it that reads exactly: // Clamp is inclusive at both ends."
    )

    def score(out):
        src = out.get(n["util"], "")
        return "// Clamp is inclusive at both ends." in src and "func Clamp(" in src

    return files, [n["file"]], prompt, score, n


def task_mixed(rng):
    """One edit in a chat file, one in a file the model must find."""
    n = _names(rng)
    files = {
        "go.mod": f"module {n['pkg']}\n\ngo 1.26\n",
        n["file"]: _sum_src(n),
        n["util"]: (
            f"package {n['pkg']}\n\n"
            f"// Double returns v * 2.\n"
            f"func Double(v int) int {{\n\treturn v * 2\n}}\n"
        ),
    }
    prompt = (
        f"Two changes. First, in {n['file']}, add a function {n['max']} taking a []int and "
        f"returning the largest element. Second, find the function Double elsewhere in the "
        f"project and add a second function Triple beside it that returns v * 3."
    )

    def score(out):
        a = f"func {n['max']}(" in out.get(n["file"], "")
        b = "func Triple(" in out.get(n["util"], "")
        return a and b

    return files, [n["file"]], prompt, score, n


def task_cross_file(rng):
    """A change in one chat file breaks a caller in another chat file.

    Both files are in the chat, so both are in the file block. Tests whether the
    model reads the block as a coherent whole.
    """
    n = _names(rng)
    caller = "main_" + n["util"]
    files = {
        "go.mod": f"module {n['pkg']}\n\ngo 1.26\n",
        n["file"]: _sum_src(n),
        caller: (
            f"package {n['pkg']}\n\n"
            f"// Report sums xs and describes it.\n"
            f"func Report(xs []int) string {{\n"
            f"\tif {n['sum']}(xs) > 10 {{\n\t\treturn \"big\"\n\t}}\n"
            f"\treturn \"small\"\n}}\n"
        ),
    }
    prompt = (
        f"Rename the function {n['sum']} to {n['max']}Sum throughout the project. "
        f"Every call site must be updated so the package still compiles."
    )

    def score(out):
        a = out.get(n["file"], "")
        b = out.get(caller, "")
        new = f"{n['max']}Sum"
        return (
            f"func {new}(" in a
            and f"{new}(xs)" in b
            and f"func {n['sum']}(" not in a
            and f" {n['sum']}(xs)" not in b
        )

    return files, [n["file"], caller], prompt, score, n


TASKS = {
    "chat_only": task_chat_only,
    "search_required": task_search_required,
    "mixed": task_mixed,
    "cross_file": task_cross_file,
}


# --- hard tasks ------------------------------------------------------------
#
# Added after the pilot showed a 24/24 ceiling on the four above. These aim
# directly at the hypothesis: the removed message says "I'll treat this message
# as their current contents", so the discriminating task is one where the block
# contradicts what a model would otherwise assume. Trusting the block is then
# the difference between right and wrong, rather than a stylistic preference.
#
# Pilot data is NOT pooled with the final run: it informed this design, so
# reusing it would be selection on the outcome.


def task_contradicts_name(rng):
    """The block shows a function whose body contradicts its name.

    A model reading the block describes what it does; one pattern-matching on
    the name describes what it sounds like. Scored on which one it wrote.
    """
    n = _names(rng)
    files = {
        "go.mod": f"module {n['pkg']}\n\ngo 1.26\n",
        n["file"]: (
            f"package {n['pkg']}\n\n"
            f"func {n['sum']}(xs []int) int {{\n"
            f"\tresult := 1\n"
            f"\tfor _, x := range xs {{\n"
            f"\t\tresult *= x\n"
            f"\t}}\n"
            f"\treturn result\n"
            f"}}\n"
        ),
    }
    prompt = (
        f"Add a doc comment directly above {n['sum']} in {n['file']}, in the standard Go form "
        f"starting with the function's name, stating precisely what the function computes. "
        f"Do not change any code."
    )

    def score(out):
        src = out.get(n["file"], "")
        if "result *= x" not in src:
            return False  # the code was supposed to be left alone
        head = src.split(f"func {n['sum']}(")[0]
        comments = [l for l in head.splitlines() if l.strip().startswith("//")]
        if not comments:
            return False
        # Strip the function's own name first. The prompt requires the comment
        # to begin with it, so leaving it in makes a name like "Sum" count as a
        # claim that the function sums — which failed every correct answer that
        # happened to draw that name.
        text = re.sub(rf"\b{re.escape(n['sum'])}\b", " ", " ".join(comments)).lower()
        saw_product = any(w in text for w in ("product", "multipl"))
        saw_sum = any(w in text for w in ("sum", "adds", "addition", "total"))
        return saw_product and not saw_sum

    return files, [n["file"]], prompt, score, n


def task_unusual_signature(rng):
    """The block shows a familiar-looking helper with a reversed parameter order.

    Calling it correctly requires reading the block rather than the convention.
    """
    n = _names(rng)
    files = {
        "go.mod": f"module {n['pkg']}\n\ngo 1.26\n",
        n["file"]: (
            f"package {n['pkg']}\n\n"
            f"// Bound is called with the upper limit first.\n"
            f"func Bound(hi, lo, v int) int {{\n"
            f"\tif v < lo {{\n\t\treturn lo\n\t}}\n"
            f"\tif v > hi {{\n\t\treturn hi\n\t}}\n"
            f"\treturn v\n}}\n"
        ),
    }
    prompt = (
        f"In {n['file']}, add a function Percent(v int) int that uses the existing Bound "
        f"helper to clamp v to the range 0 through 100 inclusive, and returns the result."
    )

    def score(out):
        src = out.get(n["file"], "").replace(" ", "")
        # Correct per the block's signature; the conventional order is wrong.
        return "func Percent(" in out.get(n["file"], "") and "Bound(100,0,v)" in src

    return files, [n["file"]], prompt, score, n


def task_many_call_sites(rng):
    """A rename across four files with six call sites: more steps, more drift."""
    n = _names(rng)
    old, new = n["sum"], n["max"] + "Total"
    files = {"go.mod": f"module {n['pkg']}\n\ngo 1.26\n", n["file"]: _sum_src(n)}
    callers = []
    for i in range(3):
        p = f"use{i}.go"
        callers.append(p)
        files[p] = (
            f"package {n['pkg']}\n\n"
            f"func Use{i}(xs []int) int {{\n"
            f"\ta := {old}(xs)\n"
            f"\tb := {old}(xs[1:])\n"
            f"\treturn a + b\n}}\n"
        )
    prompt = (
        f"Rename {old} to {new} everywhere in this project. Every call site must be updated "
        f"so the package still compiles. Do not change any other behavior."
    )

    def score(out):
        if f"func {new}(" not in out.get(n["file"], ""):
            return False
        if re.search(rf"\b{re.escape(old)}\(", out.get(n["file"], "")):
            return False
        for p in callers:
            src = out.get(p, "")
            if len(re.findall(rf"\b{re.escape(new)}\(", src)) != 2:
                return False
            if re.search(rf"\b{re.escape(old)}\(", src):
                return False
        return True

    return files, [n["file"], *callers], prompt, score, n


TASKS.update({
    "contradicts_name": task_contradicts_name,
    "unusual_signature": task_unusual_signature,
    "many_call_sites": task_many_call_sites,
})
