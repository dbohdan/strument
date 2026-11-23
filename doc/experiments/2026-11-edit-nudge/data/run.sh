#!/bin/bash
# run.sh CONFIG MODEL ARM I: one session in a fresh copy of the fixture.
# CONFIG is "openrouter" or "commandcode"; ARM names the binary, strument-base
# (before d84b6f4) or strument-nudge (at it), built beside this script. Keys
# come from the environment (OPENROUTER_API_KEY, COMMAND_CODE_API_KEY).
E=$(cd "$(dirname "$0")" && pwd); c=$1; m=$2; arm=$3; i=$4; d=$E/runs-$c/$m-$arm-$i
rm -rf "$d"; mkdir -p "$d"; cp -a "$E/fixture" "$d/proj"
(cd "$d/proj" && git init -q && git add -A && git -c user.name=fixture -c user.email=f@x commit -qm init)
cd "$d/proj" && XDG_STATE_HOME=$d/state timeout 600 "$E/strument-$arm" --config "$E/config-$c-$m.star" \
	chat --yes steps,bash -m "$(cat "$E/prompt.txt")" >"$d/out.txt" 2>&1
echo "exit=$?" >>"$d/out.txt"
(cd "$d/proj" && go test ./... >"$d/test.txt" 2>&1; echo "test-exit=$?" >>"$d/test.txt")
