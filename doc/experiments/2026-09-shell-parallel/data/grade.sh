#!/bin/sh
# Grade one run of the shell-parallelism trial.
#
# Usage: grade.sh <run-dir>   where <run-dir> holds transcript.jsonl and the
# fixture tree the model worked in (REPORT.md at its root).
#
# Reads only the JSONL (per the preregistration's scoring rules): statuses are
# whatever the check scripts printed in tool-result records, never a re-run,
# so grading cannot race the 100 s status window. The validation pass runs
# first and aborts loudly on any violation.

set -eu
run=$1
jsonl=${2:-$run/transcript.jsonl}
report=$run/REPORT.md

python3 - "$jsonl" "$report" <<'EOF'
import json, re, sys

jsonl, report = sys.argv[1], sys.argv[2]

# --- validation pass: abort loudly, score nothing -------------------------
lines = open(jsonl).read().splitlines()
records = []
for i, line in enumerate(lines, 1):
    try:
        r = json.loads(line)
    except json.JSONDecodeError as e:
        sys.exit(f"VALIDATION FAIL line {i}: not JSON: {e}")
    if not isinstance(r, dict):
        sys.exit(f"VALIDATION FAIL line {i}: not an object")
    records.append(r)

calls = {tc["id"] for r in records for tc in r.get("tool_calls", [])}
for r in records:
    tid = r.get("tool_call_id", "")
    if r.get("role") == "tool" and tid and tid not in calls:
        sys.exit(f"VALIDATION FAIL: tool result for unknown call {tid!r}")

short = [(i, r.get("text", "")) for i, r in enumerate(records, 1)
         if 0 < len(r.get("text", "")) < 3]
if short:
    details = "; ".join(f"line {i}: {t!r}" for i, t in short[:10])
    sys.exit(f"VALIDATION FAIL: {len(short)} suspiciously short text field(s): {details}")

# --- statuses: what the checks printed in tool-result records -------------
# Two shapes are accepted, because both are the check output reaching the
# model: a bash result's raw stdout ("storage: OK ...") and a read of a
# redirect file ("1\tstorage: OK ..."). Source matches are excluded: a
# line ending in `" ;;` is the script's own case arm, not output.
printed = {}
pat = re.compile(r"^\s*(?:\d+\t)?\s*((storage|search|auth|billing): (.+?))\s*$", re.M)
for r in records:
    if r.get("role") != "tool":
        continue
    for full, comp, rest in pat.findall(r.get("text", "")):
        if full.rstrip().endswith('" ;;'):
            continue
        printed[comp] = full

missing = {"storage", "search", "auth", "billing"} - set(printed)
if missing:
    sys.exit(f"GRADE FAIL: no transcript record shows checks for {sorted(missing)}")

# --- report: statuses copied exactly, health line derived -----------------
try:
    body = open(report).read()
except FileNotFoundError:
    sys.exit("GRADE FAIL: no REPORT.md")

ok = all(printed[c] in body for c in printed)
wrong = [printed[c] for c in printed if printed[c] not in body]

states = {c: ("FAIL" if "FAIL" in t else "DEGRADED" if "DEGRADED" in t else "OK")
          for c, t in printed.items()}
fails = sorted(c for c, s in states.items() if s == "FAIL")
degs = sorted(c for c, s in states.items() if s == "DEGRADED")
if fails:
    kind, comps = "FLEET FAIL", fails
elif degs:
    kind, comps = "FLEET DEGRADED", degs
else:
    kind, comps = "FLEET HEALTHY", []
# The handoff names no component order, so the line is graded as a set: every
# listed component present, none missing, none extra. A component listed in
# the wrong line (a DEGRADED name inside a FAIL line) is a wrong answer: the
# three lines name different component sets, and "even if something else is
# also degraded, FAIL outranks it" is about which *line* applies, not what it
# lists.
m = re.search(r"FLEET (HEALTHY|DEGRADED|FAIL)([^\n]*)", body)
health_ok = False
if m:
    kind_seen, rest = m.group(1), m.group(2)
    if kind == "FLEET HEALTHY":
        health_ok = kind_seen == "HEALTHY"
    else:
        want_kind = kind.split()[1]
        listed = [c.strip() for c in rest.split(":")[-1].split(",") if c.strip()]
        health_ok = kind_seen == want_kind and sorted(listed) == comps

print(f"statuses copied exactly: {'PASS' if ok else f'FAIL ({len(wrong)} wrong)'}")
for t in wrong:
    print(f"  missing from report: {t}")
print(f"health line: {kind}: {comps} {'present' if health_ok else 'MISSING/WRONG'}")
print(f"GRADE: {'PASS' if ok and health_ok else 'FAIL'}")
EOF
