"""Fold the run records into the table the write-up carries.

Counts, not judgments: "did the model report the probe's exact digits" is a
count, so the author of the change is not also the judge of it.
"""
import collections, json, pathlib, sys

D = pathlib.Path(__file__).resolve().parent.parent


def load(name):
    return json.load(open(D / name / "_results.json"))


def table(rows):
    g = collections.defaultdict(lambda: [0, 0, set()])
    for x in rows:
        k = (x["dialect"], x["model"], x["route"], x["declared_image"])
        g[k][0] += x["digits"]
        g[k][1] += 1
        g[k][2].add(x["exit"])
    return g


def main():
    before, after = table(load(sys.argv[1])), table(load(sys.argv[2]))
    print(f"{'dialect':<11}{'model':<13}{'route':<8}{'decl':<6}{'before':<9}{'after':<9}exits")
    for k in sorted(set(before) | set(after)):
        b = before.get(k)
        a = after.get(k)
        bs = f"{b[0]}/{b[1]}" if b else "-"
        as_ = f"{a[0]}/{a[1]}" if a else "-"
        ex = sorted(a[2]) if a else sorted(b[2])
        flag = "  <-- changed" if b and a and b[0] != a[0] else ""
        print(f"{k[0]:<11}{k[1]:<13}{k[2]:<8}{str(k[3]):<6}{bs:<9}{as_:<9}{ex}{flag}")
    leaks = [x["name"] for x in load(sys.argv[2]) if x.get("leak")]
    print("\nconfound leaks (must be empty):", leaks or "none")


if __name__ == "__main__":
    main()
