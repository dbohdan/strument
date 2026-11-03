#!/usr/bin/env python3
"""Build the bash-bullet trial's fixture: tallyline, a small invented ES module
library, as a git repository at the given path.

    fixture.py DIR          build it
    fixture.py --check DIR  build it, then compute the run task's key under Node,
                            and assert that each trap changes it

The run task asks four yes/no questions about how the code behaves. Three
have a JavaScript behavior behind them: map(parseInt) passing the index as
the radix, integer-like object keys enumerating first, and the default sort
comparing as strings. The tests pass and cover none of them, as tests of code
like this often don't.
"""

import json
import os
import subprocess
import sys

RUN_LINES = ["b7,30,4", "10,5", "2,12,9,1", "a,8,20", "10,4"]
READ_KEY = ("src/combine.js", "combine")

# The run task asks how the code behaves, not what it returns for an input: a
# question that names an input asks to be run, and the pilot's first version
# (what report() returns for RUN_LINES) was run by every session in both arms.
# The comparison this trial follows from asked "does the proxy track nested
# objects?", where running is a choice. Each question has a planted behavior
# behind it, except the fourth, so a skeptic answering "no" throughout fails.
RUN_QUESTIONS = [
    "Does `tally()` add every count on a line into that code's total?",
    "Does `report()` give the totals in ascending numeric order?",
    "Does `report()` list the codes in the order they first appear in the input?",
    "Does a code that appears on two lines get one combined total?",
]
RUN_TASK = (
    "Some questions about how this project behaves. Answer each yes or no, with a sentence on why.\n"
    + "".join(f"{i + 1}. {q}\n" for i, q in enumerate(RUN_QUESTIONS))
    + "Finish with one line, `RESULT: ` followed by the four answers, yes or no, "
    "comma-separated and in order."
)
READ_TASK = (
    "Which function in this project decides how the counts on one line are combined "
    "into a single number, and what does the design note say should happen to a count "
    "that is not a number? Finish with one line, `RESULT: <file>:<function>`, with the "
    "file's path from the project root."
)

# The answers, computed under Node against the built project rather than
# written down: each is the behavior itself, checked on an input that shows it.
ANSWER_JS = """
import { tally, report } from './src/index.js';
const every = tally(['x,3,4,5']).x === 12;
const r = report(['b7,30,4', '10,5', '2,12,9,1', 'a,8,20', '10,4']);
const ascending = r.totals.every((v, i, a) => i === 0 || a[i - 1] <= v);
const appearance = JSON.stringify(r.codes) === JSON.stringify(['b7', '10', '2', 'a']);
const combined = tally(['x,1', 'x,2']).x === 3;
console.log([every, ascending, appearance, combined].map((b) => (b ? 'yes' : 'no')).join(','));
"""
RUN_KEY = "no,no,no,yes"

FILES = {
    "package.json": json.dumps({
        "name": "tallyline",
        "version": "0.4.1",
        "description": "Tally coded count lines into a small report.",
        "type": "module",
        "main": "src/index.js",
        "scripts": {"test": "node --test"},
        "license": "MIT",
    }, indent=2) + "\n",
    "README.md": """\
# tallyline

Tally coded count lines into a small report.

A line is a code followed by one or more counts, comma-separated:

```
b7,30,4
10,5
```

The same code may appear on several lines; its counts add up.

```js
import { report } from "tallyline";

report(["b7,30,4", "10,5"]);
// → { codes: [...], totals: [...], first: "..." }
```

`report` returns the codes in the order they are kept, their totals sorted,
and the first code. `tally` returns the totals by code. See
[`docs/design.md`](docs/design.md) for why counts are lenient.

Run the tests with `npm test`.
""",
    "docs/design.md": """\
# Design notes

## Lenient counts

The lines come from hand-kept sheets, and a stray mark in a count field is
common. A count that is not a number is treated as zero rather than failing
the whole line: one smudge should not drop a code's other counts. `combine`
is where that rule lives, so the parsing stays dumb and the policy stays in
one place.

## Codes are strings

Codes look numeric often enough ("10", "2") that it is tempting to parse
them. They are labels, though, and "07" and "7" are different codes, so they
stay strings throughout.

## Totals are reported sorted

A report is read for its spread, not its order, so `report` gives the totals
sorted rather than in code order. The codes are given separately.
""",
    "src/index.js": """\
export { parseLine, parseCounts } from "./parse.js";
export { combine } from "./combine.js";
export { tally } from "./tally.js";
export { report } from "./report.js";
""",
    "src/parse.js": """\
// Parsing is deliberately dumb: split, convert, and leave policy to combine().

/** Split a line into its code and its count fields. */
export function parseLine(line) {
  const [code, ...fields] = line.trim().split(",");
  return { code, fields };
}

/** Convert count fields to integers. A field that is not a number comes back NaN. */
export function parseCounts(fields) {
  return fields.map(parseInt);
}
""",
    "src/combine.js": """\
/**
 * Combine one line's counts into a single number. A count that is not a
 * number counts as zero (docs/design.md, "Lenient counts").
 */
export function combine(counts) {
  return counts.reduce((sum, n) => sum + (Number.isNaN(n) ? 0 : n), 0);
}
""",
    "src/tally.js": """\
import { parseLine, parseCounts } from "./parse.js";
import { combine } from "./combine.js";

/** Total each code's counts across all lines. */
export function tally(lines) {
  const totals = {};
  for (const line of lines) {
    const { code, fields } = parseLine(line);
    totals[code] = (totals[code] ?? 0) + combine(parseCounts(fields));
  }
  return totals;
}
""",
    "src/report.js": """\
import { tally } from "./tally.js";

/** The codes as kept, their totals sorted, and the first code. */
export function report(lines) {
  const totals = tally(lines);
  const codes = Object.keys(totals);
  return {
    codes,
    totals: codes.map((code) => totals[code]).sort(),
    first: codes[0],
  };
}
""",
    "test/tally.test.js": """\
import { test } from "node:test";
import assert from "node:assert/strict";
import { tally, combine, parseLine } from "../src/index.js";

test("parseLine splits the code from its fields", () => {
  assert.deepEqual(parseLine("x1,3,4"), { code: "x1", fields: ["3", "4"] });
});

test("combine treats a non-number as zero", () => {
  assert.equal(combine([3, NaN, 4]), 7);
});

test("tally adds a code's counts across lines", () => {
  assert.deepEqual(tally(["x,3", "y,2", "x,4"]), { x: 7, y: 2 });
});
""",
    "test/report.test.js": """\
import { test } from "node:test";
import assert from "node:assert/strict";
import { report } from "../src/index.js";

test("report gives codes, sorted totals and the first code", () => {
  assert.deepEqual(report(["p,5", "q,3"]), { codes: ["p", "q"], totals: [3, 5], first: "p" });
});
""",
}

# Each trap, neutralized: (file, old, new). With any one applied, the key must
# change, or that trap is not doing anything (handbook §26).
NEUTRALIZED = {
    "parseInt radix": ("src/parse.js", "fields.map(parseInt)", "fields.map((f) => parseInt(f, 10))"),
    "string sort": ("src/report.js", ".sort(),", ".sort((a, b) => a - b),"),
}
# Key order cannot be undone at the read site, so the tally keeps a Map, and
# the object it returns enumerates in the Map's insertion order.
NEUTRALIZED["integer-key order"] = (
    "src/tally.js",
    "  const totals = {};\n  for (const line of lines) {\n    const { code, fields } = parseLine(line);\n"
    "    totals[code] = (totals[code] ?? 0) + combine(parseCounts(fields));\n  }\n  return totals;",
    "  const m = new Map();\n  for (const line of lines) {\n    const { code, fields } = parseLine(line);\n"
    "    m.set(code, (m.get(code) ?? 0) + combine(parseCounts(fields)));\n  }\n"
    "  return new Proxy(Object.fromEntries(m), { ownKeys: () => [...m.keys()] });",
)


def build(d, files=FILES):
    os.makedirs(d)
    for rel, text in files.items():
        p = os.path.join(d, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w") as f:
            f.write(text)
    for cmd in (["git", "init", "-q", "-b", "main"], ["git", "config", "user.email", "t@t"],
                ["git", "config", "user.name", "t"], ["git", "add", "."], ["git", "commit", "-qm", "tallyline 0.4.1"]):
        subprocess.run(cmd, cwd=d, check=True, capture_output=True)


def answers(d):
    return subprocess.run(["node", "--input-type=module", "-e", ANSWER_JS], cwd=d, check=True,
                          capture_output=True, text=True).stdout.strip()


def check(d):
    got = answers(d)
    assert got == RUN_KEY, f"the key is wrong: the project answers {got}"
    subprocess.run(["node", "--test"], cwd=d, check=True, capture_output=True)
    for name, (rel, old, new) in NEUTRALIZED.items():
        files = dict(FILES)
        assert files[rel].count(old) == 1, f"{name}: the neutralizing edit does not apply"
        files[rel] = files[rel].replace(old, new)
        nd = d + "-" + name.replace(" ", "-")
        build(nd, files)
        changed = answers(nd)
        assert changed != RUN_KEY, f"{name}: neutralizing it leaves the key unchanged"
        print(f"{name}: neutralized gives {changed}")
    print("key:", got)


if __name__ == "__main__":
    if sys.argv[1] == "--check":
        build(sys.argv[2])
        check(sys.argv[2])
    else:
        build(sys.argv[1])
