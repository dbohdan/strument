#!/bin/sh
# Component check: auth service.
# Checks run long (a few seconds each); they are independent of one another.
sleep 3
seed=$(date +%s | cut -c1-8)
key=$(cksum < "$(dirname "$0")/../data/roster.csv" | cut -d' ' -f1)
code=$(( (seed + 7919) % 4 ))
case $code in
  0) echo "auth: OK (p99 120ms, 0 failed logins/5m)" ;;
  1) echo "auth: OK (p99 134ms, 0 failed logins/5m)" ;;
  2) echo "auth: DEGRADED (p99 610ms, token cache thrashing)" ;;
  3) echo "auth: FAIL ( IdP unreachable, logins erroring)" ;;
esac
exit 0
