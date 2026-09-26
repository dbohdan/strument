"""Build the fixture: a year of Larkspur's harvest logs, and the answer key.

Larkspur is the invented community garden of 2026-09-tool-disclosure. Here it
has twelve monthly JSON files of harvest records and a list of plot holders.
Each task needs counting, grouping or joining across files, which is where a
hand-rolled reduce appears; one control needs a single lookup.

The key is computed from the generated data, and asserted unambiguous (no
ties), so a correct answer is exactly one answer.

Usage: python3 fixture.py DEST        writes the tree and DEST/../key.json
"""

import collections
import json
import os
import random
import sys

SEED = 20260926
FAMILIES = {
    "brassicas": ["kale", "cabbage", "broccoli", "kohlrabi", "cauliflower"],
    "legumes": ["runner bean", "pea", "broad bean", "french bean"],
    "roots": ["carrot", "beetroot", "parsnip", "radish", "turnip"],
    "alliums": ["onion", "leek", "garlic", "shallot"],
    "nightshades": ["tomato", "pepper", "potato", "aubergine"],
    "cucurbits": ["courgette", "cucumber", "squash", "pumpkin", "melon"],
}
HOLDERS = ["Ada Birch", "Bram Oakes", "Cleo Marsh", "Dev Rowan", "Esme Thorn", "Finn Hale",
           "Gwen Ashby", "Hugo Fenn", "Iris Lark", "Jude Moss", "Kit Farrow", "Lena Brook",
           "Milo Heath", "Nia Stone", "Otto Reed", "Pia Wren", "Quinn Dale", "Rosa Vale",
           "Sam Holt", "Tess Crane"]
BEDS = [f"B{i:02d}" for i in range(1, 17)]


def build(rng):
    holders_with_beds = rng.sample(HOLDERS, 16)
    plots = [{"holder": h, "bed": b} for h, b in zip(holders_with_beds, BEDS)]
    # Four holders are on the waiting list: in plots.json with no bed.
    plots += [{"holder": h, "bed": None} for h in HOLDERS if h not in holders_with_beds]
    rng.shuffle(plots)
    records, n = [], 0
    for month in range(1, 13):
        for _ in range(rng.randint(14, 26)):
            n += 1
            family = rng.choice(sorted(FAMILIES))
            records.append({
                "id": f"H-{n:04d}",
                "date": f"2026-{month:02d}-{rng.randint(1, 28):02d}",
                "bed": rng.choice(BEDS),
                "family": family,
                "variety": rng.choice(FAMILIES[family]),
                "grams": rng.randint(50, 4000),
            })
    return plots, records


def key(plots, records):
    per_family = collections.Counter(r["family"] for r in records)
    heaviest = {}
    for bed in BEDS:
        rs = sorted((r for r in records if r["bed"] == bed), key=lambda r: -r["grams"])
        if len(rs) > 1 and rs[0]["grams"] == rs[1]["grams"]:
            return None  # a tie: two correct answers
        heaviest[bed] = rs[0]["id"]
    harvested_beds = {r["bed"] for r in records}
    idle = sorted(p["holder"] for p in plots if p["bed"] is None or p["bed"] not in harvested_beds)
    return {
        "count": dict(sorted(per_family.items())),
        "groupmax": heaviest,
        "join": idle,
        "distinct": len({r["variety"] for r in records}),
        "control": next(r["grams"] for r in records if r["id"] == "H-0042"),
    }


def main():
    dest = sys.argv[1]
    rng = random.Random(SEED)
    while True:
        plots, records = build(rng)
        k = key(plots, records)
        if k is not None:
            break
    os.makedirs(os.path.join(dest, "data", "harvests"))
    with open(os.path.join(dest, "data", "plots.json"), "w") as f:
        json.dump(plots, f, indent=1)
    by_month = collections.defaultdict(list)
    for r in records:
        by_month[r["date"][:7]].append(r)
    for month, rs in sorted(by_month.items()):
        with open(os.path.join(dest, "data", "harvests", f"{month}.json"), "w") as f:
            json.dump(rs, f, indent=1)
    with open(os.path.join(dest, "README.md"), "w") as f:
        f.write("# Larkspur harvest logs\n\nOne JSON file per month under data/harvests/, and the "
                "plot holders in data/plots.json.\n")
    with open(os.path.join(os.path.dirname(os.path.abspath(dest)), "key.json"), "w") as f:
        json.dump(k, f, indent=1)
    # The key must be the data's: the join must include both kinds of idle
    # holder, and the counts must add up to every record.
    assert sum(k["count"].values()) == len(records)
    assert len(k["join"]) >= 4, k["join"]


if __name__ == "__main__":
    main()
