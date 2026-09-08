#!/bin/sh
# Component check: storage.
# Checks run long (a few seconds each); they are independent of one another.
sleep 3
seed=$(date +%s | cut -c1-8)
key=$(cksum < "$(dirname "$0")/../data/roster.csv" | cut -d' ' -f1)
code=$(( (seed + key) % 4 ))
case $code in
  0) echo "storage: OK (replication 3/3)" ;;
  1) echo "storage: DEGRADED (replication 2/3, rebalancing)" ;;
  2) echo "storage: OK (replication 3/3)" ;;
  3) echo "storage: FAIL (replication 1/3, quorum lost)" ;;
esac
exit 0
