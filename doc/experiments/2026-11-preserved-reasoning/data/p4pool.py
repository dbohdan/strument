#!/usr/bin/env python3
"""p4pool.py ORDER WORKERS DEADLINE: run phase 4's sessions from ORDER, WORKERS at a time.

ORDER has one "ARM MODEL I" per line, in the seeded shuffle. A session whose
directory has end.txt is done and skipped, so the pool can be stopped and
started again. A session starts only if its model's timeout still fits before
DEADLINE seconds from now, so a pool never leaves a session to be cut off by
the job that runs it.
"""
import os, subprocess, sys, threading, time
P = os.path.dirname(os.path.abspath(__file__))
TIMEOUT = {"mimo": 6900, "deepseek": 3000, "flint": 5400}

order, workers, deadline = sys.argv[1], int(sys.argv[2]), time.time() + int(sys.argv[3])
runs = os.environ.get("P4_RUNS", "p4-runs")
todo = [l.split() for l in open(order) if l.strip()]
todo = [t for t in todo if not os.path.exists(os.path.join(P, runs, "-".join(t), "end.txt"))]
lock = threading.Lock()


def worker():
    while True:
        with lock:
            fits = [t for t in todo if time.time() + TIMEOUT[t[1]] + 120 < deadline]
            if not fits:
                return
            t = fits[0]; todo.remove(t)
        env = dict(os.environ, P4_TIMEOUT=str(TIMEOUT[t[1]]))
        subprocess.run([sys.executable, "-I", os.path.join(P, "p4run.py"), *t], env=env)


threads = [threading.Thread(target=worker) for _ in range(workers)]
for th in threads:
    th.start()
for th in threads:
    th.join()
print(f"pool done, {len(todo)} left", flush=True)
