#!/bin/bash
# run.sh ALIAS I: one session in a fresh copy of the fixture.
L=$(cd "$(dirname "$0")" && pwd); a=$1; i=$2; d=$L/runs/$a-$i
rm -rf "$d"; mkdir -p "$d"; cp -a "$L/fixture" "$d/proj"
(cd "$d/proj" && git init -q && git add -A && git -c user.name=fixture -c user.email=f@x commit -qm init)
start=$(date +%s)
cd "$d/proj" && XDG_STATE_HOME=$d/state timeout "${TIMEOUT:-1800}" "$L/strument" --config "$L/configs/$a.star" \
	chat --yes steps,bash -p "$(cat "$L/prompt.txt")" >"$d/out.txt" 2>&1
echo "exit=$? seconds=$(( $(date +%s) - start ))" >>"$d/out.txt"
python3 -I "$L/grade.py" "$d/proj/tetris.py" "$L/hidden" >"$d/grade.txt" 2>&1
