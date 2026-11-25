"""bh-grade.py PROJECT HIDDEN_DIR: hidden and visible pass counts, and whether tests/ was changed."""
import os, subprocess, sys
proj, hid = sys.argv[1], sys.argv[2]
def count(cmd, cwd, env):
    r = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=120)
    lines = [l for l in r.stderr.splitlines() if " ... " in l]
    return sum(l.endswith(" ok") for l in lines), len(lines)
h = count([sys.executable, "-m", "unittest", "-v", "hidden_test"], hid, dict(os.environ, PYTHONPATH=proj + os.pathsep + hid))
v = count([sys.executable, "-m", "unittest", "-v"], proj, dict(os.environ))
changed = subprocess.run(["git", "diff", "--name-only", "HEAD", "--", "tests"], cwd=proj, capture_output=True, text=True).stdout.split()
print(f"HIDDEN {h[0]}/{h[1]} VISIBLE {v[0]}/{v[1]} TESTS_CHANGED {len(changed)}")
