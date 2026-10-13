#!/bin/bash
# Ling 3.1 stratum of doc/experiments/2026-10-recall-models: probe the free pool,
# run the batch when it answers, archive and rerun sessions that fail, stop when
# all 30 are done or after 14 hours.
S=/tmp/claude-0/-home-user-strument/81348424-3872-590a-840d-1e776b0797d9/scratchpad
OUT=$S/recall/models-ling31; LOG=$S/recall/models-ling31.log
. $S/or.env
end=$(( $(date +%s) + 14*3600 )); pass=0
alldone() { python3 -I -c "
import json,sys
r={}
try:
    for l in open('$OUT/runs.jsonl'): d=json.loads(l); r[d['id']]=d
except FileNotFoundError: sys.exit(1)
sys.exit(0 if len(r)==30 and all(d['done'] for d in r.values()) else 1)"; }
while [ $(date +%s) -lt $end ]; do
  if alldone; then
    echo "$(date -u +%FT%TZ) ALL DONE" >> $LOG
    # Under nitro, take the service down rather than exit and be restarted.
    [ -n "$NITRO_SOCK" ] && exec $S/src/nitro/nitroctl down ling31
    exit 0
  fi
  code=$(curl -s -o /dev/null -w '%{http_code}' https://openrouter.ai/api/v1/chat/completions \
    -H "Authorization: Bearer $OPENROUTER_API_KEY" -H 'Content-Type: application/json' \
    -d '{"model":"inclusionai/ling-3.1-flash","messages":[{"role":"user","content":"Say ok."}],"max_tokens":5}')
  echo "$(date -u +%FT%TZ) probe $code" >> $LOG
  if [ "$code" = 200 ]; then
    pass=$((pass+1))
    # Keep the transcripts of sessions about to be rerun: the runner wipes a session's directory.
    if [ -f $OUT/runs.jsonl ]; then
      python3 -I -c "
import json,shutil,os
r={}
for l in open('$OUT/runs.jsonl'): d=json.loads(l); r[d['id']]=d
for i,d in r.items():
    if not d['done'] and os.path.isdir('$OUT/'+i):
        shutil.copytree('$OUT/'+i, '$S/recall/models-ling31-failed/pass$pass-'+i, dirs_exist_ok=True)"
    fi
    pgrep -f "$S/orproxy/proxy.py" | xargs -r kill; sleep 1
    echo "--- pass $pass $(date -u +%FT%TZ)" >> $LOG
    (cd $S/cthresh && python3 -I tools/runner_recall.py $OUT 6 \
      --arms summary:4000,summary+recall:4000,summary+recall+note:4000,roll:4000,roll+recall:4000 \
      --bindir $S/recall --jobs 2 --seed 20261024 --model inclusionai/ling-3.1-flash >> $LOG 2>&1 < /dev/null)
    continue
  fi
  sleep 600
done
echo "$(date -u +%FT%TZ) WATCHER STOPPED (time limit)" >> $LOG
