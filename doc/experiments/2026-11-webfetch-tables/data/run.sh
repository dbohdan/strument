#!/bin/bash
# One run: arm ($1) and index ($2), in a fresh project and state directory.
L=$(dirname "$0"); d=$L/runs/$1-$2; rm -rf "$d"; mkdir -p "$d/proj"; (cd "$d/proj" && git init -q)
cd "$d/proj" && XDG_STATE_HOME=$d/state timeout 900 "$L/strument-$1" --config "$L/config.star" chat --yes webfetch --unattended \
  -m "Use run_code with webfetch on https://tetris.wiki/SRS#Wall_Kicks to generate the J, L, S, T, Z and the I wall kick tables as C++: const KickTable kJlstzKicks[8] and kIKicks[8], each row five {x, y} pairs, rows in the order 0->R, 0->L, R->2, R->0, 2->L, 2->R, L->0, L->2. Print them; do not write files." \
  > "$d/out.txt" 2>&1
echo "$1-$2 exit=$?"
