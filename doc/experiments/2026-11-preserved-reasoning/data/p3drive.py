"""p3drive.py: run p3-order.txt through p3run.py (folded), WORKERS at a time, skipping graded sessions."""
import concurrent.futures as cf, os, subprocess, sys
P = os.path.dirname(os.path.abspath(__file__))
jobs = [l.split() for l in open(os.path.join(P, "p3-order.txt")) if l.strip()]
todo = [j for j in jobs if not os.path.exists(os.path.join(P, "p3-runs", f"{j[0]}-folded-{j[1]}-{j[2]}", "grade.txt"))]
print(f"{len(todo)} of {len(jobs)} to run", flush=True)
def run(j):
    r = subprocess.run([sys.executable, os.path.join(P, "p3run.py"), j[0], j[1], j[2], "folded"], capture_output=True, text=True)
    return (r.stdout.strip().splitlines() or [f"{j}: no output {r.stderr[-200:]!r}"])[-1]
with cf.ThreadPoolExecutor(int(os.environ.get("WORKERS", "3"))) as ex:
    for line in ex.map(run, todo):
        print(line, flush=True)
