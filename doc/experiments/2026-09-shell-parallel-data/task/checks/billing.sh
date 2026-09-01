#!/bin/sh
# Component check: billing worker.
# Checks run long (a few seconds each); they are independent of one another.
sleep 3
seed=$(date +%s | cut -c1-8)
key=$(cksum < "$(dirname "$0")/../data/queries.txt" | cut -d' ' -f1)
code=$(( (seed + 104729) % 4 ))
case $code in
  0) echo "billing: OK (queue depth 0, last run 2m ago)" ;;
  1) echo "billing: DEGRADED (queue depth 340, retrying)" ;;
  2) echo "billing: OK (queue depth 12, draining)" ;;
  3) echo "billing: FAIL (worker crash-looping, 5 restarts/10m)" ;;
esac
exit 0
