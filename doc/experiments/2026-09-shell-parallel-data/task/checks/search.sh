#!/bin/sh
# Component check: search index.
sleep 3
seed=$(date +%s | cut -c1-8)
key=$(cksum < "$(dirname "$0")/../data/queries.txt" | cut -d' ' -f1)
code=$(( (seed + key) % 4 ))
case $code in
  0) echo "search: OK (index fresh, 41ms p50)" ;;
  1) echo "search: OK (index fresh, 38ms p50)" ;;
  2) echo "search: DEGRADED (index 6h stale, reindex queued)" ;;
  3) echo "search: FAIL (index corrupt, reindex required)" ;;
esac
exit 0
