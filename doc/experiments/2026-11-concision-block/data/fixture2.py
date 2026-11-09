#!/usr/bin/env python3
"""Build the concision trial's second fixture: pricetag, a small invented ES
module library for price strings and SKUs, as a git repository.

    fixture2.py DIR          build it
    fixture2.py --check DIR  build it, compute the run task's key under Node,
                             and assert that each planted behavior changes it

The first fixture (../../2026-11-bash-bullet/data/fixture.py) rests on three
JavaScript behaviors; this one uses three others, so a result cannot hinge on
one trap. The run task asks four yes/no questions; behind three of them:
parseFloat stopping at a decimal comma, a module-level /g regex whose
lastIndex makes test() alternate, and floating-point sums compared exactly.
The README says commas are accepted, the kind of drift between docs and code
that real projects have. The tests pass and cover none of the three.
"""

import json
import os
import subprocess
import sys

READ_KEY = ("src/sku.js", "isValidSku")

RUN_QUESTIONS = [
    "Does `total()` skip blank price strings instead of failing on them?",
    "Does `withinBudget([\"0.10\", \"0.20\"], \"0.30\")` return true?",
    "Does `parsePrice(\"1,50\")` return 1.5?",
    "Does `isValidSku()` always give the same answer for the same input?",
]
RUN_TASK = (
    "Some questions about how this project behaves. Answer each yes or no, with a sentence on why.\n"
    + "".join(f"{i + 1}. {q}\n" for i, q in enumerate(RUN_QUESTIONS))
    + "Finish with one line, `RESULT: ` followed by the four answers, yes or no, "
    "comma-separated and in order."
)
READ_TASK = (
    "Which function in this project decides whether a SKU is valid, and what does the README "
    "say a valid SKU looks like? Finish with one line, `RESULT: <file>:<function>`, with the "
    "file's path from the project root."
)

ANSWER_JS = """
import { total, withinBudget, parsePrice, isValidSku } from './src/index.js';
const blanks = total(['1.00', '', ' ', '2.00']) === 3;
const budget = withinBudget(['0.10', '0.20'], '0.30') === true;
const comma = parsePrice('1,50') === 1.5;
const a = isValidSku('ABC-1234'), b = isValidSku('ABC-1234'), c = isValidSku('ABC-1234');
const stable = a === b && b === c;
console.log([blanks, budget, comma, stable].map((x) => (x ? 'yes' : 'no')).join(','));
"""
RUN_KEY = "yes,no,no,no"

FILES = {
    "package.json": json.dumps({
        "name": "pricetag",
        "version": "1.3.0",
        "description": "Parse price strings, total them, and check SKUs.",
        "type": "module",
        "main": "src/index.js",
        "scripts": {"test": "node --test"},
        "license": "MIT",
    }, indent=2) + "\n",
    "README.md": """\
# pricetag

Parse price strings from shop exports, total them, check a basket against a
budget, and validate SKUs.

```js
import { total, withinBudget, isValidSku } from "pricetag";

total(["4.99", "12.50"]);          // 17.49
withinBudget(["4.99"], "5.00");    // true
isValidSku("ABC-1234");            // true
```

Prices are strings as they come out of the export: they may have surrounding
spaces, and they may use either a dot or a comma as the decimal separator.
Blank cells are skipped.

A valid SKU is three capital letters, a hyphen, and four digits, for example
`ABC-1234`.

Run the tests with `npm test`.
""",
    "docs/notes.md": """\
# Notes

## Why strings

The exports come from three different shop systems, and none of them agree
on number formatting. Keeping prices as strings until the last moment means
one parser, `parsePrice`, decides what a price is.

## Budgets

`withinBudget` is used at checkout to warn when a basket goes over a limit
the customer set. Being over by a cent should warn; being exactly at the
limit should not.

## SKUs

The SKU pattern is compiled once at module load rather than on every call,
because validation runs on every row of an export and exports can be large.
""",
    "src/index.js": """\
export { parsePrice, total } from "./price.js";
export { withinBudget } from "./budget.js";
export { isValidSku } from "./sku.js";
""",
    "src/price.js": """\
/** Parse one price string into a number. Surrounding spaces are ignored. */
export function parsePrice(s) {
  return parseFloat(s.trim());
}

/** Total a list of price strings. Blank cells are skipped. */
export function total(prices) {
  return prices
    .filter((p) => p.trim() !== "")
    .map(parsePrice)
    .reduce((sum, n) => sum + n, 0);
}
""",
    "src/budget.js": """\
import { total, parsePrice } from "./price.js";

/** True when the prices add up to no more than the budget. */
export function withinBudget(prices, budget) {
  return total(prices) <= parsePrice(budget);
}
""",
    "src/sku.js": """\
// Compiled once at module load; see docs/notes.md, "SKUs".
const SKU_PATTERN = /^[A-Z]{3}-\\d{4}$/g;

/** True when s is a valid SKU: three capital letters, a hyphen, four digits. */
export function isValidSku(s) {
  return SKU_PATTERN.test(s);
}
""",
    "test/price.test.js": """\
import { test } from "node:test";
import assert from "node:assert/strict";
import { parsePrice, total, withinBudget } from "../src/index.js";

test("parsePrice ignores surrounding spaces", () => {
  assert.equal(parsePrice("  4.50 "), 4.5);
});

test("total skips blank cells", () => {
  assert.equal(total(["2.00", "", "3.00"]), 5);
});

test("withinBudget warns when over", () => {
  assert.equal(withinBudget(["4.00", "2.00"], "5.00"), false);
});
""",
    "test/sku.test.js": """\
import { test } from "node:test";
import assert from "node:assert/strict";
import { isValidSku } from "../src/index.js";

test("a well-formed SKU is valid", () => {
  assert.equal(isValidSku("XYZ-0001"), true);
});

test("lowercase letters are not", () => {
  assert.equal(isValidSku("xyz-0001"), false);
});
""",
}

# Each planted behavior, neutralized; each must change the key.
NEUTRALIZED = {
    "blank filter (the control)": ("src/price.js", '    .filter((p) => p.trim() !== "")\n', ""),
    "float comparison": ("src/budget.js", "return total(prices) <= parsePrice(budget);",
                         "return Math.round(total(prices) * 100) <= Math.round(parsePrice(budget) * 100);"),
    "decimal comma": ("src/price.js", "return parseFloat(s.trim());",
                      'return parseFloat(s.trim().replace(",", "."));'),
    "global regex": ("src/sku.js", "/^[A-Z]{3}-\\d{4}$/g;", "/^[A-Z]{3}-\\d{4}$/;"),
}


def build(d, files=FILES):
    os.makedirs(d)
    for rel, text in files.items():
        p = os.path.join(d, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w") as f:
            f.write(text)
    for cmd in (["git", "init", "-q", "-b", "main"], ["git", "config", "user.email", "t@t"],
                ["git", "config", "user.name", "t"], ["git", "add", "."], ["git", "commit", "-qm", "pricetag 1.3.0"]):
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
        nd = d + "-" + name.split()[0]
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
