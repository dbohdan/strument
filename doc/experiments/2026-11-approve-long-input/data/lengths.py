"""Lengths of bash command+purpose in Strument session logs, per root."""
import glob, json, os, sys
sys.path.insert(0, sys.argv[1])
from rereads import logs, scan  # the read-dedup walker
for root in sys.argv[2:]:
    n = 0; over = {800: 0, 2000: 0, 4000: 0, 8000: 0}; mx = 0; sizes = []
    for lp in logs(root):
        calls, _, order, _ = scan(lp)
        for cid in order:
            name, args = calls[cid]
            if name != "bash":
                continue
            l = len(str(args.get("command", ""))) + len(str(args.get("purpose", "")))
            n += 1; sizes.append(l); mx = max(mx, l)
            for k in over:
                if l > k:
                    over[k] += 1
    sizes.sort()
    p = lambda q: sizes[int(q * (len(sizes) - 1))] if sizes else 0
    print(f"{os.path.basename(root.rstrip('/')):40} bash={n:5}  >800={over[800]:4} ({100*over[800]/max(n,1):4.1f}%)  >2000={over[2000]:3}  >4000={over[4000]:3}  >8000={over[8000]:3}  median={p(.5)} p95={p(.95)} max={mx}")
