#!/bin/bash
# bh-run.sh ARM MODEL I: one bug-hunt session with the ARM binary (base or replay).
P=$(cd "$(dirname "$0")" && pwd); arm=$1; m=$2; i=$3; d=$P/bh-runs/$arm-$m-$i
rm -rf "$d"; mkdir -p "$d"; cp -a "$P/bh-fixture" "$d/proj"
(cd "$d/proj" && git init -q && git add -A && git -c user.name=fixture -c user.email=f@x commit -qm init)
start=$(date +%s)
cd "$d/proj" && XDG_STATE_HOME=$d/state timeout "${TIMEOUT:-1800}" "$P/strument-$arm" --config "$P/configs/$m.star" \
	chat --yes steps,bash -p "$(cat "$P/bh-prompt.txt")" >"$d/out.txt" 2>&1
echo "exit=$? seconds=$(( $(date +%s) - start ))" >>"$d/out.txt"
python3 -I "$P/bh-grade.py" "$d/proj" "$P/bh-hidden" >"$d/grade.txt" 2>&1
