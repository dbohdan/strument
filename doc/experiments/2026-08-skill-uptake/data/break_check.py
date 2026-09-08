#!/usr/bin/env python3
"""Break the reference one rule at a time and require exactly that rule to flip.

doc/experimenting.md 17: deleting an assertion proves it runs; breaking the
thing it guards proves it discriminates. And the free finding is in the OTHER
columns -- a rule that also flips when a different rule is broken is measuring
something wider than its name.
"""
import json, re, subprocess, sys

SP = "<this directory>"
IDEAL = f"{SP}/fixtures/revenue-ideal.html"
KEY = f"{SP}/fixtures/revenue.key.json"

def score(text):
    open("/tmp/_brk.html", "w").write(text)
    out = subprocess.run([sys.executable, f"{SP}/score.py", "/tmp/_brk.html", KEY],
                         capture_output=True, text=True)
    return json.loads(out.stdout)

BREAKS = {
    # Put one off-palette data colour back.
    "R1_palette": lambda t: t.replace('fill="#007DB7"', 'fill="#1F77B4"', 1),
    # Put the panel background and the plot border back.
    "R2_chartjunk": lambda t: t.replace(
        "<svg", '<svg', 1).replace(
        '<line class="grid"',
        '<rect x="0" y="0" width="720" height="400" fill="#EFEFEF"/>\n  <line class="grid"', 1),
    # The new third clause of rule 2, broken on its own: no background, no
    # border, just the gradient-and-shadow treatment one baseline run applied.
    # Keeps every palette fill intact -- the first version swapped one out for
    # the gradient, which also dropped R1 below its two-colour floor and made
    # the break look like a mis-scoped rule instead of an impure break.
    "R2_chartjunk#effects": lambda t: t.replace(
        '<line class="grid"',
        '<defs><filter id="s"><feDropShadow/></filter></defs>\n  <line class="grid"', 1
    ).replace('<rect data-cat=', '<rect filter="url(#s)" data-cat='),
    # Replace the unit-bearing label with the generic one.
    "R3_unit": lambda t: t.replace("Revenue (thousands of USD)", "Value"),
    # Put a legend back (and take the direct labels away).
    # Coordinate-free on purpose: the first version keyed on x="642.0", which
    # stopped matching when the reference's labels moved, so the "break" was a
    # no-op and R4 looked like it discriminated when nothing had changed.
    "R4_direct_labels": lambda t: re.sub(
        r'<text[^>]*font-weight="bold"[^>]*>([^<]*)</text>',
        r'<g class="legend"><rect/></g>', t),
    # Put the vertical gridlines back.
    "R5_gridlines": lambda t: t.replace(
        '</svg>',
        "".join(f'  <line class="grid" x1="{78 + 546 * i / 6:.1f}" y1="48" '
                f'x2="{78 + 546 * i / 6:.1f}" y2="344" stroke="#D6D6D6"/>\n'
                for i in range(7)) + "</svg>"),
}

def main():
    ideal = open(IDEAL).read()
    base = score(ideal)
    print("reference:", base["n_rules"], "/5", base["rules"])
    if base["n_rules"] != 5:
        print("ABORT: reference is not 5/5"); return 1
    names = [n for n, _ in __import__("importlib").import_module("score").RULES] \
        if False else list(base["rules"])
    bad = 0
    print(f"\n{'broke':<18}" + "".join(f"{n.split('_')[0]:>5}" for n in names) + "   verdict")
    for target, fn in BREAKS.items():
        r = score(fn(ideal))
        row = "".join(("  ok " if r["rules"][n] else " FAIL") for n in names)
        flipped = {n for n in names if not r["rules"][n]}
        ok = flipped == {target.split("#")[0]}
        if not ok:
            bad += 1
        print(f"{target:<18}{row}   {'exactly ' + target if ok else 'WRONG: flipped ' + str(sorted(flipped))}")
    print("\nVERDICT:", "every rule discriminates" if not bad else f"{bad} rule(s) mis-scoped")
    return 1 if bad else 0

if __name__ == "__main__":
    sys.exit(main())
