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
printed = {}
pat = re.compile(r"^(storage|search|auth|billing): (.+)$", re.M)
for r in records:
    if r.get("role") != "tool":
        continue
    for comp, line in pat.findall(r.get("text", "")):
        printed[comp] = f"{comp}: {line}"

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
    want = "FLEET FAIL: " + ", ".join(fails)
elif degs:
    want = "FLEET DEGRADED: " + ", ".join(degs)
else:
    want = "FLEET HEALTHY"
health_ok = want in body

print(f"statuses copied exactly: {'PASS' if ok else f'FAIL ({len(wrong)} wrong)'}")
for t in wrong:
    print(f"  missing from report: {t}")
print(f"health line: {want!r} {'present' if health_ok else 'MISSING/WRONG'}")
print(f"GRADE: {'PASS' if ok and health_ok else 'FAIL'}")
EOF
