import json, re, sys, collections
NAIVE = {"blank": 0, "longest": 294, "cite": 110, "total": None}

def num(text):
    """The answer, or None. A model that wrote prose around the number still
    answered; one that wrote nothing did not. The two are different columns."""
    if not text:
        return None
    t = text.strip().replace("*", "")
    m = re.findall(r"-?\d[\d,]*", t)
    if not m:
        return None
    return int(m[-1].replace(",", ""))

def load(path):
    rows = [json.loads(l) for l in open(path)]
    for r in rows:
        a = num(r.get("answer_text"))
        r["got"] = a
        r["answered"] = a is not None
        r["correct"] = a == r["expected"]
        r["naive"] = a is not None and a == NAIVE.get(r["task"])
    return rows

if __name__ == "__main__":
    rows = load(sys.argv[1])
    print(f"{len(rows)} runs; statuses: {dict(collections.Counter(r.get('status') for r in rows))}")
    print(f"unanswered: {sum(1 for r in rows if not r['answered'])}")
