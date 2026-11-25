"""drive.py: run the shuffled trial order, WORKERS at a time, skipping graded sessions."""
import concurrent.futures as cf, os, subprocess, sys
P = os.path.dirname(os.path.abspath(__file__))
RUNNER, RUNS = os.environ.get("RUNNER", "run.sh"), os.environ.get("RUNS", "runs")
jobs = [l.split() for l in open(os.path.join(P, "order.txt")) if l.strip()]
todo = [j for j in jobs if not os.path.exists(os.path.join(P, RUNS, f"{j[0]}-{j[1]}-{j[2]}", "grade.txt"))]
print(f"{len(todo)} of {len(jobs)} to run", flush=True)
def run(j):
    try:
        subprocess.run([os.path.join(P, RUNNER), *j], check=False)
        g = open(os.path.join(P, RUNS, "-".join(j), "grade.txt")).readline().strip()
        return f"{' '.join(j)}: {g}"
    except Exception as e:  # one failed job must not stop the collection
        return f"{' '.join(j)}: FAILED {e!r}"
with cf.ThreadPoolExecutor(int(os.environ.get("WORKERS", "3"))) as ex:
    for r in ex.map(run, todo):
        print(r, flush=True)
