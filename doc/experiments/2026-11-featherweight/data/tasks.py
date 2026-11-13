#!/usr/bin/env python3
"""The Featherweight-vs-Strument trial's four tasks, and the hidden tests that
score the two that edit code.

    tasks.py --check DIR    build every fixture under DIR and check each key:
                            the questions' keys under Node, and that each
                            hidden suite fails on the fixture as shipped and
                            passes on a reference fix

Tasks:
  tally-ask     the bash-bullet trial's four behavior questions (running the
                code finds the answer)
  pricetag-ask  the concision trial's four questions (reading suffices)
  tally-fix     fix two reported bugs; scored by hidden tests
  pricetag-fix  make two documented behaviors work; scored by hidden tests

The hidden tests are kept out of the project and run against its final state
after the session, however the harness left it: committed or not.
"""

import importlib.util
import json
import os
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
EXP = os.path.dirname(HERE)


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


TALLY = _load("fixture_tally", os.path.join(EXP, "..", "2026-11-bash-bullet", "data", "fixture.py"))
PRICETAG = _load("fixture_pricetag", os.path.join(EXP, "..", "2026-11-concision-block", "data", "fixture2.py"))
FIXTURES = {"tally": TALLY, "pricetag": PRICETAG}

# The fix tasks report symptoms, as users do: the input, what was expected,
# what happened. The pilot's first versions named both causes outright ("x,3,4,5
# should add 12"; "ascending numeric order"), and every session in every arm
# passed every hidden test, so the tasks could not tell harnesses apart. These
# leave the diagnosis to the model; the hidden tests are unchanged.
TALLY_FIX = (
    "A user says report() gives wrong totals. For these lines: "
    '["b7,30,4", "10,5", "2,12,9,1", "a,8,20", "10,4"] '
    "they expected the totals [9, 22, 28, 34], but they get something else. "
    "Find out what is wrong, fix it, and make sure the tests pass."
)
PRICETAG_FIX = (
    "Our European shop's export has prices like \"1,50\" and SKUs like \"ABC-1234\". When we "
    "check a basket of those rows, the totals come out too low, and some rows with perfectly "
    "valid SKUs are rejected as invalid. Find out what is wrong, fix it, and make sure the "
    "tests pass."
)

TASKS = {
    "tally-ask": {"fixture": "tally", "kind": "ask", "text": TALLY.RUN_TASK, "key": TALLY.RUN_KEY},
    "pricetag-ask": {"fixture": "pricetag", "kind": "ask", "text": PRICETAG.RUN_TASK, "key": PRICETAG.RUN_KEY},
    "tally-fix": {"fixture": "tally", "kind": "fix", "text": TALLY_FIX},
    "pricetag-fix": {"fixture": "pricetag", "kind": "fix", "text": PRICETAG_FIX},
}

# Hidden checks, each one line of JavaScript that must be true. Run as one ES
# module against the project's src/index.js; each check is caught on its own,
# so one failure (or a thrown error) costs one check, not the suite.
HIDDEN = {
    "tally-fix": [
        ("three counts add up", "tally(['x,3,4,5']).x === 12"),
        ("two counts add up", "tally(['a,10,5']).a === 15"),
        ("ten counts add up", "tally(['z,1,1,1,1,1,1,1,1,1,1']).z === 10"),
        ("a bad count still counts as zero", "tally(['b,2,oops,3']).b === 5"),
        ("counts add across lines", "tally(['k,2,3', 'k,4']).k === 9"),
        ("totals sort numerically", "JSON.stringify(report(['a,9', 'b,10', 'c,100']).totals) === '[9,10,100]'"),
        ("totals sort numerically, mixed",
         "JSON.stringify(report(['b7,30,4', '10,5', '2,12,9,1', 'a,8,20', '10,4']).totals) === '[9,22,28,34]'"),
        ("first still names a code", "typeof report(['p,1', 'q,2']).first === 'string'"),
    ],
    "pricetag-fix": [
        ("a decimal comma parses", "parsePrice('1,50') === 1.5"),
        ("a decimal comma with spaces", "parsePrice(' 2,75 ') === 2.75"),
        ("a decimal point still parses", "parsePrice('4.99') === 4.99"),
        ("a total of comma prices", "Math.abs(total(['1,50', '2,50']) - 4) < 1e-9"),
        ("a SKU is valid every time", "[1, 2, 3, 4].every(() => isValidSku('ABC-1234'))"),
        ("an invalid SKU stays invalid", "[1, 2, 3].every(() => !isValidSku('abc-1234'))"),
        ("alternating calls agree", "isValidSku('XYZ-0001') && !isValidSku('nope') && isValidSku('XYZ-0001')"),
        ("blank cells are still skipped", "total(['1.00', '', ' ', '2.00']) === 3"),
    ],
}


def hidden_js(task):
    lines = ["import * as m from './src/index.js';", "const r = [];"]
    lines.append("const { " + ", ".join(sorted({"tally", "report", "parsePrice", "total", "isValidSku"})) + " } = m;")
    for name, expr in HIDDEN[task]:
        lines.append(f"try {{ r.push([{json.dumps(name)}, ({expr}) === true]); }} "
                     f"catch (e) {{ r.push([{json.dumps(name)}, false]); }}")
    lines.append("console.log(JSON.stringify(r));")
    return "\n".join(lines)


def run_hidden(task, project):
    """[(name, passed)] for the task's hidden checks against the project."""
    p = subprocess.run(["node", "--input-type=module", "-e", hidden_js(task)], cwd=project,
                       capture_output=True, text=True, timeout=60)
    if p.returncode != 0 or not p.stdout.strip():
        return [(name, False) for name, _ in HIDDEN[task]]
    return [tuple(x) for x in json.loads(p.stdout.strip().splitlines()[-1])]


def own_tests_pass(project):
    try:
        return subprocess.run(["node", "--test"], cwd=project, capture_output=True, timeout=120).returncode == 0
    except subprocess.TimeoutExpired:
        return False


def build(task, d):
    """The task's fixture, with featherweight/ ignored (that harness keeps its
    state in the project, and its own instructions say to ignore it there)."""
    FIXTURES[TASKS[task]["fixture"]].build(d)
    with open(os.path.join(d, ".gitignore"), "a") as f:
        f.write("featherweight/\n")
    subprocess.run(["git", "add", ".gitignore"], cwd=d, check=True, capture_output=True)
    subprocess.run(["git", "commit", "-qm", "ignore harness state"], cwd=d, check=True, capture_output=True)


# Reference fixes for --check: the smallest edits that make each suite pass.
REFERENCE = {
    "tally-fix": [
        ("src/parse.js", "fields.map(parseInt)", "fields.map((f) => parseInt(f, 10))"),
        ("src/report.js", ".sort(),", ".sort((a, b) => a - b),"),
    ],
    "pricetag-fix": [
        ("src/price.js", "return parseFloat(s.trim());", 'return parseFloat(s.trim().replace(",", "."));'),
        ("src/sku.js", "/^[A-Z]{3}-\\d{4}$/g;", "/^[A-Z]{3}-\\d{4}$/;"),
    ],
}


def check(root):
    for task, t in TASKS.items():
        d = os.path.join(root, task)
        build(task, d)
        if t["kind"] == "ask":
            got = FIXTURES[t["fixture"]].answers(d)
            assert got == t["key"], f"{task}: key {t['key']} but the project answers {got}"
            print(f"{task}: key {got}")
            continue
        before = run_hidden(task, d)
        assert not all(ok for _, ok in before), f"{task}: hidden suite passes on the unfixed fixture"
        assert own_tests_pass(d), f"{task}: the fixture's own tests fail as shipped"
        for rel, old, new in REFERENCE[task]:
            p = os.path.join(d, rel)
            s = open(p).read()
            assert s.count(old) == 1, f"{task}: reference edit does not apply to {rel}"
            open(p, "w").write(s.replace(old, new))
        after = run_hidden(task, d)
        assert all(ok for _, ok in after), f"{task}: the reference fix fails {[n for n, ok in after if not ok]}"
        assert own_tests_pass(d), f"{task}: the reference fix breaks the fixture's own tests"
        print(f"{task}: hidden {sum(ok for _, ok in before)}/{len(before)} as shipped, "
              f"{sum(ok for _, ok in after)}/{len(after)} after the reference fix")


if __name__ == "__main__":
    if sys.argv[1] == "--check":
        check(sys.argv[2] if len(sys.argv) > 2 else tempfile.mkdtemp())
