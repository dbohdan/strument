#!/bin/bash
# MiMo-V2.6-Flash stratum of doc/experiments/2026-10-recall-models, on a paid
# provider pinned to Xiaomi: run the batch, then rerun sessions that failed on a
# provider error (transcripts kept), up to three passes; under nitro, take the
# service down when finished.
S=/tmp/claude-0/-home-user-strument/81348424-3872-590a-840d-1e776b0797d9/scratchpad
OUT=$S/recall/models-mimo; LOG=$S/recall/models-mimo.log
. $S/or.env
mkdir -p $S/recall/models-mimo-failed
alldone() { python3 -I -c "
import json,sys
r={}
try:
    for l in open('$OUT/runs.jsonl'): d=json.loads(l); r[d['id']]=d
except FileNotFoundError: sys.exit(1)
sys.exit(0 if len(r)==30 and all(d['done'] for d in r.values()) else 1)"; }
for pass in 1 2 3; do
  alldone && break
  if [ -f $OUT/runs.jsonl ]; then
    python3 -I -c "
import json,shutil,os
r={}
for l in open('$OUT/runs.jsonl'): d=json.loads(l); r[d['id']]=d
for i,d in r.items():
    if not d['done'] and os.path.isdir('$OUT/'+i):
        shutil.copytree('$OUT/'+i, '$S/recall/models-mimo-failed/pass$pass-'+i, dirs_exist_ok=True)"
  fi
  echo "--- pass $pass $(date -u +%FT%TZ)" >> $LOG
  (cd $S/cthresh && python3 -I tools/runner_recall.py $OUT 6 \
    --arms summary:4000,summary+recall:4000,summary+recall+note:4000,roll:4000,roll+recall:4000 \
    --bindir $S/recall --jobs 4 --seed 20261023 --port-base 19200 \
    --model xiaomi/mimo-v2.6-flash --provider Xiaomi >> $LOG 2>&1 < /dev/null)
done
alldone && echo "$(date -u +%FT%TZ) ALL DONE" >> $LOG || echo "$(date -u +%FT%TZ) STOPPED after 3 passes" >> $LOG
[ -n "$NITRO_SOCK" ] && exec $S/src/nitro/nitroctl down mimo
