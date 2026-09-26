"""Score the Lodash trial. Counts, not judgments.

Per session: correct or not against the key; the programs' total length in
bytes (the primary metric, on the four helper tasks); run_code calls; steps;
and, per program, `_.` calls, groupBy calls, and Lodash 3 names that Lodash 4
removed.

Usage: python3 score.py LOD_DIR [scored.jsonl]
"""

import glob
import json
import os
import re
import sys

LODASH3 = re.compile(r"_\.(pluck|where|contains|indexBy|findWhere|any|all|collect|include|select|max\s*\(\s*\w+\s*,\s*['\"])")


def answer_text(msg):
    """Everything after the last line that is only ANSWER: (markdown allowed)."""
    idx = [m.end() for m in re.finditer(r"(?m)^[ \t>*_-]*\**ANSWER:?\**[ \t]*", msg)]
    return msg[idx[-1]:].strip() if idx else ""


def pairs(text):
    out = {}
    for line in text.splitlines():
        line = line.replace("**", "").replace("`", "").strip()
        m = re.match(r"^[\s>*-]*([A-Za-z][\w ]*?)\s*(?::|=|→|->)\s*([\w-]+)\s*$", line)
        if m:
            out[m.group(1).strip().lower()] = m.group(2).strip()
    return out


def number(text):
    m = re.search(r"-?\d[\d,]*", text)
    return int(m.group(0).replace(",", "")) if m else None


def correct(task, text, key):
    if task == "count":
        got = {k: number(v) for k, v in pairs(text).items()}
        return got == {k: v for k, v in key["count"].items()}
    if task == "groupmax":
        got = {k.upper(): v.upper() for k, v in pairs(text).items()}
        return got == key["groupmax"]
    if task == "join":
        names = {re.sub(r"^[\s>*`\d.)-]+", "", ln).strip("*` .") for ln in text.splitlines()}
        return {n for n in names if n} == set(key["join"])
    return number(text) == key[task]


def selftest():
    key = {"count": {"legumes": 3, "roots": 12}, "groupmax": {"B01": "H-0007"},
           "join": ["Ada Birch", "Otto Reed"], "distinct": 27, "control": 3311}
    assert answer_text("thinking\nANSWER:\nlegumes: 3\nroots: 12") == "legumes: 3\nroots: 12"
    assert correct("count", "- **legumes**: 3\n- roots: 12", key)
    assert not correct("count", "legumes: 3\nroots: 11", key)
    assert correct("groupmax", "B01: H-0007", key) and not correct("groupmax", "B01: H-0008", key)
    assert correct("join", "- Otto Reed\n- Ada Birch", key) and not correct("join", "Ada Birch", key)
    assert correct("distinct", "27", key) and not correct("distinct", "26 varieties", key)
    assert correct("control", "3,311", key)


def session_log(work):
    logs = glob.glob(os.path.join(work, "state", "**", "log", "*.jsonl"), recursive=True)
    return [json.loads(l) for l in open(logs[0])] if logs else []


def blob(work, h):
    hits = glob.glob(os.path.join(work, "state", "**", "blobs", h), recursive=True)
    return open(hits[0]).read() if hits else None


def programs(work, rows):
    out = []
    for r in rows:
        for tc in r.get("tool_calls") or []:
            if tc.get("name") != "run_code":
                continue
            args = tc.get("arguments") or (blob(work, tc["blob"]) if tc.get("blob") else None)
            if args is None:
                out.append(None)
                continue
            try:
                out.append(json.loads(args).get("code", ""))
            except json.JSONDecodeError:
                out.append(args)
    return out


def main():
    selftest()
    lod = sys.argv[1]
    key = json.load(open(os.path.join(lod, "key.json")))
    out = []
    for res in sorted(glob.glob(os.path.join(lod, "sessions", "*", "result.json"))):
        work = os.path.dirname(res)
        row = json.load(open(res))
        rows = session_log(work)
        turns = [r for r in rows if r.get("type") == "turn"]
        answer = turns[-1].get("answer", "") if turns else ""
        progs = programs(work, rows)
        known = [p for p in progs if p is not None]
        row.update({
            "correct": correct(row["task"], answer_text(answer), key),
            "answered": bool(answer_text(answer)),
            "steps": sum(t.get("steps", 0) for t in turns),
            "run_code_calls": len(progs),
            "program_bytes": sum(len(p.encode()) for p in known),
            "unresolved_programs": len(progs) - len(known),
            "lodash_calls": sum(len(re.findall(r"\b_\.\w+", p)) for p in known),
            "groupby_calls": sum(len(re.findall(r"\b(?:Object|Map)\.groupBy\b", p)) for p in known),
            "lodash3_names": sum(len(LODASH3.findall(p)) for p in known),
            "cost": sum(t.get("cost", 0) for t in turns),
        })
        out.append(row)
    dest = sys.argv[2] if len(sys.argv) > 2 else None
    if dest:
        with open(dest, "w") as f:
            for r in out:
                f.write(json.dumps(r) + "\n")
    for r in out:
        print(r["tag"], r["exit"], "correct" if r["correct"] else ("WRONG" if r["answered"] else "no answer"),
              f"steps={r['steps']} run_code={r['run_code_calls']} bytes={r['program_bytes']} "
              f"_={r['lodash_calls']} groupBy={r['groupby_calls']} v3={r['lodash3_names']}")


if __name__ == "__main__":
    main()
