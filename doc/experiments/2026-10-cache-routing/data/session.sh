#!/bin/bash
# usage: session.sh <arm> <model>: six --continue turns, four files each.
S=/tmp/claude-0/-home-user-strument/81348424-3872-590a-840d-1e776b0797d9/scratchpad; LS=$S/longsess
arm=$1; m=$2; d=$LS/w-$arm-$m; rm -rf $d $LS/state-$arm-$m; git clone -q /home/user/strument $d
mapfile -t F < $LS/files.txt
for t in 0 1 2 3 4 5; do
  f=("${F[@]:$((t*4)):4}")
  msg="Read ${f[0]}, then ${f[1]}, then ${f[2]}, then ${f[3]}, one file per step, whole. Then say in two or three sentences what the four have in common. Do not edit anything."
  cont=""; [ $t -gt 0 ] && cont="--continue"
  (cd $d && XDG_CONFIG_HOME=$LS/cfg-$arm XDG_STATE_HOME=$LS/state-$arm-$m timeout 900 $LS/strument chat $cont --no-color --yes bash -M $m -m "$msg" < /dev/null > $LS/out-$arm-$m-$t.txt 2>&1)
  echo "$arm $m turn $t exit=$? $(grep -c 'Chat history compacted' $LS/out-$arm-$m-$t.txt) compactions | $(grep -E '^Tokens' $LS/out-$arm-$m-$t.txt | tail -1)"
done
